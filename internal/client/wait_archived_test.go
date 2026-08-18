// wait_archived_test.go 验证 B67 归档门闩的快照、事件与重连契约。
//
// 职责：覆盖真实 archived、failed、兼容性错误、竞态、重连和 cursor 隔离。
// 边界：只使用 httptest HTTP/WS，不启动真实 agentd、不改变真实 HOME。
package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

func TestHTTPStatusErrorIsPermanent(t *testing.T) {
	for _, code := range []int{400, 401, 403, 404} {
		err := &httpStatusError{op: "任务详情", code: code, body: "拒绝"}
		if !isPermanent(err) {
			t.Errorf("status %d 应是永久错误", code)
		}
	}
	if isPermanent(&httpStatusError{op: "任务详情", code: 500, body: "故障"}) {
		t.Fatal("500 可能瞬时恢复，不应判永久")
	}
}

func TestClassifyArchivedSnapshot(t *testing.T) {
	archived := proto.Event{Seq: 12, TaskID: "t1", Type: proto.EventTypeArchived,
		Payload: json.RawMessage(`{"note":"完成"}`)}
	cases := []struct {
		name         string
		snap         *AttachInfo
		wantSeq      int64
		wantArchived bool
		wantErr      error
	}{
		{"已归档返回原事件", snapshot("t1", proto.TaskStateCompleted, archived), 12, true, nil},
		{"失败立即返回", snapshot("t1", proto.TaskStateFailed), 0, false, ErrDependencyFailed},
		{"completed 缺 archived", snapshot("t1", proto.TaskStateCompleted), 0, false, ErrArchivedEventMissing},
		{"活任务从最新水位续拉", snapshot("t1", proto.TaskStateRunning,
			proto.Event{Seq: 9, TaskID: "t1", Type: proto.EventTypeProgress}), 9, false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, seq, err := classifyArchivedSnapshot("t1", tc.snap)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want errors.Is(_, %v)", err, tc.wantErr)
			}
			if seq != tc.wantSeq {
				t.Fatalf("fromSeq=%d want %d", seq, tc.wantSeq)
			}
			if (got != nil) != tc.wantArchived {
				t.Fatalf("archived=%+v wantArchived=%v", got, tc.wantArchived)
			}
			if got != nil && (got.Seq != 12 || got.Type != proto.EventTypeArchived ||
				string(got.Payload) != `{"note":"完成"}`) {
				t.Fatalf("必须返回原始 archived，got=%+v", got)
			}
		})
	}
}

func snapshot(id string, state proto.TaskState, events ...proto.Event) *AttachInfo {
	return &AttachInfo{
		Task:           proto.TaskView{Task: proto.Task{ID: id, State: state}},
		RecentEvents:   events,
		PendingTickets: []proto.Ticket{},
	}
}

func newWaitArchivedServer(t *testing.T,
	attach func() *AttachInfo,
	stream func(*websocket.Conn, *http.Request),
) *httptest.Server {
	t.Helper()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/tasks/t1":
			w.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(w).Encode(attach()); err != nil {
				t.Errorf("encode attach: %v", err)
			}
		case r.URL.Path == "/ws/events":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept: %v", err)
				return
			}
			defer conn.CloseNow()
			stream(conn, r)
		default:
			http.NotFound(w, r)
		}
	})
	ts := httptest.NewServer(h)
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	return ts
}

func writeWSEventE(conn *websocket.Conn, ctx context.Context, ev proto.Event) error {
	b, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, b)
}

func writeWSEvent(t *testing.T, conn *websocket.Conn,
	ctx context.Context, ev proto.Event) {
	t.Helper()
	if err := writeWSEventE(conn, ctx, ev); err != nil {
		t.Errorf("write event seq=%d type=%s: %v", ev.Seq, ev.Type, err)
	}
}

func TestWaitArchivedAlreadyArchivedReturnsOriginalEvent(t *testing.T) {
	want := proto.Event{Seq: 12, TaskID: "t1", Type: proto.EventTypeArchived,
		Payload: json.RawMessage(`{"note":"已验收"}`)}
	ts := newWaitArchivedServer(t,
		func() *AttachInfo { return snapshot("t1", proto.TaskStateCompleted, want) },
		func(*websocket.Conn, *http.Request) { t.Fatal("已归档不应建 WS") })
	got, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
	if err != nil || got.Seq != want.Seq || string(got.Payload) != string(want.Payload) {
		t.Fatalf("got=%+v err=%v, want 原始 archived", got, err)
	}
}

func TestWaitArchivedSkipsIntermediateEventsAndDoesNotTouchCursor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	archived := proto.Event{Seq: 15, TaskID: "t1", Type: proto.EventTypeArchived,
		Payload: json.RawMessage(`{"note":"done"}`)}
	frames := []proto.Event{
		{Seq: 11, TaskID: "t1", Type: proto.EventTypeQuestion},
		{Seq: 12, TaskID: "t1", Type: proto.EventTypePermissionRequest},
		{Seq: 13, TaskID: "t1", Type: proto.EventTypeCompleted},
		{Seq: 14, TaskID: "t1", Type: proto.EventTypeProgress},
		archived,
	}
	ts := newWaitArchivedServer(t,
		func() *AttachInfo { return snapshot("t1", proto.TaskStateRunning) },
		func(conn *websocket.Conn, r *http.Request) {
			for _, ev := range frames {
				writeWSEvent(t, conn, r.Context(), ev)
			}
		})
	got, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
	if err != nil || got.Seq != archived.Seq {
		t.Fatalf("got=%+v err=%v", got, err)
	}
	// 断言整棵游标目录而不是某个具体文件：布局已改为
	// ~/.handoff/cursors/<agentd 地址>/<task>，盯死旧的平铺路径
	// ~/.handoff/cursor-<task> 会让这条隔离断言恒真——写了游标也照样通过。
	if _, err := os.Stat(filepath.Join(home, ".handoff", "cursors")); !os.IsNotExist(err) {
		t.Fatalf("B67 不得创建任何游标: %v", err)
	}
}

func TestWaitArchivedFailedBeforeAndDuringStream(t *testing.T) {
	t.Run("before", func(t *testing.T) {
		wsCalled := false
		ts := newWaitArchivedServer(t,
			func() *AttachInfo { return snapshot("t1", proto.TaskStateFailed) },
			func(*websocket.Conn, *http.Request) { wsCalled = true })
		_, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
		if !errors.Is(err, ErrDependencyFailed) || wsCalled {
			t.Fatalf("err=%v wsCalled=%v", err, wsCalled)
		}
	})
	t.Run("during", func(t *testing.T) {
		ts := newWaitArchivedServer(t,
			func() *AttachInfo { return snapshot("t1", proto.TaskStateRunning) },
			func(conn *websocket.Conn, r *http.Request) {
				writeWSEvent(t, conn, r.Context(), proto.Event{
					Seq: 7, TaskID: "t1", Type: proto.EventTypeFailed,
					Payload: json.RawMessage(`{"error":"boom"}`),
				})
			})
		_, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
		if !errors.Is(err, ErrDependencyFailed) {
			t.Fatalf("err=%v want ErrDependencyFailed", err)
		}
	})
}

func TestWaitArchivedContextDeadlineIsTotal(t *testing.T) {
	ts := newWaitArchivedServer(t,
		func() *AttachInfo { return snapshot("t1", proto.TaskStateRunning) },
		func(conn *websocket.Conn, r *http.Request) {
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			var seq int64
			for {
				select {
				case <-r.Context().Done():
					return
				case <-ticker.C:
					seq++
					if err := writeWSEventE(conn, r.Context(), proto.Event{
						Seq: seq, TaskID: "t1", Type: proto.EventTypeProgress,
					}); err != nil {
						return
					}
				}
			}
		})
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := New(ts.URL, "").WaitArchived(ctx, "t1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v want DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("总时限被帧重置，elapsed=%v", elapsed)
	}
}

func TestWaitArchivedReplaysArchiveBetweenSnapshotAndSubscribe(t *testing.T) {
	snapshotRead := make(chan struct{})
	allowWS := make(chan struct{})
	archived := proto.Event{Seq: 11, TaskID: "t1", Type: proto.EventTypeArchived,
		Payload: json.RawMessage(`{"note":"race"}`)}
	ts := newWaitArchivedServer(t,
		func() *AttachInfo {
			select {
			case <-snapshotRead:
			default:
				close(snapshotRead)
			}
			return snapshot("t1", proto.TaskStateRunning,
				proto.Event{Seq: 10, TaskID: "t1", Type: proto.EventTypeProgress})
		},
		func(conn *websocket.Conn, r *http.Request) {
			<-allowWS
			if got := r.URL.Query().Get("from_seq"); got != "10" {
				t.Errorf("from_seq=%s want 10", got)
				return
			}
			writeWSEvent(t, conn, r.Context(), archived)
		})
	go func() { <-snapshotRead; close(allowWS) }()
	got, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
	if err != nil || got.Seq != 11 {
		t.Fatalf("got=%+v err=%v", got, err)
	}
}

func TestWaitArchivedReconnectsThenFindsArchived(t *testing.T) {
	var wsCalls atomic.Int32
	archived := proto.Event{Seq: 2, TaskID: "t1", Type: proto.EventTypeArchived}
	ts := newWaitArchivedServer(t,
		func() *AttachInfo { return snapshot("t1", proto.TaskStateRunning) },
		func(conn *websocket.Conn, r *http.Request) {
			if wsCalls.Add(1) == 1 {
				_ = conn.Close(websocket.StatusGoingAway, "restart")
				return
			}
			writeWSEvent(t, conn, r.Context(), archived)
		})
	cli := NewWithWSTiming(ts.URL, "", time.Millisecond, 5*time.Millisecond, time.Millisecond)
	got, err := cli.WaitArchived(t.Context(), "t1")
	if err != nil || got.Seq != 2 || wsCalls.Load() < 2 {
		t.Fatalf("got=%+v err=%v wsCalls=%d", got, err, wsCalls.Load())
	}
}

func TestWaitArchivedPermanent401DoesNotRetry(t *testing.T) {
	var calls atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "bad token", http.StatusUnauthorized)
	}))
	t.Cleanup(ts.Close)
	_, err := NewWithWSTiming(ts.URL, "bad", time.Millisecond,
		5*time.Millisecond, time.Millisecond).WaitArchived(t.Context(), "t1")
	if err == nil || !isPermanent(err) || calls.Load() != 1 {
		t.Fatalf("err=%v permanent=%v calls=%d", err, isPermanent(err), calls.Load())
	}
}

func TestWaitArchivedRetriesTransientAttachFailure(t *testing.T) {
	var attaches atomic.Int32
	archived := proto.Event{Seq: 4, TaskID: "t1", Type: proto.EventTypeArchived}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tasks/t1" {
			if attaches.Add(1) == 1 {
				http.Error(w, "temporary", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(snapshot("t1", proto.TaskStateRunning))
			return
		}
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		defer conn.CloseNow()
		writeWSEvent(t, conn, r.Context(), archived)
	}))
	t.Cleanup(func() { ts.CloseClientConnections(); ts.Close() })
	cli := NewWithWSTiming(ts.URL, "", time.Millisecond, 5*time.Millisecond, time.Millisecond)
	got, err := cli.WaitArchived(t.Context(), "t1")
	if err != nil || got.Seq != 4 || attaches.Load() != 2 {
		t.Fatalf("got=%+v err=%v attaches=%d", got, err, attaches.Load())
	}
}

func TestWaitArchivedNormalCloseWithoutEventRechecksSnapshot(t *testing.T) {
	var attaches atomic.Int32
	archived := proto.Event{Seq: 3, TaskID: "t1", Type: proto.EventTypeArchived}
	ts := newWaitArchivedServer(t,
		func() *AttachInfo {
			if attaches.Add(1) == 1 {
				return snapshot("t1", proto.TaskStateRunning)
			}
			return snapshot("t1", proto.TaskStateCompleted, archived)
		},
		func(conn *websocket.Conn, _ *http.Request) {
			_ = conn.Close(websocket.StatusNormalClosure, "task archived")
		})
	got, err := New(ts.URL, "").WaitArchived(t.Context(), "t1")
	if err != nil || got.Seq != 3 || attaches.Load() < 2 {
		t.Fatalf("got=%+v err=%v attaches=%d", got, err, attaches.Load())
	}
}

// TestWaitArchivedRepeatedNormalCloseBacksOff 钉住「正常关闭复查」这条路径也必须退避。
//
// 为什么需要它：errArchived（对端 1000 正常关闭）在门闩里不是成功证据，要回查
// 快照才放行。若这条 continue 不走退避，一个仍可达、却反复以 1000 关闭而任务
// 始终非终态的 agentd，会把门闩变成 Attach + 拨号的紧循环——压垮对端且刷爆日志，
// 而门闩表面上还在「正常等待」。既有的 NormalCloseWithoutEventRechecksSnapshot
// 只复查一次就拿到 archived，覆盖不到重复情形。
func TestWaitArchivedRepeatedNormalCloseBacksOff(t *testing.T) {
	var attaches atomic.Int32
	ts := newWaitArchivedServer(t,
		func() *AttachInfo {
			attaches.Add(1)
			// 永远非终态：正常关闭之后回查也拿不到 archived，门闩必须继续等
			return snapshot("t1", proto.TaskStateRunning)
		},
		func(conn *websocket.Conn, _ *http.Request) {
			_ = conn.Close(websocket.StatusNormalClosure, "task archived")
		})

	// 退避 50ms 封顶、稳定门槛设得极高（本轮连接必然算「不健康」，退避才会倍增）
	c := NewWithWSTiming(ts.URL, "", 50*time.Millisecond, 50*time.Millisecond, time.Hour)
	ctx, cancel := context.WithTimeout(t.Context(), 400*time.Millisecond)
	defer cancel()

	_, err := c.WaitArchived(ctx, "t1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应因总时限到而结束，实得 %v", err)
	}
	// 400ms / 50ms = 8 轮上限，留一倍余量到 16。不退避时这里是几百上千。
	if n := attaches.Load(); n > 16 {
		t.Fatalf("正常关闭复查没有退避：400ms 内取了 %d 次快照（上限 16）", n)
	}
	if attaches.Load() < 2 {
		t.Fatalf("至少应复查一次，实得 %d", attaches.Load())
	}
}
