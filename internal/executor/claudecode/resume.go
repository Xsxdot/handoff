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
	"context"
	"fmt"
	"os"
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
			a.log.Info("claude adapter 任务已恢复", "task", req.TaskID, "session", out.SessionID)
		}
	}()

	if req.SessionID == "" {
		return executor.ResumeOutcome{}, fmt.Errorf("任务 %s 缺 executor_session，无法重建订阅", req.TaskID)
	}
	// 冷恢复互斥（spec §6）：先在 runs 上占位再拉进程，后到者直接返回
	// 「恢复进行中」。两个 claude 进程抢同一个会话是数据损坏级别的后果
	a.mu.Lock()
	if _, busy := a.runs[req.TaskID]; busy {
		a.mu.Unlock()
		a.log.Info("该任务已有运行态或恢复进行中，跳过本次恢复", "task", req.TaskID)
		return executor.ResumeOutcome{Alive: false, Note: "该任务的恢复正在进行中"}, nil
	}
	a.runs[req.TaskID] = &runState{taskID: req.TaskID} // 占位
	a.mu.Unlock()
	defer func() {
		// 失败路径清掉占位，否则这个任务永远恢复不了
		a.mu.Lock()
		if cur, ok := a.runs[req.TaskID]; ok && cur.evCh == nil {
			delete(a.runs, req.TaskID)
		}
		a.mu.Unlock()
	}()

	pi, err := readProcInfo(req.TaskDir)
	if err != nil {
		return executor.ResumeOutcome{}, err
	}
	proc := &Proc{TmuxSession: pi.TmuxSession, TaskDir: req.TaskDir, SessionID: req.SessionID}

	mode := executor.ResumeModeReattach
	dead := false
	// 判据 1：哨兵在 → 进程已退（带退出码）
	if exited, code := procExited(filepath.Join(req.TaskDir, outFileName)); exited {
		a.log.Info("恢复探活失败：claude 已退出（哨兵）", "task", req.TaskID,
			"tmux", pi.TmuxSession, "code", code)
		dead = true
	}
	// 判据 2：tmux 会话都没了，进程一定没了
	if !dead && !tmuxHasSession(pi.TmuxSession) {
		a.log.Info("恢复探活失败：tmux 会话已不存在", "task", req.TaskID, "tmux", pi.TmuxSession)
		dead = true
	}

	if dead {
		// 先回收旧会话，否则冷恢复重起时撞名（窗口 1 的 tail -f 会吊着会话）
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("回收已死执行器的 tmux 会话失败", "task", req.TaskID, "cause", kerr)
		}
		if !req.Cold {
			return executor.ResumeOutcome{Alive: false,
				Note: "claude 进程已不在（本次只允许热重连）"}, nil
		}
		// §6 约束 5（冷恢复不重建 worktree）：任务工作区可能已随归档清理，
		// 重建是 Dispatch 的职责，越界重建会让归档过的任务诈尸
		if _, serr := os.Stat(req.TaskDir); serr != nil {
			a.log.Info("任务目录已不存在，判不可恢复", "task", req.TaskID, "cause", serr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务目录已不存在（可能已归档清理），无法恢复"}, nil
		}
		if _, rerr := os.Stat(req.RepoPath); rerr != nil {
			a.log.Info("任务工作区已不存在，判不可恢复", "task", req.TaskID, "cause", rerr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务工作区已不存在（可能已归档清理），无法恢复"}, nil
		}
		// out.jsonl 轮转：冷恢复后是全新的输出流，旧 offset 无意义。旧文件留着
		// （诊断价值），新开一个，offset 归零
		if rerr := rotateOutJSONL(req.TaskDir); rerr != nil {
			a.log.Warn("轮转 out.jsonl 失败，仍尝试冷恢复", "task", req.TaskID, "cause", rerr)
		}
		a.log.Info("claude 已不在，进入冷恢复", "task", req.TaskID, "session", req.SessionID)
		newProc, err := startProc(context.Background(), StartProcReq{
			// cwd 必须是原工作区：会话文件路径按 cwd 编码
			// （~/.claude/projects/<slug(cwd)>/），传错就找不到会话
			RepoPath: req.RepoPath, TaskID: req.TaskID, TaskDir: req.TaskDir,
			SessionID: req.SessionID, Model: req.Model,
			SettingsPath: filepath.Join(req.TaskDir, settingsFileName),
			MCPPath:      filepath.Join(req.TaskDir, mcpFileName),
			Env:          req.Env, Resume: true,
		}, a.log)
		if err != nil {
			a.log.Warn("冷恢复重起 claude 失败，判不可恢复", "task", req.TaskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("重起 claude 失败：%v", err)}, nil
		}
		proc = newProc
		mode = executor.ResumeModeCold
	}

	// 存活（热重连）或冷恢复成功：重建运行态——tailer 从 offset 续读（热重连
	// 从持久化 offset，冷恢复从 0），重开 perm.sock（MCP 子进程会重连重登记），
	// 重启看门狗。两条路共用同一段代码，不复制
	r := a.newRun(req.TaskID, req.TaskDir, req.RepoPath)
	r.session = req.SessionID
	r.proc = proc
	if mode == executor.ResumeModeCold {
		r.startOffset = 0
	} else {
		r.startOffset = pi.Offset
	}
	perm, err := newPermServerFn(filepath.Join(req.TaskDir, sockFileName), a.log,
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
		Alive: true, Mode: mode, SessionID: req.SessionID,
		Note: resumeNote(mode, req.SessionID),
	}, nil
}

// rotateOutJSONL 把 out.jsonl 轮转到 out.<n>.jsonl（n 从 1 递增到第一个不存在的），
// 并重置恢复凭据里的 offset。
//
// why（冷恢复需要轮转）：冷恢复后是全新的输出流，旧 offset 无意义——不轮转的话
// tailer 从旧 offset 续读新文件，会把新会话的开头当成旧内容跳过。旧文件保留
// （诊断价值），新开 out.jsonl 让 tailer 从 0 读。
//
// 返回：nil（成功或文件本就不存在）；轮转中读写错误。
func rotateOutJSONL(taskDir string) error {
	src := filepath.Join(taskDir, outFileName)
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return nil // 本就没有旧输出流，无需轮转
	}
	// 递增找第一个不存在的后缀名
	for i := 1; ; i++ {
		dst := filepath.Join(taskDir, fmt.Sprintf("out.%d.jsonl", i))
		if _, err := os.Stat(dst); os.IsNotExist(err) {
			if rerr := os.Rename(src, dst); rerr != nil {
				return fmt.Errorf("轮转 %s -> %s: %w", src, dst, rerr)
			}
			return nil
		}
	}
}

// resumeNote 拼恢复结果的一句话结论（进 ResumeOutcome.Note 供 manager 转播）。
func resumeNote(mode, sessionID string) string {
	switch mode {
	case executor.ResumeModeReattach:
		return "executor 仍存活，已重连事件流"
	case executor.ResumeModeCold:
		return "已从磁盘载入原会话 " + sessionID + "，上下文完整"
	case executor.ResumeModeFresh:
		return "原会话已不可载入，已新开会话 " + sessionID + "，上下文从下一条指令开始"
	}
	return "已恢复"
}
