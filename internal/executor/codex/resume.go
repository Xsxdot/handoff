// resume.go —— agentd 重启后的运行态恢复与连接看门狗。
//
// 职责：
//   - Resume：按四级阶梯尝试恢复（不可恢复 / reattach / cold / fresh）
//   - watchdog：探活 app-server，判死后走统一的失败处置
//
// 边界：
//   - 不重建 worktree：任务工作区可能已随归档清理，重建是 Dispatch 的职责，
//     越界重建会让归档过的任务诈尸
//   - 不改任务状态：Resume 只如实返回结论，状态迁移归 manager
//
// codex 的两个结构性优势（相对 grok）：
//  1. rollout 落在用户级 ~/.codex/sessions/**，agentd 重启、甚至 app-server
//     进程重启后 thread 都还在盘上，冷恢复不依赖任务目录里的会话数据
//  2. 没有凭据软链要修（复用用户级 home，凭据零副本），冷恢复路径短一截
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

const (
	watchdogFastInterval  = 200 * time.Millisecond
	watchdogSlowInterval  = 2 * time.Second
	watchdogFastProbes    = 10
	watchdogFailThreshold = 3
	resumeDialTimeout     = 20 * time.Second
)

// Resume 尝试恢复一个 agentd 重启前已在执行的任务。
//
// 参数：
//   - req: 恢复请求（TaskDir 是 serve.json 所在；RepoPath 是 thread/resume 的 cwd；
//     SessionID 是落库的 threadId；Cold 决定是否允许重起进程）
//
// 返回：
//   - Alive=true：进程存活或已重起、WS 已重连、thread 已载入、事件流已重建
//   - Alive=false：判不可恢复，调用方据此转 failed 交审核者。**这不是错误**，
//     err 恒为 nil 的路径很多，调用方不要靠 err 判别
func (a *Adapter) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error) {
	taskID, taskDir, repoPath, threadID := req.TaskID, req.TaskDir, req.RepoPath, req.SessionID
	a.log.Info("codex 尝试恢复任务", "task", taskID, "task_dir", taskDir, "thread", threadID)

	if threadID == "" {
		a.log.Info("无 executor 会话 id，判不可恢复", "task", taskID)
		return executor.ResumeOutcome{}, nil
	}
	proc, err := ReadServeInfo(taskDir)
	if err != nil {
		a.log.Info("恢复凭据缺失，判不可恢复", "task", taskID, "cause", err)
		return executor.ResumeOutcome{}, nil
	}

	// 冷恢复互斥：先在 runs 上占位再拉进程，后到者直接返回「恢复进行中」。
	// 两个 app-server 抢同一个 thread 是数据损坏级别的后果。
	a.mu.Lock()
	if _, busy := a.runs[taskID]; busy {
		a.mu.Unlock()
		a.log.Info("该任务已有运行态或恢复进行中，跳过本次恢复", "task", taskID)
		return executor.ResumeOutcome{Alive: false, Note: "该任务的恢复正在进行中"}, nil
	}
	a.runs[taskID] = &runState{taskID: taskID} // 占位：evCh 为 nil 即占位标志
	a.mu.Unlock()
	defer func() {
		// 失败路径清掉占位，否则这个任务永远恢复不了
		a.mu.Lock()
		if cur, ok := a.runs[taskID]; ok && cur.evCh == nil {
			delete(a.runs, taskID)
		}
		a.mu.Unlock()
	}()

	mode := executor.ResumeModeReattach
	if !proc.Alive() {
		// 先回收旧会话：tmux 会话由窗口 1 的 tail -f 吊着，app-server 死了会话仍在，
		// 而冷恢复用的是同一个确定性会话名 handoff-<id8>，不回收就会撞名
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("回收已死 app-server 的 tmux 会话失败", "task", taskID,
				"session", proc.Session, "cause", kerr)
		}
		if !req.Cold {
			a.log.Info("app-server 已不在且不允许冷恢复，判不可恢复", "task", taskID, "port", proc.Port)
			return executor.ResumeOutcome{Alive: false,
				Note: "codex app-server 进程已不在（本次只允许热重连）"}, nil
		}
		if _, serr := os.Stat(taskDir); serr != nil {
			a.log.Info("任务目录已不存在，判不可恢复", "task", taskID, "cause", serr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务目录已不存在（可能已归档清理），无法恢复"}, nil
		}
		if _, rerr := os.Stat(repoPath); rerr != nil {
			a.log.Info("任务工作区已不存在，判不可恢复", "task", taskID, "cause", rerr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务工作区已不存在（可能已归档清理），无法恢复"}, nil
		}
		a.log.Info("app-server 已不在，进入冷恢复", "task", taskID,
			"old_port", proc.Port, "thread", threadID)
		newProc, serr := startServe(context.Background(), repoPath, taskID, taskDir, req.Env, a.log)
		if serr != nil {
			// 起不来是可预期现场（未登录/端口占用），按不可恢复处理而非错误
			a.log.Warn("冷恢复重起 app-server 失败，判不可恢复", "task", taskID, "cause", serr)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("重起 codex app-server 失败：%v", serr)}, nil
		}
		proc = newProc
		mode = executor.ResumeModeCold
		a.log.Info("冷恢复新 app-server 就绪", "task", taskID, "new_port", proc.Port)
	}

	r := newRunState(taskID, taskDir, repoPath)
	r.proc = proc
	r.threadID = threadID
	if _, c, _, gerr := turn.GitTurnStatus(repoPath, ""); gerr == nil {
		r.startCommit = c
	}

	ctx, cancel := context.WithTimeout(context.Background(), resumeDialTimeout)
	defer cancel()
	cli, err := Dial(ctx, proc.WSURL(), &handler{a: a, r: r}, a.log)
	if err != nil {
		a.log.Warn("WS 重连失败，判不可恢复", "task", taskID, "cause", err)
		return executor.ResumeOutcome{Alive: false,
			Note: fmt.Sprintf("重连 codex app-server 失败：%v", err)}, nil
	}
	r.cli = cli
	if _, err := cli.Call(ctx, methodInitialize, map[string]any{
		"clientInfo":   map[string]any{"name": "handoff", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		_ = cli.Close()
		a.log.Warn("重连后 initialize 失败，判不可恢复", "task", taskID, "cause", err)
		return executor.ResumeOutcome{Alive: false,
			Note: fmt.Sprintf("重连后握手失败：%v", err)}, nil
	}
	if err := cli.Notify(methodInitialized, nil); err != nil {
		a.log.Warn("重连后 initialized 通知失败，继续尝试载入 thread", "task", taskID, "cause", err)
	}

	// thread/resume 必须把三个安全参数一起重传（spec §5.6）：恢复路径是最容易让
	// 安全档位悄悄退回开发机 config 的地方。恢复后的第一个 turn/start 会再钉一遍，
	// 两层都钉是刻意的。
	if _, err := cli.Call(ctx, methodThreadResume, map[string]any{
		"threadId":          threadID,
		"cwd":               repoPath,
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
	}); err != nil {
		if !req.Cold {
			_ = cli.Close()
			a.log.Warn("thread/resume 失败，判不可恢复", "task", taskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("载入原 thread 失败：%v", err)}, nil
		}
		// 第 4 级：原 thread 载不进，新开一个。上下文断了，manager 会据 Mode=fresh
		// 播报给审核者——这一条必须让人知道，它决定下一条指令要不要重述背景
		a.log.Warn("thread/resume 失败，降级新开会话", "task", taskID, "cause", err)
		if nerr := a.openThreadOnConn(ctx, r, repoPath, req.Model); nerr != nil {
			_ = cli.Close()
			a.log.Warn("降级新开会话也失败，判不可恢复", "task", taskID, "cause", nerr)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("原 thread 载不进且新建会话失败：%v", nerr)}, nil
		}
		threadID, mode = r.threadID, executor.ResumeModeFresh
	}
	r.threadID = threadID

	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	go a.watchdog(r)

	a.log.Info("codex 任务已恢复", "task", taskID, "thread", threadID,
		"mode", mode, "port", proc.Port)
	return executor.ResumeOutcome{Alive: true, Mode: mode, SessionID: threadID,
		Note: resumeNote(mode, threadID)}, nil
}

// openThreadOnConn 在既有连接上新开一个 thread（冷恢复降级第 4 级用）。
//
// 从 openThread 拆出「已握手之后 thread/start」那一段复用，不复制一份。
func (a *Adapter) openThreadOnConn(ctx context.Context, r *runState, cwd, model string) error {
	params := map[string]any{
		"cwd":               cwd,
		"sandbox":           "workspace-write",
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
	}
	if model != "" {
		params["model"] = model
	}
	res, err := r.cli.Call(ctx, methodThreadStart, params)
	if err != nil {
		return fmt.Errorf("codex thread/start: %w", err)
	}
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(res, &out); err != nil || out.Thread.ID == "" {
		return fmt.Errorf("codex thread/start 未返回 threadId: %s", res)
	}
	r.threadID = out.Thread.ID
	a.log.Info("codex 新 thread 已建立", "cwd", cwd, "thread", r.threadID)
	return nil
}

// resumeNote 拼恢复结果的一句话结论（进 ResumeOutcome.Note 供 manager 转播）。
func resumeNote(mode, threadID string) string {
	switch mode {
	case executor.ResumeModeReattach:
		return "codex app-server 仍存活，已重连事件流"
	case executor.ResumeModeCold:
		return "codex app-server 已重起并载入原会话（thread " + threadID + "），上下文完整"
	case executor.ResumeModeFresh:
		return "原会话载不进，已新开会话（thread " + threadID + "）；" +
			"上下文从下一条指令开始，回复时请重述必要背景"
	}
	return ""
}

// watchdog 探活 app-server，连续判死到阈值后走统一的失败处置。
//
// 为什么先快后慢：启动初期最容易死（未登录、端口占用），快探能让失败在几秒内
// 暴露；进入稳定期后降到 2s，避免为一个长时间跑的任务持续制造探活流量。
//
// 为什么要连续 watchdogFailThreshold 次才判死：Alive 是 TCP 连通判据（proc.go
// 里说明的弱判据），单次失败可能只是瞬时的连接抖动。
func (a *Adapter) watchdog(r *runState) {
	fails := 0
	for i := 0; ; i++ {
		interval := watchdogSlowInterval
		if i < watchdogFastProbes {
			interval = watchdogFastInterval
		}
		time.Sleep(interval)

		r.emitMu.Lock()
		stopping := r.stopping
		closed := r.evClosed
		r.emitMu.Unlock()
		if stopping || closed {
			a.log.Debug("看门狗退出（任务已停止或事件流已终结）", "task", r.taskID)
			return
		}
		if r.proc == nil {
			return
		}
		if r.proc.Alive() {
			fails = 0
			continue
		}
		fails++
		a.log.Warn("codex app-server 探活失败", "task", r.taskID,
			"port", r.proc.Port, "fails", fails)
		if fails < watchdogFailThreshold {
			continue
		}
		a.log.Error("codex app-server 已判死", "task", r.taskID, "port", r.proc.Port)
		a.dropIf(r.taskID, r)
		a.emitFailed(r, "codex app-server 进程已退出；serve 日志尾部: "+r.proc.LogTail())
		return
	}
}
