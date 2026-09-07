// workspace_capability.go —— 工作区 Capability 的 agentd 适配器（B233.4）。
//
// 职责：把既有包级 git 函数暴露为 workspace.Capability；供 Manager 持有。
// 边界：不搬 workspace.go；不写账本；不授权 push；具体类型只在组装点构造。
package agentd

import (
	"context"
	"errors"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// ErrWorkspaceUnavailable 表示 Manager 未注入工作区能力。
// nil 不得解释成跳过 EnsureRepoUsable 或脏检查——调用方必须失败。
var ErrWorkspaceUnavailable = errors.New("工作区能力未注入")

// gitCapability 把包级 git 函数适配成 Capability。P1(a)：实现仍留在本包。
type gitCapability struct{}

// NewGitCapability 返回生产用工作区能力。只在组装点调用。
func NewGitCapability() workspace.Capability { return gitCapability{} }

var _ workspace.Capability = gitCapability{}

// SetWorkspace 注入工作区能力。测试可换 fake；生产由组装点绑定。
func (m *Manager) SetWorkspace(c workspace.Capability) { m.ws = c }

// Workspace 返回已注入的能力，可能为 nil。
func (m *Manager) Workspace() workspace.Capability { return m.ws }

func (m *Manager) requireWorkspace() error {
	if m == nil || m.ws == nil {
		return ErrWorkspaceUnavailable
	}
	return nil
}

func (gitCapability) EnsureRepoUsable(ctx context.Context, repo string) error {
	return EnsureRepoUsable(ctx, repo)
}

func (gitCapability) ResolveBaseline(ctx context.Context, repo, sha string) (workspace.Baseline, error) {
	b, err := ResolveBaseline(ctx, repo, sha)
	if err != nil {
		return workspace.Baseline{}, err
	}
	return workspace.Baseline{Start: b.Start, Ahead: b.Ahead, Fetched: b.Fetched}, nil
}

func (gitCapability) Prepare(ctx context.Context, req workspace.PrepareReq) (workspace.Prepared, error) {
	ws, err := PrepareWorkspace(ctx, WorkspaceReq{
		Repo: req.Repo, TaskID: req.TaskID, Branch: req.Branch, NewBranch: req.NewBranch,
		Base: req.Base, Worktree: req.Worktree, NewWorktree: req.NewWorktree, WorktreesDir: req.WorktreesDir,
	})
	if err != nil {
		return workspace.Prepared{}, err
	}
	return workspace.Prepared{
		Branch: ws.Branch, WorkDir: ws.WorkDir, Managed: ws.Managed,
		NewBranchTip: ws.NewBranchTip, PrevRef: ws.PrevRef,
		RepoDirtyCount: ws.RepoDirtyCount, RepoDirtyFiles: ws.RepoDirtyFiles,
	}, nil
}

func (gitCapability) CreateManual(ctx context.Context, repo, worktreesDir string, req workspace.ManualReq) (workspace.ManualTree, error) {
	// 刻意不把 CardIDs 传入包级函数：挂卡是应用的事。
	ws, err := CreateManualWorktree(ctx, repo, worktreesDir, proto.CreateWorktreeReq{
		Mode: req.Mode, Branch: req.Branch, Base: req.Base,
	})
	if err != nil {
		return workspace.ManualTree{}, err
	}
	return workspace.ManualTree{Path: ws.Path, Branch: ws.Branch, Head: ws.Head, Managed: ws.Managed}, nil
}

func (gitCapability) DiffRange(ctx context.Context, repo, base, head string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return DiffRange(repo, base, head)
}

func (gitCapability) ReadFile(ctx context.Context, repo, rel string) (workspace.FileContent, error) {
	if err := ctx.Err(); err != nil {
		return workspace.FileContent{}, err
	}
	f, err := ReadFile(repo, rel)
	if err != nil {
		return workspace.FileContent{}, err
	}
	return workspace.FileContent{Content: f.Content, Size: f.Size, Truncated: f.Truncated, Binary: f.Binary, SHA256: f.SHA256}, nil
}

func (gitCapability) RecycleManaged(ctx context.Context, repo, workdir string) error {
	return RemoveManagedWorktree(ctx, repo, workdir)
}
