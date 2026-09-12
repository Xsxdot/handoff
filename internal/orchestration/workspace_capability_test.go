package orchestration

import (
	"context"
	"errors"
	agentd "github.com/Xsxdot/handoff/internal/agentd"
	"sync"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/workspace"
)

type countingCap struct {
	inner   workspace.Capability
	mu      sync.Mutex
	ensure  int
	resolve int
	prepare int
	recycle int
	create  int
	diff    int
	read    int
}

func (c *countingCap) inc(p *int) {
	c.mu.Lock()
	*p++
	c.mu.Unlock()
}

func (c *countingCap) EnsureRepoUsable(ctx context.Context, repo string) error {
	c.inc(&c.ensure)
	return c.inner.EnsureRepoUsable(ctx, repo)
}
func (c *countingCap) ResolveBaseline(ctx context.Context, repo, sha string) (workspace.Baseline, error) {
	c.inc(&c.resolve)
	return c.inner.ResolveBaseline(ctx, repo, sha)
}
func (c *countingCap) Prepare(ctx context.Context, req workspace.PrepareReq) (workspace.Prepared, error) {
	c.inc(&c.prepare)
	return c.inner.Prepare(ctx, req)
}
func (c *countingCap) CreateManual(ctx context.Context, repo, dir string, req workspace.ManualReq) (workspace.ManualTree, error) {
	c.inc(&c.create)
	return c.inner.CreateManual(ctx, repo, dir, req)
}
func (c *countingCap) DiffRange(ctx context.Context, repo, base, head string) (string, error) {
	c.inc(&c.diff)
	return c.inner.DiffRange(ctx, repo, base, head)
}
func (c *countingCap) ReadFile(ctx context.Context, repo, rel string) (workspace.FileContent, error) {
	c.inc(&c.read)
	return c.inner.ReadFile(ctx, repo, rel)
}
func (c *countingCap) RecycleManaged(ctx context.Context, repo, workdir string) error {
	c.inc(&c.recycle)
	return c.inner.RecycleManaged(ctx, repo, workdir)
}

func TestDispatchCallsCapability(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New(nil)
	m, _, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	spy := &countingCap{inner: workspace.NewCapability()}
	m.SetWorkspace(spy)
	pid := registerTestProject(t, m, repo)
	task, err := m.Dispatch(context.Background(), agentd.DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !task.WorktreeManaged || task.WorkDir == "" {
		t.Fatalf("new-worktree 元数据缺失: %+v", task)
	}
	if task.BaseCommit != "" && !workspace.IsCommitSHA(task.BaseCommit) {
		t.Fatalf("BaseCommit 必须是 40 位小写 hex 或空仓空串，实得 %q", task.BaseCommit)
	}
	if spy.prepare < 1 || spy.resolve < 1 || spy.ensure < 1 {
		t.Fatalf("Dispatch 必须经 Capability：ensure=%d resolve=%d prepare=%d", spy.ensure, spy.resolve, spy.prepare)
	}
}

func TestDispatchNilCapabilityFailsClosed(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New(nil)
	m, _, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	m.SetWorkspace(nil)
	pid := registerTestProject(t, m, repo)
	_, err := m.Dispatch(context.Background(), agentd.DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "fake", NewWorktree: true,
	})
	if !errors.Is(err, agentd.ErrWorkspaceUnavailable) {
		t.Fatalf("nil Capability 必须失败且错误链含 agentd.ErrWorkspaceUnavailable，实得 %v", err)
	}
}
