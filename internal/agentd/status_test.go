// GET /api/status 的服务端聚合测试：字段齐全性、探活三态、总时限。
package agentd_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
)

// probeStub 是一个可控探活结论的假 adapter，用来在服务端测试里制造三态现场。
// 它实现 executor.Adapter 的五动作 + Probe + ProcHandle（footprinter）；五动作
// 全部返回零值即可，status 路径不会调到它们。
type probeStub struct {
	alive bool
	note  string
	err   error
	delay time.Duration
}

func (p *probeStub) Start(ctx context.Context, req executor.StartReq) error { return nil }
func (p *probeStub) Events(taskID string) <-chan executor.AdapterEvent      { return nil }
func (p *probeStub) Send(ctx context.Context, taskID, text string) error    { return nil }
func (p *probeStub) RespondPermission(ctx context.Context, taskID, permID, decision, reason string) error {
	return nil
}
func (p *probeStub) Stop(taskID string) error { return nil }

// ProcHandle 实现 footprinter：交出一个指向「不可能存在」的假 pid 的句柄。
//
// 2147480000 接近 int32 上限，macOS/linux 内核永远分配不到这么高的 pid——
// classify 的 leader_reuse 检查与组成员收集都必然落空，Footprint 稳定返回
// VerdictOK + 0 成员，不依赖本机真实进程现场。
func (p *probeStub) ProcHandle(taskID, taskDir string) (prochost.Handle, error) {
	return prochost.Handle{
		PID:       2147480000,
		LockPath:  filepath.Join(taskDir, "shim.lock"),
		StartedAt: 1,
	}, nil
}

func (p *probeStub) Probe(executor.ProbeReq) (executor.ProbeOutcome, error) {
	if p.delay > 0 {
		time.Sleep(p.delay)
	}
	if p.err != nil {
		return executor.ProbeOutcome{}, p.err
	}
	return executor.ProbeOutcome{Alive: p.alive, Note: p.note}, nil
}

// statusEnv 聚合 status 测试环境：真实 store + httptest server + manager(probeStub)。
type statusEnv struct {
	ts *httptest.Server
	st interface {
		CreateTask(t *proto.Task) error
	}
	// mgr 是同一个 manager 的引用：FootprintAll 等直接调方法的测试不走 HTTP。
	mgr *agentd.Manager
}

// newStatusEnv 构造 status 测试环境：manager 以单只 probeStub 注册为 "stub"，
// 缺省执行者也指到它，保证种子任务的探活都能走到 stub 上。
func newStatusEnv(t *testing.T, ad executor.Adapter) *statusEnv {
	t.Helper()
	cfg := &config.Config{
		Token:    testToken,
		DataDir:  t.TempDir(),
		Listen:   "127.0.0.1:7777",
		Executor: config.ExecutorConfig{Default: "stub"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := newTestEnvWithCfg(t, cfg, logger)
	mgr := agentd.NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"stub": ad},
		cfg, nil, nil, nil, logger)
	env.srv.SetManager(mgr)
	return &statusEnv{ts: env.ts, st: env.st, mgr: mgr}
}

// newTestManager 构造一个能直接调 Manager.Status() 的 manager。
//
// 与 newStatusEnv 的差异：只构造 Manager，不挂 HTTP server——本测试直接调
// Status() 方法本身，不需要经过 wire 层。
func newTestManager(t *testing.T) *agentd.Manager {
	t.Helper()
	cfg := &config.Config{
		Token:    testToken,
		DataDir:  t.TempDir(),
		Listen:   "127.0.0.1:7777",
		Executor: config.ExecutorConfig{Default: "stub"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := newTestEnvWithCfg(t, cfg, logger)
	return agentd.NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"stub": &probeStub{alive: true}},
		cfg, nil, nil, nil, logger)
}

// seedTask 落一条指定状态的任务（executor 一律 "stub"，保证探活路由到 stub）。
func (e *statusEnv) seedTask(t *testing.T, id string, state proto.TaskState) {
	t.Helper()
	now := time.Now().UTC()
	if err := e.st.CreateTask(&proto.Task{ID: id, State: state, Executor: "stub",
		Name: id, RepoPath: "/repo", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
}

// seedRunningTask 落一条 running 任务。
func (e *statusEnv) seedRunningTask(t *testing.T, id string) {
	t.Helper()
	e.seedTask(t, id, proto.TaskStateRunning)
}

// getStatus 带 Bearer 请求 /api/status 并解出响应。
func (e *statusEnv) getStatus(t *testing.T) *proto.StatusResp {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, e.ts.URL+"/api/status", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码=%d, want 200", resp.StatusCode)
	}
	var out proto.StatusResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解码 status 响应: %v", err)
	}
	return &out
}

// 六个状态的计数键必须恒存在，哪怕计数为零——缺键与零值对消费方是两回事。
func TestStatusTaskCountsAlwaysHaveSixKeys(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	got := env.getStatus(t)
	for _, s := range []string{"pending", "running", "waiting_answer",
		"waiting_review", "completed", "failed"} {
		if _, ok := got.TaskCounts[s]; !ok {
			t.Fatalf("task_counts 缺键 %q——缺键与零值对消费方是两回事", s)
		}
	}
}

func TestStatusReportsWebEmbeddedStubOverHTTP(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	got := env.getStatus(t)
	if got.WebEmbedded == nil {
		t.Fatal("默认构建的真实 /api/status 必须带 web_embedded=false")
	}
	if *got.WebEmbedded {
		t.Fatal("默认构建不应报告已嵌入 Web 控制台")
	}
}

// 探活为 alive 时 Live=alive、Note 为空。
func TestStatusProbeAlive(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	env.seedRunningTask(t, "T-alive")
	got := env.getStatus(t)
	if len(got.Active) != 1 {
		t.Fatalf("活跃任务数=%d，want 1", len(got.Active))
	}
	if got.Active[0].Live != proto.LiveAlive {
		t.Fatalf("Live=%q, want %q", got.Active[0].Live, proto.LiveAlive)
	}
}

// 探活为 dead 时 Live=dead 且 Note 原样带出（协调者靠它判断怎么处置）。
func TestStatusProbeDead(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: false, note: "tmux 会话 handoff-abcdef01 不存在"})
	env.seedRunningTask(t, "T-dead")
	got := env.getStatus(t)
	if got.Active[0].Live != proto.LiveDead {
		t.Fatalf("Live=%q, want %q", got.Active[0].Live, proto.LiveDead)
	}
	if got.Active[0].Note != "tmux 会话 handoff-abcdef01 不存在" {
		t.Fatalf("Note=%q，判死理由必须原样带出", got.Active[0].Note)
	}
}

// 探针自身失败（err != nil）→ unknown，**绝不能是 dead**。
func TestStatusProbeErrorIsUnknownNotDead(t *testing.T) {
	env := newStatusEnv(t, &probeStub{err: errors.New("凭据损坏")})
	env.seedRunningTask(t, "T-unknown")
	got := env.getStatus(t)
	if got.Active[0].Live != proto.LiveUnknown {
		t.Fatalf("Live=%q, want %q——探不出结论时猜 dead 就是制造假阳性",
			got.Active[0].Live, proto.LiveUnknown)
	}
}

// 探活超时 → unknown，同样不是 dead。
func TestStatusProbeTimeoutIsUnknown(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true, delay: 3 * time.Second})
	env.seedRunningTask(t, "T-slow")
	start := time.Now()
	got := env.getStatus(t)
	if got.Active[0].Live != proto.LiveUnknown {
		t.Fatalf("Live=%q, want %q（超时不判死）", got.Active[0].Live, proto.LiveUnknown)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("单次探活超时应在 2s 左右收敛，实际耗时 %v", elapsed)
	}
}

// 终结态任务不出现在 Active 里。
func TestStatusExcludesTerminalTasks(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	env.seedTask(t, "T-done", proto.TaskStateCompleted)
	got := env.getStatus(t)
	if len(got.Active) != 0 {
		t.Fatalf("completed 是终结态，不应出现在 active 里，得到 %d 条", len(got.Active))
	}
	if got.TaskCounts["completed"] != 1 {
		t.Fatalf("completed 计数=%d, want 1", got.TaskCounts["completed"])
	}
}

// 无 token → 401（走既有鉴权中间件，回归性断言）。
func TestStatusRequiresAuth(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	resp, err := http.Get(env.ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码=%d, want 401——status 不开匿名口", resp.StatusCode)
	}
}

// TestStatusAlwaysReportsUpdate 锁住「Update 恒返回」。
//
// why：改之前 Update 只在 pending.json 存在时才填，而现在闸二与巡检
// 每台机器都要读 Managed——只在特殊情况下才给的字段，消费方拿到 nil
// 只能猜，而猜出来的诊断会说谎。
func TestStatusAlwaysReportsUpdate(t *testing.T) {
	m := newTestManager(t)
	resp, err := m.Status()
	if err != nil {
		t.Fatal(err)
	}
	if resp.Update == nil {
		t.Fatal("Update 必须恒返回，nil 的语义是「对端没给」")
	}
	if resp.Version.Platform == "" {
		t.Fatal("Platform 必须上报，空串的语义是「对端过旧」")
	}
}

// plainStub 是只实现五动作 + Probe 的最小 adapter。
//
// probeStub 加了 ProcHandle 后已经是 footprinter 了，验证「不支持足迹」的路径
// 需要另一个不带该方法的独立类型。
type plainStub struct{}

func (p *plainStub) Start(ctx context.Context, req executor.StartReq) error { return nil }
func (p *plainStub) Events(taskID string) <-chan executor.AdapterEvent      { return nil }
func (p *plainStub) Send(ctx context.Context, taskID, text string) error    { return nil }
func (p *plainStub) RespondPermission(ctx context.Context, taskID, permID, decision, reason string) error {
	return nil
}
func (p *plainStub) Stop(taskID string) error { return nil }

func (p *plainStub) Probe(executor.ProbeReq) (executor.ProbeOutcome, error) {
	return executor.ProbeOutcome{Alive: true}, nil
}

// TestStatusFillsProcsForActiveTasks 验证活跃任务带上进程数。
//
// 假 pid 用 2147480000：接近 int32 上限，本机内核永远分配不到，足迹判定必然
// 稳定在 VerdictOK + 0 成员，不会被真实进程干扰。
func TestStatusFillsProcsForActiveTasks(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	env.seedRunningTask(t, "T-procs")
	got := env.getStatus(t)
	for _, a := range got.Active {
		if a.ID != "T-procs" {
			continue
		}
		if a.Procs == nil {
			t.Fatal("活跃任务应带 Procs（取不到时也该留 nil，见下）")
		}
		return
	}
	t.Fatalf("响应里没有任务 T-procs")
}

// TestStatusProcsNilWhenUnsupported adapter 不支持时 Procs 必须是 nil，不能填 0。
//
// nil 表示「没这个信息」，0 表示「确实没有进程」。填 0 就是制造假阳性——
// 与 Watchers / Live 三态是同一条纪律。
func TestStatusProcsNilWhenUnsupported(t *testing.T) {
	env := newStatusEnv(t, &plainStub{})
	env.seedRunningTask(t, "T-plain")
	got := env.getStatus(t)
	for _, a := range got.Active {
		if a.Procs != nil {
			t.Fatalf("不支持的 adapter 应留 nil，got %d", *a.Procs)
		}
	}
}

// TestFootprintAllCoversArchivedTasks 验证体检覆盖已归档任务。
//
// 这是这条命令存在的理由：Done 只删 worktree、不删任务目录，历史任务的
// proc.json 都还在。若只扫活跃任务，它与 status 就没有区别了。
func TestFootprintAllCoversArchivedTasks(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	archived := "T-archived"
	env.seedTask(t, archived, proto.TaskStateCompleted)

	resp, err := env.mgr.FootprintAll()
	if err != nil {
		t.Fatalf("FootprintAll 失败: %v", err)
	}
	for _, r := range resp.Rows {
		if r.TaskID == archived {
			return
		}
	}
	t.Fatalf("体检结果里没有已归档任务 %s（共 %d 行）", archived, len(resp.Rows))
}

// TestFootprintAllReportsVerdict 验证判定结论如实带出，不被抹成 0。
func TestFootprintAllReportsVerdict(t *testing.T) {
	env := newStatusEnv(t, &probeStub{alive: true})
	env.seedTask(t, "T-verdict", proto.TaskStateCompleted)

	resp, err := env.mgr.FootprintAll()
	if err != nil {
		t.Fatalf("FootprintAll 失败: %v", err)
	}
	if len(resp.Rows) == 0 {
		t.Fatal("应至少有一行")
	}
	if resp.Rows[0].Verdict == "" {
		t.Fatal("Verdict 不得为空——判不出结论也要如实说，不能只给一个 0")
	}
}

// Listen 为单网卡 IP 时 ListenAux 必须给出 loopback 变体；Listen 保持
// cfg.Listen 不变（身份/配对语义，消费方不该看到列表）。loopback 配置恒为空。
func TestStatusListenAux(t *testing.T) {
	cfg := &config.Config{
		Token:    testToken,
		DataDir:  t.TempDir(),
		Listen:   "100.64.0.5:7777",
		Executor: config.ExecutorConfig{Default: "stub"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	env := newTestEnvWithCfg(t, cfg, logger)
	mgr := agentd.NewManager(env.st, env.srv.Hub(),
		map[string]executor.Adapter{"stub": &probeStub{alive: true}}, cfg, nil, nil, nil, logger)

	st, err := mgr.Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Listen != "100.64.0.5:7777" {
		t.Fatalf("Listen=%q, 应保持 cfg.Listen 原值", st.Listen)
	}
	if st.ListenAux != "127.0.0.1:7777" {
		t.Fatalf("ListenAux=%q, want 127.0.0.1:7777", st.ListenAux)
	}

	loop, err := newTestManager(t).Status()
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if loop.ListenAux != "" {
		t.Fatalf("loopback 配置 ListenAux=%q, 应为空", loop.ListenAux)
	}
}
