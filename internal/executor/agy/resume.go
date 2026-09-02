// resume.go —— 执行恢复：热重连与冷恢复。
package agy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor"
)

// writeTaskEnvFn 是冷恢复准备任务环境的测试缝，用于覆盖写入后失败的回滚路径。
var writeTaskEnvFn = WriteTaskEnv

// Resume 恢复 agentd 重启前已在执行的任务。
func (a *Adapter) Resume(req executor.ResumeReq) (out executor.ResumeOutcome, err error) {
	a.log.Info("agy adapter 恢复任务执行", "task", req.TaskID, "session", req.SessionID)
	defer func() {
		if err != nil {
			a.log.Error("agy adapter 恢复任务失败", "task", req.TaskID, "cause", err)
		} else if out.Alive {
			a.log.Info("agy adapter 任务已恢复", "task", req.TaskID, "session", out.SessionID)
		}
	}()

	if req.SessionID == "" {
		return executor.ResumeOutcome{}, fmt.Errorf("任务 %s 缺 executor_session，无法重建订阅", req.TaskID)
	}
	a.mu.Lock()
	if _, busy := a.runs[req.TaskID]; busy {
		a.mu.Unlock()
		a.log.Info("该任务已有运行态或恢复进行中，跳过本次恢复", "task", req.TaskID)
		return executor.ResumeOutcome{Alive: false, Note: "该任务的恢复正在进行中"}, nil
	}
	a.runs[req.TaskID] = &runState{taskID: req.TaskID}
	a.mu.Unlock()
	defer func() {
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
	proc := &Proc{Handle: pi.Handle, TaskDir: req.TaskDir, SessionID: req.SessionID}

	r := a.newRun(req.TaskID, req.TaskDir, req.RepoPath)
	r.session = req.SessionID

	// 恢复时先起权限服务端监听（与 Start 流程对齐）：确保 agy 进程就绪发出 PreToolUse 时 socket 已经在受理
	sockPath := filepath.Join(req.TaskDir, "perm.sock")
	ps, perr := newPermServerFn(sockPath, a.log, func(ask permAsk) {
		a.onPermissionAsk(r, ask)
	})
	if perr != nil {
		a.drop(req.TaskID)
		a.log.Error("恢复权限服务端失败，判不可恢复", "task", req.TaskID, "cause", perr)
		return executor.ResumeOutcome{Alive: false,
			Note: fmt.Sprintf("启动权限服务端失败：%v", perr)}, nil
	}
	r.permSrv = ps

	mode := executor.ResumeModeReattach
	dead := !proc.Alive()
	if dead {
		if exited, code := procExited(filepath.Join(req.TaskDir, outFileName)); exited {
			a.log.Info("恢复探活失败：agy 已退出（哨兵）", "task", req.TaskID,
				"shim_pid", pi.Handle.PID, "code", code)
		} else {
			a.log.Info("恢复探活失败：存活锁已释放", "task", req.TaskID, "shim_pid", pi.Handle.PID)
		}
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("回收已死执行者失败", "task", req.TaskID, "cause", kerr)
		}
		if !req.Cold {
			_ = ps.Close()
			a.drop(req.TaskID)
			return executor.ResumeOutcome{Alive: false,
				Note: "agy 进程已不在（本次只允许热重连）"}, nil
		}
		if _, serr := os.Stat(req.TaskDir); serr != nil {
			_ = ps.Close()
			a.drop(req.TaskID)
			a.log.Info("任务目录已不存在，判不可恢复", "task", req.TaskID, "cause", serr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务目录已不存在，无法恢复"}, nil
		}
		if _, rerr := os.Stat(req.RepoPath); rerr != nil {
			_ = ps.Close()
			a.drop(req.TaskID)
			a.log.Info("任务工作区已不存在，判不可恢复", "task", req.TaskID, "cause", rerr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务工作区已不存在，无法恢复"}, nil
		}
		if rerr := rotateOutJSONL(req.TaskDir); rerr != nil {
			a.log.Warn("轮转 out.jsonl 失败，仍尝试冷恢复", "task", req.TaskID, "cause", rerr)
		}

		tmpDir, managedEnv := managedTaskTmpEnv(req.TaskDir, req.TaskID)
		if terr := ensureTaskTmp(req.TaskID, tmpDir, a.log); terr != nil {
			_ = ps.Close()
			a.drop(req.TaskID)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("创建临时目录失败：%v", terr)}, nil
		}
		env := append(append([]string{}, req.Env...), managedEnv...)

		selfExe, _ := os.Executable()
		if selfExe == "" {
			selfExe = "handoff"
		}
		if _, _, werr := writeTaskEnvFn(req.RepoPath, req.TaskDir, req.TaskID, "", sockPath, selfExe, ""); werr != nil {
			_ = ps.Close()
			restoreErr := RestoreTaskEnv(req.TaskDir)
			a.drop(req.TaskID)
			a.log.Error("冷恢复更新 hooks.json 失败", "task", req.TaskID, "cause", werr)
			if restoreErr != nil {
				a.log.Error("冷恢复更新 hooks.json 失败后还原也失败", "task", req.TaskID, "cause", restoreErr)
			}
			note := fmt.Sprintf("准备任务环境失败：%v", werr)
			if restoreErr != nil {
				note += fmt.Sprintf("；还原 hooks 失败：%v", restoreErr)
			}
			return executor.ResumeOutcome{Alive: false,
				Note: note}, nil
		}

		a.log.Info("agy 已不在，进入冷恢复", "task", req.TaskID, "session", req.SessionID)
		newProc, err := startProc(context.Background(), StartProcReq{
			RepoPath: req.RepoPath, TaskID: req.TaskID, TaskDir: req.TaskDir,
			SessionID: req.SessionID, Model: req.Model,
			Env: env, MarkRoot: req.MarkRoot, Resume: true,
		}, a.log)
		if err != nil {
			_ = ps.Close()
			restoreErr := RestoreTaskEnv(req.TaskDir)
			a.drop(req.TaskID)
			a.log.Warn("冷恢复重起 agy 失败，判不可恢复", "task", req.TaskID, "cause", err)
			if restoreErr != nil {
				a.log.Error("冷恢复重起 agy 失败后还原 hooks 也失败", "task", req.TaskID, "cause", restoreErr)
			}
			note := fmt.Sprintf("重起 agy 失败：%v", err)
			if restoreErr != nil {
				note += fmt.Sprintf("；还原 hooks 失败：%v", restoreErr)
			}
			return executor.ResumeOutcome{Alive: false,
				Note: note}, nil
		}
		proc = newProc
		mode = executor.ResumeModeCold
	}

	r.proc = proc

	if mode == executor.ResumeModeCold {
		r.startOffset = 0
	} else {
		r.startOffset = pi.Offset
	}
	r.captureStartCommit(a)
	go r.streamLoop(a)
	go a.watchdog(r)

	note := "已重新连接到运行中的 agy 进程"
	if mode == executor.ResumeModeCold {
		note = "agy 进程已在原会话上冷启动恢复"
	}
	return executor.ResumeOutcome{
		Alive:     true,
		Mode:      mode,
		SessionID: req.SessionID,
		Note:      note,
	}, nil
}
