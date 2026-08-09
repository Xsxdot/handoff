// reap.go —— 运行态丢失时的兜底回收（B20）。
//
// 职责：按 serve.json 或确定性会话名回收 tmux 会话，不留孤儿进程。
// 边界：不删任务目录、不碰 worktree（那是归档与 B15 的职责）；
//       **不删 ~/.codex/sessions**——那是 codex 自己的会话历史，删了会破坏
//       用户本人的 `codex resume`（spec §5.5）。
package codex

import "log/slog"

// Reap 回收一个任务残留的 tmux 会话。
//
// 参数：
//   - taskID: 任务 ID（serve.json 读不到时按 handoff-<id8> 兜底）
//   - taskDir: 任务目录（用于读 serve.json）
//
// 返回：回收失败的错误；会话本就不存在时返回 nil（回收是幂等的）
func (a *Adapter) Reap(taskID, taskDir string) error {
	log := a.log
	if log == nil {
		log = slog.Default()
	}
	p, err := ReadServeInfo(taskDir)
	if err != nil {
		// serve.json 没了不代表进程没了——会话名是确定性的，按它兜底
		log.Info("codex serve.json 不可读，按确定性会话名兜底回收",
			"task", taskID, "cause", err)
		p = &Proc{Session: "handoff-" + id8(taskID), TaskDir: taskDir}
	}
	log.Info("codex 回收任务残留", "task", taskID, "session", p.Session)
	return p.Kill()
}
