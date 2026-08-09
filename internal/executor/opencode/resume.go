// resume.go —— 执行恢复：热重连与冷恢复。
//
// 职责：
//   - Resume：按 ResumeReq 走恢复阶梯，返回实际走到的级别
//
// 边界：
//   - 不判断「该不该恢复」的业务前提（如是否有未决权限工单）——那需要工单知识，
//     属 manager（见 manager.go 的 volatilePermitter）
//   - 不改任务状态：adapter 不写 store（见 executor 包级边界）
package opencode

import (
	"fmt"
	"path/filepath"

	"github.com/xushixin/handoff/internal/executor"
)

// Resume 重建 agentd 重启前已在执行的任务（spec §8「存活则重连 SSE 继续」）：
// 从任务目录的 serve.json 恢复 serve 连接凭据并探活（tmux 会话存在 + HTTP 应答）；
// 存活则重建 SSE 订阅、看门狗与事件通道，返回 Reattach。
//
// 参数：
//   - req: 恢复请求（TaskDir 是 serve.json 所在，即 DataDir/tasks/<id>；
//     RepoPath 用于重启后重新捕获 git 兜底分类的起点 commit 基线；
//     SessionID 是落库的 opencode 会话 id）
//
// 返回：
//   - Alive=false 时调用方（manager）把任务转 failed 交审核者裁决（保守优于静默）
//   - err: 重建失败（serve.json 缺失/损坏、SessionID 为空），此时视为不可恢复
//
// 注意：
//   - SessionID 为空时拒绝恢复：mapEvent 按会话 id 过滤事件，空 id 会把全部
//     事件当「其他会话」丢弃，静默恢复等于无声断流，宁可交审核者裁决
//   - 重启时正在进行的回合文本累积在内存里已丢失：重建后的回合从 SSE 重放的新
//     快照重新累积（partSeen/partSnap 重新对账），idle 分类的 git 基线以重启
//     时刻的 HEAD 为准——这是 MVP 接受的缝隙，由 e2e 清单「agentd 重启」项
//     实测观察
//   - 与 Start 的对称性：Stop（done 归档）对恢复出来的运行态同样有效，
//     会 kill 掉 tmux 会话回收资源
func (a *Adapter) Resume(req executor.ResumeReq) (out executor.ResumeOutcome, err error) {
	a.log.Info("adapter 恢复任务执行", "task", req.TaskID, "session", req.SessionID)
	defer func() {
		if err != nil {
			a.log.Error("adapter 恢复任务失败", "task", req.TaskID, "cause", err)
		} else if out.Alive {
			a.log.Info("adapter 任务已恢复", "task", req.TaskID, "session", req.SessionID)
		}
	}()

	if req.SessionID == "" {
		return executor.ResumeOutcome{}, fmt.Errorf("任务 %s 缺 executor_session，无法重建订阅", req.TaskID)
	}
	si, err := readServeInfo(req.TaskDir)
	if err != nil {
		return executor.ResumeOutcome{}, err
	}
	// ServeLogPath 从 taskDir 推导（serve.json 不持久化它）：serve 死亡诊断的
	// serve.log 尾部读取需要路径，重启恢复的任务同样要能用
	proc := &Proc{Port: si.Port, Password: si.Password, TmuxSession: si.TmuxSession,
		ServeLogPath: filepath.Join(req.TaskDir, serveLogFileName)}
	if !proc.Alive() {
		a.log.Info("恢复探活失败：执行器已不在", "task", req.TaskID, "tmux", proc.TmuxSession)
		// 回收残留会话：Alive() 为假只说明 serve 进程没了，tmux 会话本身可能
		// 还被第二窗口的 tail -f render.log 吊着。不回收，每个这类任务都会永久
		// 遗留一个 tmux 会话 + 一个 tail 进程，而后续再无任何路径会碰它们
		// （本任务的运行态没建起来，Stop 无从调用）。Kill 幂等，会话早已消失
		// 时它返回错误也无妨——证据都在磁盘上的 serve.log/render.log 里
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("回收已死执行器的 tmux 会话失败，可能需人工清理",
				"task", req.TaskID, "tmux", proc.TmuxSession, "cause", kerr)
		}
		return executor.ResumeOutcome{}, nil
	}
	r := a.newRun(req.TaskID, req.TaskDir, req.RepoPath)
	r.session = req.SessionID
	r.api = NewAPI(fmt.Sprintf("http://127.0.0.1:%d", proc.Port), proc.Password)
	r.handle = procHandle{p: proc}
	r.captureStartCommit(a)
	go r.subscribeLoop(a)
	go a.watchdog(r)
	return executor.ResumeOutcome{
		Alive: true, Mode: executor.ResumeModeReattach, SessionID: req.SessionID,
		Note: "executor 仍存活，已重连事件流",
	}, nil
}
