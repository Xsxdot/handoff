// 本文件负责一件事：把「cwd 落在哪」归并成「项目在这台机器上的那一个位置」。
//
// 职责：
//   - MainWorktreeRoot：无论调用点在主仓、主仓子目录、linked worktree 还是
//     worktree 子目录，一律返回**主工作树的根目录**
//
// 边界：
//   - 不读登记表、不碰数据库：它只回答「这个目录属于哪个仓库根」
//   - 不判断该仓库有没有 origin：那是登记层 projectOriginURL 的事
//   - 不做 symlink 求真（不 EvalSymlinks）：返回值来自 git 自己的输出与调用方
//     给的目录，保持与 git 一致的视角
//
// 为什么必须归并（B62）：项目位置表以 project_id 为主键，一个项目在一台机器上
// 只能有一行。本仓库当前有十几个 linked worktree，它们与主仓 origin 相同、
// project_id 相同——不归并就会撞主键，允许多行则项目树彻底没法看。
package agentd

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// MainWorktreeRoot 返回 dir 所属 git 仓库的主工作树根目录。
//
// 参数：
//   - ctx: 控制 git 调用生命周期
//   - dir: 任意目录（主仓根/主仓子目录/linked worktree/worktree 子目录）
//
// 返回：
//   - 主工作树根目录的绝对路径（已 Clean）
//   - 错误：dir 不是 git 仓库（或 git 不可用）时返回包装 ErrRepoUnusable 的错误，
//     报文带 git 的 stderr 原文
//
// 注意：
//   - git rev-parse --git-common-dir 在**主仓内**返回相对路径（根目录 ".git"，
//     子目录 "../.git"），在 **linked worktree 内**返回指向主仓的绝对路径。
//     两种形态都要处理：相对时以 dir 为基准拼接，再取父目录
//   - 返回值一律绝对化：位置表的 path 列是绝对路径，UNIQUE 约束要靠它才有意义
func MainWorktreeRoot(ctx context.Context, dir string) (string, error) {
	out, stderr, err := gitRun(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		log().Warn("主工作树归并失败：目录不是 git 仓库", "dir", dir,
			"stderr", truncateRunes(strings.TrimSpace(stderr), 300), "cause", err)
		return "", fmt.Errorf("%w: %s 不是 git 仓库: %s: %v",
			ErrRepoUnusable, dir, strings.TrimSpace(stderr), err)
	}
	common := strings.TrimSpace(out)
	if common == "" {
		log().Warn("主工作树归并失败：git 返回空的 common-dir", "dir", dir)
		return "", fmt.Errorf("%w: %s 的 git-common-dir 为空", ErrRepoUnusable, dir)
	}
	// 相对路径（主仓内的 ".git" / "../.git"）以 dir 为基准展开；绝对路径
	//（linked worktree 内）原样使用——它已经指向主仓的 .git。
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	root, err := filepath.Abs(filepath.Dir(common))
	if err != nil {
		log().Error("主工作树归并失败：绝对化仓库根出错", "dir", dir, "common", common, "cause", err)
		return "", fmt.Errorf("%w: 绝对化 %s: %v", ErrRepoUnusable, common, err)
	}
	if root != dir {
		log().Info("主工作树归并", "from", dir, "root", root)
	}
	return filepath.Clean(root), nil
}
