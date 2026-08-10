// machineauthority inventory：用 Git 2.25 基线命令发现本机仓库状态。
//
// 职责：
//   - DiscoverWorkspaces：`git worktree list --porcelain` + `rev-parse` 发现
//     main/worktree 工作区
//   - DiscoverGitRefs：`git for-each-ref refs/heads` 发现本地分支
//   - InspectPath/Clone：检查/克隆一个目录
//
// 边界：
//   - Git 命令仅使用 2.25 基线：worktree list --porcelain、for-each-ref、
//     rev-parse、remote get-url。不得直接使用 2.36 才有的 worktree list -z
//   - 不做业务校验：inventory 只报告事实，校验由上层（ProjectService）承担
package machineauthority

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/gitidentity"
	"github.com/xushixin/handoff/internal/store"
)

// Inventory 针对一个仓库根目录做只读扫描。
type Inventory struct {
	Root string
}

// runGit 在仓库根执行 git 命令。
func (inv *Inventory) runGit(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", inv.Root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w (%s)", args, err, truncate(out, 300))
	}
	return string(out), nil
}

// truncate 截断长输出到 max 字节。
func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}

// DiscoverWorkspaces 扫描 main 与全部 worktree，返回稳定 Workspace 列表。
//
// 使用 `git worktree list --porcelain`：每个区块以 "worktree <path>" 开头，
// 后接 HEAD/branch/detached 行；该格式自 Git 2.13 稳定，属于 2.25 基线。
func (inv *Inventory) DiscoverWorkspaces(ctx context.Context) ([]controlplane.Workspace, error) {
	out, err := inv.runGit(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	commonDir := ""
	if cd, cerr := inv.runGit(ctx, "rev-parse", "--git-common-dir"); cerr == nil {
		commonDir = strings.TrimSpace(cd)
	}
	repoIdentity := inv.repoIdentity(ctx)

	now := time.Now().UTC()
	var workspaces []controlplane.Workspace
	// worktree list 输出以空行分隔区块
	for _, block := range splitBlocks(out) {
		path := blockLine(block, "worktree ")
		if path == "" {
			continue
		}
		canonical, cerr := store.CanonicalPath(path)
		if cerr != nil {
			canonical = filepath.Clean(path)
		}
		branch := blockLine(block, "branch refs/heads/")
		headOID := blockLine(block, "HEAD ")
		kind := controlplane.WorkspaceKindWorktree
		if canonical == inv.rootCanonical() {
			kind = controlplane.WorkspaceKindMain
		}
		workspaces = append(workspaces, controlplane.Workspace{
			ID:            uuid.NewString(),
			Kind:          kind,
			Path:          path,
			CanonicalPath: canonical,
			RepoIdentity:  repoIdentity,
			GitCommonDir:  commonDir,
			Branch:        branch,
			HeadOID:       headOID,
			Availability:  controlplane.AvailabilityAvailable,
			LastScannedAt: now,
		})
	}
	if len(workspaces) == 0 {
		return nil, fmt.Errorf("仓库 %s 没有任何 worktree", inv.Root)
	}
	return workspaces, nil
}

// rootCanonical 返回仓库根的 canonical path（main workspace 判定基准）。
func (inv *Inventory) rootCanonical() string {
	if c, err := store.CanonicalPath(inv.Root); err == nil {
		return c
	}
	return filepath.Clean(inv.Root)
}

// DiscoverGitRefs 扫描本地分支，返回 GitRef 列表（locationID 由调用方提供）。
func (inv *Inventory) DiscoverGitRefs(ctx context.Context, locationID string) ([]controlplane.GitRef, error) {
	out, err := inv.runGit(ctx, "for-each-ref", "--format=%(refname:short) %(objectname)", "refs/heads")
	if err != nil {
		return nil, err
	}
	// 哪个分支在哪个 worktree 上检出：main 仓库 + worktree 各自 current branch。
	checkout := inv.checkedOutBranches(ctx)
	var refs []controlplane.GitRef
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		name, oid := fields[0], fields[1]
		refs = append(refs, controlplane.GitRef{
			LocationID:             locationID,
			Name:                   name,
			HeadOID:                oid,
			CheckedOutWorkspaceIDs: checkout[name],
		})
	}
	return refs, nil
}

// checkedOutBranches 返回 branch → 检出它的 workspace 路径集合（main 也用
// canonical path 作 key，供 reconcile 关联）。
func (inv *Inventory) checkedOutBranches(ctx context.Context) map[string][]string {
	out := make(map[string][]string)
	ws, err := inv.DiscoverWorkspaces(ctx)
	if err != nil {
		return out
	}
	for _, w := range ws {
		if w.Branch != "" {
			out[w.Branch] = append(out[w.Branch], w.CanonicalPath)
		}
	}
	return out
}

// InspectPath 检查一个目录是否为 git 仓库并返回规范化信息。
func (inv *Inventory) InspectPath(ctx context.Context, path string) (PathInspection, error) {
	expanded, err := resolveOwnerPath(path)
	if err != nil {
		return PathInspection{}, err
	}
	path = expanded
	canonical, cerr := store.CanonicalPath(path)
	if cerr != nil {
		return PathInspection{}, fmt.Errorf("规范化路径 %s: %w", path, cerr)
	}
	// 目录可访问性
	if _, err := os.Stat(canonical); err != nil {
		return PathInspection{}, fmt.Errorf("路径 %s 不可访问: %w", canonical, err)
	}
	inspection := PathInspection{Path: path, CanonicalPath: canonical}
	// 是否 git 仓库：git -C canonical rev-parse --git-dir 成功即为仓库
	cmd := exec.CommandContext(ctx, "git", "-C", canonical, "rev-parse", "--git-dir")
	if out, err := cmd.CombinedOutput(); err != nil {
		inspection.IsRepo = false
		return inspection, nil
	} else {
		_ = out
	}
	inspection.IsRepo = true
	if commonDir, err := inv.runGitAt(ctx, canonical, "rev-parse", "--git-common-dir"); err == nil {
		inspection.GitCommonDir = strings.TrimSpace(commonDir)
	}
	inspection.RepoIdentity = inv.repoIdentityAt(ctx, canonical)
	inspection.Branch = inv.currentBranchAt(ctx, canonical)
	inspection.HeadOID = inv.headOIDAt(ctx, canonical)
	return inspection, nil
}

// runGitAt 在指定目录执行 git。
func (inv *Inventory) runGitAt(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %v: %w (%s)", args, err, truncate(out, 300))
	}
	return string(out), nil
}

// repoIdentity 返回仓库根的规范化 repo identity。
func (inv *Inventory) repoIdentity(ctx context.Context) string {
	return inv.repoIdentityAt(ctx, inv.Root)
}

// repoIdentityAt 读取 remote origin URL 并规范化。
func (inv *Inventory) repoIdentityAt(ctx context.Context, dir string) string {
	raw, err := inv.runGitAt(ctx, dir, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	id, err := gitidentity.CanonicalRepoIdentity(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return id
}

// currentBranchAt 返回目录当前分支（detached 返回空）。
func (inv *Inventory) currentBranchAt(ctx context.Context, dir string) string {
	out, err := inv.runGitAt(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	b := strings.TrimSpace(out)
	if b == "HEAD" {
		return "" // detached
	}
	return b
}

// headOIDAt 返回目录当前 HEAD commit。
func (inv *Inventory) headOIDAt(ctx context.Context, dir string) string {
	out, err := inv.runGitAt(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// Clone 克隆一个仓库到指定目录并返回检查结果。
//
// 为什么先 Inspect 再 Clone 不矛盾：Clone 目标已存在时交由上层（ProjectService）
// 判定幂等复用或冲突，inventory 本身只负责「不存在则克隆」。
func (inv *Inventory) Clone(ctx context.Context, cmd CloneCommand) (PathInspection, error) {
	if cmd.GitURL == "" || cmd.ClonePath == "" {
		return PathInspection{}, fmt.Errorf("clone 命令缺 git_url 或 clone_path")
	}
	clonePath, err := resolveOwnerPath(cmd.ClonePath)
	if err != nil {
		return PathInspection{}, err
	}
	if _, err := os.Stat(clonePath); err == nil {
		return PathInspection{}, fmt.Errorf("clone 目标已存在: %s（须由上层判定幂等复用或冲突）", cmd.ClonePath)
	}
	if err := os.MkdirAll(filepath.Dir(clonePath), 0o755); err != nil {
		return PathInspection{}, fmt.Errorf("创建 clone 父目录: %w", err)
	}
	gitCmd := exec.CommandContext(ctx, "git", "clone", "--", cmd.GitURL, clonePath)
	if err := gitCmd.Run(); err != nil {
		// clone stderr 常回显完整 remote URL；URL 可能带 userinfo/token，不能进入
		// ProjectService 的结构化错误日志或 Operation.Error。
		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
		return PathInspection{}, fmt.Errorf("git clone 失败 (exit=%d): %w", exitCode, err)
	}
	return inv.InspectPath(ctx, clonePath)
}

// resolveOwnerPath 在实际执行命令的 owner agentd 上展开 `~`。
// 桌面或控制 agentd 不得提前展开，否则远端 clone 会错误使用本机 home。
func resolveOwnerPath(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("读取 owner home: %w", err)
	}
	return expandOwnerPath(path, home)
}

func expandOwnerPath(path, home string) (string, error) {
	switch {
	case path == "~":
		return filepath.Clean(home), nil
	case strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`):
		return filepath.Join(home, path[2:]), nil
	case strings.HasPrefix(path, "~"):
		return "", fmt.Errorf("不支持其他用户 home 路径 %q", path)
	default:
		return path, nil
	}
}

// splitBlocks 把 worktree list --porcelain 输出按空行切分为区块。
func splitBlocks(out string) []string {
	blocks := strings.Split(out, "\n\n")
	// 每个区块以 "worktree <path>" 开头；尾随空行产生的空区块过滤掉
	valid := blocks[:0]
	for _, b := range blocks {
		if strings.TrimSpace(b) != "" {
			valid = append(valid, b)
		}
	}
	return valid
}

// blockLine 取区块中指定前缀的行值（去掉前缀）。
func blockLine(block, prefix string) string {
	for _, line := range strings.Split(block, "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}
