// resume.go —— agentd 重启后的执行恢复与 serve 存活看门狗。
//
// 职责：
//   - Resume：读 serve.json → 端口探活 → 修复 auth 软链 → 重连 ACP →
//     session/load → 重建事件循环
//   - watchdog：周期探活，判死时以脱敏的 serve.log 尾部产出 failed result
//
// 边界：
//   - 不判断「该不该恢复」的业务前提（如是否有未决权限工单）——那需要工单知识，
//     属 manager（见 manager.go 的 volatilePermitter）
//
// 看门狗节奏与 opencode 同规格：活跃期 200ms 高频，连续 fastProbes 次成功且无
// 新事件后降到 2s（任务挂夜里时省下每天数十万次探活），任一失败立即回高频；
// 连续 failThreshold 次失败才判死，吸收抖动不误杀。
package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

const (
	watchdogFastInterval  = 200 * time.Millisecond
	watchdogSlowInterval  = 2 * time.Second
	watchdogFastProbes    = 10
	watchdogFailThreshold = 3

	// authSyncInterval 是凭据巡检的节流间隔。lstat 是微秒级，节流不是为了性能，
	// 是为了别让日志和写盘跟着 200ms 的探活节拍变吵。30 秒也是权威副本可能
	// 陈旧的时间上界。
	authSyncInterval = 30 * time.Second
)

// Resume 尝试恢复一个 agentd 重启前已在执行的任务。
//
// 参数：
//   - req: 恢复请求（TaskDir 是 serve.json 与 grokhome 所在，即 DataDir/tasks/<id>；
//     RepoPath 是 ACP session/load 的 cwd；SessionID 是落库的会话 id）
//
// 返回：
//   - Alive=true：serve 存活、ACP 已重连、会话已载入、事件流已重建；
//     Mode 说明走到的级别（reattach 热重连 / cold 冷恢复 / fresh 新会话）
//   - Alive=false：serve 已不在且不允许冷恢复、或凭据缺失，调用方据此转
//     failed 交审核者
func (a *Adapter) Resume(req executor.ResumeReq) (out executor.ResumeOutcome, err error) {
	taskID, taskDir, repoPath, sessionID := req.TaskID, req.TaskDir, req.RepoPath, req.SessionID
	a.log.Info("grok 尝试恢复任务", "task", taskID, "task_dir", taskDir, "session", sessionID)

	// 没有会话 id 就没法 session/load——恢复的前提不成立，这不是错误
	if sessionID == "" {
		a.log.Info("无 executor 会话 id，判不可恢复", "task", taskID)
		return executor.ResumeOutcome{}, nil
	}
	proc, err := ReadServeInfo(taskDir)
	if err != nil {
		a.log.Info("恢复凭据缺失，判不可恢复", "task", taskID, "cause", err)
		return executor.ResumeOutcome{}, nil
	}

	// 冷恢复互斥（spec §6）：先在 runs 上占位再拉进程，后到者直接返回
	// 「恢复进行中」。两个 serve 抢同一个会话是数据损坏级别的后果
	a.mu.Lock()
	if _, busy := a.runs[taskID]; busy {
		a.mu.Unlock()
		a.log.Info("该任务已有运行态或恢复进行中，跳过本次恢复", "task", taskID)
		return executor.ResumeOutcome{Alive: false, Note: "该任务的恢复正在进行中"}, nil
	}
	a.runs[taskID] = &runState{taskID: taskID} // 占位
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
		if !req.Cold {
			a.log.Info("serve 已不在且不允许冷恢复，判不可恢复",
				"task", taskID, "port", proc.Port)
			return executor.ResumeOutcome{Alive: false,
				Note: "serve 进程已不在（本次只允许热重连）"}, nil
		}
		// 冷恢复：会话数据在 <taskDir>/grokhome/sessions/<urlencode(cwd)>/<session-id>/，
		// 只要 taskDir 在它就在。重起一个 serve（新端口，GROK_HOME 不变）后，
		// 下面的 session/load 原样可用——这是三个 adapter 里改动最小的一个
		//
		// §6 约束 5（冷恢复不重建 worktree）：任务工作区可能已随归档清理，
		// 重建是 Dispatch 的职责，越界重建会让归档过的任务诈尸
		if _, serr := os.Stat(req.TaskDir); serr != nil {
			a.log.Info("任务目录已不存在，判不可恢复", "task", taskID, "cause", serr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务目录已不存在（可能已归档清理），无法恢复"}, nil
		}
		if _, rerr := os.Stat(req.RepoPath); rerr != nil {
			a.log.Info("任务工作区已不存在，判不可恢复", "task", taskID, "cause", rerr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务工作区已不存在（可能已归档清理），无法恢复"}, nil
		}
		a.log.Info("serve 已不在，进入冷恢复", "task", taskID,
			"old_port", proc.Port, "session", sessionID)
		newProc, err := startServe(context.Background(), repoPath, taskID,
			taskDir, req.Model, req.Env, a.log)
		if err != nil {
			// 起不来是可预期现场（配额/凭据过期），按不可恢复处理而非错误
			a.log.Warn("冷恢复重起 serve 失败，判不可恢复", "task", taskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("重起 grok serve 失败：%v", err)}, nil
		}
		proc = newProc
		mode = executor.ResumeModeCold
		a.log.Info("冷恢复新 serve 就绪", "task", taskID, "new_port", proc.Port)
	}
	// token 刷新期间软链可能已被干掉（spec §3.3 实测），重连前先修好
	// （冷恢复路径同样要调：token 刷新期间软链可能已被干掉，见 B26）
	if err := EnsureAuthLink(filepath.Join(taskDir, homeDirName)); err != nil {
		a.log.Warn("修复 auth 软链失败，仍尝试重连", "task", taskID, "cause", err)
	}

	r := &runState{
		taskID: taskID, taskDir: taskDir, repoPath: repoPath, sessionID: sessionID,
		proc: proc, evCh: make(chan executor.AdapterEvent, 64),
		acc: newTurnAccumulator(), pending: map[string]pendingPerm{},
	}
	if _, c, _, gerr := turn.GitTurnStatus(repoPath, ""); gerr == nil {
		r.startCommit = c
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cli, err := DialACP(ctx, proc.WSURL(), &acpHandler{a: a, r: r}, a.log)
	if err != nil {
		a.log.Warn("ACP 重连失败，判不可恢复", "task", taskID, "cause", err)
		return executor.ResumeOutcome{}, nil
	}
	r.cli = cli
	if _, err := cli.Call(ctx, "initialize", map[string]any{
		"protocolVersion": 1,
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": true, "writeTextFile": true},
			"terminal": false,
		},
	}); err != nil {
		_ = cli.Close()
		a.log.Warn("重连后 initialize 失败，判不可恢复", "task", taskID, "cause", err)
		return executor.ResumeOutcome{}, nil
	}
	if _, err := cli.Call(ctx, "session/load", map[string]any{
		"sessionId": sessionID, "cwd": repoPath, "mcpServers": []any{},
	}); err != nil {
		if !req.Cold {
			_ = cli.Close()
			a.log.Warn("session/load 失败，判不可恢复", "task", taskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("载入原会话失败：%v", err)}, nil
		}
		// 第 4 级：原会话载不进，新开一个。上下文断了，manager 会据 Mode=fresh
		// 播报给审核者——这一条必须让人知道，它决定下一条指令要不要重述背景
		a.log.Warn("session/load 失败，降级新开会话", "task", taskID, "cause", err)
		newID, nerr := a.newSessionOnConn(ctx, cli, repoPath)
		if nerr != nil {
			_ = cli.Close()
			a.log.Warn("降级新开会话也失败，判不可恢复", "task", taskID, "cause", nerr)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("原会话载不进且新建会话失败：%v", nerr)}, nil
		}
		sessionID, mode = newID, executor.ResumeModeFresh
	}
	r.sessionID = sessionID

	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	go a.watchdog(r)

	a.log.Info("grok 任务已恢复", "task", taskID, "session", sessionID, "port", proc.Port)
	return executor.ResumeOutcome{Alive: true, Mode: mode, SessionID: sessionID,
		Note: resumeNote(mode, sessionID)}, nil
}

// newSessionOnConn 在既有 ACP 连接上开新会话（冷恢复降级第 4 级用）。
//
// 从 openSession 抽出「已连上之后 session/new」那一段复用，不复制一份——
// openSession 负责 initialize+session/new 全流程，这里只需 session/new。
func (a *Adapter) newSessionOnConn(ctx context.Context, cli *ACPClient, cwd string) (string, error) {
	newRes, err := cli.Call(ctx, "session/new", map[string]any{
		"cwd": cwd, "mcpServers": []any{},
	})
	if err != nil {
		if strings.Contains(err.Error(), "Authentication required") {
			return "", fmt.Errorf("grok 未登录或凭据已失效，请在本机执行 `grok login` 后重试: %w", err)
		}
		return "", fmt.Errorf("ACP session/new: %w", err)
	}
	var sess struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(newRes, &sess); err != nil || sess.SessionID == "" {
		return "", fmt.Errorf("ACP session/new 未返回 sessionId: %s", newRes)
	}
	a.log.Info("grok ACP 新会话已建立", "cwd", cwd, "session", sess.SessionID)
	return sess.SessionID, nil
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

// watchdog 周期探活 serve，连续 watchdogFailThreshold 次失败即判死。
//
// 快慢双档：有新事件时 200ms 高频；连续 watchdogFastProbes 次成功且无新事件
// （任务挂夜里等审核）后降到 2s——高频探活每天每任务数十万次 HTTP 请求，
// 降频后一个量级；任一失败立即回高频，保证判死不被降频拖太慢。
//
// 注意：本函数**不重发 session/prompt**——恢复的是「正在跑的回合」的观察通道，
// 不是重开一轮。若恢复时该回合早已结束（事件已错过），任务会停在 waiting_review
// 由审核者处置——这比擅自重发一轮安全。
func (a *Adapter) watchdog(r *runState) {
	// 退场前一律补最后一次巡检：看门狗有两个出口（evClosed 正常退场、探活判死
	// 退场），用 defer 才能结构性地同时覆盖，而不是在两个 return 前各抄一遍——
	// 抄漏一个就等于漏掉"任务跑挂了但刚刷新过"这一整类。
	defer a.syncAuthOnce(r)

	interval, okStreak, failStreak := watchdogFastInterval, 0, 0
	for {
		time.Sleep(interval)
		r.emitMu.Lock()
		closed := r.evClosed
		r.emitMu.Unlock()
		if closed {
			return // 任务已终结，看门狗退场
		}
		a.syncAuthThrottled(r)
		if r.proc.Alive() {
			failStreak = 0
			okStreak++
			if okStreak >= watchdogFastProbes {
				interval = watchdogSlowInterval
			}
			continue
		}
		okStreak, interval = 0, watchdogFastInterval
		failStreak++
		if failStreak < watchdogFailThreshold {
			a.log.Warn("grok serve 探活失败", "task", r.taskID, "streak", failStreak)
			continue
		}
		a.log.Error("grok serve 判定死亡", "task", r.taskID, "port", r.proc.Port)
		a.emitFailed(r, "grok serve 进程已死亡；serve 日志尾部: "+r.proc.LogTail())
		return
	}
}

// syncAuthThrottled 按 authSyncInterval 节流地跑一轮凭据巡检。
//
// 注意：只被看门狗 goroutine 调用，因此读写 r.lastAuthSync 无需加锁。
func (a *Adapter) syncAuthThrottled(r *runState) {
	if time.Since(r.lastAuthSync) < authSyncInterval {
		return
	}
	a.syncAuthOnce(r)
}

// syncAuthOnce 无条件跑一轮凭据巡检（退场路径与节流路径共用）。
//
// 错误已在 SyncAuthToAuthority 内部记过日志，这里只做兜底记录：巡检失败不该
// 影响看门狗判死这件正事。
func (a *Adapter) syncAuthOnce(r *runState) {
	r.lastAuthSync = time.Now()
	if r.taskDir == "" {
		return // 没有任务目录就没有任务级 home，无从巡检
	}
	if err := SyncAuthToAuthority(filepath.Join(r.taskDir, homeDirName),
		a.log.With("task", r.taskID)); err != nil {
		a.log.Warn("grok 凭据巡检未完成，下轮重试", "task", r.taskID, "cause", err)
	}
}
