package orchestration

// b460_webfetch_test.go —— B460 S4 V5 编排两个入口：结构化 webfetch 请求经 Manager
// handlePermission（非 OpenCode）与 OpenCode approval.Client 两条生产路径，都是无工单、
// 无 approver_decision、恰一条 permission_auto_allow（rule=webfetch、url、tool=webfetch）；
// 同一 permission_id 重放不重复写审计（HasAutoAllow 依赖它）。
import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/approval"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
)

const b460URL = "https://go.dev/doc/"

func b460AssertWebFetchAudit(t *testing.T, m *Manager, taskID, permID string) {
	t.Helper()
	evs, err := m.st.EventsFromAsc(taskID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	audits := 0
	for _, e := range evs {
		switch e.Type {
		case proto.EventTypePermissionRequest, proto.EventTypeApproverDecision:
			t.Fatalf("webfetch 放行不得产生 %s", e.Type)
		case proto.EventTypePermissionAutoAllow:
			audits++
			var p permissionAutoAllowPayload
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				t.Fatal(err)
			}
			if p.PermissionID != permID || p.Rule != permgate.RuleWebFetch || p.URL != b460URL ||
				p.Tool != executor.PermToolWebFetch {
				t.Fatalf("审计 payload = %#v", p)
			}
			var raw map[string]any
			_ = json.Unmarshal(e.Payload, &raw)
			if raw["url"] != b460URL || raw["rule"] != "webfetch" {
				t.Fatalf("审计键名必须是 url / rule（F10），实得 %v", raw)
			}
		}
	}
	if audits != 1 {
		t.Fatalf("permission_auto_allow 条数 = %d，期望 1", audits)
	}
	pend, err := m.st.PendingTickets(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pend) != 0 {
		t.Fatalf("webfetch 放行不得建工单，实得 %d 张", len(pend))
	}
}

func TestB460WebFetchManagerPathAudits(t *testing.T) {
	m, st, _, adapter := newTestManager(t)
	taskID := "wf-mgr"
	now := time.Now().UTC()
	mustCreateTask(t, st, &proto.Task{ID: taskID, RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now})
	ev := executor.AdapterEvent{Type: "permission", PermissionID: "wf-1", Text: "WebFetch: " + b460URL,
		Perm: &executor.PermRequest{Tool: executor.PermToolWebFetch, URL: b460URL}}
	m.handlePermission(context.Background(), taskID, ev)
	if got := adapter.recordedPerms(); len(got) != 1 || got[0] != "wf-1:once" {
		t.Fatalf("应原生回传一次 once，实得 %v", got)
	}
	b460AssertWebFetchAudit(t, m, taskID, "wf-1")
	// 重放：IsReplay 认审计事件为幂等标记，不再写第二条审计
	m.handlePermission(context.Background(), taskID, ev)
	b460AssertWebFetchAudit(t, m, taskID, "wf-1")
	if !m.permAuthority.(*approval.Authority).HasAutoAllow(taskID, "wf-1") {
		t.Fatal("HasAutoAllow 必须认出 webfetch 审计事件")
	}
}

func TestB460WebFetchOpenCodePathAudits(t *testing.T) {
	m, c, task := setupTestTaskClient(t, "wf-oc", "v1")
	res, err := c.Request(context.Background(), executor.ApprovalRequest{
		NativeID: "wf-2", Text: "webfetch: " + b460URL,
		Perm: &executor.PermRequest{Tool: executor.PermToolWebFetch, URL: b460URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision.Status != executor.ApprovalAllow {
		t.Fatalf("status=%q want allow", res.Decision.Status)
	}
	b460AssertWebFetchAudit(t, m, task.ID, "wf-2")
}
