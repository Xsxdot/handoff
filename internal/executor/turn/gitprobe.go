// gitprobe.go —— 回合的 git 事实取证。
//
// 职责：读当前分支与 HEAD，并与回合起点 commit 比对判断「是否有新提交」
// 边界：只读 git，绝不写；不做任何裁决，裁决由调用方基于事实决定

package turn

import (
	"fmt"
	"os/exec"
	"strings"
)

// GitTurnStatus 返回工作区当前分支、HEAD commit，以及相对 startCommit 是否有新提交。
//
// 参数：
//   - repoPath: 任务工作目录（仓库或 worktree 路径）
//   - startCommit: 回合起点的 HEAD；空串表示起点未知，此时 hasNew 恒为 false
//
// 返回：分支名、HEAD hash、是否有新提交、错误
//
// 为什么需要它：模型可能不守收尾纪律（不输出 trailer）。此时唯一可信的是 git
// 实况——有新提交才可能是「干完了」，没有就该交审核者，绝不替模型宣布完成。
func GitTurnStatus(repoPath, startCommit string) (branch, commit string, hasNew bool, err error) {
	run := func(args ...string) (string, error) {
		out, err := exec.Command("git", append([]string{"-C", repoPath}, args...)...).Output()
		if err != nil {
			return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
		}
		return strings.TrimSpace(string(out)), nil
	}
	if branch, err = run("rev-parse", "--abbrev-ref", "HEAD"); err != nil {
		return "", "", false, err
	}
	if commit, err = run("rev-parse", "HEAD"); err != nil {
		return branch, "", false, err
	}
	// commit != "" 这一条不能省：rev-parse 成功却返回空串时，省掉它会让 hasNew
	// 变成 true，等于替模型宣布完成——方向恰好与本函数存在的理由相反。
	// 逐字保留 opencode 原判据，纯重构不许顺手简化防御条件。
	return branch, commit, startCommit != "" && commit != "" && commit != startCommit, nil
}
