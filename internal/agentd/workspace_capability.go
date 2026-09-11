// workspace_capability.go —— 工作区 Capability 的 agentd 持有面（B233.15 迁出实现）。
//
// 职责：Manager 持有并取用已注入的 workspace.Capability；组装结果引用。
// 边界：生产实现由 internal/workspace 提供（workspace.NewCapability）；本文件不
// 再定义适配器、不写账本、不授权 push。
package agentd

import (
	"context"
	"errors"
	"os"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// ErrWorkspaceUnavailable 表示 Manager 未注入工作区能力。
// nil 不得解释成跳过 EnsureRepoUsable 或脏检查——调用方必须失败。
var ErrWorkspaceUnavailable = errors.New("工作区能力未注入")

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

// assembleResultRef 在审阅现场组装结果引用。commit 取 rev-parse；失败则空串，不编造。
func (m *Manager) assembleResultRef(ctx context.Context, task *proto.Task, repo, headRev string) (workspace.ResultRef, error) {
	commit := ""
	if got, err := workspace.ProbeCommit(ctx, repo, headRev); err == nil {
		if workspace.IsCommitSHA(got) {
			commit = got
		} else {
			m.log.Warn("rev-parse 不是 40 位小写 hex，commit 留空", "repo", repo, "head", headRev, "got", got)
		}
	}
	path, branch := "", ""
	if task != nil {
		branch = task.Branch
		path = task.Workdir()
		if _, err := os.Stat(path); err != nil {
			path = ""
		}
	}
	ref, err := workspace.NewResultRef(repo, branch, commit, path)
	if err != nil {
		m.log.Error("组装结果引用失败", "repo", repo, "branch", branch, "commit", commit, "cause", err)
		return workspace.ResultRef{}, err
	}
	m.log.Info("结果引用已组装", "repo", ref.Repo, "branch", ref.Branch, "commit", ref.Commit, "path", ref.Path)
	return ref, nil
}
