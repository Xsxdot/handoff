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
	"path/filepath"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

const (
	watchdogFastInterval  = 200 * time.Millisecond
	watchdogSlowInterval  = 2 * time.Second
	watchdogFastProbes    = 10
	watchdogFailThreshold = 3
)

// Resume 尝试恢复一个 agentd 重启前已在执行的任务。
//
// 参数：
//   - taskID/taskDir/repoPath: 任务标识与目录
//   - sessionID: 落库的 executor 会话 id（空则无法 session/load，判不可恢复）
//
// 返回：
//   - true：serve 存活、ACP 已重连、会话已载入、事件流已重建
//   - false：serve 已不在或凭据缺失，调用方据此转 failed 交审核者
func (a *Adapter) Resume(taskID, taskDir, repoPath, sessionID string) (bool, error) {
	a.log.Info("grok 尝试恢复任务", "task", taskID, "task_dir", taskDir, "session", sessionID)

	// 没有会话 id 就没法 session/load——恢复的前提不成立，这不是错误
	if sessionID == "" {
		a.log.Info("无 executor 会话 id，判不可恢复", "task", taskID)
		return false, nil
	}
	proc, err := ReadServeInfo(taskDir)
	if err != nil {
		a.log.Info("恢复凭据缺失，判不可恢复", "task", taskID, "cause", err)
		return false, nil
	}
	if !proc.Alive() {
		a.log.Info("serve 已不在，判不可恢复", "task", taskID, "port", proc.Port)
		return false, nil
	}
	// token 刷新期间软链可能已被干掉（spec §3.3 实测），重连前先修好
	if err := EnsureAuthLink(filepath.Join(taskDir, homeDirName)); err != nil {
		a.log.Warn("修复 auth 软链失败，仍尝试重连", "task", taskID, "cause", err)
	}

	r := &runState{
		taskID: taskID, taskDir: taskDir, repoPath: repoPath, sessionID: sessionID,
		proc: proc, evCh: make(chan executor.AdapterEvent, 64),
		acc: newTurnAccumulator(), pending: map[string]json.RawMessage{},
	}
	if _, c, _, gerr := turn.GitTurnStatus(repoPath, ""); gerr == nil {
		r.startCommit = c
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cli, err := DialACP(ctx, proc.WSURL(), &acpHandler{a: a, r: r}, a.log)
	if err != nil {
		a.log.Warn("ACP 重连失败，判不可恢复", "task", taskID, "cause", err)
		return false, nil
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
		return false, nil
	}
	if _, err := cli.Call(ctx, "session/load", map[string]any{
		"sessionId": sessionID, "cwd": repoPath, "mcpServers": []any{},
	}); err != nil {
		_ = cli.Close()
		a.log.Warn("session/load 失败，判不可恢复", "task", taskID, "cause", err)
		return false, nil
	}

	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	go a.watchdog(r)

	a.log.Info("grok 任务已恢复", "task", taskID, "session", sessionID, "port", proc.Port)
	return true, nil
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
	interval, okStreak, failStreak := watchdogFastInterval, 0, 0
	for {
		time.Sleep(interval)
		r.emitMu.Lock()
		closed := r.evClosed
		r.emitMu.Unlock()
		if closed {
			return // 任务已终结，看门狗退场
		}
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
