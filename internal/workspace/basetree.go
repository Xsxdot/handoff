// basetree.go —— B400 首派基线护栏的仓库侧读取：判断附件路径是否存在于解析出的
// 基线远端提交树。
//
// 职责：解析基线分支名（空则取项目默认分支）→ 远端补拉并解析到 SHA → 逐路径判在场性。
// 边界：只读；不改任何 ref、不建分支/工作树、不写盘；不判断「哪条才是工作线」，
// 只回答「这些路径在不在那棵树上」。
package workspace

import (
	"context"
	"strings"
)

// BaseTreeMissingPaths 报告 paths 里哪些不在 base 分支的远端提交树中。
//
// 参数：
//   - ctx: 上层上下文
//   - repo: 已通过 EnsureRepoUsable 的项目仓库路径
//   - base: 基线分支名；空 = 先解析项目默认分支（ResolveDefaultBaseBranch）
//   - paths: 仓内相对 git 路径
//
// 返回：实际使用的基线分支名、缺失路径（保持入参顺序、不去重）、错误。
//
// 注意：
//   - 一律经 ResolveDispatchBase 补拉并解析到**远端** SHA，不读本地陈旧分支——
//     这正是「检查读远端 commit 树」的落点；
//   - 路径在场性用 `git ls-tree -r --name-only <sha> -- ':(literal)<path>'` 判定：
//     输出非空即在场（缺失时空输出且退出 0）；不做内容比对；
//   - base 为空且 origin/HEAD 缺失时返回 ResolveDefaultBaseBranch 的错误。
func BaseTreeMissingPaths(ctx context.Context, repo, base string, paths []string) (string, []string, error) {
	resolvedBase := strings.TrimSpace(base)
	if resolvedBase == "" {
		defaultBase, err := ResolveDefaultBaseBranch(ctx, repo)
		if err != nil {
			return "", nil, err
		}
		resolvedBase = defaultBase
	}
	sha, _, err := ResolveDispatchBase(ctx, repo, resolvedBase, false)
	if err != nil {
		return resolvedBase, nil, err
	}
	missing := make([]string, 0)
	for _, p := range paths {
		if !repoRelativePathSafe(p) {
			missing = append(missing, p)
			continue
		}
		out, _, err := gitProbe(ctx, repo, "ls-tree", "-r", "--name-only", sha, "--", ":(literal)"+p)
		if err != nil || strings.TrimSpace(out) == "" {
			missing = append(missing, p)
		}
	}
	return resolvedBase, missing, nil
}

// repoRelativePathSafe 拒绝会把 git 参数面打开的路径形态（空、绝对、以 - 开头、
// 含 .. 段）。附件路径由平台写入，正常都安全；这里 fail-closed 把可疑路径当作
// 「不在树上」，绝不把它拼进 git 参数。
func repoRelativePathSafe(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || strings.HasPrefix(p, "/") || strings.HasPrefix(p, "-") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." {
			return false
		}
	}
	return true
}
