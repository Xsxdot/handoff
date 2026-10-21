// b453_proxy_env_test.go —— B453 段 3：账号代理随载体进任务环境（spec S4，plan Q6/V9）。
//
// 职责：派发请求带账号快照（名/kind/代理）→ 任务行三列落库（快照语义）→ 首回合、启动恢复、
// 续接冷恢复三处 env 组装都以账号代理覆盖六个代理键、NO_PROXY 缺失时补 localhost；未带账号
// 代理时 env 与改动前逐字节一致（回归锁）。
// 边界：覆盖规则本体由 proxycfg.WithProxyEnv 的单测锁；这里锁「三处都接上、从任务行取」。
package orchestration

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/proto"
)

const b453Proxy = "socks5://u:p@127.0.0.1:7897"

func clearProcessNoProxy(t *testing.T) {
	t.Helper()
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
}

func assertAccountProxyEnv(t *testing.T, where string, env []string) {
	t.Helper()
	for _, k := range []string{"HTTPS_PROXY", "https_proxy", "HTTP_PROXY", "http_proxy", "ALL_PROXY", "all_proxy"} {
		if !slices.Contains(env, k+"="+b453Proxy) {
			t.Fatalf("%s：env 缺 %s=账号代理：%v", where, k, env)
		}
	}
	if !slices.Contains(env, "NO_PROXY=localhost,127.0.0.1,::1") {
		t.Fatalf("%s：NO_PROXY 缺失时应补 localhost：%v", where, env)
	}
}

func TestDispatchAccountProxyEnvOverridesAndPersistsSnapshot(t *testing.T) {
	clearProcessNoProxy(t)
	ad := &envRecordingAdapter{Adapter: fake.New(nil)}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)
	home := "/carrier/home"
	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "账号代理", HomeDir: &home,
		Account: "cx", AccountKind: "codex", AccountProxy: b453Proxy,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	assertAccountProxyEnv(t, "首回合", ad.gotEnv)
	if !slices.Contains(ad.gotEnv, "HOME="+home) {
		t.Fatalf("账号代理不得挤掉载体 HOME：%v", ad.gotEnv)
	}
	stored, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Account != "cx" || stored.AccountKind != "codex" || stored.AccountProxy != b453Proxy {
		t.Fatalf("任务行账号快照 = %q/%q/%q，want cx/codex/代理", stored.Account, stored.AccountKind, stored.AccountProxy)
	}
}

func TestDispatchWithoutAccountProxyEnvByteIdentical(t *testing.T) {
	ad := &envRecordingAdapter{Adapter: fake.New(nil)}
	m, _, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	repo := initTestRepo(t)
	pid := registerTestProject(t, m, repo)
	home := "/carrier/home"
	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "账号无代理", HomeDir: &home, Account: "cx", AccountKind: "codex",
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	want := []string{"HOME=" + home, proto.ArtifactsEnvVar + "=" + filepath.Join(m.cfg.DataDir, "tasks", task.ID, "artifacts")}
	if !slices.Equal(ad.gotEnv, want) {
		t.Fatalf("账号未填代理时 env 必须与改动前一致：%v，want %v", ad.gotEnv, want)
	}
}

func TestResumeTaskProxyEnvFromTaskRow(t *testing.T) {
	clearProcessNoProxy(t)
	ad := &recordingRestorer{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "acct-resume", RepoPath: "/r", Executor: "fake",
		HomeDir: "/carrier/home", State: proto.TaskStateRunning, ExecutorSession: "sess-1",
		Account: "cx", AccountKind: "codex", AccountProxy: b453Proxy})
	m.ResumeTask("acct-resume")
	ad.mu.Lock()
	got := ad.got
	ad.mu.Unlock()
	assertAccountProxyEnv(t, "启动恢复", got.Env)
}

func TestContinueColdResumeProxyEnvFromTaskRow(t *testing.T) {
	clearProcessNoProxy(t)
	ad := &ladderAdapter{chanAdapter: chanAdapter{evCh: make(chan executor.AdapterEvent, 1)},
		outcome: executor.ResumeOutcome{Alive: true, Mode: executor.ResumeModeCold, SessionID: "sess-1"}}
	m, st, _ := newTestManagerWithAds(t, map[string]executor.Adapter{"fake": ad}, "fake")
	mustCreateTask(t, st, &proto.Task{ID: "acct-continue", RepoPath: "/r", Executor: "fake",
		HomeDir: "/carrier/home", State: proto.TaskStateWaitingReview, ExecutorSession: "sess-1",
		Account: "cx", AccountKind: "codex", AccountProxy: b453Proxy})
	if err := m.Continue(context.Background(), "acct-continue", "继续"); err != nil {
		t.Fatalf("Continue: %v", err)
	}
	ad.mu.Lock()
	got := ad.gotReq
	ad.mu.Unlock()
	assertAccountProxyEnv(t, "续接冷恢复", got.Env)
}
