package agentd

// B233.5 T4 接缝测试：裸任务占用在启动失败、stop/done 终态与卡入口释放，且 continue 不重复准入。

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
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
