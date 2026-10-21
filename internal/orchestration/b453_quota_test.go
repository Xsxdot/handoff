package orchestration

// b453_quota_test.go —— B453 S6/S7：回合失败分类为额度耗尽后的编排动作（V12 编排侧、V14）。
//
// 入口是真实 Manager.Dispatch（真 git 仓 + fake 执行器）；agentd 侧回调用 fakeQuotaHandler
// 记录调用并按配置回应。失败文本用 codex 的 insufficient_quota（OpenAI 公开错误码，R6a 真样本
// 到后签名表会换，但这一条是公开码不会被换掉）。
import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/proto"
)

const b453QuotaText = "stream error: insufficient_quota: You exceeded your current quota, please check your plan"

type fakeQuotaHandler struct {
	mu      sync.Mutex
	marks   []proto.Task
	reruns  []proto.Task
	hit     QuotaHit
	outcome RerunOutcome
	err     error
	// linkSeenAtRerun 记录 RerunReadonly 被调时原任务的状态：必须仍非终态（先派后落终态，Q9）
	stateAtRerun proto.TaskState
	st           interface {
		GetTask(string) (*proto.Task, error)
	}
}

func (h *fakeQuotaHandler) MarkTaskQuota(task *proto.Task, f QuotaFailure) QuotaHit {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.marks = append(h.marks, *task)
	hit := h.hit
	hit.Account = task.Account
	return hit
}

func (h *fakeQuotaHandler) RerunReadonly(ctx context.Context, task *proto.Task, f QuotaFailure) (RerunOutcome, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reruns = append(h.reruns, *task)
	if h.st != nil {
		if cur, err := h.st.GetTask(task.ID); err == nil {
			h.stateAtRerun = cur.State
		}
	}
	return h.outcome, h.err
}

func (h *fakeQuotaHandler) counts() (marks, reruns int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.marks), len(h.reruns)
}

func b453Dispatch(t *testing.T, e *b452Env, mode string) *proto.Task {
	t.Helper()
	req := DispatchReq{ProjectID: e.pid, Prompt: "调查额度", Mode: mode,
		Squad: "sq", Carrier: "ca", Account: "acct-a", AccountKind: "codex"}
	if mode != proto.TaskModeReadonly {
		req.NewBranch, req.NewWorktree = "b453-quota", true
	}
	task, err := e.m.Dispatch(context.Background(), req)
	if err != nil {
		t.Fatalf("派发失败: %v", err)
	}
	return task
}

func (e *b452Env) events(t *testing.T, id string) []proto.Event {
	t.Helper()
	evs, err := e.st.EventsFrom(id, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	return evs
}

func payloadText(ev proto.Event) string { return string(ev.Payload) }

// waitFailedEvent 等 failed 事件落库并返回它。stopWithReason 先迁 failed、后追加 failed 事件
// （B97 次序，产品侧不改），waitTaskState 看到 failed 时事件可能还没写——读在两步之间会空过
// 或误红（finish-check 在 linux-01 16 核上确定性复现）。凡「终态后读 failed 事件/原因」都经这里。
func (e *b452Env) waitFailedEvent(t *testing.T, id string) proto.Event {
	t.Helper()
	var got proto.Event
	eventually(t, 3*time.Second, "failed 事件落库", func() bool {
		for _, ev := range e.events(t, id) {
			if ev.Type == proto.EventTypeFailed {
				got = ev
				return true
			}
		}
		return false
	})
	return got
}

// V12 编排侧：只读任务撞额度 → 标账号、回调派新任务、先写 rerun_by 再落 failed，时间线有
// 「因额度耗尽改由 X 重跑」；不出 turn_failed，不停 waiting_review；终态回调一次。
func TestB453QuotaReadonlyRerunLinksThenFails(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: executor.Result{OK: false, FailReason: b453QuotaText}}})
	h := &fakeQuotaHandler{st: e.st, outcome: RerunOutcome{
		Ref: proto.TaskRef{Task: "t-new", Machine: "linux-01"}, Carrier: "cb"}}
	e.m.SetQuotaHandler(h)
	task := b453Dispatch(t, e, proto.TaskModeReadonly)
	waitTaskState(t, e.st, task.ID, proto.TaskStateFailed)
	e.waitFailedEvent(t, task.ID)

	cur, err := e.st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.RerunBy == nil || *cur.RerunBy != (proto.TaskRef{Task: "t-new", Machine: "linux-01"}) {
		t.Fatalf("原任务应带 rerun_by，得 %+v", cur.RerunBy)
	}
	marks, reruns := h.counts()
	if marks != 1 || reruns != 1 {
		t.Fatalf("标账号/重跑各应一次，得 %d/%d", marks, reruns)
	}
	if h.stateAtRerun.IsTerminal() {
		t.Fatalf("重跑回调时原任务已终态 %s：终态与链接必须一起可见（先派后落终态）", h.stateAtRerun)
	}
	evs := e.events(t, task.ID)
	progressAt, failedAt := -1, -1
	for i, ev := range evs {
		switch ev.Type {
		case proto.EventTypeTurnFailed:
			t.Fatalf("只读撞额度不应出 turn_failed（会让 --wait 先以 3 结束）: %s", payloadText(ev))
		case proto.EventTypeProgress:
			if strings.Contains(payloadText(ev), "因额度耗尽改由 cb 重跑") {
				progressAt = i
			}
		case proto.EventTypeFailed:
			failedAt = i
			if !strings.Contains(payloadText(ev), "额度耗尽") || !strings.Contains(payloadText(ev), "t-new") {
				t.Fatalf("failed 原因应写明额度耗尽与新任务: %s", payloadText(ev))
			}
		}
	}
	if progressAt < 0 || failedAt < 0 || progressAt > failedAt {
		t.Fatalf("时间线应先有「因额度耗尽改由 cb 重跑」再 failed：progress=%d failed=%d", progressAt, failedAt)
	}
	eventually(t, 3*time.Second, "终态回调一次", func() bool { return e.note.count() == 1 })
}

// b453StopProbe 在 fake 外拦 Stop：stopWithReason 迁 failed 之前必先 Stop 执行器，这一刻读
// 任务行的 rerun_by——Q9「先写链接、再落终态」的确定性观察点（订阅 hub 收 failed 事件再读库
// 会与「链接晚写」赛跑，变异下可能偶然绿）。
type b453StopProbe struct {
	*b452StartRecorder
	st interface {
		GetTask(string) (*proto.Task, error)
	}
	mu      sync.Mutex
	atStop  []*proto.TaskRef
	stateAt []proto.TaskState
}

func (a *b453StopProbe) Stop(taskID string) error {
	if cur, err := a.st.GetTask(taskID); err == nil {
		a.mu.Lock()
		a.atStop = append(a.atStop, cur.RerunBy)
		a.stateAt = append(a.stateAt, cur.State)
		a.mu.Unlock()
	}
	return a.b452StartRecorder.Stop(taskID)
}

// 审查第 1 轮 I3b：只读撞额度换人重跑时，原任务迁 failed 的那一刻 rerun_by 已在库里
// （Q9：客户端见 failed 而无链接即以 3 结束）。观察点两处：Stop 执行器的当下（迁 failed 之前），
// 与终态回调拿到的快照。变异 M6b（把写链接挪到 stopWithReason 之后）在 Stop 观察点确定性转红。
func TestB453QuotaReadonlyRerunLinkVisibleBeforeFailed(t *testing.T) {
	fk := fake.New([]fake.Step{{Finish: executor.Result{OK: false, FailReason: b453QuotaText}}})
	rec := &b452StartRecorder{Adapter: fk}
	probe := &b453StopProbe{b452StartRecorder: rec}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": probe}, "fake")
	probe.st = st
	note := &b452Notifier{}
	m.SetTerminalNotifier(note)
	repo := initTestRepo(t)
	e := &b452Env{m: m, st: st, fk: fk, rec: rec, note: note, repo: repo, pid: registerTestProject(t, m, repo)}
	want := proto.TaskRef{Task: "t-new", Machine: "linux-01"}
	m.SetQuotaHandler(&fakeQuotaHandler{st: st, outcome: RerunOutcome{Ref: want, Carrier: "cb"}})
	task := b453Dispatch(t, e, proto.TaskModeReadonly)
	waitTaskState(t, st, task.ID, proto.TaskStateFailed)
	eventually(t, 3*time.Second, "终态回调一次", func() bool { return note.count() == 1 })

	probe.mu.Lock()
	atStop, stateAt := append([]*proto.TaskRef(nil), probe.atStop...), append([]proto.TaskState(nil), probe.stateAt...)
	probe.mu.Unlock()
	if len(atStop) == 0 {
		t.Fatal("撞额度落 failed 应经 stopWithReason 停执行器，Stop 未被调用")
	}
	last := len(atStop) - 1
	if stateAt[last].IsTerminal() {
		t.Fatalf("Stop 观察点应在迁 failed 之前，得状态 %s", stateAt[last])
	}
	if atStop[last] == nil || *atStop[last] != want {
		t.Fatalf("迁 failed 之前 rerun_by 应已写入（Q9 先写链接再落终态），得 %+v", atStop[last])
	}
	note.mu.Lock()
	snap := note.tasks[0]
	note.mu.Unlock()
	if snap.State != proto.TaskStateFailed || snap.RerunBy == nil || *snap.RerunBy != want {
		t.Fatalf("终态回调快照应为 failed 且带 rerun_by，得 %s / %+v", snap.State, snap.RerunBy)
	}
}

// S6.5：没有可换的成员 → 原任务以额度耗尽失败，原因带回调的 S5 文案；无链接。
func TestB453QuotaReadonlyNoCandidateFailsWithReason(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: executor.Result{OK: false, FailReason: b453QuotaText}}})
	s5 := "小队 sq 没有可用载体——cb：账号 acct-b 无额度"
	h := &fakeQuotaHandler{st: e.st, err: errors.New(s5)}
	e.m.SetQuotaHandler(h)
	task := b453Dispatch(t, e, proto.TaskModeReadonly)
	waitTaskState(t, e.st, task.ID, proto.TaskStateFailed)
	reason := payloadText(e.waitFailedEvent(t, task.ID))
	cur, _ := e.st.GetTask(task.ID)
	if cur.RerunBy != nil {
		t.Fatalf("无人可换不应有链接: %+v", cur.RerunBy)
	}
	if !strings.Contains(reason, "额度耗尽") || !strings.Contains(reason, s5) {
		t.Fatalf("failed 原因应含额度耗尽与 S5 文案，得 %s", reason)
	}
	eventually(t, 3*time.Second, "终态回调一次", func() bool { return e.note.count() == 1 })
}

// V14：分支任务撞额度 → 标账号、waiting_review、工作树保留、恰一张 notice 工单（字段齐全）；
// 答「已知悉」不调用执行器 Send/RespondPermission，任务仍 waiting_review。
func TestB453QuotaBranchOpensNoticeTicket(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: executor.Result{OK: false, FailReason: b453QuotaText}}})
	recover := time.Now().Add(2*time.Hour + 41*time.Minute)
	h := &fakeQuotaHandler{st: e.st, hit: QuotaHit{Windows: []string{"5h 已用 100%"}, RecoverAt: &recover}}
	e.m.SetQuotaHandler(h)
	task := b453Dispatch(t, e, "")
	waitTaskState(t, e.st, task.ID, proto.TaskStateWaitingReview)
	eventually(t, 3*time.Second, "turn_failed 落库", func() bool {
		return indexOfType(e.eventTypes(t, task.ID), proto.EventTypeTurnFailed) >= 0
	})
	pending, err := e.st.PendingTickets(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Kind != proto.TicketKindNotice {
		t.Fatalf("应恰有一张 notice 待答工单，得 %+v", pending)
	}
	var req proto.NoticeRequest
	if err := json.Unmarshal(pending[0].Request, &req); err != nil {
		t.Fatal(err)
	}
	if req.Kind != proto.TicketKindNotice {
		t.Fatalf("请求体 kind 应为 notice: %+v", req)
	}
	for _, want := range []string{"ca", "acct-a", "5h 已用 100%", "2h41m", task.Branch, task.WorkDir, "接管见 B459"} {
		if !strings.Contains(req.Notice, want) {
			t.Fatalf("notice 全文缺 %q：\n%s", want, req.Notice)
		}
	}
	if req.Carrier != "ca" || req.Account != "acct-a" || req.Branch != task.Branch || req.Worktree != task.WorkDir || req.RecoverAt == nil {
		t.Fatalf("notice 结构字段不全: %+v", req)
	}
	if marks, reruns := h.counts(); marks != 1 || reruns != 0 {
		t.Fatalf("分支任务应标账号一次、不重跑，得 %d/%d", marks, reruns)
	}
	if _, err := os.Stat(task.WorkDir); err != nil {
		t.Fatalf("工作树应保留: %v", err)
	}
	// 答「已知悉」：只记 delivered，不回传执行器
	sendsBefore, permsBefore := len(e.fk.Sends()), len(e.fk.Perms())
	if applied, err := e.m.AnswerTicket(context.Background(), task.ID, pending[0].ID, "已知悉"); err != nil || !applied {
		t.Fatalf("答复 notice 失败: applied=%v err=%v", applied, err)
	}
	if err := e.m.RelayAnswer(task.ID, pending[0].ID, "已知悉"); err != nil {
		t.Fatalf("notice 中继应成功（只记 delivered）: %v", err)
	}
	if len(e.fk.Sends()) != sendsBefore || len(e.fk.Perms()) != permsBefore {
		t.Fatalf("notice 答复不得调用执行器：sends %d→%d perms %d→%d",
			sendsBefore, len(e.fk.Sends()), permsBefore, len(e.fk.Perms()))
	}
	tk, err := e.st.GetTicket(pending[0].ID)
	if err != nil || tk.DeliveredAt == nil {
		t.Fatalf("notice 答复后应记 delivered: %+v err=%v", tk, err)
	}
	e.m.ResumeIfIdle(context.Background(), task.ID)
	if cur, _ := e.st.GetTask(task.ID); cur.State != proto.TaskStateWaitingReview {
		t.Fatalf("答复 notice 后任务应仍 waiting_review，得 %s", cur.State)
	}
}

// 限速不算额度（Q10）：普通回合失败路径，不标账号、不重跑、不开 notice。
func TestB453RateLimitedTurnFailureIsOrdinary(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: executor.Result{OK: false, FailReason: "HTTP 429 Too Many Requests"}}})
	h := &fakeQuotaHandler{st: e.st}
	e.m.SetQuotaHandler(h)
	task := b453Dispatch(t, e, proto.TaskModeReadonly)
	waitTaskState(t, e.st, task.ID, proto.TaskStateWaitingReview)
	if marks, reruns := h.counts(); marks != 0 || reruns != 0 {
		t.Fatalf("限速不得标账号或重跑，得 %d/%d", marks, reruns)
	}
	if pending, _ := e.st.PendingTickets(task.ID); len(pending) != 0 {
		t.Fatalf("限速不得开工单: %+v", pending)
	}
}

// 未注入回调（组装缺失）：只读撞额度退回普通失败路径（waiting_review 交人），不静默丢结果。
func TestB453QuotaReadonlyWithoutHandlerFallsBackToReview(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: executor.Result{OK: false, FailReason: b453QuotaText}}})
	task := b453Dispatch(t, e, proto.TaskModeReadonly)
	waitTaskState(t, e.st, task.ID, proto.TaskStateWaitingReview)
	eventually(t, 3*time.Second, "turn_failed 落库", func() bool {
		return indexOfType(e.eventTypes(t, task.ID), proto.EventTypeTurnFailed) >= 0
	})
}

// 续接（人已拍板接着干）关掉未答的 notice：否则续接回合进行中 --wait 会因陈旧告知立即以 5 返回。
func TestB453ContinueVoidsPendingNotice(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: executor.Result{OK: false, FailReason: b453QuotaText}}})
	e.m.SetQuotaHandler(&fakeQuotaHandler{st: e.st})
	task := b453Dispatch(t, e, "")
	waitTaskState(t, e.st, task.ID, proto.TaskStateWaitingReview)
	eventually(t, 3*time.Second, "notice 落库", func() bool {
		p, _ := e.st.PendingTickets(task.ID)
		return len(p) == 1
	})
	e.fk.Add(task.ID, fake.Step{Finish: executor.Result{OK: true, Summary: "s"}})
	if err := e.m.Continue(context.Background(), task.ID, "额度已恢复，接着做"); err != nil {
		t.Fatalf("continue: %v", err)
	}
	if p, _ := e.st.PendingTickets(task.ID); len(p) != 0 {
		t.Fatalf("续接后告知工单应作废，仍有 %+v", p)
	}
}

// 真机 R6b/R7 瑕疵 1：执行器失败原因 = 通用处置句（零文本产出…可 continue 续接重试）+ 会话错误。
// 判额度耗尽后，failed / turn_failed 原因与 notice 只写错误原文（Result.ErrorDetail），不再拼
// 那句——只读原任务已终止、分支任务 notice 写明不续接原会话，「可 continue」是错的指引。
const b453ZeroTextGeneric = "回合结束但零文本产出（可能是供应商流中断）；executor 仍在线，可 continue 续接重试"

func b453ZeroTextQuotaResult() executor.Result {
	return executor.Result{OK: false, FailReason: b453ZeroTextGeneric + "；会话错误：" + b453QuotaText, ErrorDetail: b453QuotaText}
}

func assertNoGenericZeroText(t *testing.T, where, text string) {
	t.Helper()
	if !strings.Contains(text, "insufficient_quota") {
		t.Fatalf("%s 应带错误原文：%s", where, text)
	}
	for _, bad := range []string{"零文本产出", "可 continue 续接重试"} {
		if strings.Contains(text, bad) {
			t.Fatalf("%s 不应拼通用零文本句 %q：%s", where, bad, text)
		}
	}
}

func TestB453QuotaReasonOmitsGenericZeroTextReadonly(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: b453ZeroTextQuotaResult()}})
	h := &fakeQuotaHandler{st: e.st, outcome: RerunOutcome{Ref: proto.TaskRef{Task: "t-new", Machine: "linux-01"}, Carrier: "cb"}}
	e.m.SetQuotaHandler(h)
	task := b453Dispatch(t, e, proto.TaskModeReadonly)
	waitTaskState(t, e.st, task.ID, proto.TaskStateFailed)
	assertNoGenericZeroText(t, "failed 原因", payloadText(e.waitFailedEvent(t, task.ID)))
}

func TestB453QuotaReasonOmitsGenericZeroTextBranch(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: b453ZeroTextQuotaResult()}})
	e.m.SetQuotaHandler(&fakeQuotaHandler{st: e.st})
	task := b453Dispatch(t, e, "")
	waitTaskState(t, e.st, task.ID, proto.TaskStateWaitingReview)
	eventually(t, 3*time.Second, "turn_failed 落库", func() bool {
		return indexOfType(e.eventTypes(t, task.ID), proto.EventTypeTurnFailed) >= 0
	})
	for _, ev := range e.events(t, task.ID) {
		if ev.Type == proto.EventTypeTurnFailed {
			assertNoGenericZeroText(t, "turn_failed 原因", payloadText(ev))
		}
	}
	pending, err := e.st.PendingTickets(task.ID)
	if err != nil || len(pending) != 1 {
		t.Fatalf("应恰有一张 notice: %+v err=%v", pending, err)
	}
	var req proto.NoticeRequest
	if err := json.Unmarshal(pending[0].Request, &req); err != nil {
		t.Fatal(err)
	}
	assertNoGenericZeroText(t, "notice 全文", req.Notice)
}

// 非额度失败照旧：通用处置句原样进 turn_failed（「可 continue」对普通零文本回合是对的）。
func TestB453NonQuotaZeroTextKeepsGenericReason(t *testing.T) {
	e := newB452Env(t, []fake.Step{{Finish: executor.Result{OK: false, FailReason: b453ZeroTextGeneric + "；会话错误：upstream 500", ErrorDetail: "upstream 500"}}})
	e.m.SetQuotaHandler(&fakeQuotaHandler{st: e.st})
	task := b453Dispatch(t, e, "")
	waitTaskState(t, e.st, task.ID, proto.TaskStateWaitingReview)
	eventually(t, 3*time.Second, "turn_failed 落库", func() bool {
		return indexOfType(e.eventTypes(t, task.ID), proto.EventTypeTurnFailed) >= 0
	})
	for _, ev := range e.events(t, task.ID) {
		if ev.Type == proto.EventTypeTurnFailed && !strings.Contains(payloadText(ev), "可 continue 续接重试") {
			t.Fatalf("普通零文本失败应保留通用处置句：%s", payloadText(ev))
		}
	}
}
