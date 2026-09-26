// background_diag_test.go 锁 B409.6 (S4-U6) 的后台远端 worker 记账：unlinked
// 摘要与 room attach 的每轮刷新有独立 refresh_id（rf- 前缀），每个配置 target
// 有单独耗时/outcome/task_count；后台耗时不在触发请求的收口行里冒充请求等待。
package agentd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// withTargets 原地注入 target 配置（写时复制），供后台刷新记账测试使用。
func withTargets(t *testing.T, env *ledgerEnv, targets map[string]config.Target) {
	t.Helper()
	next := *env.srv.conf()
	next.Targets = targets
	env.srv.cfg.Store(&next)
	t.Cleanup(func() { _ = env.srv.CloseTargets() })
}

// recordsWithPrefix 返回含指定前缀属性值的记录数。
func recordsWithAttrPrefix(capture *diagLogCapture, key, prefix string) int {
	n := 0
	capture.mu.Lock()
	defer capture.mu.Unlock()
	for _, r := range capture.records {
		if v, ok := attrValue(r, key); ok && strings.HasPrefix(v.String(), prefix) {
			n++
		}
	}
	return n
}

// TestUnlinkedRefreshPerTargetAccounting 锁 U2 摘要刷新的逐台记账。
func TestUnlinkedRefreshPerTargetAccounting(t *testing.T) {
	capture := &diagLogCapture{}
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]proto.TaskView{{Task: proto.Task{
			ID: "t-ok", Name: "远端任务", State: proto.TaskStateRunning,
		}}})
	}))
	failing := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tasks" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	env := newDiagEnv(t, capture)
	withTargets(t, env, map[string]config.Target{
		"ok-target":   {Addr: strings.TrimPrefix(remote.URL, "http://"), Token: testToken},
		"fail-target": {Addr: strings.TrimPrefix(failing.URL, "http://"), Token: testToken},
	})
	defer env.srv.CloseTargets()

	if code, body := getCardsWithTimeout(t, env.testAgentdEnv, 300*time.Millisecond); code != http.StatusOK {
		t.Fatalf("触发刷新的 cards 请求: %d %s", code, body)
	}
	waitForUnlinkedStatusWithin(t, env, "partial", 15*time.Second)

	// 刷新开始/完成与每台 target 的结束记录都带 rf- 前缀的 refresh_id。
	start, ok := capture.find("未挂账摘要后台刷新开始")
	if !ok {
		t.Fatalf("缺刷新开始日志")
	}
	if v, ok := attrValue(start, "refresh_id"); !ok || !strings.HasPrefix(v.String(), "rf-") {
		t.Fatalf("刷新开始缺 refresh_id")
	}
	done, ok := capture.find("未挂账摘要后台刷新完成")
	if !ok {
		t.Fatalf("缺刷新完成日志")
	}
	if v, ok := attrValue(done, "refresh_id"); !ok || !strings.HasPrefix(v.String(), "rf-") {
		t.Fatalf("刷新完成缺 refresh_id")
	}

	// 逐台记账：ok-target success、fail-target error，各自有耗时与 task_count。
	okRec, ok := capture.find("未挂账 target 读取结束")
	_ = okRec
	_ = ok
	outcomes := map[string]string{}
	for _, r := range capture.records {
		if r.Message != "未挂账 target 读取结束" {
			continue
		}
		attrs := recordAttrs(r)
		name := attrs["target"]
		if attrs["target_call_ns"] == "" || attrs["outcome"] == "" {
			t.Fatalf("target 结束记录缺耗时/outcome: %v", attrs)
		}
		if !strings.HasPrefix(attrs["refresh_id"], "rf-") {
			t.Fatalf("target 结束记录缺 refresh_id: %v", attrs)
		}
		outcomes[name] = attrs["outcome"]
	}
	if outcomes["ok-target"] != "success" {
		t.Fatalf("ok-target outcome=%v want success", outcomes)
	}
	if outcomes["fail-target"] != "error" {
		t.Fatalf("fail-target outcome=%v want error", outcomes)
	}

	// 关联隔离：cards 请求收口行是 op- 前缀的 operation_id，不含 refresh 的 rf- id。
	completion, ok := capture.find("读取操作完成")
	if !ok {
		t.Fatalf("缺 cards 收口日志")
	}
	if v, ok := attrValue(completion, "operation_id"); !ok || !strings.HasPrefix(v.String(), "op-") {
		t.Fatalf("cards 收口应使用 op- 关联: %q", v.String())
	}
	if _, ok := attrValue(completion, "refresh_id"); ok {
		t.Fatal("请求收口行不得携带 refresh_id（后台耗时不冒充请求等待）")
	}
}

// TestRoomAttachRefreshPerTargetAccounting 锁 U4 attach 后台刷新的逐 link 记账。
func TestRoomAttachRefreshPerTargetAccounting(t *testing.T) {
	capture := &diagLogCapture{}
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"task":            map[string]any{"id": "T-attach", "repo_path": "/repo", "work_dir": "/relay/B1"},
			"pending_tickets": []any{}, "recent_events": []any{},
		})
	}))
	env := newDiagEnv(t, capture)
	withTargets(t, env, map[string]config.Target{
		"relay": {Addr: strings.TrimPrefix(remote.URL, "http://"), Token: "remote-token"},
	})
	card := seedCard(t, env, "attach 记账卡")
	if err := env.ledger.LinkTask(card.ID, "relay", "T-attach", ledger.PurposeImplement, "test"); err != nil {
		t.Fatal(err)
	}
	if code, body := ledgerGet(t, env.testAgentdEnv, "/api/rooms?limit=50"); code != http.StatusOK {
		t.Fatalf("rooms 列表: %d %s", code, body)
	}
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := capture.find("房间 attach 后台刷新成功"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	found := false
	for _, r := range capture.records {
		if r.Message != "房间 attach 后台刷新成功" {
			continue
		}
		attrs := recordAttrs(r)
		if attrs["target"] != "relay" || attrs["target_call_ns"] == "" || attrs["outcome"] != "success" {
			t.Fatalf("attach 后台刷新记录缺逐台字段: %v", attrs)
		}
		if !strings.HasPrefix(attrs["refresh_id"], "rf-") {
			t.Fatalf("attach 刷新缺 refresh_id: %v", attrs)
		}
		found = true
	}
	if !found {
		t.Fatalf("缺 attach 后台刷新成功记录：\n%s", capture.allText())
	}
}
