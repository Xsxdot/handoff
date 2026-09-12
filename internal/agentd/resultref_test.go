package agentd

import (
	"context"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestAssembleResultRefEmptyCommitAllowed(t *testing.T) {
	m, _, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fake.New(nil)}, "fake", nil)
	ref, err := m.AssembleResultRef(context.Background(), &proto.Task{Branch: "feat/x"}, "/no/such/repo", "HEAD")
	if err != nil {
		t.Fatalf("commit 未知应允许空：%v", err)
	}
	if ref.Commit != "" {
		t.Fatalf("不得编造 commit：%+v", ref)
	}
}

func TestBaseCommitDoesNotDriftWithDefaultBranch(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New(nil)
	m, st, _ := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	pid := registerTestProject(t, m, repo)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := task.BaseCommit
	gitT(t, repo, "commit", "--allow-empty", "-m", "after-dispatch")
	cur, err := st.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cur.BaseCommit != base {
		t.Fatalf("BaseCommit 不得随默认分支漂：%q → %q", base, cur.BaseCommit)
	}
}
