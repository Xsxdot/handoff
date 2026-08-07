// store 包测试：用真实 SQLite 文件验证任务/事件/工单的读写与约束。
package store_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
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
	if len(got) != 1 || got[0].Seq != e1.Seq {
		t.Fatalf("EventsFrom limit 未生效: %+v", got)
	}

	got, _ = s.EventsFrom("task-a", 0, 10)
	if string(got[0].Payload) != string(e1.Payload) || string(got[1].Payload) != string(e3.Payload) {
		t.Errorf("payload 回读不一致: %s/%s vs %s/%s", got[0].Payload, got[1].Payload, e1.Payload, e3.Payload)
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
