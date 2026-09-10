package agentd

// B233.5 T2 接缝测试：裸 HTTP 派发按载体/小队统一解析、准入与物理绑定。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	handoffclient "github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

type receiverTestEnv struct {
	*ledgerEnv
	mgr       *Manager
	projectID string
}

func newReceiverTestEnv(t *testing.T) *receiverTestEnv {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger)
	cfg := env.srv.conf()
	cfg.Executor.Default = "opencode"
	mgr := NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": fake.New(nil)}, cfg,
		nil, nil, newTestGate(t), discardLogger())
	env.srv.SetManager(mgr)
	repo := initTestRepo(t)
	return &receiverTestEnv{ledgerEnv: env, mgr: mgr, projectID: registerTestProject(t, mgr, repo)}
}

func seedDefaultFakeCarrier(t *testing.T, srv *Server, cli string) {
	t.Helper()
	svc := srv.Scheduling()
	if svc == nil {
		t.Fatal("需要 SetupAutomation")
	}
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "muse", Machine: "local", CLI: cli,
		HomeDir: "~/.handoff/home/muse", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 8, Status: scheduling.StatusOnline,
	})
	if err := svc.SetDefaultCarrier("muse"); err != nil {
		t.Fatalf("SetDefaultCarrier: %v", err)
	}
}

func postDispatch(t *testing.T, srv *Server, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/tasks", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleDispatch(rr, req)
	return rr
}

func dispatchBody(projectID, extra string) string {
	return `{"project_id":"` + projectID + `","prompt":"x"` + extra + `}`
}

func decodeDispatchTask(t *testing.T, rr *httptest.ResponseRecorder) proto.Task {
	t.Helper()
	var task proto.Task
	if err := json.Unmarshal(rr.Body.Bytes(), &task); err != nil {
		t.Fatalf("解码派发响应: %v; body=%s", err, rr.Body.String())
	}
	return task
}

func assertDispatchError(t *testing.T, rr *httptest.ResponseRecorder, code int, text string) {
	t.Helper()
	if rr.Code != code || !strings.Contains(rr.Body.String(), text) {
		t.Fatalf("派发响应=(%d,%s)，want (%d, contains %q)", rr.Code, rr.Body.String(), code, text)
	}
}

func TestHandleDispatchEmptyReceiverBindsDefault(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, ""))
	if rr.Code != http.StatusOK {
		t.Fatalf("默认载体派发返回 %d: %s", rr.Code, rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	if task.Carrier != "muse" || task.Target != "local" || task.Executor != "fake" || task.HomeDir != "~/.handoff/home/muse" {
		t.Fatalf("默认载体快照不对: %+v", task)
	}
	if err := env.srv.Scheduling().Release("", "muse"); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDispatchNoDefaultFails(t *testing.T) {
	env := newReceiverTestEnv(t)
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, ""))
	assertDispatchError(t, rr, http.StatusBadRequest, "没有有效的默认载体")
	if tasks, err := env.st.ListTasks(); err != nil || len(tasks) != 0 {
		t.Fatalf("无默认不得创建任务: tasks=%+v err=%v", tasks, err)
	}
}

func TestHandleDispatchExplicitReceiver(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, `,"receiver":"muse"`))
	if rr.Code != http.StatusOK {
		t.Fatalf("显式载体派发返回 %d: %s", rr.Code, rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	if task.Carrier != "muse" || task.Target != "local" || task.Executor != "fake" {
		t.Fatalf("显式载体快照不对: %+v", task)
	}
	if err := env.srv.Scheduling().Release("", "muse"); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDispatchNameConflict(t *testing.T) {
	env := newReceiverTestEnv(t)
	facade := ledgerapi.New(env.ledger)
	carrierBody, err := json.Marshal(scheduling.Carrier{Name: "muse", Machine: "local", CLI: "fake", Credential: scheduling.CredentialStandalone, Status: scheduling.StatusOnline})
	if err != nil {
		t.Fatal(err)
	}
	squadBody, err := json.Marshal(scheduling.Squad{Name: "muse", Role: scheduling.RoleExecutor})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Put("carrier", "muse", 0, carrierBody, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Put("squad", "muse", 0, squadBody, "test"); err != nil {
		t.Fatal(err)
	}
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, `,"receiver":"muse"`))
	assertDispatchError(t, rr, http.StatusConflict, "名称同时登记为载体和小队")
}

func TestHandleDispatchSquadReceiver(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	if err := env.srv.Scheduling().PutSquad(scheduling.Squad{Name: "rd", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "muse", MaxConcurrency: 8}}}, 0); err != nil {
		t.Fatal(err)
	}
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, `,"receiver":"rd"`))
	if rr.Code != http.StatusOK {
		t.Fatalf("小队载体派发返回 %d: %s", rr.Code, rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	if task.Squad != "rd" || task.Carrier != "muse" || task.Target != "local" || task.Executor != "fake" {
		t.Fatalf("小队绑定快照不对: %+v", task)
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyMemberKey("rd", "muse")); got != 1 {
		t.Fatalf("小队成员占用=%d, want 1", got)
	}
	stop := runAction(env.srv, actionRequest(task.ID, "stop", ""), env.srv.handleStop)
	if stop.Code != http.StatusOK {
		t.Fatalf("小队派发 stop 返回 %d: %s", stop.Code, stop.Body.String())
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyMemberKey("rd", "muse")); got != 0 {
		t.Fatalf("小队 stop 后成员占用=%d, want 0", got)
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("小队 stop 后载体占用=%d, want 0", got)
	}
}

// TestB23310ClientDispatchUsesFrozenIdentity 验证客户端已经解析出的冻结身份
// 是 /api/tasks 的准入依据；Receiver 仅保留为审计提示，不得重新选择载体。
func TestB23310ClientDispatchUsesFrozenIdentity(t *testing.T) {
	env := newReceiverTestEnv(t)
	svc := env.srv.Scheduling()
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "carrier-A", Machine: "local", CLI: "fake",
		HomeDir: "/home/carrier-A", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1, Status: scheduling.StatusOnline})
	putOnlineCarrier(t, svc, scheduling.Carrier{Name: "carrier-B", Machine: "local", CLI: "fake",
		HomeDir: "/home/carrier-B", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1, Status: scheduling.StatusOnline})
	if err := svc.PutSquad(scheduling.Squad{Name: "squad-A", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "carrier-A", MaxConcurrency: 1}}}, 0); err != nil {
		t.Fatalf("登记 squad-A: %v", err)
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "squad-B", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "carrier-B", MaxConcurrency: 1}}}, 0); err != nil {
		t.Fatalf("登记 squad-B: %v", err)
	}
	home := "/home/carrier-A"
	task, err := handoffclient.New(env.ts.URL, env.token).Dispatch(context.Background(), handoffclient.DispatchOpts{
		ProjectID: env.projectID, Prompt: "x", Target: "local", Executor: "fake", Model: "model-A",
		Receiver: "squad-B", Carrier: "carrier-A", Squad: "squad-A", HomeDir: &home,
	})
	if err != nil {
		t.Fatalf("冻结身份派发: %v", err)
	}
	if task.Carrier != "carrier-A" || task.Squad != "squad-A" || task.HomeDir != home ||
		task.Target != "local" || task.Executor != "fake" || task.Model != "model-A" {
		t.Fatalf("任务未保存冻结身份: %+v", task)
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("carrier-A")); got != 1 {
		t.Fatalf("冻结载体占用=%d, want 1", got)
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyCarrierKey("carrier-B")); got != 0 {
		t.Fatalf("Receiver 指向的载体被错误占用=%d, want 0", got)
	}
	if got := runningCountIn(t, receiverOccupancyFacade(env.ledgerEnv), scheduling.OccupancyMemberKey("squad-A", "carrier-A")); got != 1 {
		t.Fatalf("冻结小队成员占用=%d, want 1", got)
	}
}

func TestHandleDispatchPhysicalOverride(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, `,"target":"other","executor":"grok","home_dir":"/other"`))
	assertDispatchError(t, rr, http.StatusConflict, "禁止覆盖已绑定载体")
	if tasks, err := env.st.ListTasks(); err != nil || len(tasks) != 0 {
		t.Fatalf("物理覆盖拒绝不得创建任务: tasks=%+v err=%v", tasks, err)
	}
	rr = postDispatch(t, env.srv, dispatchBody(env.projectID, `,"target":"local","executor":"fake","home_dir":"~/.handoff/home/muse"`))
	if rr.Code != http.StatusOK {
		t.Fatalf("重述相同物理身份应成功: %d %s", rr.Code, rr.Body.String())
	}
	if err := env.srv.Scheduling().Release("", "muse"); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDispatchModelOverlay(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, `,"model":"gpt-y"`))
	if rr.Code != http.StatusOK {
		t.Fatalf("模型覆盖派发返回 %d: %s", rr.Code, rr.Body.String())
	}
	task := decodeDispatchTask(t, rr)
	if task.Model != "gpt-y" || task.Target != "local" || task.Executor != "fake" || task.HomeDir != "~/.handoff/home/muse" {
		t.Fatalf("模型覆盖改变绑定身份: %+v", task)
	}
	if err := env.srv.Scheduling().Release("", "muse"); err != nil {
		t.Fatal(err)
	}
}

func TestHandleDispatchNoSlotBusy(t *testing.T) {
	env := newReceiverTestEnv(t)
	putOnlineCarrier(t, env.srv.Scheduling(), scheduling.Carrier{Name: "muse", Machine: "local", CLI: "fake",
		HomeDir: "~/.handoff/home/muse", Credential: scheduling.CredentialStandalone, MaxConcurrency: 1})
	if err := env.srv.Scheduling().SetDefaultCarrier("muse"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.srv.Scheduling().AdmitCarrier("muse"); err != nil {
		t.Fatal(err)
	}
	rr := postDispatch(t, env.srv, dispatchBody(env.projectID, ""))
	assertDispatchError(t, rr, http.StatusConflict, "小队或载体并发已满")
	if tasks, err := env.st.ListTasks(); err != nil || len(tasks) != 0 {
		t.Fatalf("满员不得创建任务: tasks=%+v err=%v", tasks, err)
	}
	if rows, err := ledgerapi.New(env.ledger).List(scheduling.KindIgnitionQueue); err != nil || len(rows) != 0 {
		t.Fatalf("裸派发满员不得入 ignition_queue: rows=%+v err=%v", rows, err)
	}
	if err := env.srv.Scheduling().Release("", "muse"); err != nil {
		t.Fatal(err)
	}
}

func setupSquadEnvWithHome(t *testing.T, home string) (*ledgerEnv, *fakeTargetMachine) {
	t.Helper()
	env := newLedgerEnv(t)
	env.srv.SetupAutomation(env.ledger)
	yes := true
	ftm := newFakeTargetMachine(t, &yes)
	registerFakeTarget(t, env.srv, "ftm", ftm)
	if ver := seedDisciplineOnLedger(t, env, "implement", "# 实现纪律\n完成即 commit\n"); ver < 1 {
		t.Fatalf("纪律块版本异常: %d", ver)
	}
	putOnlineCarrier(t, env.srv.Scheduling(), scheduling.Carrier{Name: "c1", Machine: "ftm", CLI: "opencode",
		HomeDir: home, Credential: scheduling.CredentialStandalone, MaxConcurrency: 2})
	if err := env.srv.Scheduling().PutSquad(scheduling.Squad{Name: "sq1", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 8}}}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	return env, ftm
}

func TestStartCardStepUsesBindingHomeDir(t *testing.T) {
	const home = "~/.handoff/home/c1"
	env, ftm := setupSquadEnvWithHome(t, home)
	cardID := seedSquadFlow(t, env, "sq1", 1)[0]
	env.srv.runStepFn = func(ctx context.Context, runner *ledgerstep.StepRunner, cardID, node string) {
		env.srv.runStep(ctx, runner, cardID, node)
	}
	if err := env.srv.startCardStep(cardID, proto.CardStepReq{Step: "implement", Actor: "web:test"}); err != nil {
		t.Fatalf("受理: %v", err)
	}
	waitFor(t, func() bool { return ftm.dispatchCount() == 1 })
	if got := ftm.lastDispatch()["home_dir"]; got != home {
		t.Fatalf("卡节点 HOME 必须来自 Binding 快照，got %v want %q", got, home)
	}
	src, err := os.ReadFile("cardstep.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start := strings.Index(body, "func (s *Server) startCardStep(")
	if start < 0 {
		t.Fatal("找不到 startCardStep")
	}
	end := strings.Index(body[start+1:], "\nfunc ")
	if end > 0 {
		body = body[start : start+1+end]
	} else {
		body = body[start:]
	}
	if strings.Contains(body, "s.scheduling.Carrier(") {
		t.Fatal("startCardStep 不得在准入后重新读取活载体 HOME")
	}
}
