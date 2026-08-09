// agentd control_stream 测试：桌面控制流的订阅与重放。
//
// 职责：
//   - WS /v1/control/stream?after=<revision> 先订阅再补发
//   - 无窗口竞态：先取列表再连流不丢事件
//   - 重复去重、慢客户端有界断开、cursor 参数校验
//
// 边界：
//   - 使用真实 store + httptest WS
//   - 使用独立的 ControlHub 与 durable control_events，不复用 task Hub
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
)

// TestControlStreamCursorValidation 验证 after 参数校验。
func TestControlStreamCursorValidation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: testToken}
	srv := NewServer(cfg, st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	srv.SetProjectService(controlplane.NewProjectService(st, nilCommander{}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// 非法 after（负数）→ 400
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/control/stream?after=-1", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("负数 after status = %d, want 400", resp.StatusCode)
	}
}

// TestControlStreamSubscribeAndReplay 验证订阅后能收到事件（重放）。
func TestControlStreamSubscribeAndReplay(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: testToken}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := NewServer(cfg, st, logger)
	srv.SetProjectService(controlplane.NewProjectService(st, nilCommander{}, logger))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// 先制造一条 control event：投影一个 workspace
	ws := controlplane.Workspace{ID: "ws1", MachineID: "m1", Kind: controlplane.WorkspaceKindMain,
		Path: "/r", CanonicalPath: "/r"}
	payload, _ := json.Marshal(ws)
	ev := controlplane.MachineEvent{
		MachineID: "m1", EventID: "evt-1", Kind: controlplane.MachineEventWorkspaceUpsert,
		ResourceID: "ws1", Payload: payload,
	}
	if _, applied, err := st.ApplyMachineEvent(bg(), ev); err != nil || !applied {
		t.Fatalf("ApplyMachineEvent: applied=%v err=%v", applied, err)
	}
	rev, _ := st.LatestControlRevision(bg())

	// 订阅 after=0 → 应重放 1 条
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/control/stream?after=0"
	conn, _, err := websocket.Dial(bg(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + testToken}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)
	_, raw, err := conn.Read(bg())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var envelope struct {
		Revision   int64           `json:"revision"`
		Kind       string          `json:"kind"`
		ResourceID string          `json:"resource_id"`
		Payload    json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if envelope.Revision != rev {
		t.Fatalf("revision = %d, want %d", envelope.Revision, rev)
	}
	if envelope.Kind != "workspace.upsert" {
		t.Fatalf("kind = %q", envelope.Kind)
	}
}

// ctx 返回带超时的测试上下文。
func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 10*time.Second)
}

// bg 返回无取消的测试上下文。
func bg() context.Context { return context.Background() }
