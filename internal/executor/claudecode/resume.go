// resume.go —— 执行恢复：热重连与冷恢复。
//
// 职责：
//   - Resume：按 ResumeReq 走恢复阶梯，返回实际走到的级别
//
// 边界：
//   - 不判断「该不该恢复」的业务前提（如是否有未决权限工单）——那需要工单知识，
//     属 manager（见 manager.go 的 volatilePermitter）
//   - 不改任务状态：adapter 不写 store（见 executor 包级边界）
package claudecode

import (
	"fmt"
	"path/filepath"

	"github.com/xushixin/handoff/internal/executor"
)

// Resume 恢复 agentd 重启前已在执行的任务（manager 的 restorer 可选接口）。
//
// 存活判据（本 adapter 与 opencode 的关键差异）：tmux has-session **不可用**
// ——窗口 1 的 tail -f render.log 会一直活着，claude 早死了会话依然存在
// （opencode 靠 HTTP 探活兜住这一点，claude 没有这个面）。判据是两条，缺一
// 即视为死亡：
//  1. out.jsonl 中不含 handoff_exit 哨兵（含则进程已退，带退出码）
//  2. tmux 会话存在（会话都没了，进程一定没了）
//
// 参数：
//   - req: 恢复请求（TaskDir 是 claude.json 所在，即 DataDir/tasks/<id>；
//     RepoPath 用于重启后重新捕获 git 兜底分类的起点 commit 基线；
//     SessionID 是落库的 claude 会话 id）
//
// 返回：
//   - Alive=false 时调用方（manager）把任务转 failed 交审核者裁决（保守优于静默）
//   - err: 重建失败（claude.json 缺失/损坏、SessionID 为空），视为不可恢复
//
// 注意：
//   - 恢复出的运行态与 Start 对称：Stop（done 归档）对其同样有效
//   - 挂起权限不需要在此重建应答：MCP 子进程（claude 拉起的）会自行重连
//     perm.sock 重登记同一 tool_use_id，manager 侧 ticket 按 id 幂等去重
func (a *Adapter) Resume(req executor.ResumeReq) (out executor.ResumeOutcome, err error) {
	a.log.Info("claude adapter 恢复任务执行", "task", req.TaskID, "session", req.SessionID)
	defer func() {
		if err != nil {
			a.log.Error("claude adapter 恢复任务失败", "task", req.TaskID, "cause", err)
		} else if out.Alive {
			a.log.Info("claude adapter 任务已恢复", "task", req.TaskID, "session", req.SessionID)
		}
	}()

	if req.SessionID == "" {
		return executor.ResumeOutcome{}, fmt.Errorf("任务 %s 缺 executor_session，无法重建订阅", req.TaskID)
	}
	pi, err := readProcInfo(req.TaskDir)
	if err != nil {
		return executor.ResumeOutcome{}, err
	}
	proc := &Proc{TmuxSession: pi.TmuxSession, TaskDir: req.TaskDir, SessionID: req.SessionID}

	// 判据 1：哨兵在 → 进程已退（带退出码），交审核者裁决
	if exited, code := procExited(filepath.Join(req.TaskDir, outFileName)); exited {
		a.log.Info("恢复探活失败：claude 已退出（哨兵）", "task", req.TaskID,
			"tmux", pi.TmuxSession, "code", code)
		// 回收残留会话：窗口 1 的 tail -f 会一直吊着 tmux 会话，不回收即成孤儿
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("回收已死执行器的 tmux 会话失败，可能需人工清理",
				"task", req.TaskID, "tmux", pi.TmuxSession, "cause", kerr)
		}
		return executor.ResumeOutcome{}, nil
	}
	// 判据 2：tmux 会话都没了，进程一定没了
	if !tmuxHasSession(pi.TmuxSession) {
		a.log.Info("恢复探活失败：tmux 会话已不存在", "task", req.TaskID, "tmux", pi.TmuxSession)
		return executor.ResumeOutcome{}, nil
	}

	// 存活：重建运行态——tailer 从已消费 offset 续读（已消费回合不重放），
	// 重开 perm.sock（MCP 子进程会重连重登记），重启看门狗
	r := a.newRun(req.TaskID, req.TaskDir, req.RepoPath)
	r.session = req.SessionID
	r.proc = proc
	r.startOffset = pi.Offset
	perm, err := newPermServer(filepath.Join(req.TaskDir, sockFileName), a.log,
		func(ask permAsk) { a.onPermissionAsk(r, ask) })
	if err != nil {
		a.drop(req.TaskID)
		return executor.ResumeOutcome{}, err
	}
	r.perm = perm
	r.captureStartCommit(a)
	go r.streamLoop(a)
	go a.watchdog(r)
	return executor.ResumeOutcome{
		Alive: true, Mode: executor.ResumeModeReattach, SessionID: req.SessionID,
		Note: "executor 仍存活，已重连事件流",
	}, nil
}
