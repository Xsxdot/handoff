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
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/agentd"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
)

// probeStub 是一个可控探活结论的假 adapter，用来在服务端测试里制造三态现场。
// 它只需实现 executor.Adapter 的五动作 + Probe；五动作全部返回零值即可，
// status 路径不会调到它们。
type probeStub struct {
	alive bool
	note  string
	err   error
	delay time.Duration
}

func (p *probeStub) Start(ctx context.Context, req executor.StartReq) error { return nil }
func (p *probeStub) Events(taskID string) <-chan executor.AdapterEvent      { return nil }
func (p *probeStub) Send(ctx context.Context, taskID, text string) error    { return nil }
func (p *probeStub) RespondPermission(ctx context.Context, taskID, permID, decision string) error {
	return nil
}
func (p *probeStub) Stop(taskID string) error { return nil }

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
		cfg, nil, nil, logger)
	env.srv.SetManager(mgr)
	return &statusEnv{ts: env.ts, st: env.st}
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

// 探活为 dead 时 Live=dead 且 Note 原样带出（审核者靠它判断怎么处置）。
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
