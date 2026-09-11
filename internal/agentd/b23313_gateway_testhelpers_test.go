// b23313_gateway_testhelpers_test.go —— B233.13 迁包后 gateway 白盒测试的通用夹具。
//
// 职责：为仍留在 package agentd 的出站/门面白盒测试提供构造真实编排实现的助手
// （经 ManagerFactory，见 b23313_managerfactory_test.go），以及从编排测试迁出的
// 通用 adapter/断言助手在本包的等价副本（两包各自独立，不跨测试包 import）。
//
// 边界：只做夹具，不含被迁移的 Manager 行为断言（那些在 internal/orchestration）。
package agentd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// looseTempDir 建一个测试用临时目录，收尾时尽力删除、删不掉也不判用例失败。
func looseTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "agentd-test-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// newTestGate 造一个只带内置黑名单的判据网关。
func newTestGate(t *testing.T) *permgate.Gate {
	t.Helper()
	g, err := permgate.New(nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("permgate.New: %v", err)
	}
	return g
}

// eventually 轮询 cond 直到为真或超时，超时即 Fatal。
func eventually(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("等待超时（%s）：%s", timeout, desc)
}

// chanAdapter 是测试用空操作 adapter：事件通道由测试直接控制。
type chanAdapter struct {
	mu           sync.Mutex
	evCh         chan executor.AdapterEvent
	providerName string
	lastStart    executor.StartReq
	perms        []string
	sends        []string
	respondErr   error
	denyInBand   bool
}

func (a *chanAdapter) Name() string {
	if a.providerName != "" {
		return a.providerName
	}
	return "fake"
}

func (a *chanAdapter) Report() executor.CapabilityReport {
	return executor.CapabilityReport{
		Harness: a.Name(),
		Caps:    []executor.CapabilityDecl{{Name: executor.CapExecution, Supported: true}},
	}
}

func (a *chanAdapter) setProviderName(name string) { a.providerName = name }

func (a *chanAdapter) setRespondErr(err error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.respondErr = err
}

func (a *chanAdapter) setDenyReasonInBand(v bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.denyInBand = v
}

func (a *chanAdapter) DenyReasonInBand() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.denyInBand
}

func (a *chanAdapter) Start(_ context.Context, req executor.StartReq) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.lastStart = req
	return nil
}
func (a *chanAdapter) Events(string) <-chan executor.AdapterEvent { return a.evCh }

func (a *chanAdapter) Send(_ context.Context, _ string, text string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.respondErr != nil {
		return a.respondErr
	}
	a.sends = append(a.sends, text)
	return nil
}

func (a *chanAdapter) RespondPermission(_ context.Context, _ string, permID, decision, _ string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.respondErr != nil {
		return a.respondErr
	}
	a.perms = append(a.perms, permID+":"+decision)
	return nil
}

func (a *chanAdapter) Stop(string) error { return nil }

func (a *chanAdapter) permsRec() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.perms...)
}

func (a *chanAdapter) recordedPerms() []string { return a.permsRec() }

func (a *chanAdapter) lastStartReq() executor.StartReq {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastStart
}

func (a *chanAdapter) sendsRec() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.sends...)
}

// recordingProfile 记录 ProfileReq 供断言。
type recordingProfile struct {
	mu   sync.Mutex
	reqs []executor.ProfileReq
}

func (p *recordingProfile) Inspect(context.Context, executor.ProfileReq) (executor.ProfileReport, error) {
	return executor.ProfileReport{}, nil
}

func (p *recordingProfile) Prepare(_ context.Context, req executor.ProfileReq) (executor.ProfileReport, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reqs = append(p.reqs, req)
	return executor.ProfileReport{HomeDir: req.HomeDir, Isolated: req.Isolated, Prepared: true}, nil
}

func (p *recordingProfile) Verify(context.Context, executor.ProfileReq) (executor.ProfileReport, error) {
	return executor.ProfileReport{}, nil
}

func (p *recordingProfile) snapshot() []executor.ProfileReq {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]executor.ProfileReq(nil), p.reqs...)
}

type profileRecordingAdapter struct {
	*chanAdapter
	profile *recordingProfile
}

func (a *profileRecordingAdapter) Profile() executor.Profile { return a.profile }

// failStartAdapter 是 Start 恒失败的 adapter（executor 起不来）。
type failStartAdapter struct {
	providerName string
}

func (a *failStartAdapter) Name() string {
	if a.providerName != "" {
		return a.providerName
	}
	return "fake"
}
func (a *failStartAdapter) setProviderName(name string) { a.providerName = name }
func (a *failStartAdapter) Report() executor.CapabilityReport {
	return executor.CapabilityReport{
		Harness: a.Name(),
		Caps:    []executor.CapabilityDecl{{Name: executor.CapExecution, Supported: true}},
	}
}

func (a *failStartAdapter) Start(context.Context, executor.StartReq) error {
	return context.DeadlineExceeded
}

func (a *failStartAdapter) Events(string) <-chan executor.AdapterEvent {
	ch := make(chan executor.AdapterEvent)
	close(ch)
	return ch
}

func (a *failStartAdapter) Send(context.Context, string, string) error { return nil }
func (a *failStartAdapter) RespondPermission(context.Context, string, string, string, string) error {
	return nil
}
func (a *failStartAdapter) Stop(string) error { return nil }

// newManagerForTest 经 ManagerFactory 组装真实编排实现；未接线即 Fatal。
func newManagerForTest(t *testing.T, d ManagerDeps) TestManager {
	t.Helper()
	if ManagerFactory == nil {
		t.Fatal("ManagerFactory 未接线（应由 package agentd_test 的 init 设置）")
	}
	return ManagerFactory(d)
}

// newTestManager 经 ManagerFactory 组装真实编排实现（真实 store + hub + 可控事件通道）。
func newTestManager(t *testing.T) (TestManager, *store.Store, *Hub, *chanAdapter) {
	t.Helper()
	ad := &chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}
	m, st, hub := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	return m, st, hub, ad
}

// newTestManagerWithAds 组装带 adapter 注册表的真实编排实现。
func newTestManagerWithAds(t *testing.T, ads map[string]executor.Adapter, defaultName string) (TestManager, *store.Store, *Hub) {
	t.Helper()
	return newTestManagerWithApprover(t, ads, defaultName, nil)
}

// newTestManagerWithApprover 组装真实编排实现；approver 参数保留兼容签名（本包不需
// 审批者，传 nil）。
func newTestManagerWithApprover(t *testing.T, ads map[string]executor.Adapter, defaultName string, _ any) (TestManager, *store.Store, *Hub) {
	t.Helper()
	for name, ad := range ads {
		if named, ok := ad.(interface{ setProviderName(string) }); ok {
			named.setProviderName(name)
		}
	}
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub()
	logger := discardLogger()
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: defaultName}}
	if ManagerFactory == nil {
		t.Fatal("ManagerFactory 未接线（应由 package agentd_test 的 init 设置）")
	}
	m := ManagerFactory(ManagerDeps{
		Store: st, Hub: hub, Ads: ads, Cfg: cfg,
		Gate: newTestGate(t), Log: logger,
		LiveConfig: func() *config.Config { return cfg },
	})
	return m, st, hub
}

// testManagerCfg 返回白盒 Manager 测试的缺省配置（Token/DataDir/缺省执行者）。
func testManagerCfg(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
}

// newManagerForServer 用测试 env 的 store/hub/活配置组装真实编排实现并挂到 srv。
func newManagerForServer(t *testing.T, srv *Server, ads map[string]executor.Adapter) TestManager {
	t.Helper()
	mgr := newManagerForTest(t, ManagerDeps{
		Store: srv.st, Hub: srv.Hub(), Ads: ads, Cfg: srv.conf(),
		EnvMapping: srv.EnvMapping, Gate: newTestGate(t), Log: discardLogger(),
		LiveConfig: srv.Conf(),
	})
	srv.SetManager(mgr)
	return mgr
}

// newTestManagerWithCfg 同 newTestManagerWithApprover，但用调用方给定的 cfg
// （活配置闭包返回同一指针，调用方可在构造后改字段，Manager 立即可见）。
func newTestManagerWithCfg(t *testing.T, ads map[string]executor.Adapter, cfg *config.Config) (TestManager, *store.Store, *Hub) {
	t.Helper()
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	hub := NewHub()
	m := newManagerForTest(t, ManagerDeps{
		Store: st, Hub: hub, Ads: ads, Cfg: cfg,
		Gate: newTestGate(t), Log: discardLogger(),
		LiveConfig: func() *config.Config { return cfg },
	})
	return m, st, hub
}

// mustCreateTask 直接落库一个任务。
func mustCreateTask(t *testing.T, st *store.Store, task *proto.Task) {
	t.Helper()
	if err := st.CreateTask(task); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

// registerTestProject 把 repo 登记成项目位置，返回 project_id。
func registerTestProject(t *testing.T, m TestManager, repo string) string {
	t.Helper()
	origin := "git@handoff.test:" + replaceSlashes(repo) + ".git"
	gitAt(t, repo, "remote", "add", "origin", origin)
	loc, err := m.RegisterProject(context.Background(), RegisterProjectReq{OriginURL: origin, Path: repo})
	if err != nil {
		t.Fatalf("registerTestProject(%s): %v", repo, err)
	}
	return loc.ProjectID
}

// mustDone 归档任务，失败即 Fatal。
func mustDone(t *testing.T, m TestManager, taskID, note string) {
	t.Helper()
	if _, err := m.Done(context.Background(), taskID, note); err != nil {
		t.Fatalf("Done: %v", err)
	}
}

// createRunningTask 创建任务并置 running。
func createRunningTask(t *testing.T, st *store.Store, id string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.CreateTask(&proto.Task{ID: id, Target: "local", State: proto.TaskStatePending, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	if err := st.UpdateTaskState(id, proto.TaskStateRunning); err != nil {
		t.Fatalf("置为 running: %v", err)
	}
}

// resultEvent 构造一个 OK 的 result 事件。
func resultEvent() executor.AdapterEvent {
	return executor.AdapterEvent{Type: "result", Result: &executor.Result{
		OK: true, Branch: "handoff/x", CommitHash: "deadbeef", Summary: "done",
	}}
}

// waitTaskState 等待任务到达目标状态。
func waitTaskState(t *testing.T, st *store.Store, taskID string, want proto.TaskState) {
	t.Helper()
	eventually(t, 2*time.Second, "任务到达状态 "+string(want), func() bool {
		task, err := st.GetTask(taskID)
		return err == nil && task.State == want
	})
}

// newTestServerWithManager 组装共享 store/hub 的白盒 server+真实编排实现。
func newTestServerWithManager(t *testing.T) (*Server, TestManager, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(looseTempDir(t), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := &config.Config{Token: "test", DataDir: t.TempDir(), Executor: config.ExecutorConfig{Default: "fake"}}
	logger := discardLogger()
	srv := NewServer(cfg, st, logger)
	if ManagerFactory == nil {
		t.Fatal("ManagerFactory 未接线（应由 package agentd_test 的 init 设置）")
	}
	mgr := ManagerFactory(ManagerDeps{
		Store: st, Hub: srv.Hub(), Ads: nil, Cfg: cfg,
		Gate: newTestGate(t), Log: logger,
		LiveConfig: srv.Conf(),
	})
	srv.SetManager(mgr)
	return srv, mgr, st
}

// mustEvents 返回任务全部事件（seq 升序）。
func mustEvents(t *testing.T, st *store.Store, taskID string) []proto.Event {
	t.Helper()
	evs, err := st.EventsFrom(taskID, 0, 1000)
	if err != nil {
		t.Fatalf("EventsFrom: %v", err)
	}
	return evs
}

// hasEvent 判断事件列表是否含指定类型。
func hasEvent(evs []proto.Event, typ proto.EventType) bool {
	for _, e := range evs {
		if e.Type == typ {
			return true
		}
	}
	return false
}

func replaceSlashes(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			out = append(out, '-')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}
