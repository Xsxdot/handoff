package agentd

// B233.5 T4 接缝测试：裸任务占用在启动失败、stop/done 终态与卡入口释放，且 continue 不重复准入。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/store"
)

// occupancyStartFailAdapter 模拟 executor 已准入但启动失败的边界，保留真实失败原因。
type occupancyStartFailAdapter struct{}

func (occupancyStartFailAdapter) Name() string { return "opencode" }

func (occupancyStartFailAdapter) Report() executor.CapabilityReport {
	return executor.CapabilityReport{
		Harness: "opencode",
		Caps:    []executor.CapabilityDecl{{Name: executor.CapExecution, Supported: true}},
	}
}

func (occupancyStartFailAdapter) Start(context.Context, executor.StartReq) error {
	return errors.New(`exec: "tmux": executable file not found in $PATH`)
}

func (occupancyStartFailAdapter) Events(string) <-chan executor.AdapterEvent {
	ch := make(chan executor.AdapterEvent)
	close(ch)
	return ch
}

func (occupancyStartFailAdapter) Send(context.Context, string, string) error { return nil }

func (occupancyStartFailAdapter) RespondPermission(context.Context, string, string, string, string) error {
	return nil
}

func (occupancyStartFailAdapter) Stop(string) error { return nil }

func actionRequest(taskID, action, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+taskID+"/"+action, strings.NewReader(body))
	req.SetPathValue("id", taskID)
	return req
}

func runAction(s *Server, req *http.Request, handler http.HandlerFunc) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	handler(rr, req)
	return rr
}

func receiverOccupancyFacade(env *ledgerEnv) *ledgerapi.Facade {
	return ledgerapi.New(env.ledger)
}

// schedulingCallCounter 是测试缝上的 registry 访问计数器：真实 Manager 的
// Continue/Resume 不应触碰编制域，因此 Select/Admit/AdmitFrozen 均不能在本轮
// 产生任何编制 registry 读写。计数器只观察调用，不改变真实 registry 行为。
type schedulingCallCounter struct {
	inner schedclient.Registry
	mu    sync.Mutex
	calls int
}

func (r *schedulingCallCounter) record() {
	r.mu.Lock()
	r.calls++
	r.mu.Unlock()
}

func (r *schedulingCallCounter) Calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *schedulingCallCounter) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	r.record()
	return r.inner.Put(kind, id, expectVersion, body, actor)
}

func (r *schedulingCallCounter) Get(kind, id string) (schedclient.Record, error) {
	r.record()
	return r.inner.Get(kind, id)
}

func (r *schedulingCallCounter) List(kind string) ([]schedclient.Record, error) {
	r.record()
	return r.inner.List(kind)
}

func (r *schedulingCallCounter) Delete(kind, id string, expectVersion int, actor string) error {
	r.record()
	return r.inner.Delete(kind, id, expectVersion, actor)
}

func TestHandleDispatchStartFailureReleases(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "opencode")
	cfg := env.srv.conf()
	env.srv.SetManager(NewManager(env.st, env.srv.Hub(),
		map[string]executor.Adapter{"opencode": occupancyStartFailAdapter{}}, cfg,
		nil, nil, newTestGate(t), discardLogger()))

	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("准入前载体计数=%d, want 0", got)
	}
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, ""))
	if rr.Code == http.StatusOK || !strings.Contains(rr.Body.String(), "tmux") {
		t.Fatalf("启动失败应携带真实原因，响应=(%d,%s)", rr.Code, rr.Body.String())
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("启动失败后载体计数=%d, want 0", got)
	}
}

func TestHandleStopReleasesBareCarrier(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	task := decodeDispatchTask(t, postDispatch(t, env.srv, dispatchBody(env.projectID, "")))
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 1 {
		t.Fatalf("stop 前载体计数=%d, want 1", got)
	}
	rr := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if rr.Code != http.StatusOK {
		t.Fatalf("stop 返回 %d: %s", rr.Code, rr.Body.String())
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("stop 后载体计数=%d, want 0", got)
	}
}

func TestHandleDoneReleasesBareCarrier(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	if _, err := env.srv.Scheduling().AdmitCarrier("muse"); err != nil {
		t.Fatalf("AdmitCarrier: %v", err)
	}
	const taskID = "done-carrier-release"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
		Carrier: "muse", HomeDir: "~/.handoff/home/muse", State: proto.TaskStateWaitingReview,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 1 {
		t.Fatalf("done 前载体计数=%d, want 1", got)
	}
	rr := runAction(env.srv, actionRequest(taskID, "done", `{}`), env.srv.handleDone)
	if rr.Code != http.StatusOK {
		t.Fatalf("done 返回 %d: %s", rr.Code, rr.Body.String())
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("done 后载体计数=%d, want 0", got)
	}
}

type closeStoreAfterDoneStopAdapter struct {
	*fake.Fake
	st *store.Store
}

func (a *closeStoreAfterDoneStopAdapter) Stop(taskID string) error {
	if err := a.Fake.Stop(taskID); err != nil {
		return err
	}
	return a.st.Close()
}

// TestHandleDoneDoesNotReturnSuccessWithoutTerminalSnapshot 让真实 Manager.Done
// 在终态迁移后关闭任务 store，模拟终态成功但随后 GetTask 读快照失败；handler 不得
// 写 200 假装已经完成释放，同时仍须用已取得的身份快照补做一次释放，避免容量泄漏。
func TestHandleDoneDoesNotReturnSuccessWithoutTerminalSnapshot(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	if _, err := env.srv.Scheduling().AdmitCarrier("muse"); err != nil {
		t.Fatalf("AdmitCarrier: %v", err)
	}
	const taskID = "done-snapshot-read-failure"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
		Carrier: "muse", HomeDir: "~/.handoff/home/muse", State: proto.TaskStateWaitingReview,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	adapter := &closeStoreAfterDoneStopAdapter{Fake: fake.New(nil), st: env.st}
	env.srv.SetManager(NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": adapter},
		env.srv.conf(), nil, nil, newTestGate(t), discardLogger()))

	rr := runAction(env.srv, actionRequest(taskID, "done", `{}`), env.srv.handleDone)
	if rr.Code == http.StatusOK {
		t.Fatalf("终态快照读取失败不应返回 200: %s", rr.Body.String())
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("终态快照读取失败后载体占用=%d，want 0", got)
	}
}

// TestHandleStopDoesNotReturnSuccessWithoutTerminalSnapshot 让真实 Manager.Stop
// 在 failed 事件落库后关闭任务 store，模拟终态迁移成功但 handler 读不到终态快照；
// handleStop 不得写 200，且必须用迁移前身份只释放一次任务占用。
func TestHandleStopDoesNotReturnSuccessWithoutTerminalSnapshot(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	if _, err := env.srv.Scheduling().AdmitCarrier("muse"); err != nil {
		t.Fatalf("AdmitCarrier: %v", err)
	}
	const taskID = "stop-snapshot-read-failure"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
		Carrier: "muse", HomeDir: "~/.handoff/home/muse", State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	facade := receiverOccupancyFacade(env.ledgerEnv)
	before, err := facade.Get("sched_running", scheduling.OccupancyCarrierKey("muse"))
	if err != nil {
		t.Fatalf("读取 stop 前载体记录: %v", err)
	}

	failedEvent := make(chan struct{})
	storeClosed := make(chan struct{})
	go func() {
		<-failedEvent
		_ = env.st.Close()
		close(storeClosed)
	}()
	env.st.SetEventHook(func(event proto.Event) {
		if event.Type != proto.EventTypeFailed {
			return
		}
		failedEvent <- struct{}{}
		<-storeClosed
	})

	rr := runAction(env.srv, actionRequest(taskID, "stop", ""), env.srv.handleStop)
	if rr.Code == http.StatusOK {
		t.Fatalf("终态快照读取失败不应返回 200: %s", rr.Body.String())
	}
	after, err := facade.Get("sched_running", scheduling.OccupancyCarrierKey("muse"))
	if err != nil {
		t.Fatalf("读取 stop 后载体记录: %v", err)
	}
	if got := runningCountIn(t, facade, scheduling.OccupancyCarrierKey("muse")); got != 0 || after.Version != before.Version+1 {
		t.Fatalf("终态快照读取失败后应只释放一次：before=(v%d,count1) after=(v%d,count%d)",
			before.Version, after.Version, got)
	}
}

func TestHandleDoneIdempotentDoesNotReleaseCompletedCarrier(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	if _, err := env.srv.Scheduling().AdmitCarrier("muse"); err != nil {
		t.Fatalf("AdmitCarrier: %v", err)
	}
	const taskID = "done-carrier-release-idem"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
		Carrier: "muse", HomeDir: "~/.handoff/home/muse", State: proto.TaskStateCompleted,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	facade := receiverOccupancyFacade(env.ledgerEnv)
	before, err := facade.Get("sched_running", scheduling.OccupancyCarrierKey("muse"))
	if err != nil {
		t.Fatalf("读取 done 前载体记录: %v", err)
	}
	rr := runAction(env.srv, actionRequest(taskID, "done", `{}`), env.srv.handleDone)
	if rr.Code != http.StatusOK {
		t.Fatalf("已 completed 的 done 返回 %d: %s", rr.Code, rr.Body.String())
	}
	afterFirst, err := facade.Get("sched_running", scheduling.OccupancyCarrierKey("muse"))
	if err != nil {
		t.Fatalf("读取首次幂等 done 后载体记录: %v", err)
	}
	if got := runningCountIn(t, facade, scheduling.OccupancyCarrierKey("muse")); afterFirst.Version != before.Version || got != 1 {
		t.Fatalf("已 completed 的 done 不应释放载体：before=(v%d,count1) after=(v%d,count%d)",
			before.Version, afterFirst.Version, got)
	}
	rr = runAction(env.srv, actionRequest(taskID, "done", `{}`), env.srv.handleDone)
	if rr.Code != http.StatusOK {
		t.Fatalf("重复 completed done 返回 %d: %s", rr.Code, rr.Body.String())
	}
	afterSecond, err := facade.Get("sched_running", scheduling.OccupancyCarrierKey("muse"))
	if err != nil {
		t.Fatalf("读取重复幂等 done 后载体记录: %v", err)
	}
	if got := runningCountIn(t, facade, scheduling.OccupancyCarrierKey("muse")); afterSecond.Version != afterFirst.Version || got != 1 {
		t.Fatalf("重复 completed done 不应再次释放载体：after1=(v%d,count1) after2=(v%d,count%d)",
			afterFirst.Version, afterSecond.Version, got)
	}
}

func TestReleaseSchedulingBindingCarrierOnly(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	binding, err := env.srv.Scheduling().AdmitCarrier("muse")
	if err != nil {
		t.Fatalf("AdmitCarrier: %v", err)
	}
	env.srv.releaseSchedulingBinding("card-carrier-only", binding)
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("空小队 binding 释放后载体计数=%d, want 0", got)
	}
	env.srv.releaseSchedulingBinding("card-carrier-only-again", binding)
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("重复释放后载体计数=%d, want 0", got)
	}
}

func TestContinueDoesNotAdmit(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	fk := fake.New([]fake.Step{{Finish: executor.Result{OK: true}}})
	cfg := env.srv.conf()
	env.srv.SetManager(NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": fk}, cfg,
		nil, nil, newTestGate(t), discardLogger()))
	task := decodeDispatchTask(t, postDispatch(t, env.srv, dispatchBody(env.projectID, "")))
	waitTaskState(t, env.st, task.ID, proto.TaskStateWaitingReview)
	before, err := env.st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 1 {
		t.Fatalf("continue 前载体计数=%d, want 1", got)
	}
	rr := runAction(env.srv, actionRequest(task.ID, "continue", `{"instructions":"再跑一次"}`), env.srv.handleContinue)
	if rr.Code != http.StatusOK {
		t.Fatalf("continue 返回 %d: %s", rr.Code, rr.Body.String())
	}
	after, err := env.st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Carrier != before.Carrier || after.HomeDir != before.HomeDir {
		t.Fatalf("continue 改写任务载体快照: before=(%q,%q) after=(%q,%q)", before.Carrier, before.HomeDir, after.Carrier, after.HomeDir)
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 1 {
		t.Fatalf("continue 后载体计数=%d, want 1（不得二次准入）", got)
	}
	rr = runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if rr.Code != http.StatusOK {
		t.Fatalf("continue 清理 stop 返回 %d: %s", rr.Code, rr.Body.String())
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("continue 后 stop 未释放载体计数=%d", got)
	}
}

// TestB23310ContinueResumeNeverAdmit 锁住真实 Manager.Continue 与 ResumeTask
// 只消费已有任务身份：二者都不得再次调用 Select/Admit/AdmitFrozen 或改写
// sched_running 的版本/count。
func TestB23310ContinueResumeNeverAdmit(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	svc := env.srv.Scheduling()
	if err := svc.PutSquad(scheduling.Squad{Name: "resume-squad", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "muse", MaxConcurrency: 1}}}, 0); err != nil {
		t.Fatalf("PutSquad: %v", err)
	}
	if _, err := svc.AdmitFrozen(scheduling.Binding{Squad: "resume-squad", Carrier: "muse",
		Target: "local", Executor: "fake", HomeDir: "~/.handoff/home/muse", Model: "frozen-model"}); err != nil {
		t.Fatalf("AdmitFrozen: %v", err)
	}
	facade := receiverOccupancyFacade(env.ledgerEnv)
	keys := []string{
		scheduling.OccupancyCarrierKey("muse"),
		scheduling.OccupancyMemberKey("resume-squad", "muse"),
	}
	type recordSnapshot struct {
		version int
		count   int
	}
	before := make(map[string]recordSnapshot, len(keys))
	for _, key := range keys {
		record, err := facade.Get("sched_running", key)
		if err != nil {
			t.Fatalf("读取 %s 初始记录: %v", key, err)
		}
		before[key] = recordSnapshot{version: record.Version, count: runningCountIn(t, facade, key)}
	}

	counter := &schedulingCallCounter{inner: facadeAsRegistry{f: receiverOccupancyFacade(env.ledgerEnv)}}
	env.srv.SetScheduling(scheduling.New(counter))
	adapter := &resumeOnlyAdapter{chanAdapter: &chanAdapter{evCh: make(chan executor.AdapterEvent)}}
	mgr := NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": adapter}, env.srv.conf(),
		nil, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr)
	const taskID = "continue-resume-no-admit"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake", Model: "frozen-model",
		Carrier: "muse", Squad: "resume-squad", HomeDir: "~/.handoff/home/muse",
		State: proto.TaskStateWaitingReview, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	if err := mgr.Continue(context.Background(), taskID, "继续"); err != nil {
		t.Fatalf("Manager.Continue: %v", err)
	}
	if alive := mgr.ResumeTask(taskID); !alive {
		t.Fatal("Manager.ResumeTask 应真实消费已有任务并返回存活")
	}
	gotTask, err := env.st.GetTask(taskID)
	if err != nil {
		t.Fatalf("读取续作后任务: %v", err)
	}
	if gotTask.Carrier != "muse" || gotTask.Squad != "resume-squad" || gotTask.Model != "frozen-model" {
		t.Fatalf("Continue/Resume 改写冻结任务身份: %+v", gotTask)
	}
	if calls := counter.Calls(); calls != 0 {
		t.Fatalf("Continue/Resume 不应调用 Select/Admit/AdmitFrozen，编制 registry 调用次数=%d", calls)
	}
	for _, key := range keys {
		record, err := facade.Get("sched_running", key)
		if err != nil {
			t.Fatalf("读取 %s 续作后记录: %v", key, err)
		}
		if got := runningCountIn(t, facade, key); got != before[key].count || record.Version != before[key].version {
			t.Fatalf("续作改写 %s：before=(v%d,count%d) after=(v%d,count%d)", key,
				before[key].version, before[key].count, record.Version, got)
		}
	}
}

// resumeOnlyAdapter 让 ResumeTask 走真实恢复入口，同时关闭事件流以免测试留下
// 永久运行的中介 goroutine；它不承载任何编制调用。
type resumeOnlyAdapter struct {
	*chanAdapter
}

func (a *resumeOnlyAdapter) Resume(executor.ResumeReq) (executor.ResumeOutcome, error) {
	return executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeReattach}, nil
}

func (a *resumeOnlyAdapter) Events(string) <-chan executor.AdapterEvent {
	ch := make(chan executor.AdapterEvent)
	close(ch)
	return ch
}
