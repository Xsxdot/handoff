// 本文件测试显式恢复操作 RecoverStuck（P0-5 的收口）。
//
// 职责：
//   - 锁定「应答已落库但未送达 executor」这一卡死态的三条恢复路径：
//     重投成功回 running、executor 已不在则交协调者、无卡死则空操作
//
// 边界：
//   - 白盒测试（package agentd）：直接驱动 manager，不经 HTTP；
//     路由与 CLI 侧在 integration/cmd 测
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// stuckTicket 造出「应答已落库但未送达 executor」的卡死现场：
// 任务停在 waiting_answer，工单已答（不在 pending 里），delivered_at 为空。
func stuckTicket(t *testing.T, st *store.Store, taskID, ticketID, kind, answer string) {
	t.Helper()
	req, _ := json.Marshal(ticketRequest{Kind: kind, Permission: "bash: ls", Question: "选 A 还是 B"})
	if _, err := st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: kind, Request: req, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateTicket: %v", err)
	}
	if err := st.AnswerTicket(ticketID, answer); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	if err := st.UpdateTaskState(taskID, proto.TaskStateWaitingAnswer); err != nil {
		t.Fatalf("置 waiting_answer: %v", err)
	}
}

// TestResumeRedeliversUndeliveredAnswer 验证恢复操作会把「已落库未送达」的应答
// 重新投递给 executor，并在成功后把任务放回 running。
//
// 这是 P0-5 的主路径：reply 返回 502 后工单已被消耗，attach 看不到挂起项，
// reply/continue/done 三条路全封死——在此之前协调者没有任何自助恢复手段。
func TestResumeRedeliversUndeliveredAnswer(t *testing.T) {
	mgr, st, _, ad := newTestManager(t)
	const taskID = "task-resume-ok"
	createRunningTask(t, st, taskID)
	stuckTicket(t, st, taskID, taskID+":perm-1", "gate", "allow")

	rep, err := mgr.RecoverStuck(taskID, false)
	if err != nil {
		t.Fatalf("RecoverStuck: %v", err)
	}
	if rep.Redelivered != 1 {
		t.Errorf("应重投 1 条应答，实际 %d", rep.Redelivered)
	}
	if got := ad.permsRec(); len(got) != 1 || got[0] != "perm-1:once" {
		t.Errorf("应以裸 permID + once 重投，实际 %v", got)
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.State != proto.TaskStateRunning {
		t.Errorf("重投成功后任务应回 running，实际 %s", task.State)
	}

	// 幂等：已送达的应答不得再投一次（避免重复 respond / 重复 prompt）
	rep2, err := mgr.RecoverStuck(taskID, false)
	if err != nil {
		t.Fatalf("第二次 RecoverStuck: %v", err)
	}
	if rep2.Redelivered != 0 {
		t.Errorf("已送达的应答不应重复投递，实际重投 %d 条", rep2.Redelivered)
	}
}

// TestResumeWhenExecutorGone 验证 executor 已不在时，恢复操作把任务交给协调者
// （failed 事件 + waiting_review + 挂起工单作废），而不是让它继续卡在 waiting_answer。
//
// 修复前这条路只有「运维重启 agentd 让 RecoverOnStartup 探活」一种走法，
// 协调者在 CLI 上无路可走。
func TestResumeWhenExecutorGone(t *testing.T) {
	mgr, st, _, ad := newTestManager(t)
	ad.setRespondErr(fmt.Errorf("模拟 executor 已退出: %w", executor.ErrTaskNotRunning))
	const taskID = "task-resume-gone"
	createRunningTask(t, st, taskID)
	stuckTicket(t, st, taskID, taskID+":perm-1", "gate", "allow")
	// 另有一条未答工单：executor 已不在，它同样不该继续显示为可操作
	req, _ := json.Marshal(ticketRequest{Kind: "gate", Permission: "bash: rm -rf build"})
	if _, err := st.CreateTicket(&proto.Ticket{
		ID: taskID + ":perm-2", TaskID: taskID, Kind: "gate", Request: req, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("CreateTicket perm-2: %v", err)
	}

	rep, err := mgr.RecoverStuck(taskID, false)
	if err != nil {
		t.Fatalf("RecoverStuck: %v", err)
	}
	if rep.Redelivered != 0 || !rep.ExecutorGone {
		t.Errorf("executor 已不在时应报告 executor_gone 且零重投，实际 %+v", rep)
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.State != proto.TaskStateWaitingReview {
		t.Errorf("executor 已不在时应交协调者（waiting_review），实际 %s", task.State)
	}
	pending, err := st.PendingTickets(taskID)
	if err != nil {
		t.Fatalf("PendingTickets: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("executor 已不在，挂起工单应作废，实际还有 %d 条", len(pending))
	}
	evs, err := st.EventsFromAsc(taskID, 0, 100)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	hasFailed := false
	for _, ev := range evs {
		if ev.Type == proto.EventTypeTurnFailed {
			hasFailed = true
		}
	}
	if !hasFailed {
		t.Error("应留下 turn_failed 事件说明为何进入审核（否则协调者不知道发生了什么）")
	}
}

// TestResumeTransientFailureKeepsRetryable 验证瞬时失败（executor 还活着但调用
// 出错）时，任务保持 waiting_answer 且应答仍标记为未送达——协调者可以再 resume
// 一次，不会因为一次失败就被判定成「executor 已死」而误入审核。
func TestResumeTransientFailureKeepsRetryable(t *testing.T) {
	mgr, st, _, ad := newTestManager(t)
	ad.setRespondErr(errors.New("connection reset by peer"))
	const taskID = "task-resume-transient"
	createRunningTask(t, st, taskID)
	stuckTicket(t, st, taskID, taskID+":perm-1", "gate", "allow")

	rep, err := mgr.RecoverStuck(taskID, false)
	if err == nil {
		t.Fatal("瞬时失败应返回错误，让协调者知道这次没成功")
	}
	if rep != nil && rep.ExecutorGone {
		t.Error("瞬时失败不得判定为 executor 已死")
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.State != proto.TaskStateWaitingAnswer {
		t.Errorf("瞬时失败应保持 waiting_answer 可重试，实际 %s", task.State)
	}

	// executor 恢复后再 resume 一次应当成功
	ad.setRespondErr(nil)
	rep2, err := mgr.RecoverStuck(taskID, false)
	if err != nil {
		t.Fatalf("恢复后重试 RecoverStuck: %v", err)
	}
	if rep2.Redelivered != 1 {
		t.Errorf("恢复后应重投成功，实际重投 %d 条", rep2.Redelivered)
	}
}

// TestResumeNoopWhenNotStuck 验证没有卡死时恢复操作是空操作，不改状态、不重投。
func TestResumeNoopWhenNotStuck(t *testing.T) {
	mgr, st, _, ad := newTestManager(t)
	const taskID = "task-resume-noop"
	createRunningTask(t, st, taskID)

	rep, err := mgr.RecoverStuck(taskID, false)
	if err != nil {
		t.Fatalf("RecoverStuck: %v", err)
	}
	if rep.Redelivered != 0 || rep.ExecutorGone {
		t.Errorf("无卡死时应为空操作，实际 %+v", rep)
	}
	if len(ad.permsRec()) != 0 || len(ad.sendsRec()) != 0 {
		t.Error("无卡死时不得向 executor 发起任何调用")
	}
	task, err := st.GetTask(taskID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.State != proto.TaskStateRunning {
		t.Errorf("状态不应被改动，实际 %s", task.State)
	}
}

// TestNormalDeliveryMarksTicketDelivered 验证正常应答链路（有等待者）会标记送达，
// 否则 resume 会把已经送达过的应答再投一次。
func TestNormalDeliveryMarksTicketDelivered(t *testing.T) {
	mgr, st, hub, _ := newTestManager(t)
	const taskID = "task-delivery-mark"
	createRunningTask(t, st, taskID)
	permID := "perm-1"
	ticketID := taskID + ":" + permID

	mgr.handlePermission(context.Background(), taskID, executor.AdapterEvent{
		Type: "permission", PermissionID: permID, Text: "bash: ls",
	})
	waitAnswerRegistered(t, hub, ticketID)
	if err := st.AnswerTicket(ticketID, "allow"); err != nil {
		t.Fatalf("AnswerTicket: %v", err)
	}
	hub.NotifyAnswer(ticketID, "allow")

	eventually(t, 2*time.Second, "应答已标记送达", func() bool {
		undelivered, err := st.UndeliveredAnswers(taskID)
		return err == nil && len(undelivered) == 0
	})
}

// recordingRestorer 记录 Resume 收到的实参，供断言 manager 侧的请求装配。
type recordingRestorer struct {
	chanAdapter
	mu  sync.Mutex
	got executor.ResumeReq
}

func (r *recordingRestorer) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.got = req
	return executor.ResumeOutcome{}, nil // 不存活：本用例只验请求装配
}

// TestResumeTaskAssemblesRequest 钉住 ResumeTask 的请求装配：
// 启动恢复必须 Cold=false（不冷恢复，why 见 spec §4），且 Env/Model 必须传下去
// ——漏传 Env 会让冷恢复起出一个没有用户密钥的进程，编译照过、只在真机静默失败。
func TestResumeTaskAssemblesRequest(t *testing.T) {
	ad := &recordingRestorer{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	// env 文件：<DataDir>/env/dev.env，配置里把 fake 指向它
	envDir := filepath.Join(m.cfg.DataDir, "env")
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envDir, "dev.env"), []byte("FOO=bar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.cfg.Env = map[string]string{"fake": "dev.env"}
	m.env = envfile.NewResolver(envfile.Dir(m.cfg.DataDir), envfile.Static(m.cfg.Env), slog.New(slog.NewTextHandler(io.Discard, nil)))

	mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
		Model: "sonnet", State: proto.TaskStateRunning, ExecutorSession: "sess-1"})
	if alive := m.ResumeTask("t1"); alive {
		t.Fatalf("Resume 返回不存活时 ResumeTask 应为 false")
	}
	ad.mu.Lock()
	got := ad.got
	ad.mu.Unlock()
	if got.Cold {
		t.Fatalf("启动恢复必须 Cold=false，实际 true")
	}
	if got.SessionID != "sess-1" || got.Model != "sonnet" {
		t.Fatalf("会话/模型未透传: %+v", got)
	}
	if len(got.Env) != 1 || got.Env[0] != "FOO=bar" {
		t.Fatalf("env 未透传（漏传会让冷恢复丢掉用户密钥）: %v", got.Env)
	}
}

// TestResumeTaskReparsesPersistedDisciplineName 验证启动恢复从 SQLite 读出的名字
// 再解析纪律块，而不是只凭 executor 重新选块；两个分支都要守住，避免点名路径
// 正确而空名字的兼容兜底悄悄退化。
func TestResumeTaskReparsesPersistedDisciplineName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		want    string
		notWant string
	}{
		{name: "review", want: "只读，不写", notWant: "每个 task 完成即 commit"},
		{name: "", want: "每个 task 完成即 commit", notWant: "只读，不写"},
	} {
		t.Run(map[string]string{"review": "named", "": "fallback"}[tc.name], func(t *testing.T) {
			ad := &recordingRestorer{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
			m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
			mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
				DisciplineName: tc.name, State: proto.TaskStateRunning, ExecutorSession: "sess-1"})

			if alive := m.ResumeTask("t1"); alive {
				t.Fatal("Resume 返回不存活时 ResumeTask 应为 false")
			}
			ad.mu.Lock()
			got := ad.got
			ad.mu.Unlock()
			if !strings.Contains(got.Discipline, tc.want) {
				t.Fatalf("纪律块应含 %q，实得前 80 字节 %q", tc.want, got.Discipline[:min(len(got.Discipline), 80)])
			}
			if strings.Contains(got.Discipline, tc.notWant) {
				t.Fatalf("纪律块不应含 %q", tc.notWant)
			}
		})
	}
}

// TestContinueReparsesPersistedDisciplineName 验证 waiting_review 任务走 Continue
// 的冷恢复阶梯时，也从落盘名字重解析；否则首回合正确、续接回合会换块。
func TestContinueReparsesPersistedDisciplineName(t *testing.T) {
	for _, tc := range []struct {
		name    string
		want    string
		notWant string
	}{
		{name: "review", want: "只读，不写", notWant: "每个 task 完成即 commit"},
		{name: "", want: "每个 task 完成即 commit", notWant: "只读，不写"},
	} {
		t.Run(map[string]string{"review": "named", "": "fallback"}[tc.name], func(t *testing.T) {
			ad := &ladderAdapter{
				chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
				outcome:     executor.ResumeOutcome{Alive: true},
			}
			m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
			mustCreateTask(t, st, &proto.Task{ID: "t1", RepoPath: "/r", Executor: "fake",
				DisciplineName: tc.name, State: proto.TaskStateWaitingReview, ExecutorSession: "sess-1"})

			if err := m.Continue(context.Background(), "t1", "继续干"); err != nil {
				t.Fatalf("Continue: %v", err)
			}
			ad.mu.Lock()
			got := ad.gotReq
			ad.mu.Unlock()
			if !got.Cold {
				t.Fatal("Continue 触发的恢复必须 Cold=true")
			}
			if !strings.Contains(got.Discipline, tc.want) {
				t.Fatalf("纪律块应含 %q，实得前 80 字节 %q", tc.want, got.Discipline[:min(len(got.Discipline), 80)])
			}
			if strings.Contains(got.Discipline, tc.notWant) {
				t.Fatalf("纪律块不应含 %q", tc.notWant)
			}
		})
	}
}
