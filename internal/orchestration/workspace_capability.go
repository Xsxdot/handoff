// workspace_capability.go —— 编排侧对工作区能力的持有与结果引用组装（B233.13 迁入）。
//
// 职责：Manager 持有注入的 workspace.Capability；RequireWorkspace 做未注入判据；
// AssembleResultRef 在审阅现场组装结果引用。
// 边界：不实现 git（gitCapability 留 gateway）；具体能力只在组装点构造。
package orchestration

import (
	"context"
	"os"
	"strings"

	agentd "github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// SetWorkspace 注入工作区能力。测试可换 fake；生产由组装点绑定。
func (m *Manager) SetWorkspace(c workspace.Capability) { m.ws = c }

// Workspace 返回已注入的能力，可能为 nil。
func (m *Manager) Workspace() workspace.Capability { return m.ws }

// RequireWorkspace 校验工作区能力是否已注入。
func (m *Manager) RequireWorkspace() error {
	if m == nil || m.ws == nil {
		return agentd.ErrWorkspaceUnavailable
	}
	return nil
}

// AssembleResultRef 在审阅现场组装结果引用。commit 取 rev-parse；失败则空串，不编造。
func (m *Manager) AssembleResultRef(ctx context.Context, task *proto.Task, repo, headRev string) (workspace.ResultRef, error) {
	commit := ""
	if out, _, err := agentd.GitProbe(ctx, repo, "rev-parse", headRev); err == nil {
		got := strings.TrimSpace(out)
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
