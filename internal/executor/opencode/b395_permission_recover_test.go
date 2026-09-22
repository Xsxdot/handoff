package opencode

import (
	"context"
	"encoding/json"
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

// TestB395ReconnectRecoversPendingPermission 锁 spec §4.2/§4.5：SSE 断连重连后
// 必须重新发现并重放本任务未决的权限，使回合有限收口，而不是永挂等人工。
//
// 红（本节点亲跑，原始输出见台账）：重连只做回合对账，零 permission 事件，
// 5s 窗超时红。
// 绿（T1.3a）：重连后 GET /permission 命中 per_lost 并重放 permission 事件。
// 变异自验：注释掉 onReconnect 里的 rediscoverPendingPermissions 调用 → 复红。
//
// 缝：真实 subscribeLoop 的 onReconnect（生产重连路径），不是直接调内部方法——
// 判据钉在「重连这一动作之后用户能收到权限事件」，而不是「方法被调用过」。
func TestB395ReconnectRecoversPendingPermission(t *testing.T) {
	quietLog(t)
	var mu sync.Mutex
	conns := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/session":
			fmt.Fprint(w, `{"id":"sess-1"}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/prompt_async"):
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/permission":
			// 断连间隙新产生的挂起权限（本会话一条 + 别的会话一条，验证过滤）
			fmt.Fprint(w, `[
				{"id":"per_lost","sessionID":"sess-1","permission":"bash","patterns":["ls"],
				 "metadata":{"command":"ls"},"tool":{"messageID":"m","callID":"c"}},
				{"id":"per_other","sessionID":"ses_other","permission":"bash","patterns":[],
				 "metadata":{"command":"whoami"},"tool":{"messageID":"m2","callID":"c2"}}]`)
		case r.Method == http.MethodGet && r.URL.Path == "/session/sess-1/message":
			fmt.Fprint(w, `[]`) // 对账无终态，不干扰权限断言
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
				return // 断流：触发重连
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
	taskID := "task-b395-reconnect"
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
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type != "permission" {
				continue
			}
			if ev.PermissionID != "per_lost" {
				t.Fatalf("只应重放本会话的 per_lost，实得 %q（别的会话的权限不得串台）",
					ev.PermissionID)
			}
			if !strings.Contains(ev.Text, "ls") {
				t.Fatalf("重放权限描述应含命令 ls，实得 %q", ev.Text)
			}
			return // 绿
		case <-deadline:
			t.Fatal("重连后未重新发现挂起权限 per_lost（B395 红：权限丢失后回合永挂）")
		}
	}
}

// TestB395RediscoverFiltersNonTaskSession 锁归属过滤：GET /permission 返回别的
// 会话的挂起权限时不得重放（与实时路径 acceptForeign 同规则）。
//
// 本用例入口是未导出方法 rediscoverPendingPermissions——属**内部锁**，理由见
// plan §7 占位符扫描自我声明（从声明缝可构造，但会与上一条重复起同一张假 server；
// 本条只作附加，不顶替上一条缝级断言）。
func TestB395RediscoverFiltersNonTaskSession(t *testing.T) {
	quietLog(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/permission" {
			fmt.Fprint(w, `[
				{"id":"per_other","sessionID":"ses_other","permission":"bash","patterns":[],
				 "metadata":{"command":"whoami"},"tool":{"messageID":"m2","callID":"c2"}}]`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")

	a.rediscoverPendingPermissions(context.Background(), "task-1")
	// 只盯 permission 事件：acceptForeign 对陌生会话会另发一条「认亲失败」的
	// progress 工单（可观测性，不是重放）；本用例锁的是「不得重放权限」
	for {
		ev, ok := drainOne(r)
		if !ok {
			return // 没有 permission 事件，过滤生效
		}
		if ev.Type == "permission" {
			t.Fatalf("别的会话的挂起权限不应重放，却收到 %+v", ev)
		}
	}
}

// TestPendingPermissionAskedPropsRoundTrip 锁序列化边界（五项检查 #2）：PendingPermission
// 经 permissionAskedProps 编码成 SSE properties，再经 mapPermissionAsked 解码，
// 描述必须与实时路径同形（metadata.command / patterns / 无描述兜底三条真实形态），
// 且不 panic。入口是编码函数 permissionAskedProps（内部锁，声明见 plan §7），跑的是
// **真实序列化边界**。
func TestPendingPermissionAskedPropsRoundTrip(t *testing.T) {
	quietLog(t)
	a := New(nil)
	r := a.newRun("task-1", t.TempDir(), t.TempDir())
	r.session = "ses_a"
	r.approval = nil // 走 a.emit，便于 drainOne 读事件

	cases := []struct {
		name    string
		perm    PendingPermission
		wantID  string
		wantTxt string
	}{
		{
			name: "metadata.command 存在（真实 perm_bash 形态）",
			perm: PendingPermission{
				ID: "per_1", SessionID: "ses_a", Permission: "bash",
				Patterns: []string{"ls"}, Metadata: json.RawMessage(`{"command":"ls -la"}`),
				Tool: PendingPermissionTool{MessageID: "m", CallID: "c"},
			},
			wantID: "per_1", wantTxt: "bash: ls -la",
		},
		{
			name: "metadata 缺失 → 退回 patterns（区分缺失与零值）",
			perm: PendingPermission{
				ID: "per_2", SessionID: "ses_a", Permission: "edit",
				Patterns: []string{"probe.md"}, Metadata: nil,
				Tool: PendingPermissionTool{MessageID: "m2", CallID: "c2"},
			},
			wantID: "per_2", wantTxt: "edit: probe.md",
		},
		{
			name: "metadata 为空对象 → 结构提取不出，仍带兜底描述（不 panic）",
			perm: PendingPermission{
				ID: "per_3", SessionID: "ses_a", Permission: "",
				Patterns: nil, Metadata: json.RawMessage(`{}`),
				Tool: PendingPermissionTool{},
			},
			wantID: "per_3", wantTxt: "opencode 未提供权限描述（id per_3），请 handoff attach 查看现场",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			props := permissionAskedProps(tc.perm)
			r.turnMu.Lock()
			a.mapPermissionAsked(r, props)
			r.turnMu.Unlock()
			ev, ok := drainOne(r)
			if !ok {
				t.Fatal("未产出 permission 事件")
			}
			if ev.Type != "permission" || ev.PermissionID != tc.wantID {
				t.Fatalf("事件 = %+v，want permission/%s", ev, tc.wantID)
			}
			if ev.Text != tc.wantTxt {
				t.Fatalf("描述 = %q，want %q", ev.Text, tc.wantTxt)
			}
		})
	}
}

// TestPendingPermissionPropsProjection 锁序列化边界的关键投影性质：
//   - metadata 缺失（len==0）时 permissionAskedProps **不得**写出 metadata 键，
//     否则消费侧无法区分「服务端没给」与「给了空对象」；
//   - metadata 非空时必须原样带出（不得丢字段）；
//   - patterns 缺失与空切片都编码成数组（消费侧同义），id/tool 键逐字保留。
//
// 为什么不断言 json.RawMessage 的 nil/零值往返：encoding/json 把 nil RawMessage
// 编码成字面量 null、解码回 []byte("null") 而非 nil，这是标准库既定行为，不是本
// 卡要锁的性质。真正承重的是「producer 是否写出该键」（上方三条），消费侧对
// 「键缺失 vs 空对象」的区分由 TestPendingPermissionAskedPropsRoundTrip 的三个
// 用例覆盖（缺失→退回 patterns；空对象→无描述兜底）。
func TestPendingPermissionPropsProjection(t *testing.T) {
	noMeta := permissionAskedProps(PendingPermission{
		ID: "per_2", SessionID: "ses_a", Permission: "edit",
		Patterns: []string{"probe.md"}, Metadata: nil,
	})
	if strings.Contains(string(noMeta), `"metadata"`) {
		t.Fatalf("metadata 缺失时不得写出该键，实得 %s", noMeta)
	}
	if !strings.Contains(string(noMeta), `"callID"`) {
		t.Fatalf("tool 键必须保留，实得 %s", noMeta)
	}

	withMeta := permissionAskedProps(PendingPermission{
		ID: "per_1", SessionID: "ses_a", Permission: "bash",
		Patterns: []string{"ls"}, Metadata: json.RawMessage(`{"command":"ls -la"}`),
		Tool: PendingPermissionTool{MessageID: "m", CallID: "c"},
	})
	var back map[string]any
	if err := json.Unmarshal(withMeta, &back); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if back["id"] != "per_1" || back["sessionID"] != "ses_a" || back["permission"] != "bash" {
		t.Fatalf("关键字段丢失: %v", back)
	}
	md, ok := back["metadata"].(map[string]any)
	if !ok || md["command"] != "ls -la" {
		t.Fatalf("metadata 未原样带出: %v", back["metadata"])
	}
	tool, ok := back["tool"].(map[string]any)
	if !ok || tool["messageID"] != "m" || tool["callID"] != "c" {
		t.Fatalf("tool 未原样带出: %v", back["tool"])
	}

	// PendingPermission 自身能被 GET /permission 的真实响应解析（perm_bash 形态）。
	var p PendingPermission
	raw := []byte(`{"id":"per_bash","sessionID":"ses_x","permission":"bash",
		"patterns":["ls"],"metadata":{"command":"ls"},"always":["ls *"],
		"tool":{"messageID":"m","callID":"c"}}`)
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatalf("解析真实响应失败: %v", err)
	}
	if p.ID != "per_bash" || p.Tool.CallID != "c" || string(p.Metadata) == "" {
		t.Fatalf("真实响应字段丢失: %+v", p)
	}
}
