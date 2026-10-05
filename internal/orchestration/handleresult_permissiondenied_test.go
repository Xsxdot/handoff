// handleresult_permissiondenied_test.go —— B383 Wave 0 manager 消费面回归：
// 被拒 result（VoidReason=权限被拒，adapter 对被拒空回合的新产物）经 handleResult
// 的既有 !OK 分支处置——挂起工单作废审计带 VoidReasonPermissionDenied（executor
// 仍在线，审计必须说真话）、turn_failed 事件、任务落 waiting_review（continue 可续）。
//
// manager 侧零改动：VoidReason 由 result 侧提供的既有契约（B74 审计说真话）天然
// 承接新档位，本文件把它钉住，防止未来有人把作废理由硬编码回去。
package orchestration

import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestPermissionDeniedResultVoidsTicketsAndLandsWaitingReview 断言完整消费链：
// 挂起工单作废（审计理由=权限被拒）→ turn_failed → waiting_review。
func TestPermissionDeniedResultVoidsTicketsAndLandsWaitingReview(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "t-perm-denied", proto.TaskStateRunning)

	m.handleResult("t-perm-denied", executor.AdapterEvent{Type: "result", SessionID: "sess-1",
		Result: &executor.Result{
			OK:         false,
			FailReason: "回合因权限被拒终止：\n  - bash: git push --dry-run origin main",
			VoidReason: executor.VoidReasonPermissionDenied,
		}})

	// 任务落 waiting_review 而不是任何终态：协调者可继续用 continue 续接
	cur, err := st.GetTask("t-perm-denied")
	if err != nil {
		t.Fatal(err)
	}
	if cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("被拒 result 后任务应落 waiting_review，实得 %s", cur.State)
	}
	// 作废审计必须带新档位理由：executor 仍在线，记「已终结」就是说谎
	ev := lastEventOfType(t, m, "t-perm-denied", string(proto.EventTypeTicketsVoided))
	var p TicketsVoidedPayload
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.Reason != executor.VoidReasonPermissionDenied {
		t.Fatalf("作废审计理由=%q，期望 %q", p.Reason, executor.VoidReasonPermissionDenied)
	}
	if p.Voided != 1 {
		t.Fatalf("应作废 1 张挂起工单，实得 %d", p.Voided)
	}
	// 事件类型必须是 turn_failed 而不是 failed：任务没有终结，只是回合失败
	// （B100：发 failed 会让 wait --follow 误报任务终结）
	lastEventOfType(t, m, "t-perm-denied", string(proto.EventTypeTurnFailed))
}
