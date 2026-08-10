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

import "strings"

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
