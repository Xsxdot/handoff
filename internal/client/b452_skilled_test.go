// b452_skilled_test.go —— B452 S5/S8 客户端侧：终态等待（WaitFinal）、按机取任务、产物下载与安全解包。
//
// 边界：只用 httptest HTTP/WS 桩，不起真实 agentd、不动真实 HOME。
package client

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

func b452Completed(seq int64, text string) proto.Event {
	p, _ := json.Marshal(map[string]any{"branch": "", "commit": "", "summary": "s", "final_text": text})
	return proto.Event{Seq: seq, TaskID: "t1", Type: proto.EventTypeCompleted, Payload: p}
}

func b452Failed(seq int64, typ proto.EventType, reason string) proto.Event {
	p, _ := json.Marshal(map[string]any{"fail_reason": reason})
	return proto.Event{Seq: seq, TaskID: "t1", Type: typ, Payload: p}
}

func b452Ev(seq int64, typ proto.EventType) proto.Event {
	return proto.Event{Seq: seq, TaskID: "t1", Type: typ, Payload: json.RawMessage(`{}`)}
}

// b452FinalServer 是事件流桩：attach 返回快照，ws 每次连接调用 stream；记录两条路径收到的 query。
type b452FinalServer struct {
	mu        sync.Mutex
	attachQ   []string
	wsQ       []string
	attachN   atomic.Int32
	wsN       atomic.Int32
	attach    func(n int32) *AttachInfo
	stream    func(n int32, conn *websocket.Conn, r *http.Request)
	ts        *httptest.Server
	artifacts http.HandlerFunc
}

func newB452FinalServer(t *testing.T, s *b452FinalServer) *b452FinalServer {
	t.Helper()
	s.ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/tasks/t1":
			n := s.attachN.Add(1)
			s.mu.Lock()
			s.attachQ = append(s.attachQ, r.URL.RawQuery)
			s.mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(s.attach(n))
		case r.URL.Path == "/api/tasks/t1/artifacts" && s.artifacts != nil:
			s.artifacts(w, r)
		case r.URL.Path == "/ws/events":
			n := s.wsN.Add(1)
			s.mu.Lock()
			s.wsQ = append(s.wsQ, r.URL.RawQuery)
			s.mu.Unlock()
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			if s.stream != nil {
				s.stream(n, conn, r)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(func() { s.ts.CloseClientConnections(); s.ts.Close() })
	return s
}

func b452Client(url string) *Client {
	return NewWithWSTiming(url, "", 10*time.Millisecond, 50*time.Millisecond, time.Second)
}

// TestB452WaitFinalCompletedInStreamByMachine：回合进行中 → 跟流拿到 completed；两条路径都带 machine；
// 不写任何 cursor。
func TestB452WaitFinalCompletedInStreamByMachine(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(int32) *AttachInfo {
			return snapshot("t1", proto.TaskStateRunning, b452Ev(1, proto.EventTypeProgress))
		},
		stream: func(_ int32, conn *websocket.Conn, r *http.Request) {
			writeWSEvent(t, conn, r.Context(), b452Ev(2, proto.EventTypeProgress))
			writeWSEvent(t, conn, r.Context(), b452Completed(3, "## 结论\n答案"))
			writeWSEvent(t, conn, r.Context(), b452Ev(4, proto.EventTypeArchived))
			<-r.Context().Done()
		},
	})
	out, err := b452Client(s.ts.URL).WaitFinal(t.Context(), "t1", "linux-01", nil)
	if err != nil {
		t.Fatalf("WaitFinal: %v", err)
	}
	if !out.Completed || !out.HasFinalText || out.FinalText != "## 结论\n答案" || out.Event.Seq != 3 {
		t.Fatalf("outcome = %+v", out)
	}
	if s.attachQ[0] != "machine=linux-01" {
		t.Fatalf("取任务应带 machine，得 %q", s.attachQ[0])
	}
	if !strings.Contains(s.wsQ[0], "machine=linux-01") || !strings.Contains(s.wsQ[0], "from_seq=1") {
		t.Fatalf("事件流应带 machine 且从快照水位起，得 %q", s.wsQ[0])
	}
	if _, err := os.Stat(filepath.Join(home, ".handoff", "cursors")); !os.IsNotExist(err) {
		t.Fatalf("WaitFinal 不得创建任何游标: %v", err)
	}
}

// TestB452WaitFinalReconnectsAfterDrop：中途断线 → 重取快照、从新水位重连，仍拿到 completed。
func TestB452WaitFinalReconnectsAfterDrop(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(n int32) *AttachInfo {
			if n == 1 {
				return snapshot("t1", proto.TaskStateRunning)
			}
			return snapshot("t1", proto.TaskStateRunning, b452Ev(2, proto.EventTypeProgress))
		},
		stream: func(n int32, conn *websocket.Conn, r *http.Request) {
			if n == 1 {
				writeWSEvent(t, conn, r.Context(), b452Ev(2, proto.EventTypeProgress))
				conn.CloseNow() // 异常断线
				return
			}
			writeWSEvent(t, conn, r.Context(), b452Completed(3, "重连后的回文"))
			<-r.Context().Done()
		},
	})
	out, err := b452Client(s.ts.URL).WaitFinal(t.Context(), "t1", "", nil)
	if err != nil || !out.Completed || out.FinalText != "重连后的回文" {
		t.Fatalf("outcome=%+v err=%v", out, err)
	}
	if len(s.wsQ) != 2 || !strings.Contains(s.wsQ[1], "from_seq=2") || strings.Contains(s.wsQ[1], "machine=") {
		t.Fatalf("应重连一次、从水位 2 续、不带空 machine，得 %q", s.wsQ)
	}
}

// TestB452WaitFinalLastTurnAfterContinue：已在 waiting_review、经过一次 continue 的任务 → 取最后一回合结果。
func TestB452WaitFinalLastTurnAfterContinue(t *testing.T) {
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(int32) *AttachInfo {
			return snapshot("t1", proto.TaskStateWaitingReview,
				b452Completed(3, "首回合"), b452Ev(4, proto.EventTypeProgress),
				b452Completed(7, "末回合"), b452Ev(8, proto.EventTypeProgress))
		},
		stream: func(int32, *websocket.Conn, *http.Request) { t.Error("回合已结束不应建流") },
	})
	out, err := b452Client(s.ts.URL).WaitFinal(t.Context(), "t1", "", nil)
	if err != nil || !out.Completed || out.FinalText != "末回合" || out.Event.Seq != 7 {
		t.Fatalf("应取最后一回合，得 outcome=%+v err=%v", out, err)
	}
}

// TestB452WaitFinalNonCompletion：turn_failed / failed / 未完成就归档 → Completed=false，带原因。
func TestB452WaitFinalNonCompletion(t *testing.T) {
	cases := []struct {
		name   string
		state  proto.TaskState
		events []proto.Event
		reason string
	}{
		{"turn_failed", proto.TaskStateWaitingReview,
			[]proto.Event{b452Failed(3, proto.EventTypeTurnFailed, "缺 trailer")}, "缺 trailer"},
		{"failed", proto.TaskStateFailed,
			[]proto.Event{b452Failed(3, proto.EventTypeFailed, "进程退出")}, "进程退出"},
		{"回合失败后被归档", proto.TaskStateCompleted,
			[]proto.Event{b452Failed(3, proto.EventTypeTurnFailed, "回合炸了"), b452Ev(4, proto.EventTypeArchived)}, "回合炸了"},
		{"无任何回合结果就归档", proto.TaskStateCompleted,
			[]proto.Event{b452Ev(4, proto.EventTypeArchived)}, "未完成"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newB452FinalServer(t, &b452FinalServer{
				attach: func(int32) *AttachInfo { return snapshot("t1", tc.state, tc.events...) },
				stream: func(_ int32, conn *websocket.Conn, r *http.Request) {
					// 只有「最近窗口里没有回合结果」时才会回放；回放给同一批事件后正常关闭
					for _, ev := range tc.events {
						writeWSEvent(t, conn, r.Context(), ev)
					}
					_ = conn.Close(websocket.StatusNormalClosure, "task archived")
				},
			})
			out, err := b452Client(s.ts.URL).WaitFinal(t.Context(), "t1", "", nil)
			if err != nil {
				t.Fatalf("WaitFinal: %v", err)
			}
			if out.Completed || !strings.Contains(out.Reason, tc.reason) {
				t.Fatalf("应非完成且原因含 %q，得 %+v", tc.reason, out)
			}
		})
	}
}

// TestB452WaitFinalReplaysWhenResultBeyondWindow：回合已结束但最近窗口里没有回合结果 → 从 0 回放补找。
func TestB452WaitFinalReplaysWhenResultBeyondWindow(t *testing.T) {
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(int32) *AttachInfo {
			return snapshot("t1", proto.TaskStateWaitingReview, b452Ev(51, proto.EventTypeProgress),
				b452Ev(52, proto.EventTypeTicketAnswered))
		},
		stream: func(_ int32, conn *websocket.Conn, r *http.Request) {
			writeWSEvent(t, conn, r.Context(), b452Completed(10, "窗外的回文"))
			writeWSEvent(t, conn, r.Context(), b452Ev(51, proto.EventTypeProgress))
			writeWSEvent(t, conn, r.Context(), b452Ev(52, proto.EventTypeTicketAnswered))
			<-r.Context().Done()
		},
	})
	out, err := b452Client(s.ts.URL).WaitFinal(t.Context(), "t1", "", nil)
	if err != nil || !out.Completed || out.FinalText != "窗外的回文" {
		t.Fatalf("outcome=%+v err=%v", out, err)
	}
	if !strings.Contains(s.wsQ[0], "from_seq=0") {
		t.Fatalf("补找应从 0 回放，得 %q", s.wsQ[0])
	}
}

// TestB452WaitFinalDeadline：总时限到 → ctx 错误（调用方转 124），期间提问事件交给 onAttention。
func TestB452WaitFinalDeadline(t *testing.T) {
	s := newB452FinalServer(t, &b452FinalServer{
		attach: func(int32) *AttachInfo { return snapshot("t1", proto.TaskStateRunning) },
		stream: func(_ int32, conn *websocket.Conn, r *http.Request) {
			writeWSEvent(t, conn, r.Context(), b452Ev(1, proto.EventTypeQuestion))
			<-r.Context().Done()
		},
	})
	ctx, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	var seen []proto.EventType
	_, err := b452Client(s.ts.URL).WaitFinal(ctx, "t1", "", func(ev proto.Event) { seen = append(seen, ev.Type) })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应 DeadlineExceeded，得 %v", err)
	}
	if len(seen) != 1 || seen[0] != proto.EventTypeQuestion {
		t.Fatalf("提问事件应交给 onAttention，得 %v", seen)
	}
}

// TestB452AttachOnNotFoundIsPermanent：任务不存在 → 立即报错，不重试。
func TestB452AttachOnNotFoundIsPermanent(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(ts.Close)
	_, err := b452Client(ts.URL).WaitFinal(t.Context(), "t1", "m", nil)
	if code, ok := StatusCode(err); !ok || code != http.StatusNotFound {
		t.Fatalf("应 404 立即失败，得 %v", err)
	}
}

func b452TarGz(t *testing.T, entries []tar.Header, bodies map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		body := bodies[h.Name]
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			if _, err := io.WriteString(tw, body); err != nil {
				t.Fatal(err)
			}
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// TestB452ArtifactsDownloadStatuses：200 → 字节流、204 → 空、404 → ErrArtifactsUnsupported；带 machine。
func TestB452ArtifactsDownloadStatuses(t *testing.T) {
	pkg := b452TarGz(t, []tar.Header{{Name: "report.md", Typeflag: tar.TypeReg, Mode: 0o644}},
		map[string]string{"report.md": "r"})
	for _, tc := range []struct {
		name   string
		status int
	}{{"200", 200}, {"204", 204}, {"404", 404}} {
		t.Run(tc.name, func(t *testing.T) {
			var gotQ string
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQ = r.URL.RawQuery
				if r.URL.Path != "/api/tasks/t1/artifacts" {
					http.NotFound(w, r)
					return
				}
				w.WriteHeader(tc.status)
				if tc.status == 200 {
					_, _ = w.Write(pkg)
				}
			}))
			t.Cleanup(ts.Close)
			rc, err := b452Client(ts.URL).Artifacts(t.Context(), "t1", "linux-01")
			if gotQ != "machine=linux-01" {
				t.Fatalf("应带 machine，得 %q", gotQ)
			}
			switch tc.status {
			case 200:
				if err != nil || rc == nil {
					t.Fatalf("200 应返回字节流: %v", err)
				}
				b, _ := io.ReadAll(rc)
				rc.Close()
				if !bytes.Equal(b, pkg) {
					t.Fatal("字节流不一致")
				}
			case 204:
				if err != nil || rc != nil {
					t.Fatalf("204 应 (nil,nil)，得 %v %v", rc, err)
				}
			case 404:
				if !errors.Is(err, ErrArtifactsUnsupported) {
					t.Fatalf("404 应 ErrArtifactsUnsupported，得 %v", err)
				}
			}
		})
	}
}

// TestB452ExtractArtifacts：正常包逐字节解出；含 ../、绝对路径、符号链接的包整次失败、零文件落地。
func TestB452ExtractArtifacts(t *testing.T) {
	t.Run("正常", func(t *testing.T) {
		pkg := b452TarGz(t, []tar.Header{
			{Name: "sub/", Typeflag: tar.TypeDir, Mode: 0o755},
			{Name: "sub/a.txt", Typeflag: tar.TypeReg, Mode: 0o644},
			{Name: "report.md", Typeflag: tar.TypeReg, Mode: 0o644},
		}, map[string]string{"sub/a.txt": "AAA", "report.md": "# r\n"})
		dest := filepath.Join(t.TempDir(), "out")
		if err := os.Mkdir(dest, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := ExtractArtifacts(bytes.NewReader(pkg), dest); err != nil {
			t.Fatalf("解包: %v", err)
		}
		for name, want := range map[string]string{"sub/a.txt": "AAA", "report.md": "# r\n"} {
			b, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(name)))
			if err != nil || string(b) != want {
				t.Fatalf("%s = %q %v", name, b, err)
			}
		}
	})
	bad := map[string][]tar.Header{
		"上跳":   {{Name: "ok.txt", Typeflag: tar.TypeReg}, {Name: "../x", Typeflag: tar.TypeReg}},
		"绝对路径": {{Name: "ok.txt", Typeflag: tar.TypeReg}, {Name: "/etc/x", Typeflag: tar.TypeReg}},
		"符号链接": {{Name: "ok.txt", Typeflag: tar.TypeReg}, {Name: "l", Typeflag: tar.TypeSymlink, Linkname: "/"}},
	}
	for name, hdrs := range bad {
		t.Run(name, func(t *testing.T) {
			pkg := b452TarGz(t, hdrs, map[string]string{"ok.txt": "先落地的文件", "../x": "x", "/etc/x": "x"})
			parent := t.TempDir()
			dest := filepath.Join(parent, "out")
			if err := os.Mkdir(dest, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := ExtractArtifacts(bytes.NewReader(pkg), dest); err == nil {
				t.Fatal("恶意条目应整次失败")
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatalf("失败后目标目录应被整体删除（零文件落地），得 %v", err)
			}
			if _, err := os.Stat(filepath.Join(parent, "x")); !os.IsNotExist(err) {
				t.Fatal("上跳条目不得落到目标目录之外")
			}
		})
	}
}
