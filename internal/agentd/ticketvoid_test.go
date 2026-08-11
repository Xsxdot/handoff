// 本文件覆盖 B63：任务走到终态时统一作废剩余挂起工单。
//
// 测试为白盒（package agentd）：直接驱动 m.transit，绕开 Done/Stop 的前置门禁，
// 让每条用例只钉住「终态迁移 ⇒ 作废」这一件事。
package agentd

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// mustTaskWithTicket 造一个指定状态的任务，并挂一张未回答的 gate 工单。
func mustTaskWithTicket(t *testing.T, st *store.Store, id string, state proto.TaskState) {
	t.Helper()
	mustCreateTask(t, st, &proto.Task{ID: id, RepoPath: t.TempDir(), Executor: "fake",
		State: state, CreatedAt: time.Now(), UpdatedAt: time.Now()})
	if _, err := st.CreateTicket(&proto.Ticket{ID: id + ":p1", TaskID: id, Kind: "gate",
		Request: []byte(`{"kind":"gate","permission":"bash: ls"}`), CreatedAt: time.Now()}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
}

// pendingCount 返回任务当前挂起（未回答）工单数。
func pendingCount(t *testing.T, st *store.Store, taskID string) int {
	t.Helper()
	pending, err := st.PendingTickets(taskID)
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	return len(pending)
}

// voidedEvents 返回任务的全部 tickets_voided 事件。
func voidedEvents(t *testing.T, st *store.Store, taskID string) []proto.Event {
	t.Helper()
	evs, err := st.EventsFromAsc(taskID, 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	var got []proto.Event
	for _, e := range evs {
		if e.Type == proto.EventTypeTicketsVoided {
			got = append(got, e)
		}
	}
	return got
}

// 终态迁移必须作废剩余工单并留痕：否则审核者接管时会被引去 reply 一个必然 404 的 id。
func TestTransitToTerminalVoidsPendingTickets(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "T-void", proto.TaskStateWaitingReview)

	if err := m.transit("T-void", proto.TaskStateCompleted, "done"); err != nil {
		t.Fatalf("transit: %v", err)
	}

	if n := pendingCount(t, st, "T-void"); n != 0 {
		t.Errorf("终态后挂起工单 = %d，期望 0", n)
	}
	evs := voidedEvents(t, st, "T-void")
	if len(evs) != 1 {
		t.Fatalf("tickets_voided 事件 = %d 条，期望 1 条", len(evs))
	}
	var p ticketsVoidedPayload
	if err := json.Unmarshal(evs[0].Payload, &p); err != nil {
		t.Fatalf("解析 payload: %v", err)
	}
	if p.Voided != 1 || p.Reason != "done" {
		t.Errorf("payload = %+v，期望 {Voided:1 Reason:done}", p)
	}
}

// 回合结束（waiting_review）**不得**作废：grok/opencode 的提问中继就是「回合已结束、
// 人稍后 reply --answer 补答」，B3/B49 真机验过 relayed=true。这条是护栏。
func TestTransitToWaitingReviewKeepsPendingTickets(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "T-keep", proto.TaskStateRunning)

	if err := m.transit("T-keep", proto.TaskStateWaitingReview, "result"); err != nil {
		t.Fatalf("transit: %v", err)
	}

	if n := pendingCount(t, st, "T-keep"); n != 1 {
		t.Errorf("回合结束后挂起工单 = %d，期望 1（跨回合中继依赖它）", n)
	}
	if evs := voidedEvents(t, st, "T-keep"); len(evs) != 0 {
		t.Errorf("回合结束不该产出 tickets_voided，实得 %d 条", len(evs))
	}
}

// 迁移失败（非法/并发 CAS 输）时不得作废：任务还活着，砸掉的是它的合法工单。
func TestFailedTransitDoesNotVoid(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "T-bad", proto.TaskStatePending)

	// pending → completed 不在 transitTable 里，必然被拒
	if err := m.transit("T-bad", proto.TaskStateCompleted, "done"); err == nil {
		t.Fatal("pending → completed 应被拒绝")
	}

	if n := pendingCount(t, st, "T-bad"); n != 1 {
		t.Errorf("迁移失败后挂起工单 = %d，期望 1", n)
	}
	if evs := voidedEvents(t, st, "T-bad"); len(evs) != 0 {
		t.Errorf("迁移失败不该产出 tickets_voided，实得 %d 条", len(evs))
	}
}

// 没有挂起工单的正常收尾不产出事件：绝大多数任务如此，无条件发事件等于给每个
// 正常任务的事件流添噪音。
func TestTerminalWithoutTicketsIsSilent(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustCreateTask(t, st, &proto.Task{ID: "T-quiet", RepoPath: t.TempDir(), Executor: "fake",
		State: proto.TaskStateWaitingReview, CreatedAt: time.Now(), UpdatedAt: time.Now()})

	if err := m.transit("T-quiet", proto.TaskStateCompleted, "done"); err != nil {
		t.Fatalf("transit: %v", err)
	}

	if evs := voidedEvents(t, st, "T-quiet"); len(evs) != 0 {
		t.Errorf("无挂起工单时不该产出 tickets_voided，实得 %d 条", len(evs))
	}
}

// 重复进终态（transit 的幂等分支）不得产出第二条事件。
func TestRepeatedTerminalTransitIsIdempotent(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	mustTaskWithTicket(t, st, "T-twice", proto.TaskStateWaitingReview)

	if err := m.transit("T-twice", proto.TaskStateCompleted, "done"); err != nil {
		t.Fatalf("首次 transit: %v", err)
	}
	if err := m.transit("T-twice", proto.TaskStateCompleted, "done"); err != nil {
		t.Fatalf("重复 transit 应幂等返回 nil: %v", err)
	}

	if evs := voidedEvents(t, st, "T-twice"); len(evs) != 1 {
		t.Errorf("tickets_voided = %d 条，期望 1 条", len(evs))
	}
}
