// facade_test.go —— B233.28 W1：编排门面 AnswerTicket / ResumeIfIdle 的缝级断言。
//
// 职责：锁契约 §5.2 #8/#9/#10（应答落库 + ticket_answered 事件、幂等/冲突/不存在/
// 跨任务不泄露）与 #11/#12（无待办才回迁 running、并发 CAS 收敛）。
//
// 边界：纯编排包内白盒；用真实 store（newTestManager），穿过 store 的序列化边界。
package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// seedFacadeWaitingAnswerTask 落一条 waiting_answer 任务，可选挂一张未答工单。
func seedFacadeWaitingAnswerTask(t *testing.T, st *store.Store, taskID, ticketID string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: taskID, RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateWaitingAnswer, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask(%s): %v", taskID, err)
	}
	if ticketID != "" {
		if _, err := st.CreateTicket(&proto.Ticket{ID: ticketID, TaskID: taskID, Kind: "ask",
			Request: json.RawMessage(`{}`), CreatedAt: now}); err != nil {
			t.Fatalf("CreateTicket(%s): %v", ticketID, err)
		}
	}
}

// TestFacadeAnswerTicketAnswersAndEmitsEvent 锁契约 §5.2 #8/#9/#10：
// applied=true 落库 + ticket_answered 事件（payload 线格式逐字）；同值幂等
// (false,nil)；异值 ErrTicketConflict；不存在/跨任务 ErrNotFound 且不泄露。
func TestFacadeAnswerTicketAnswersAndEmitsEvent(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	seedFacadeWaitingAnswerTask(t, st, "t1", "tk1")
	ctx := context.Background()

	applied, err := m.AnswerTicket(ctx, "t1", "tk1", "答")
	if err != nil || !applied {
		t.Fatalf("AnswerTicket = (%v,%v)，want (true,nil)", applied, err)
	}
	tk, err := st.GetTicket("tk1")
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if tk.Answer == nil || *tk.Answer != "答" {
		t.Fatalf("应答未落库: %+v", tk.Answer)
	}
	evs, err := st.EventsFrom("t1", 0, 100)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	var answered *proto.Event
	for i := range evs {
		if evs[i].Type == proto.EventTypeTicketAnswered {
			answered = &evs[i]
		}
	}
	if answered == nil {
		t.Fatal("applied=true 必须追加 ticket_answered 事件")
	}
	want := `{"ticket_id":"tk1","answer":"答"}`
	if string(answered.Payload) != want {
		t.Fatalf("ticket_answered payload = %s，want %s", answered.Payload, want)
	}

	applied, err = m.AnswerTicket(ctx, "t1", "tk1", "答")
	if err != nil || applied {
		t.Fatalf("同值重答 = (%v,%v)，want (false,nil)", applied, err)
	}
	if _, err := m.AnswerTicket(ctx, "t1", "tk1", "别的"); !errors.Is(err, store.ErrTicketConflict) {
		t.Fatalf("异值重答 err=%v，want ErrTicketConflict", err)
	}
	if _, err := m.AnswerTicket(ctx, "t1", "ghost", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("未知工单 err=%v，want ErrNotFound", err)
	}
	if _, err := st.CreateTicket(&proto.Ticket{ID: "tk-other", TaskID: "t-other", Kind: "ask",
		Request: json.RawMessage(`{}`), CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("CreateTicket(other): %v", err)
	}
	if _, err := m.AnswerTicket(ctx, "t1", "tk-other", "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("跨任务工单 err=%v，want ErrNotFound", err)
	}
}

// TestFacadeResumeIfIdleOnlyWhenNoPending 锁契约 §5.2 #11/#12：
// 非 waiting_answer 不动；有待办不动；无待办才回迁 running。
func TestFacadeResumeIfIdleOnlyWhenNoPending(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: "t-run", RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask(t-run): %v", err)
	}
	seedFacadeWaitingAnswerTask(t, st, "t-pending", "tk-p")
	seedFacadeWaitingAnswerTask(t, st, "t-idle", "")

	m.ResumeIfIdle(ctx, "t-run")
	if task, _ := st.GetTask("t-run"); task.State != proto.TaskStateRunning {
		t.Fatalf("running 任务被误改: %s", task.State)
	}
	m.ResumeIfIdle(ctx, "t-pending")
	if task, _ := st.GetTask("t-pending"); task.State != proto.TaskStateWaitingAnswer {
		t.Fatalf("仍有待办却回迁了: %s", task.State)
	}
	m.ResumeIfIdle(ctx, "t-idle")
	if task, _ := st.GetTask("t-idle"); task.State != proto.TaskStateRunning {
		t.Fatalf("无待办未回迁: %s", task.State)
	}
}

// TestFacadeResumeIfIdleConcurrentSingleTransition 锁契约 §5.2 #12 的 CAS 收敛：
// 两个并发回迁，最终恰到达 running，无 panic/死锁（唯一赢家由 store CAS 承担）。
func TestFacadeResumeIfIdleConcurrentSingleTransition(t *testing.T) {
	m, st, _, _ := newTestManager(t)
	seedFacadeWaitingAnswerTask(t, st, "t-race", "")
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.ResumeIfIdle(context.Background(), "t-race")
		}()
	}
	wg.Wait()
	if task, _ := st.GetTask("t-race"); task.State != proto.TaskStateRunning {
		t.Fatalf("并发回迁最终态 = %s，want running", task.State)
	}
}
