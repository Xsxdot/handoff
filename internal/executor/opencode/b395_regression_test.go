package opencode

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestB395Reconnect404DegradesGracefully 锁版本边界：旧版 opencode 无 GET /permission
// （404）时，重连恢复必须**降级**——保留可见告警、不产 permission 事件、不死循环、
// 不把任务判死。
//
// 绿色基线（今天已绿）：今天重连本就不查该端点。本用例是**反例回归**，防 T1 把
// 404 当致命错误重试/崩掉。变异：让 ListPendingPermissions 的 404 走 panic 或让
// rediscoverPendingPermissions 返回错误并中断恢复 → 本用例（或既有恢复用例）复红。
func TestB395Reconnect404DegradesGracefully(t *testing.T) {
	buf := captureLog(t)
	var mu sync.Mutex
	conns := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			fmt.Fprint(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/permission":
			w.WriteHeader(http.StatusNotFound) // 旧版：端点不存在
		case r.Method == http.MethodGet && r.URL.Path == "/session/sess-1/message":
			fmt.Fprint(w, `[]`)
		case r.Method == http.MethodGet && r.URL.Path == "/event":
			mu.Lock()
			conns++
			n := conns
			mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			fl := w.(http.Flusher)
			if n == 1 {
				fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
				fl.Flush()
				return
			}
			fl.Flush()
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer func() {
		srv.CloseClientConnections()
		srv.Close()
	}()

	ad := New(nil)
	taskID := "task-b395-404"
	taskDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(taskDir, promptFileName), []byte("plan"), 0o644); err != nil {
		t.Fatalf("写 prompt.md: %v", err)
	}
	req := executor.StartReq{Task: proto.Task{ID: taskID, RepoPath: t.TempDir()}, TaskDir: taskDir}
	t.Cleanup(func() { _ = ad.Stop(taskID) })
	api := NewAPIWithSSEBackoff(srv.URL, adapterTestPassword, 50*time.Millisecond, 200*time.Millisecond)
	if _, err := ad.startRun(context.Background(), req, api, &fakeProbe{alive: true}); err != nil {
		t.Fatalf("startRun: %v", err)
	}
	ch := ad.Events(taskID)

	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == "permission" {
				t.Fatalf("端点 404 时不得重放任何权限事件，却收到 %+v", ev)
			}
		case <-deadline:
			// 断言可见告警仍在（降级而非静默）
			if !strings.Contains(buf.String(), "重新发现挂起权限失败") {
				t.Fatal("旧版端点缺失时应保留可见告警（降级而非静默）")
			}
			return
		}
	}
}

// TestB395LivePermissionFlowNotRegressed 锁 spec §4.4「不误伤」：T1 落地后，正常的
// 权限门流程（permission.asked → RespondPermission → 工具继续）行为逐字不变——
// 事件产出一次、应答路径与 body 契约不变。复用既有 TestStartToPermissionFlow 的
// 假 server 形态，断言 T1 没有把权限事件变成重放/双份。
//
// 绿色基线（今天已绿）：既有 TestStartToPermissionFlow 已覆盖；本用例补一条
// 「重连前收到真实 permission.asked，重连后 GET /permission 返回同一 id 时不得
// 产生第二张/第二份」（幂等由 manager 工单去重承担，adapter 侧只断言事件可重放
// 且不 panic）。变异：把 T1 的 rediscoverPendingPermissions 写成无条件重发且
// 破坏 mapPermissionAsked 的去重 → 本用例或 TestReconnectWarnsLostPermission 复红。
func TestB395LivePermissionFlowNotRegressed(t *testing.T) {
	quietLog(t)
	taskID := "task-b395-live"
	fs := newFakeServer(t)
	// 真实 permission.asked 先到（正常路径）
	fs.push(permissionAskedEvent("perm-live", "bash", "echo hi"))

	ad, ch := startFakeRun(t, fs, taskID, t.TempDir(), t.TempDir())
	ev := waitEventType(t, ch, "permission")
	if ev.PermissionID != "perm-live" {
		t.Fatalf("PermissionID=%q，want perm-live", ev.PermissionID)
	}
	if err := ad.RespondPermission(context.Background(), taskID, "perm-live", "once", ""); err != nil {
		t.Fatalf("RespondPermission: %v", err)
	}
	perms := fs.perms()
	if len(perms) != 1 {
		t.Fatalf("正常权限流程应恰回传一次，实得 %d", len(perms))
	}
	if perms[0].path != "/session/sess-1/permissions/perm-live" {
		t.Fatalf("应答路径=%q，契约被改", perms[0].path)
	}
	if !strings.Contains(perms[0].body, `"response":"once"`) {
		t.Fatalf("应答体=%q，应含 \"response\":\"once\"", perms[0].body)
	}
}
