// peer client 测试：HTTP 错误映射与 deadline。
//
// 职责：
//   - 401/403 → AUTH_FAILED
//   - 网络错误 → unavailable
//   - Hello/EventsAfter 的请求路径与解码
//
// 边界：
//   - 使用 httptest 模拟远端，不发起真实网络
package peer

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

func TestClientProxiesPtyLifecycleAndBidirectionalStream(t *testing.T) {
	session := desktopapi.PtySessionDTO{TerminalSessionID: "term-1", Incarnation: "inc-1",
		WorkspaceID: "ws-1", State: "active", Shell: "/bin/zsh", ThroughSeq: 1}
	streamInput := make(chan desktopapi.PtyClientFrameDTO, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer remote-token" {
			t.Fatalf("auth = %q", r.Header.Get("Authorization"))
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/workspaces/ws-1/terminals":
			writeTestJSON(w, session)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/terminals/term-1":
			writeTestJSON(w, session)
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/terminals/term-1":
			if r.URL.Query().Get("incarnation") != "inc-1" {
				t.Errorf("close incarnation = %q", r.URL.Query().Get("incarnation"))
			}
			ended := session
			ended.State = "ended"
			writeTestJSON(w, ended)
		case r.Method == http.MethodGet && r.URL.Path == "/v1/terminals/term-1/stream":
			if r.URL.Query().Get("incarnation") != "inc-1" || r.URL.Query().Get("after") != "0" {
				t.Errorf("stream query = %v", r.URL.Query())
			}
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept PTY websocket: %v", err)
				return
			}
			defer conn.CloseNow()
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
				TerminalSessionID: "term-1", Incarnation: "inc-1", WorkspaceID: "ws-1", ThroughSeq: 1, State: "active",
				Capabilities: workspaceapi.DefaultPtyCapabilities()})
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "data",
				TerminalSessionID: "term-1", Incarnation: "inc-1", WorkspaceID: "ws-1",
				Seq: 1, ThroughSeq: 1, DataBase64: "cmVwbGF5"})
			_, raw, err := conn.Read(r.Context())
			if err != nil {
				t.Errorf("read PTY input: %v", err)
				return
			}
			var input desktopapi.PtyClientFrameDTO
			if err := json.Unmarshal(raw, &input); err != nil {
				t.Errorf("decode PTY input: %v", err)
				return
			}
			streamInput <- input
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "data",
				TerminalSessionID: "term-1", Incarnation: "inc-1", WorkspaceID: "ws-1",
				Seq: 2, ThroughSeq: 2, DataBase64: "bGl2ZQ=="})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()

	client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
	workspace := workspaceapi.WorkspaceRef{WorkspaceID: "ws-1"}
	created, err := client.CreateTerminal(context.Background(), workspace,
		workspaceapi.CreateTerminalCommand{CommandID: "command-1", Cols: 120, Rows: 40})
	if err != nil || created.TerminalSessionID != "term-1" {
		t.Fatalf("CreateTerminal = %+v, %v", created, err)
	}
	if got, err := client.GetTerminal(context.Background(), "term-1"); err != nil || got.WorkspaceID != "ws-1" {
		t.Fatalf("GetTerminal = %+v, %v", got, err)
	}
	subscription, err := client.ConnectTerminal(context.Background(), "term-1", "inc-1", 0)
	if err != nil {
		t.Fatalf("ConnectTerminal: %v", err)
	}
	defer subscription.Cancel()
	if len(subscription.Replay) != 1 || subscription.Replay[0].Seq != 1 {
		t.Fatalf("replay = %+v", subscription.Replay)
	}
	input := workspaceapi.PtyClientFrame{Version: 1, Kind: workspaceapi.PtyClientFrameInput,
		TerminalSessionID: "term-1", Incarnation: "inc-1", DataBase64: "ZWNobyBoaQ=="}
	if err := subscription.Send(context.Background(), input); err != nil {
		t.Fatalf("Send: %v", err)
	}
	select {
	case got := <-streamInput:
		if got.DataBase64 != input.DataBase64 || got.Kind != "input" {
			t.Fatalf("wire input = %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("远端未收到 PTY input")
	}
	select {
	case frame := <-subscription.Events:
		if frame.Seq != 2 || frame.DataBase64 != "bGl2ZQ==" {
			t.Fatalf("live frame = %+v", frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到 PTY live frame")
	}
	if closed, err := client.CloseTerminal(context.Background(), "term-1", "inc-1"); err != nil || closed.State != workspaceapi.PtyStateEnded {
		t.Fatalf("CloseTerminal = %+v, %v", closed, err)
	}
}

func TestClientPreservesPtyCursorExpiredSnapshot(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept PTY websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
			TerminalSessionID: "term-expired", Incarnation: "inc-expired", WorkspaceID: "ws-expired",
			ThroughSeq: 3, State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "problem",
			TerminalSessionID: "term-expired", Incarnation: "inc-expired", WorkspaceID: "ws-expired", ThroughSeq: 3,
			Problem: &desktopapi.Problem{Code: desktopapi.ProblemCursorExpired, Message: "expired"}})
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "snapshot",
			TerminalSessionID: "term-expired", Incarnation: "inc-expired", WorkspaceID: "ws-expired", Seq: 3, ThroughSeq: 3,
			DataBase64: "Ym91bmRlZA=="})
		_, _, _ = conn.Read(r.Context())
	}))
	defer ts.Close()
	client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "token"})
	subscription, err := client.ConnectTerminal(context.Background(), "term-expired", "inc-expired", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	if !subscription.CursorExpired || subscription.Snapshot == nil ||
		subscription.Snapshot.ThroughSeq != 3 || subscription.Snapshot.DataBase64 != "Ym91bmRlZA==" ||
		len(subscription.Replay) != 0 {
		t.Fatalf("cursor recovery = expired:%t snapshot:%+v replay:%+v",
			subscription.CursorExpired, subscription.Snapshot, subscription.Replay)
	}
}

func TestClientRejectsPtyReplayWorkspaceDrift(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept PTY websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
			TerminalSessionID: "term-1", Incarnation: "inc-1", WorkspaceID: "ws-1", ThroughSeq: 1,
			State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "data",
			TerminalSessionID: "term-1", Incarnation: "inc-1", WorkspaceID: "ws-other",
			Seq: 1, ThroughSeq: 1, DataBase64: "bGVhaw=="})
	}))
	defer ts.Close()
	client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
	_, err := client.ConnectTerminal(context.Background(), "term-1", "inc-1", 0)
	if err == nil || !strings.Contains(err.Error(), "workspace 漂移") {
		t.Fatalf("workspace drift error = %v", err)
	}
}

func TestClientRejectsNonMonotonicPtyReplay(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept PTY websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
			TerminalSessionID: "term-1", Incarnation: "inc-1", WorkspaceID: "ws-1", ThroughSeq: 2,
			State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
		for range 2 {
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "data",
				TerminalSessionID: "term-1", Incarnation: "inc-1", WorkspaceID: "ws-1",
				Seq: 1, ThroughSeq: 1, DataBase64: "eA=="})
		}
	}))
	defer ts.Close()
	client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
	_, err := client.ConnectTerminal(context.Background(), "term-1", "inc-1", 0)
	if err == nil || !strings.Contains(err.Error(), "seq 非严格单调") {
		t.Fatalf("non-monotonic replay error = %v", err)
	}
}

func TestClientRejectsReplayDataWithLargeRedundantFields(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept PTY websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
			TerminalSessionID: "term-replay-fields", Incarnation: "inc-replay-fields", WorkspaceID: "ws-replay-fields",
			ThroughSeq: 1, State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "data",
			TerminalSessionID: "term-replay-fields", Incarnation: "inc-replay-fields", WorkspaceID: "ws-replay-fields",
			Seq: 1, ThroughSeq: 1, DataBase64: "eA==",
			Capabilities: map[string]int{strings.Repeat("padding", 24_000): 1}})
	}))
	defer ts.Close()
	client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
	_, err := client.ConnectTerminal(context.Background(), "term-replay-fields", "inc-replay-fields", 0)
	if err == nil || !strings.Contains(err.Error(), "replay data frame 字段非法") {
		t.Fatalf("redundant replay fields error = %v", err)
	}
}

func TestClientRejectsInvalidSubscribedCursorAndLiveStateRegression(t *testing.T) {
	t.Run("subscribed cursor behind requested cursor", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept PTY websocket: %v", err)
				return
			}
			defer conn.CloseNow()
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
				TerminalSessionID: "term-cursor", Incarnation: "inc-cursor", WorkspaceID: "ws-cursor",
				ThroughSeq: 1, State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
		}))
		defer ts.Close()
		client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
		_, err := client.ConnectTerminal(context.Background(), "term-cursor", "inc-cursor", 2)
		if err == nil || !strings.Contains(err.Error(), "subscribed frame 字段非法") {
			t.Fatalf("subscribed cursor error = %v", err)
		}
	})

	t.Run("active status cannot regress to starting", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept PTY websocket: %v", err)
				return
			}
			defer conn.CloseNow()
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
				TerminalSessionID: "term-state", Incarnation: "inc-state", WorkspaceID: "ws-state",
				State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "status",
				TerminalSessionID: "term-state", Incarnation: "inc-state", WorkspaceID: "ws-state",
				State: "starting"})
		}))
		defer ts.Close()
		client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
		subscription, err := client.ConnectTerminal(context.Background(), "term-state", "inc-state", 0)
		if err != nil {
			t.Fatal(err)
		}
		defer subscription.Cancel()
		if streamErr := <-subscription.Done; streamErr == nil || !strings.Contains(streamErr.Error(), "status frame 字段非法") {
			t.Fatalf("live state regression error = %v", streamErr)
		}
	})
}

func TestClientRejectsGapInLivePtyFrames(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept PTY websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
			TerminalSessionID: "term-live", Incarnation: "inc-live", WorkspaceID: "ws-live",
			ThroughSeq: 0, State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "data",
			TerminalSessionID: "term-live", Incarnation: "inc-live", WorkspaceID: "ws-live",
			Seq: 2, ThroughSeq: 2, DataBase64: "Z2Fw"})
		<-r.Context().Done()
	}))
	defer ts.Close()
	client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
	subscription, err := client.ConnectTerminal(context.Background(), "term-live", "inc-live", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	select {
	case streamErr := <-subscription.Done:
		if streamErr == nil || !strings.Contains(streamErr.Error(), "live seq 非连续") {
			t.Fatalf("live gap error = %v", streamErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("live PTY gap 未中断订阅")
	}
}

func TestClientAcceptsMaximumRingSnapshotOverWebSocket(t *testing.T) {
	snapshot := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("x"), 4<<20))
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept PTY websocket: %v", err)
			return
		}
		defer conn.CloseNow()
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
			TerminalSessionID: "term-snapshot", Incarnation: "inc-snapshot", WorkspaceID: "ws-snapshot",
			ThroughSeq: 9, State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "problem",
			TerminalSessionID: "term-snapshot", Incarnation: "inc-snapshot", WorkspaceID: "ws-snapshot",
			ThroughSeq: 9, Problem: &desktopapi.Problem{Code: desktopapi.ProblemCursorExpired}})
		writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "snapshot",
			TerminalSessionID: "term-snapshot", Incarnation: "inc-snapshot", WorkspaceID: "ws-snapshot",
			Seq: 9, ThroughSeq: 9, DataBase64: snapshot})
	}))
	defer ts.Close()
	client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
	subscription, err := client.ConnectTerminal(context.Background(), "term-snapshot", "inc-snapshot", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer subscription.Cancel()
	if !subscription.CursorExpired || subscription.Snapshot == nil ||
		len(subscription.Snapshot.DataBase64) != len(snapshot) {
		t.Fatalf("maximum snapshot = expired:%t snapshot-bytes:%d",
			subscription.CursorExpired, len(subscription.Snapshot.DataBase64))
	}
}

func TestClientStopsLiveStreamAtExitAndRejectsMalformedTerminalFrames(t *testing.T) {
	t.Run("malformed exit", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept PTY websocket: %v", err)
				return
			}
			defer conn.CloseNow()
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
				TerminalSessionID: "term-malformed", Incarnation: "inc-malformed", WorkspaceID: "ws-malformed",
				State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "exit",
				TerminalSessionID: "term-malformed", Incarnation: "inc-malformed", WorkspaceID: "ws-malformed",
				State: "active"})
		}))
		defer ts.Close()
		client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
		subscription, err := client.ConnectTerminal(context.Background(), "term-malformed", "inc-malformed", 0)
		if err != nil {
			t.Fatal(err)
		}
		defer subscription.Cancel()
		if streamErr := <-subscription.Done; streamErr == nil || !strings.Contains(streamErr.Error(), "exit frame 字段非法") {
			t.Fatalf("malformed exit error = %v", streamErr)
		}
	})

	t.Run("exit is terminal", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept PTY websocket: %v", err)
				return
			}
			defer conn.CloseNow()
			exitCode := 0
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "subscribed",
				TerminalSessionID: "term-exit", Incarnation: "inc-exit", WorkspaceID: "ws-exit",
				State: "active", Capabilities: workspaceapi.DefaultPtyCapabilities()})
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "exit",
				TerminalSessionID: "term-exit", Incarnation: "inc-exit", WorkspaceID: "ws-exit",
				State: "ended", ExitCode: &exitCode})
			writePtyTestFrame(t, conn, desktopapi.PtyServerFrameDTO{Version: 1, Kind: "data",
				TerminalSessionID: "term-exit", Incarnation: "inc-exit", WorkspaceID: "ws-exit",
				Seq: 1, ThroughSeq: 1, DataBase64: "bGF0ZQ=="})
		}))
		defer ts.Close()
		client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-token"})
		subscription, err := client.ConnectTerminal(context.Background(), "term-exit", "inc-exit", 0)
		if err != nil {
			t.Fatal(err)
		}
		defer subscription.Cancel()
		frame := <-subscription.Events
		if frame.Kind != workspaceapi.PtyFrameExit {
			t.Fatalf("terminal frame = %+v", frame)
		}
		if streamErr := <-subscription.Done; streamErr != nil {
			t.Fatalf("exit done = %v", streamErr)
		}
		if _, ok := <-subscription.Events; ok {
			t.Fatal("exit 后仍接受 live frame")
		}
	})
}

func writePtyTestFrame(t *testing.T, conn *websocket.Conn, frame desktopapi.PtyServerFrameDTO) {
	t.Helper()
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Write(context.Background(), websocket.MessageText, raw); err != nil {
		t.Errorf("write PTY frame: %v", err)
	}
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

// TestClientHelloSuccess 验证 Hello 请求成功并解码。
func TestClientHelloSuccess(t *testing.T) {
	var called atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called.Add(1)
		if r.URL.Path != "/v1/peer/hello" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("缺 Authorization 头")
		}
		json.NewEncoder(w).Encode(Hello{ProtocolVersion: 1, Capabilities: map[string]int{"catalog": 1}})
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{Endpoint: ts.URL, Token: "secret-token"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	h, err := c.Hello(ctx)
	if err != nil {
		t.Fatalf("Hello: %v", err)
	}
	if h.ProtocolVersion != 1 || h.Capabilities["catalog"] != 1 {
		t.Fatalf("hello = %+v", h)
	}
	if called.Load() != 1 {
		t.Fatalf("请求次数 = %d, want 1", called.Load())
	}
}

// TestClientHelloAuthFailed 验证 401 映射为 ErrAuthFailed。
func TestClientHelloAuthFailed(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{Endpoint: ts.URL, Token: "wrong"})
	if _, err := c.Hello(context.Background()); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
}

// TestClientHelloNetworkError 验证网络错误映射为 ErrUnavailable。
func TestClientHelloNetworkError(t *testing.T) {
	c := NewClient(ClientConfig{Endpoint: "http://127.0.0.1:1", Token: "t"}) // 无监听端口
	if _, err := c.Hello(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// TestClientEventsAfterWire 验证 events-after 的请求与解码。
func TestClientEventsAfterWire(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("machine_id") != "m1" || r.URL.Query().Get("after") != "5" {
			t.Errorf("query = %v", r.URL.Query())
		}
		json.NewEncoder(w).Encode([]MachineEvent{{MachineID: "m1", MachineSeq: 6, EventID: "e6"}})
	}))
	defer ts.Close()

	c := NewClient(ClientConfig{Endpoint: ts.URL, Token: "t"})
	evs, err := c.EventsAfter(context.Background(), "m1", 5, 100)
	if err != nil {
		t.Fatalf("EventsAfter: %v", err)
	}
	if len(evs) != 1 || evs[0].MachineSeq != 6 {
		t.Fatalf("events = %+v", evs)
	}
}

func TestClientProxiesWorkspaceFileWithoutLeakingTokenInPayload(t *testing.T) {
	var gotBody []byte
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workspaces/ws-1/file" || r.Header.Get("Authorization") != "Bearer remote-secret" {
			t.Fatalf("request path/auth = %s / %q", r.URL.Path, r.Header.Get("Authorization"))
		}
		gotBody, _ = io.ReadAll(r.Body)
		write := desktopapi.FileDocumentDTO{WorkspaceID: "ws-1", Path: "README.md", Version: "sha256:new", ContentBase64: "bmV3", Size: 3, ModifiedAt: time.Now().UTC()}
		_ = json.NewEncoder(w).Encode(write)
	}))
	defer ts.Close()
	client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "remote-secret"})
	doc, err := client.WriteFile(context.Background(), workspaceapi.WorkspaceRef{WorkspaceID: "ws-1"}, workspaceapi.WriteFileCommand{
		CommandID: "cmd-1", Path: "README.md", IfMatch: "sha256:old", ContentBase64: "bmV3",
	})
	if err != nil || doc.Version != "sha256:new" {
		t.Fatalf("WriteFile = %+v, %v", doc, err)
	}
	if bytes.Contains(gotBody, []byte("remote-secret")) {
		t.Fatalf("remote token 泄漏进请求体: %s", gotBody)
	}
}

func TestClientMapsRemoteResourceProblem(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(desktopapi.Problem{Code: desktopapi.ProblemVersionConflict, Message: "版本冲突"})
	}))
	defer ts.Close()
	client := NewClient(ClientConfig{Endpoint: ts.URL, Token: "token"})
	_, err := client.ReadFile(context.Background(), workspaceapi.WorkspaceRef{WorkspaceID: "ws"}, "README.md")
	var resourceErr *workspaceapi.Error
	if !errors.As(err, &resourceErr) || resourceErr.Code != workspaceapi.ErrorVersionConflict {
		t.Fatalf("error = %T %v", err, err)
	}
}
