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
	"context"
	"fmt"
	"os"
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
			a.log.Info("adapter 任务已恢复", "task", req.TaskID, "session", out.SessionID)
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

	mode := executor.ResumeModeReattach
	if !proc.Alive() {
		// 回收残留会话：Alive() 为假只说明 serve 进程没了，tmux 会话本身可能
		// 还被第二窗口的 tail -f render.log 吊着。冷恢复要新建同名会话，
		// 不先回收会直接撞名（这条在原实现里就有，冷恢复路径更需要它）
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("回收已死执行器的 tmux 会话失败，可能需人工清理",
				"task", req.TaskID, "tmux", proc.TmuxSession, "cause", kerr)
		}
		if !req.Cold {
			a.log.Info("serve 已不在且不允许冷恢复，判不可恢复",
				"task", req.TaskID, "tmux", proc.TmuxSession)
			return executor.ResumeOutcome{Alive: false,
				Note: "serve 进程已不在（本次只允许热重连）"}, nil
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
		a.log.Info("serve 已不在，进入冷恢复", "task", req.TaskID, "session", req.SessionID)
		// 任务物料在 taskDir 里是持久的，路径确定性推导；重写一次保证内容与
		// 当前 model 一致（PlanContent 只在首轮 prompt 用得上，冷恢复不需要）
		configPath := filepath.Join(req.TaskDir, configFileName)
		newProc, err := startServe(context.Background(), req.RepoPath, req.TaskID,
			req.TaskDir, configPath, req.Env, a.log)
		if err != nil {
			a.log.Warn("冷恢复重起 serve 失败，判不可恢复", "task", req.TaskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("重起 opencode serve 失败：%v", err)}, nil
		}
		if werr := writeServeInfo(req.TaskDir, newProc); werr != nil {
			a.log.Warn("冷恢复写 serve.json 失败，下次重启恢复将不可用",
				"task", req.TaskID, "cause", werr)
		}
		proc = newProc
		mode = executor.ResumeModeCold
		a.log.Info("冷恢复新 serve 就绪", "task", req.TaskID, "port", proc.Port)
	}
	r := a.newRun(req.TaskID, req.TaskDir, req.RepoPath)
	sessionID := req.SessionID
	r.session = sessionID
	r.api = NewAPI(fmt.Sprintf("http://127.0.0.1:%d", proc.Port), proc.Password)
	r.handle = procHandle{p: proc}

	if mode == executor.ResumeModeCold {
		// 会话在全局 sqlite 里，进程重起不影响它——但要确认它真的还在，
		// 不能默认。不在就降级新会话并如实播报（上下文断了是审核者需要知道的）
		has, err := r.api.HasSession(context.Background(), sessionID)
		if err != nil {
			a.log.Warn("查询会话列表失败，保守降级新会话", "task", req.TaskID, "cause", err)
			has = false
		}
		if !has {
			newID, nerr := r.api.CreateSession(context.Background())
			if nerr != nil {
				a.log.Warn("降级新建会话失败，判不可恢复", "task", req.TaskID, "cause", nerr)
				return executor.ResumeOutcome{Alive: false,
					Note: fmt.Sprintf("原会话已不在且新建失败：%v", nerr)}, nil
			}
			a.log.Warn("原会话已不在，已新开会话", "task", req.TaskID,
				"old", sessionID, "new", newID)
			sessionID, mode = newID, executor.ResumeModeFresh
		}
		r.session = sessionID
	}
	r.captureStartCommit(a)
	go r.subscribeLoop(a)
	go a.watchdog(r)
	return executor.ResumeOutcome{
		Alive: true, Mode: mode, SessionID: sessionID,
		Note: resumeNote(mode, sessionID),
	}, nil
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
