// named.go —— B233.15 新收的具名工作区能力（跨子系统消费面）。
//
// 职责：把编排/登记/回收/预览/bundle 侧原先直接调用的 git 操作收成按使用方
// 语义命名的具名能力，使 git 执行原语（gitRun/gitProbe/gitRunNet/gitExec）不出包。
//
// 边界：只做单次 git 语义动作与错误包装，不含编排、不写账本、不授权 push。
package workspace

import (
	"context"
	"fmt"
	"strings"
)

// DeleteBranch 删除本地分支 refs/heads/<branch>（git branch -D）。
//
// 参数：ctx 控制本次 git 调用；repo 主仓库路径；branch 分支名。
// 返回：失败时返回包装 stderr 原文的错误（是否吞掉由调用方按补偿语义决定）。
//
// 注意：只删分支，不删工作树；force 语义（-D）沿用原补偿路径：补偿只删本次
// 自建且自创建以来零提交的分支，调用方已先复核，故此处固定 -D。
func DeleteBranch(ctx context.Context, repo, branch string) error {
	if _, stderr, err := gitRun(ctx, repo, "branch", "-D", branch); err != nil {
		return fmt.Errorf("git branch -D %s: %s: %w", branch, strings.TrimSpace(stderr), err)
	}
	return nil
}

// RestoreWorktree 把非 managed 工作树切回原 ref（git checkout <ref>）。
//
// 参数：ctx 控制本次 git 调用；workdir 工作树路径；ref 原 ref（分支名或 sha）。
// 返回：失败时返回包装 stderr 原文的错误。
func RestoreWorktree(ctx context.Context, workdir, ref string) error {
	if _, stderr, err := gitRun(ctx, workdir, "checkout", ref); err != nil {
		return fmt.Errorf("git -C %s checkout %s: %s: %w", workdir, ref, strings.TrimSpace(stderr), err)
	}
	return nil
}

// OriginURL 读取仓库 origin 地址（git remote get-url origin）。
//
// 参数：ctx 控制本次 git 调用；repo 仓库路径。
// 返回：origin 地址；仓库不可用或没有 origin 时返回包装 ErrRepoUnusable 的错误。
//
// 注意：没有 origin 的仓库不能登记——project_id 由 origin 派生。
func OriginURL(ctx context.Context, repo string) (string, error) {
	out, stderr, err := gitRun(ctx, repo, "remote", "get-url", "origin")
	if err != nil {
		return "", fmt.Errorf("%w: 读取 %s 的 origin 失败: %s: %v",
			ErrRepoUnusable, repo, strings.TrimSpace(stderr), err)
	}
	url := strings.TrimSpace(out)
	if url == "" {
		return "", fmt.Errorf("%w: 仓库 %s 没有配置 origin remote", ErrRepoUnusable, repo)
	}
	return url, nil
}

// CloneRepo 在 parentDir 下克隆 originURL 到 dest。
//
// 参数：withProxy=true 走出网代理路径（gitRunNet），false 走本地直连（gitRun）。
// 返回：stderr 原文与错误；调用方据 stderr 组装用户可见文案。
//
// 注意：`--` 分隔符防止 URL/路径被 git 当成选项。
func CloneRepo(ctx context.Context, parentDir, originURL, dest string, withProxy bool) (string, error) {
	args := []string{"clone", "--", originURL, dest}
	var stderr string
	var err error
	if withProxy {
		_, stderr, err = gitRunNet(ctx, parentDir, args...)
	} else {
		_, stderr, err = gitRun(ctx, parentDir, args...)
	}
	return stderr, err
}

// TopLevel 探活目录所属工作树的根（git rev-parse --show-toplevel）。
//
// 参数：ctx 控制本次 git 调用；dir 任意目录。
// 返回：根路径与是否命中；失败返回 ("", false)（fail-open，供预览元数据降级）。
func TopLevel(ctx context.Context, dir string) (string, bool) {
	out, _, err := gitProbe(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false
	}
	val := strings.TrimSpace(out)
	if val == "" {
		return "", false
	}
	return val, true
}

// HeadBranch 读取目录当前分支名（git symbolic-ref --short -q HEAD）。
//
// 参数：ctx 控制本次 git 调用；dir 工作树路径。
// 返回：分支名；detached 或读取失败时返回空串（fail-open，绝不编造分支名）。
func HeadBranch(ctx context.Context, dir string) string {
	out, _, err := gitProbe(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// ProbeCommit 把 rev 解析成 40 位提交号（git rev-parse <rev>）。
//
// 参数：ctx 控制本次 git 调用；repo 仓库路径；rev 任意 rev。
// 返回：去空白的 rev-parse 输出与错误；调用方负责 IsCommitSHA 复核。
func ProbeCommit(ctx context.Context, repo, rev string) (string, error) {
	out, _, err := gitProbe(ctx, repo, "rev-parse", rev)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
