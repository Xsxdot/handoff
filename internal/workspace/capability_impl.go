// capability_impl.go —— 工作区 Capability 的生产实现（B233.15 从 agentd 迁入）。
//
// 职责：把本包的 git/工作区函数适配成 Capability；供 Manager 持有。
// 边界：不写账本、不授权 push；具体类型只在组装点经 NewCapability 构造。
package workspace

import (
	"context"

	"github.com/Xsxdot/handoff/internal/proto"
)

// gitCapability 把本包 git/工作区函数适配成 Capability。
type gitCapability struct{}

// NewCapability 返回生产用工作区能力。只在组装点调用。
func NewCapability() Capability { return gitCapability{} }

var _ Capability = gitCapability{}

func (gitCapability) EnsureRepoUsable(ctx context.Context, repo string) error {
	return EnsureRepoUsable(ctx, repo)
}

func (gitCapability) ResolveBaseline(ctx context.Context, repo, sha string) (Baseline, error) {
	return ResolveBaseline(ctx, repo, sha)
}

func (gitCapability) Prepare(ctx context.Context, req PrepareReq) (Prepared, error) {
	ws, err := PrepareWorkspace(ctx, WorkspaceReq{
		Repo: req.Repo, TaskID: req.TaskID, Branch: req.Branch, NewBranch: req.NewBranch,
		Base: req.Base, Worktree: req.Worktree, NewWorktree: req.NewWorktree, WorktreesDir: req.WorktreesDir,
	})
	if err != nil {
		return Prepared{}, err
	}
	return Prepared{
		Branch: ws.Branch, WorkDir: ws.WorkDir, Managed: ws.Managed,
		NewBranchTip: ws.NewBranchTip, PrevRef: ws.PrevRef,
		RepoDirtyCount: ws.RepoDirtyCount, RepoDirtyFiles: ws.RepoDirtyFiles,
	}, nil
}

func (gitCapability) CreateManual(ctx context.Context, repo, worktreesDir string, req ManualReq) (ManualTree, error) {
	// 刻意不把 CardIDs 传入包级函数：挂卡是应用的事。
	ws, err := CreateManualWorktree(ctx, repo, worktreesDir, proto.CreateWorktreeReq{
		Mode: req.Mode, Branch: req.Branch, Base: req.Base,
	})
	if err != nil {
		return ManualTree{}, err
	}
	return ManualTree{Path: ws.Path, Branch: ws.Branch, Head: ws.Head, Managed: ws.Managed}, nil
}

func (gitCapability) DiffRange(ctx context.Context, repo, base, head string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return DiffRange(repo, base, head)
}

func (gitCapability) ReadFile(ctx context.Context, repo, rel string) (FileContent, error) {
	if err := ctx.Err(); err != nil {
		return FileContent{}, err
	}
	f, err := ReadFile(repo, rel)
	if err != nil {
		return FileContent{}, err
	}
	return FileContent{Content: f.Content, Size: f.Size, Truncated: f.Truncated, Binary: f.Binary, SHA256: f.SHA256}, nil
}

func (gitCapability) RecycleManaged(ctx context.Context, repo, workdir string) error {
	return RemoveManagedWorktree(ctx, repo, workdir)
}
