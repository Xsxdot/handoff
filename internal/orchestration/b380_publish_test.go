// b380_publish_test.go —— B380 C-1：三个 ticket_answered 写入点补 hub.Publish。
//
// 职责：锁两个编排侧发布点（Manager.AnswerTicket 人工 reply、Manager.approvePermission
// 审批者批准）真的把 ticket_answered 送进 hub 实时流——否则账本镜像断流，
// OpenTickets 重放出幽灵未决单（C-2 兜底外仍须根因层修好）。
//
// 边界：编排包内白盒，用真实 store + hub；approval 面发布点由 approval 包内测试锁。
package orchestration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

func waitTicketAnswered(t *testing.T, ch <-chan proto.Event) TicketAnsweredPayload {
	t.Helper()
	select {
	case ev := <-ch:
		if ev.Type != proto.EventTypeTicketAnswered {
			t.Fatalf("发布事件类型 = %s, want ticket_answered", ev.Type)
		}
		var p TicketAnsweredPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatalf("解码 ticket_answered payload: %v", err)
		}
		return p
	case <-time.After(2 * time.Second):
		t.Fatal("2s 内未在 hub 实时流收到 ticket_answered")
		return TicketAnsweredPayload{}
	}
}

// TestB380AnswerTicketPublishes 锁人工 reply 路径：AnswerTicket 落库成功后
// 必须向 hub 发布同一 payload，幂等重答（applied=false）不发布。
func TestB380AnswerTicketPublishes(t *testing.T) {
	m, st, hub, _ := newTestManager(t)
	seedFacadeWaitingAnswerTask(t, st, "t1", "tk1")
	ch, cancel := hub.Subscribe("t1")
	defer cancel()

	applied, err := m.AnswerTicket(context.Background(), "t1", "tk1", "答")
	if err != nil || !applied {
		t.Fatalf("AnswerTicket = (%v,%v), want (true,nil)", applied, err)
	}
	p := waitTicketAnswered(t, ch)
	if p.TicketID != "tk1" || p.Answer != "答" {
		t.Fatalf("发布 payload = %+v", p)
	}
	// 幂等重答：applied=false，不得再发布
	applied, err = m.AnswerTicket(context.Background(), "t1", "tk1", "答")
	if err != nil || applied {
		t.Fatalf("幂等重答 = (%v,%v), want (false,nil)", applied, err)
	}
	select {
	case ev := <-ch:
		t.Fatalf("幂等重答不应发布，却收到 %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestB380ApprovePermissionPublishes 锁 Manager 中介审批者批准路径：
// approvePermission 落 ticket_answered 后必须发布。
func TestB380ApprovePermissionPublishes(t *testing.T) {
	m, st, hub, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{
		ID: "T1", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	})
	ch, cancel := hub.Subscribe("T1")
	defer cancel()

	ev := executor.AdapterEvent{
		Type: "permission", PermissionID: "p1", Text: "go test ./...",
		Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "go test ./..."},
	}
	m.approvePermission("T1", "T1:p1", "p1", ev.Text, permFingerprintFor(ev), "低危命令", "approver")

	p := waitTicketAnswered(t, ch)
	if p.TicketID != "T1:p1" || p.Answer != "allow" {
		t.Fatalf("发布 payload = %+v", p)
	}
}
