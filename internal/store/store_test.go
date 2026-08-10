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

// TestEventsFromAsc 验证 WS 重放专用变体的语义：fromSeq 之后按 seq 升序、limit 生效、
// 超 limit 时截断**尾部**保留最旧的 limit 条——与 EventsFrom（截最旧）相反，
// 保证客户端 cursor 只前进到确实收到的位置，缺口可凭更大 from_seq 重连续拉。
func TestEventsFromAsc(t *testing.T) {
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

	// 无 limit 截断：fromSeq 之后全部事件按 seq 升序
	got, err := s.EventsFromAsc("t1", 0, 10)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("EventsFromAsc(0,10) 条数=%d, want 5", len(got))
	}
	for i, want := range seqs {
		if got[i].Seq != want {
			t.Fatalf("第 %d 条 seq=%d, want %d（升序）", i, got[i].Seq, want)
		}
	}

	// fromSeq 起始（不含）
	got, err = s.EventsFromAsc("t1", seqs[1], 10)
	if err != nil {
		t.Fatalf("EventsFromAsc(fromSeq): %v", err)
	}
	if len(got) != 3 || got[0].Seq != seqs[2] || got[2].Seq != seqs[4] {
		t.Fatalf("EventsFromAsc(seqs[1],10) = %+v, want [seqs[2] seqs[3] seqs[4]]", got)
	}

	// limit 截断语义：截**尾部**、留最旧的 limit 条（与 EventsFrom 的「留最新」相反）
	got, err = s.EventsFromAsc("t1", 0, 3)
	if err != nil {
		t.Fatalf("EventsFromAsc(0,3): %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("EventsFromAsc(0,3) 条数=%d, want 3", len(got))
	}
	for i, want := range seqs[:3] {
		if got[i].Seq != want {
			t.Fatalf("第 %d 条 seq=%d, want %d（截断尾部保留最旧）", i, got[i].Seq, want)
		}
	}

	// 对照：EventsFrom 同参数返回最新 3 条（截断最旧）——WS 重放绝不能用它
	gotLatest, err := s.EventsFrom("t1", 0, 3)
	if err != nil {
		t.Fatalf("EventsFrom(0,3): %v", err)
	}
	if len(gotLatest) != 3 || gotLatest[0].Seq != seqs[2] {
		t.Fatalf("EventsFrom(0,3) = %+v, want 最新 3 条 [seqs[2]..seqs[4]]（与 Asc 截断方向相反）", gotLatest)
	}

	// fromSeq 之后无事件：返回空而非错误
	got, err = s.EventsFromAsc("t1", seqs[4], 10)
	if err != nil {
		t.Fatalf("EventsFromAsc(seqs[4],10): %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("EventsFromAsc(seqs[4],10) 条数=%d, want 0", len(got))
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

// TestCreateTaskPersistsPhase2Fields 验证二期新增字段（name/executor/model/work_dir/worktree_managed）
// 能随 CreateTask 落库并从 GetTask 完整回读。
func TestCreateTaskPersistsPhase2Fields(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	task := &proto.Task{
		ID: "t1", RepoPath: "/repo", State: proto.TaskStatePending,
		Name: "重构支付", Executor: "opencode", Model: "gpt-5-mini",
		WorkDir: "/wt/t1", WorktreeManaged: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask("t1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "重构支付" || got.Executor != "opencode" || got.Model != "gpt-5-mini" ||
		got.WorkDir != "/wt/t1" || !got.WorktreeManaged {
		t.Fatalf("二期字段未持久化: %+v", got)
	}
	if got.Workdir() != "/wt/t1" {
		t.Fatalf("Workdir() 应返回 WorkDir，得到 %s", got.Workdir())
	}
}

// TestWorkdirFallsBackToRepoPath 验证 WorkDir 为空（原地模式）时 Workdir() 回退到 RepoPath。
func TestWorkdirFallsBackToRepoPath(t *testing.T) {
	tk := proto.Task{RepoPath: "/repo"}
	if tk.Workdir() != "/repo" {
		t.Fatalf("WorkDir 为空时 Workdir() 应回退 RepoPath")
	}
}

// TestAnswerTicketRefreshesTaskUpdatedAt 验证回答工单会刷新所属任务的 updated_at
// （P1-15a 二次告警的活动信号）：看门狗凭 updated_at 前进判定「stalled 之后有
// 回复」，回答必须推进它，否则「已 stalled → 审核者回答 → executor 仍死」永远
// 不再告警。
func TestAnswerTicketRefreshesTaskUpdatedAt(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	t0 := time.Now().UTC().Truncate(time.Millisecond)
	task := &proto.Task{ID: "task-1", RepoPath: "/r", State: proto.TaskStateRunning, CreatedAt: t0, UpdatedAt: t0}
	if err := s.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if _, err := s.CreateTicket(&proto.Ticket{ID: "t1", TaskID: "task-1", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: t0}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}

	time.Sleep(5 * time.Millisecond) // 保证 updated_at 严格前进（时间戳秒/纳秒粒度）
	if err := s.AnswerTicket("t1", "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	got, err := s.GetTask("task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if !got.UpdatedAt.After(t0) {
		t.Fatalf("回答后任务 updated_at=%v, want 晚于 %v（回答必须刷新活动时间）", got.UpdatedAt, t0)
	}
}

// TestVoidPendingTickets 验证作废语义（P1-16）：仅作废未回答工单，回答过的不受
// 影响；作废后不再出现在 PendingTickets；重复调用幂等返回 0。
func TestVoidPendingTickets(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	t0 := time.Now().UTC().Truncate(time.Millisecond)
	for _, id := range []string{"task-1", "task-2"} {
		if err := s.CreateTask(&proto.Task{ID: id, RepoPath: "/r", State: proto.TaskStatePending, CreatedAt: t0, UpdatedAt: t0}); err != nil {
			t.Fatalf("CreateTask(%s): %v", id, err)
		}
	}
	// task-1 两个挂起 + 一个已回答
	for _, id := range []string{"t1", "t2", "t3"} {
		if _, err := s.CreateTicket(&proto.Ticket{ID: id, TaskID: "task-1", Kind: "gate",
			Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: t0}); err != nil {
			t.Fatalf("CreateTicket(%s): %v", id, err)
		}
	}
	if err := s.AnswerTicket("t3", "allow"); err != nil {
		t.Fatalf("AnswerTicket(t3): %v", err)
	}
	// task-2 一个挂起（不应被 task-1 的作废误伤）
	if _, err := s.CreateTicket(&proto.Ticket{ID: "t4", TaskID: "task-2", Kind: "gate",
		Request: json.RawMessage(`{"kind":"gate"}`), CreatedAt: t0}); err != nil {
		t.Fatalf("CreateTicket(t4): %v", err)
	}

	n, err := s.VoidPendingTickets("task-1")
	if err != nil {
		t.Fatalf("VoidPendingTickets: %v", err)
	}
	if n != 2 {
		t.Fatalf("作废数=%d, want 2（t3 已回答不算）", n)
	}
	// 幂等：第二次作废 0 条
	n2, _ := s.VoidPendingTickets("task-1")
	if n2 != 0 {
		t.Fatalf("重复作废数=%d, want 0", n2)
	}

	pend, err := s.PendingTickets("task-1")
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pend) != 0 {
		t.Fatalf("作废后 task-1 PendingTickets=%d, want 0（answer 非空天然移出待办）", len(pend))
	}
	// 审计痕迹保留：answer 为 VoidAnswer
	for _, id := range []string{"t1", "t2"} {
		got, _ := s.GetTicket(id)
		if got.Answer == nil || *got.Answer != store.VoidAnswer {
			t.Fatalf("作废工单 %s answer=%v, want VoidAnswer(%q)", id, got.Answer, store.VoidAnswer)
		}
	}
	// 其他任务的挂起不受影响
	pend2, err := s.PendingTickets("task-2")
	if err != nil {
		t.Fatalf("PendingTickets(task-2): %v", err)
	}
	if len(pend2) != 1 || pend2[0].ID != "t4" {
		t.Fatalf("task-2 PendingTickets=%+v, want [t4]（跨任务误伤）", pend2)
	}
	// 已回答工单保持原答案
	got3, _ := s.GetTicket("t3")
	if got3.Answer == nil || *got3.Answer != "allow" {
		t.Fatalf("已回答工单 t3 answer=%v, want allow（不得被作废覆盖）", got3.Answer)
	}
}

// TestTicketHasEvent 验证工单通知事件的存在性判定（manager 自愈分支的依据）：
// 只认精确匹配的 ticket_id，不受同任务其他工单事件干扰，也不跨任务误判。
func TestTicketHasEvent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	// 工单存在但尚未追加通知事件 —— 这正是「崩溃落在两次写之间」的半截状态
	if has, err := s.TicketHasEvent("task-1", "task-1:perm-a"); err != nil || has {
		t.Fatalf("无事件时应返回 false：has=%v err=%v", has, err)
	}

	if _, err := s.AppendEvent("task-1", proto.EventTypePermissionRequest,
		map[string]string{"ticket_id": "task-1:perm-a", "permission": "bash"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if has, err := s.TicketHasEvent("task-1", "task-1:perm-a"); err != nil || !has {
		t.Fatalf("事件已落库时应返回 true：has=%v err=%v", has, err)
	}

	// 同任务的另一工单不得被误判为已有事件
	if has, err := s.TicketHasEvent("task-1", "task-1:perm-b"); err != nil || has {
		t.Errorf("另一工单应返回 false：has=%v err=%v", has, err)
	}
	// 事件按任务分区，跨任务同 id 不得命中
	if has, err := s.TicketHasEvent("task-2", "task-1:perm-a"); err != nil || has {
		t.Errorf("跨任务查询应返回 false：has=%v err=%v", has, err)
	}

	// question 事件同样计入（提问工单走同一自愈判定）
	if _, err := s.AppendEvent("task-1", proto.EventTypeQuestion,
		map[string]string{"ticket_id": "ask-1", "question": "选 A 还是 B"}); err != nil {
		t.Fatalf("AppendEvent question: %v", err)
	}
	if has, err := s.TicketHasEvent("task-1", "ask-1"); err != nil || !has {
		t.Errorf("question 事件应计入：has=%v err=%v", has, err)
	}
}

// TestCreateTaskPersistsBaseline 验证基线两字段能落库并回读——「这个任务建在
// 哪个提交上、当时仓库比它多几个提交」必须是事后查得到的事实，而不是只在
// 派发那一刻的日志里闪过。
func TestCreateTaskPersistsBaseline(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer s.Close()

	const sha = "d64bac4d64bac4d64bac4d64bac4d64bac4d64ba"
	task := &proto.Task{
		ID: "t-base", RepoPath: "/repo", State: proto.TaskStatePending,
		BaseCommit: sha, BaseAhead: 3,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}
	if err := s.CreateTask(task); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetTask("t-base")
	if err != nil {
		t.Fatal(err)
	}
	if got.BaseCommit != sha || got.BaseAhead != 3 {
		t.Fatalf("基线字段未持久化: base_commit=%q base_ahead=%d", got.BaseCommit, got.BaseAhead)
	}
	list, err := s.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].BaseCommit != sha || list[0].BaseAhead != 3 {
		t.Fatalf("ListTasks 未带出基线字段: %+v", list)
	}
}
