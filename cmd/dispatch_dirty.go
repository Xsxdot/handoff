// 本文件实现 dispatch 的本地工作区完整性校验（backlog B29）。
//
// 职责：
//   - 把 git status --porcelain 的输出分成「已跟踪改动」与「未跟踪文件」两类
//   - 已跟踪改动拒发（--allow-dirty 可放行），未跟踪只警告
//   - 全部提示走调用方给的 stderr writer
//
// 边界：
//   - 只看当前工作目录（cwd）这一棵树；agentd 侧任务仓库的脏检查是另一回事，
//     由 internal/agentd 的 ensureCleanWorktree 负责，两者互不替代
//   - 不发起任何网络请求：拒发必须发生在 HTTP 请求之前
//   - 不解释 git 的退出码：status 本身失败时降级放行，不把派发挡死
package cmd

import (
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// classifyLocalDirty 把 git status --porcelain 的输出分成「已跟踪改动」与
// 「未跟踪文件」两类。
//
// 参数：
//   - porcelain: git status --porcelain 的原始 stdout
//
// 返回：
//   - tracked: 已跟踪文件的改动路径（含已暂存的 M /A ，它们同样没进 commit）
//   - untracked: 未跟踪文件路径（?? 开头）
//   - 两者在无对应条目时为 nil
func classifyLocalDirty(porcelain string) (tracked, untracked []string) {
	for _, line := range strings.Split(porcelain, "\n") {
		// porcelain v1 每行形如 "XY PATH"：两位状态码 + 一个空格 + 路径。
		// 短于 4 的行（空行、意外输出）没有可用路径，跳过而不是当成空文件名
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// 重命名/拷贝形如 "R  old -> new"：审核者关心改动落在哪个新路径上
		if i := strings.LastIndex(path, " -> "); i >= 0 {
			path = path[i+len(" -> "):]
		}
		if strings.HasPrefix(line, "??") {
			untracked = append(untracked, path)
			continue
		}
		tracked = append(tracked, path)
	}
	return tracked, untracked
}

// dirtyListLimit 是提示里最多列出的文件数。列全了会把有用信息挤出视线，
// 而审核者只需要「哪一类文件脏了」就够决定下一步。
const dirtyListLimit = 5

// formatDirtyList 把文件名列表拼成一行给人读；超过 dirtyListLimit 截断并补计数。
//
// 参数：files 为路径列表，可为空
// 返回：逗号连接的单行文本；files 为空时返回空串
func formatDirtyList(files []string) string {
	if len(files) == 0 {
		return ""
	}
	if len(files) <= dirtyListLimit {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s ... 另有 %d 处",
		strings.Join(files[:dirtyListLimit], ", "), len(files)-dirtyListLimit)
}

// checkLocalWorktree 校验当前工作目录是否有「不会随基线送到 executor」的改动。
//
// 参数：
//   - errOut: 提示与警告的输出目标（调用方传 cmd.ErrOrStderr()）
//   - allowDirty: 为真时已跟踪改动只警告不拒发（--allow-dirty）
//
// 返回：
//   - 已跟踪文件有未提交改动且 allowDirty 为假 → 返回可行动的错误（调用方据此中止派发）
//   - 其余一律返回 nil；未跟踪文件与被放行的已跟踪改动都已写入 errOut
//
// 注意：
//   - 必须在发起 HTTP 请求之前调用——拒发的价值就在于不产生任何远端副作用
//   - git status 自身失败时降级放行：调用点已确认 cwd 是 git 仓库（HEAD 解析成功），
//     走到这里的失败属异常情形，不该因此把派发挡死
func checkLocalWorktree(errOut io.Writer, allowDirty bool) error {
	out, err := exec.Command("git", "status", "--porcelain").Output()
	if err != nil {
		fmt.Fprintln(errOut, "提示: 读取本地工作区状态失败，已跳过完整性校验:", err)
		return nil
	}
	tracked, untracked := classifyLocalDirty(string(out))
	if len(untracked) > 0 {
		fmt.Fprintf(errOut, "提示: 本地有 %d 个未跟踪文件不会被派发（executor 看不到）：%s\n",
			len(untracked), formatDirtyList(untracked))
	}
	if len(tracked) == 0 {
		return nil
	}
	// 放行也必须留痕：静默的 --allow-dirty 就是新的 B29
	if allowDirty {
		fmt.Fprintf(errOut, "警告: 本地有 %d 处未提交的已跟踪改动，--allow-dirty 已放行（executor 看不到它们）：%s\n",
			len(tracked), formatDirtyList(tracked))
		return nil
	}
	return fmt.Errorf("本地工作区有 %d 处未提交的已跟踪改动，executor 看不到它们：%s\n"+
		"远程派发会基于不含这些改动的基线开工。请先 git commit 或 git stash；"+
		"确要照现状派发，加 --allow-dirty",
		len(tracked), formatDirtyList(tracked))
}
