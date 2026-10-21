package agentd

// b452_pair_test.go —— B452 双 agentd 竖切夹具（plan §4 段 0 第 1 条承重假设）。
//
// 职责：一个测试进程里起两台真实 agentd（受理机 A = t452-mac、执行机 B = t452-linux），共用同一个
// *ledger.Store（共享编制登记与名额），各自独立 store / DataDir / HTTP 监听；A 的 targets 把
// t452-linux 指向 B 的真实监听地址。假 Runner（fake adapter）只装在 B 上——A 上若有任务被本地
// 拉起，fake 不在 A 的 adapter 表里会直接失败，因而「只有 B 开工」由装配本身保证。
// 边界：只供 B452 测试；不 mock 对端，不改生产装配。
import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	handoffclient "github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

const (
	b452MachineA = "t452-mac"
	b452MachineB = "t452-linux"
	b452CarrierB = "t452-oc"
	b452CarrierA = "t452-local"
)

// b452Host 是一台测试 agentd：真实 Server + 真实 HTTP 监听 + 真实编制域 + 真实编排。
type b452Host struct {
	*ledgerEnv
	name  string
	mgr   *orchestration.Manager
	fake  *fake.Fake // nil = 本机不装执行器
	sched *scheduling.Service
	logs  *b448LogCapture
}

// newB452Host 在共享账本 st 上起一台 agentd。steps 非 nil 时装一个 fake adapter（名 "fake"）。
func newB452Host(t *testing.T, st *ledger.Store, ledgerPath, name string, steps []fake.Step) *b452Host {
	t.Helper()
	backend, err := store.Open(filepath.Join(looseTempDir(t), "handoff.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Close() })
	cfg := &config.Config{Token: testToken, DataDir: t.TempDir(), Name: name,
		Executor: config.ExecutorConfig{Default: "fake"}}
	logs := &b448LogCapture{}
	srv := NewServer(cfg, backend, slog.New(logs.handler()))
	srv.SetLedger(st)
	sched := SetupAutomationForTest(t, srv, st)
	var fk *fake.Fake
	ads := map[string]executor.Adapter{}
	if steps != nil {
		fk = fake.New(steps)
		ads["fake"] = fk
	}
	mgr := newManagerForServer(t, srv, ads)
	srv.SetConfigPath(filepath.Join(t.TempDir(), "config.yaml"))
	ts := testhttp.NewServer(t, srv.Handler())
	cfg.Listen = strings.TrimPrefix(ts.URL, "http://")
	env := &ledgerEnv{testAgentdEnv: &testAgentdEnv{srv: srv, ts: ts, st: backend, token: testToken},
		ledger: st, ledgerPath: ledgerPath}
	return &b452Host{ledgerEnv: env, name: name, mgr: mgr, fake: fk, sched: sched, logs: logs}
}

func (h *b452Host) addr() string { return strings.TrimPrefix(h.ts.URL, "http://") }

// setTargets 整体替换活配置 targets（name → addr）。
func (h *b452Host) setTargets(t *testing.T, targets map[string]string) {
	t.Helper()
	if err := h.srv.swapConf(func(c *config.Config) error {
		c.Targets = map[string]config.Target{}
		for name, addr := range targets {
			c.Targets[name] = config.Target{Addr: addr, Token: testToken}
		}
		return nil
	}); err != nil {
		t.Fatalf("登记 targets: %v", err)
	}
}

// taskCount 读本机任务表行数。
func (h *b452Host) taskCount(t *testing.T) int {
	t.Helper()
	tasks, err := h.st.ListTasks()
	if err != nil {
		t.Fatalf("ListTasks(%s): %v", h.name, err)
	}
	return len(tasks)
}

// b452Pair 是共享账本的一对 agentd：a 受理机（无执行器），b 执行机（fake 执行器 + 已登记项目）。
type b452Pair struct {
	a, b      *b452Host
	repo      string // B 上登记的项目仓库
	projectID string
}

// newB452Pair 起一对 agentd，登记载体 t452-oc（machine=t452-linux，CLI fake）并在 B 上登记项目。
func newB452Pair(t *testing.T, steps []fake.Step) *b452Pair {
	t.Helper()
	ledgerPath := filepath.Join(t.TempDir(), "ledger.db")
	st, err := ledger.Open(ledgerPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	seedAgentdLedger(t, st)
	if steps == nil {
		steps = []fake.Step{}
	}
	b := newB452Host(t, st, ledgerPath, b452MachineB, steps)
	a := newB452Host(t, st, ledgerPath, b452MachineA, nil)
	a.setTargets(t, map[string]string{b452MachineB: b.addr()})
	b.setTargets(t, map[string]string{b452MachineA: a.addr()})
	putOnlineCarrier(t, a.sched, scheduling.Carrier{
		Name: b452CarrierB, Machine: b452MachineB, CLI: "fake",
		HomeDir: "", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 1, Status: scheduling.StatusOnline,
	})
	repo := initTestRepo(t)
	projectID := registerTestProject(t, b.mgr, repo)
	return &b452Pair{a: a, b: b, repo: repo, projectID: projectID}
}

// counts 读载体两级名额（共享账本）。
func (p *b452Pair) carrierCount(t *testing.T, carrier string) int {
	t.Helper()
	return runningCountIn(t, p.a.srv.autoLedger, "carrier/"+carrier)
}

// deadPeerAddr 返回一个确定「连接被拒」的地址：起一个本地 httptest 服务后立即关闭。
// 不用 TEST-NET 之类的真实地址——没有代理时那种拨号会挂到拨号超时，测试变慢变抖。
func deadPeerAddr(t *testing.T) string {
	t.Helper()
	ts := testhttp.NewServer(t, http.NotFoundHandler())
	addr := strings.TrimPrefix(ts.URL, "http://")
	ts.Close()
	return addr
}

// TestB452PairFrozenDispatchOverRealHTTP 是段 0 第 1 条承重假设：A 经 clientForTarget(t452-linux)
// 发出带冻结身份的派发，B 用既有冻结分支建任务；名额只在 B 的 AdmitFrozen 里占（共享账本 +1）。
// 不改产品代码，只验证夹具与既有 wire 能承载 S3 的转发。
func TestB452PairFrozenDispatchOverRealHTTP(t *testing.T) {
	p := newB452Pair(t, nil)
	cl, err := p.a.srv.clientForTarget(b452MachineB)
	if err != nil {
		t.Fatalf("A 取 t452-linux 客户端: %v", err)
	}
	if cl.BaseURL() != p.b.ts.URL {
		t.Fatalf("A 的 t452-linux 客户端应指向 B（%s），得 %s", p.b.ts.URL, cl.BaseURL())
	}
	home := ""
	task, err := cl.Dispatch(context.Background(), handoffclient.DispatchOpts{
		ProjectID: p.projectID, Prompt: "段 0 探针", Target: b452MachineB,
		Carrier: b452CarrierB, HomeDir: &home, FrozenTarget: b452MachineB, Executor: "fake",
		NewWorktree: true,
	})
	if err != nil {
		t.Fatalf("A 经 clientForTarget 冻结派发到 B 失败: %v", err)
	}
	if task.Target != b452MachineB || task.Carrier != b452CarrierB {
		t.Fatalf("B 建的任务身份 = target:%q carrier:%q", task.Target, task.Carrier)
	}
	if n := p.b.taskCount(t); n != 1 {
		t.Fatalf("B 任务表应 1 行，得 %d", n)
	}
	if n := p.a.taskCount(t); n != 0 {
		t.Fatalf("A 任务表应 0 行，得 %d", n)
	}
	if got := p.carrierCount(t, b452CarrierB); got != 1 {
		t.Fatalf("共享账本载体名额应 1，得 %d", got)
	}
	// 清理：经 B 的 HTTP stop 归还名额
	req, _ := http.NewRequest(http.MethodPost, p.b.ts.URL+"/api/tasks/"+task.ID+"/stop", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+testToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.b.ts.Client().Do(req)
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	raw, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("stop 应 200，得 %d %s", resp.StatusCode, raw)
	}
	if got := p.carrierCount(t, b452CarrierB); got != 0 {
		t.Fatalf("stop 后名额应归零，得 %d", got)
	}
}
