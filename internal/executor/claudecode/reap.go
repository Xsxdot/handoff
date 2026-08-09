// reap.go —— 无内存运行态时的确定性兜底回收。
//
// 职责：
//   - Reap：按 claude.json 或确定性命名找到 tmux 会话并杀掉
//
// 边界：
//   - 不碰任务状态（adapter 不写 store）；回收不掉只返回错误，留不留事件是 manager 的事
package claudecode

// Reap 在没有内存运行态时按确定性命名兜底回收 executor 侧资源。
//
// 回收顺序：
//  1. 读 taskDir 下的 claude.json 拿 tmux 会话名（最准，session_id/offset 也在里面）
//  2. 文件缺失/损坏时退到确定性命名 "handoff-" + id8(taskID)（与 StartProc 同规则）
//  3. tmux kill-session
//
// 存活判据的特别之处：对 claude 而言 tmux has-session **不是**进程存活判据
// （窗口 1 的 tail -f render.log 会一直吊着会话，claude 早死了会话依然存在，
// 见 Resume 的 doc）——但对 Reap **正合适**：Reap 要确认的就是「tmux 会话没了」，
// 不是「claude 进程没了」。所以「会话已不存在」判定用 tmuxHasSession。
//
// 返回：
//   - 会话本就不存在时返回 nil——目标是「确保它没了」，不是「确保我杀了它」
func (a *Adapter) Reap(taskID, taskDir string) error {
	session := "handoff-" + id8(taskID)
	source := "确定性命名"
	if pi, err := readProcInfo(taskDir); err == nil && pi.TmuxSession != "" {
		session, source = pi.TmuxSession, "claude.json"
	} else if err != nil {
		a.log.Warn("读 claude.json 失败，退到确定性命名回收", "task", taskID, "cause", err)
	}
	a.log.Info("兜底回收 executor 资源", "task", taskID, "tmux", session, "source", source)
	if err := tmuxKill(session); err != nil {
		// 会话已经不在时 tmux kill-session 也会报错——先确认它是不是真没了
		if !tmuxHasSession(session) {
			a.log.Info("兜底回收：会话已不存在，视为成功", "task", taskID, "tmux", session)
			return nil
		}
		return err
	}
	return nil
}
