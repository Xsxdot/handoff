// redaction_diag_test.go 锁 B409.6 (S4-U6) 的日志脱敏红线：注入哨兵 DSN 密码、
// Bearer/relay token、消息正文、@ token 与远端 task title 后，全部关联日志
// （外围收口、collab 投影、ledger 阶段、后台刷新）不得出现任一哨兵值；同时
// 安全字段（error_class/failed_stage/operation_id/rows/bytes）保留，不回归为
// 沉默。反例（把正文/参数写进日志的回归）必须让本测试变红。
package agentd

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

const (
	sentinelDSNPassword = "sentinel-pg-passwd-9f3a"
	sentinelBearer      = "sentinel-bearer-7a2c"
	sentinelBody        = "sentinel-body-正文-5d2e"
	sentinelMention     = "sentinel-mention-3c8b"
	sentinelTaskTitle   = "sentinel-task-title-1e90"
)

// TestReadDiagLogsRedactSensitiveValues 注入全套哨兵并扫全部捕获日志。
func TestReadDiagLogsRedactSensitiveValues(t *testing.T) {
	capture := &diagLogCapture{}
	env := newDiagEnv(t, capture)

	// 哨兵①②：消息正文与 @ token 落账（bind 参数与 payload 都含哨兵）。
	session, err := env.ledger.CreateSession("脱敏金样", "user:sycm", "user:sycm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.ledger.RecordRoomMessage("", proto.RoomMessage{
		Kind: proto.RoomMsgUser, Room: session.ID, Body: sentinelBody,
		Mentions: []string{sentinelMention},
	}, "user:sycm"); err != nil {
		t.Fatal(err)
	}
	// 哨兵③④：远端 target 携带 Bearer token 且不可达（错误路径走 pool/client）。
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	next := *env.srv.conf()
	next.Targets = map[string]config.Target{
		"sentinel-target": {Addr: strings.TrimPrefix(remote.URL, "http://"), Token: sentinelBearer},
	}
	env.srv.cfg.Store(&next)
	t.Cleanup(func() { _ = env.srv.CloseTargets() })

	// 哨兵⑤：陈旧快照携带 task title（合法可见于响应，绝不可见于日志）。
	env.srv.unlinkedCache = &unlinkedSummarySnapshot{
		Tasks:            []map[string]any{{"target": "sentinel-target", "task_id": "t-old", "title": sentinelTaskTitle, "state": proto.TaskStateRunning}},
		TargetConfigs:    map[string]config.Target{"sentinel-target": next.Targets["sentinel-target"]},
		SuccessfulTarget: map[string]struct{}{"sentinel-target": {}},
		ObservedAt:       time.Now().Add(-10 * time.Minute).UTC(),
	}

	// 走全部五条 B409 读路由 + 触发后台刷新。
	for _, path := range []string{
		"/api/cards",
		"/api/sessions",
		"/api/sessions/" + session.ID,
		"/api/rooms?limit=50&cursor=",
		"/api/rooms/" + session.ID + "/messages?limit=10",
	} {
		code, body := diagGet(t, env, path)
		if code != http.StatusOK && code != http.StatusUpgradeRequired {
			t.Fatalf("GET %s: %d %s", path, code, body)
		}
	}
	// 等待后台 target 刷新尝试（错误路径日志）；首次建链可能较慢，放宽窗口。
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := capture.find("未挂账 target 读取结束"); ok {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 红线一：任何哨兵值不得出现在任何日志里。
	all := capture.allText()
	for name, sentinel := range map[string]string{
		"dsn_password": sentinelDSNPassword,
		"bearer":       sentinelBearer,
		"body":         sentinelBody,
		"mention":      sentinelMention,
		"task_title":   sentinelTaskTitle,
	} {
		if strings.Contains(all, sentinel) {
			t.Fatalf("哨兵 %s 泄漏进日志", name)
		}
	}

	// 红线二：不因脱敏回归为沉默——分类、阶段与关联字段仍在。
	rec, ok := capture.find("读取操作完成")
	if !ok {
		t.Fatalf("缺外围收口日志（脱敏不得吞掉诊断）：\n%s", all)
	}
	attrs := recordAttrs(rec)
	if attrs["operation_id"] == "" || attrs["outcome"] == "" || attrs["elapsed_ns"] == "" {
		t.Fatalf("收口安全字段缺失: %v", attrs)
	}
	foundErrorClass := false
	for _, r := range capture.records {
		if v, ok := attrValue(r, "error_class"); ok && v.String() != "" {
			foundErrorClass = true
		}
	}
	if !foundErrorClass {
		t.Fatalf("后台 target 失败应有 error_class 分类（脱敏不是沉默）：\n%s", all)
	}
}
