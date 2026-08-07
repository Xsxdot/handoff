// store 包测试：用真实 SQLite 文件验证任务/事件/工单的读写与约束。
package store_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// TestTaskLifecycle 覆盖 Create→Get 回读一致、合法状态链、非法迁移拒绝与字段白名单。
func TestTaskLifecycle(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	t0 := time.Now().UTC().Truncate(time.Millisecond)
	task := &proto.Task{
		ID:              "task-1",
		Target:          "codex",
		RepoPath:        "/Users/x/repo",
		Branch:          "feat/mvp",
		PlanPath:        "plans/mvp.md",
		PlanSummary:     "MVP 实现计划",
		ExecutorSession: "sess-1",
		State:           proto.TaskStatePending,
		CreatedAt:       t0,
		UpdatedAt:       t0,
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	got, err := s.GetTask(task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if got.ID != task.ID || got.Target != task.Target || got.RepoPath != task.RepoPath ||
		got.Branch != task.Branch || got.PlanPath != task.PlanPath || got.PlanSummary != task.PlanSummary ||
		got.ExecutorSession != task.ExecutorSession || got.State != task.State {
		t.Errorf("回读字段不一致: got %+v, want %+v", got, task)
	}
	if !got.CreatedAt.Equal(task.CreatedAt) || !got.UpdatedAt.Equal(task.UpdatedAt) {
		t.Errorf("回读时间不一致: got %v/%v, want %v/%v", got.CreatedAt, got.UpdatedAt, task.CreatedAt, task.UpdatedAt)
	}

	if _, err := s.GetTask("not-exist"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTask(不存在) err = %v, want ErrNotFound", err)
	}

	chain := []proto.TaskState{
		proto.TaskStateRunning,
		proto.TaskStateWaitingAnswer,
		proto.TaskStateRunning,
		proto.TaskStateCompleted,
	}
	for _, st := range chain {
		if err := s.UpdateTaskState(task.ID, st); err != nil {
			t.Fatalf("UpdateTaskState(%s): %v", st, err)
		}
	}
	got, _ = s.GetTask(task.ID)
	if got.State != proto.TaskStateCompleted {
		t.Fatalf("state = %s, want completed", got.State)
	}

	if err := s.UpdateTaskState(task.ID, proto.TaskStateRunning); !errors.Is(err, store.ErrBadTransit) {
		t.Fatalf("completed→running err = %v, want ErrBadTransit", err)
	}

	if err := s.SetTaskField(task.ID, "plan_summary", "更新后的摘要"); err != nil {
		t.Fatalf("SetTaskField: %v", err)
	}
	got, _ = s.GetTask(task.ID)
	if got.PlanSummary != "更新后的摘要" {
		t.Errorf("plan_summary = %q, want 更新后的摘要", got.PlanSummary)
	}

	if err := s.SetTaskField(task.ID, "id", "hijack"); err == nil {
		t.Fatalf("SetTaskField(id) 应被白名单拒绝")
	}
}

// TestUpdateTaskStateCAS 验证 UpdateTaskState 的 CAS 守卫：两个迁移者并发从 pending 出发
// 竞争迁到 running，恰好一个成功、另一个必须收到 ErrBadTransit（基于过期快照的写被拒），
// 迁移不会被 last-writer-wins 静默丢失，最终状态必须为 running。
func TestUpdateTaskStateCAS(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	// 多次迭代：CAS 语义在每次竞争中都应恰好一胜一败，而非偶发通过。
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("task-cas-%d", i)
		now := time.Now().UTC()
		task := &proto.Task{ID: id, Target: "codex", RepoPath: "/r", State: proto.TaskStatePending,
			CreatedAt: now, UpdatedAt: now}
		if err := s.CreateTask(task); err != nil {
			t.Fatalf("CreateTask: %v", err)
		}

		start := make(chan struct{})
		errs := make(chan error, 2)
		var wg sync.WaitGroup
		for g := 0; g < 2; g++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				errs <- s.UpdateTaskState(id, proto.TaskStateRunning)
			}()
		}
		close(start) // 同时放行两个迁移者，最大化快照重叠
		wg.Wait()
		close(errs)

		var ok, rejected int
		for err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, store.ErrBadTransit):
				rejected++
			default:
				t.Fatalf("迭代 %d: UpdateTaskState 意外错误: %v", i, err)
			}
		}
		if ok != 1 || rejected != 1 {
			t.Fatalf("迭代 %d: 成功 %d 个、被拒 %d 个，want 恰好 1 个成功 1 个被拒", i, ok, rejected)
		}
		got, _ := s.GetTask(id)
		if got.State != proto.TaskStateRunning {
			t.Fatalf("迭代 %d: 最终状态 = %s, want running", i, got.State)
		}
	}
}

// TestEventSeqMonotonic 验证两个任务交错追加时 seq 全局单调，且 EventsFrom 按任务+seq 过滤。
func TestEventSeqMonotonic(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	e1, err := s.AppendEvent("task-a", proto.EventTypeProgress, map[string]any{"step": 1})
	if err != nil {
		t.Fatalf("AppendEvent 1: %v", err)
	}
	e2, err := s.AppendEvent("task-b", proto.EventTypeProgress, map[string]any{"step": 1})
	if err != nil {
		t.Fatalf("AppendEvent 2: %v", err)
	}
	e3, err := s.AppendEvent("task-a", proto.EventTypeCompleted, map[string]any{"done": true})
	if err != nil {
		t.Fatalf("AppendEvent 3: %v", err)
	}
	if !(e1.Seq < e2.Seq && e2.Seq < e3.Seq) {
		t.Fatalf("seq 应全局单调: e1=%d e2=%d e3=%d", e1.Seq, e2.Seq, e3.Seq)
	}

	got, err := s.EventsFrom("task-a", 0, 10)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	if len(got) != 2 || got[0].Seq != e1.Seq || got[1].Seq != e3.Seq {
		t.Fatalf("EventsFrom(task-a, 0, 10) = %+v, want [e1 e3]", got)
	}

	got, _ = s.EventsFrom("task-a", e1.Seq, 10)
	if len(got) != 1 || got[0].Seq != e3.Seq {
		t.Fatalf("EventsFrom(task-a, e1.Seq, 10) = %+v, want [e3]", got)
	}

	got, _ = s.EventsFrom("task-a", 0, 1)
	if len(got) != 1 || got[0].Seq != e3.Seq {
		t.Fatalf("EventsFrom limit 未生效或未取最新: %+v, want [e3]（截断取最新不取最旧）", got)
	}

	got, _ = s.EventsFrom("task-a", 0, 10)
	if string(got[0].Payload) != string(e1.Payload) || string(got[1].Payload) != string(e3.Payload) {
		t.Errorf("payload 回读不一致: %s/%s vs %s/%s", got[0].Payload, got[1].Payload, e1.Payload, e3.Payload)
	}
}

// TestEventsFromLatestN 验证超 limit 截断语义：返回**最新** limit 条（seq 升序），
// 截掉最旧而非最新——attach 的 recent_events 与 WS 补发依赖「最新窗口」，
// >limit 积压时新事件（如 completed）不能被丢。
func TestEventsFromLatestN(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	var seqs []int64
	for i := 1; i <= 5; i++ {
		ev, err := s.AppendEvent("t1", proto.EventTypeProgress, map[string]any{"n": i})
		if err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
		seqs = append(seqs, ev.Seq)
	}

	// 5 条取 3：必须是最新 3 条且按 seq 升序（seqs[2..4]）
	got, err := s.EventsFrom("t1", 0, 3)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("EventsFrom(0,3) 条数=%d, want 3", len(got))
	}
	for i, want := range seqs[2:] {
		if got[i].Seq != want {
			t.Fatalf("第 %d 条 seq=%d, want %d（最新窗口内，截断最旧）", i, got[i].Seq, want)
		}
	}

	// 与 fromSeq 组合：从 seqs[0] 之后取最新 2 条 = [seqs[3], seqs[4]]
	got, err = s.EventsFrom("t1", seqs[0], 2)
	if err != nil {
		t.Fatalf("EventsFrom(fromSeq): %v", err)
	}
	if len(got) != 2 || got[0].Seq != seqs[3] || got[1].Seq != seqs[4] {
		t.Fatalf("EventsFrom(seqs[0], 2) = %+v, want [seqs[3] seqs[4]]", got)
	}
}

// TestTicketIdempotent 验证同 id 工单幂等创建、回答后移出待办、answer 可回读。
func TestTicketIdempotent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	t0 := time.Now().UTC().Truncate(time.Millisecond)
	tk := &proto.Ticket{
		ID:        "ticket-1",
		TaskID:    "task-1",
		Kind:      "gate",
		Request:   json.RawMessage(`{"perm":"run_plan"}`),
		CreatedAt: t0,
	}
	created, err := s.CreateTicket(tk)
	if err != nil || !created {
		t.Fatalf("第一次 CreateTicket: created=%v err=%v, want true/nil", created, err)
	}
	created2, err := s.CreateTicket(tk)
	if err != nil || created2 {
		t.Fatalf("第二次 CreateTicket: created=%v err=%v, want false/nil", created2, err)
	}

	got, err := s.GetTicket(tk.ID)
	if err != nil {
		t.Fatalf("GetTicket: %v", err)
	}
	if got.ID != tk.ID || got.TaskID != tk.TaskID || got.Kind != tk.Kind || string(got.Request) != string(tk.Request) {
		t.Errorf("GetTicket 回读不一致: got %+v, want %+v", got, tk)
	}
	if got.Answer != nil || got.AnsweredAt != nil {
		t.Errorf("新建工单不应有 answer: %+v", got)
	}
	if !got.CreatedAt.Equal(tk.CreatedAt) {
		t.Errorf("CreatedAt 回读不一致: %v vs %v", got.CreatedAt, tk.CreatedAt)
	}

	if _, err := s.GetTicket("not-exist"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetTicket(不存在) err = %v, want ErrNotFound", err)
	}

	pend, err := s.PendingTickets(tk.TaskID)
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pend) != 1 || pend[0].ID != tk.ID {
		t.Fatalf("PendingTickets = %+v, want [ticket-1]", pend)
	}

	answer := "同意执行"
	if err := s.AnswerTicket(tk.ID, answer); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	pend2, _ := s.PendingTickets(tk.TaskID)
	if len(pend2) != 0 {
		t.Fatalf("回答后 PendingTickets = %+v, want 空", pend2)
	}
	got2, _ := s.GetTicket(tk.ID)
	if got2.Answer == nil || *got2.Answer != answer || got2.AnsweredAt == nil {
		t.Errorf("回答后 GetTicket: answer=%v answered_at=%v, want %q/非空", got2.Answer, got2.AnsweredAt, answer)
	}
}
