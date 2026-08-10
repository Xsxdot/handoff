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
// 从任务目录的 proc.json 恢复 serve 连接凭据并探活（存活锁 + HTTP 应答）；
// 存活则重建 SSE 订阅、看门狗与事件通道，返回 Reattach。
//
// 参数：
//   - req: 恢复请求（TaskDir 是 proc.json 所在，即 DataDir/tasks/<id>；
//     RepoPath 用于重启后重新捕获 git 兜底分类的起点 commit 基线；
//     SessionID 是落库的 opencode 会话 id）
//
// 返回：
//   - Alive=false 时调用方（manager）把任务转 failed 交审核者裁决（保守优于静默）
//   - err: 重建失败（proc.json 缺失/损坏、SessionID 为空），此时视为不可恢复
//
// 注意：
//   - SessionID 为空时拒绝恢复：mapEvent 按会话 id 过滤事件，空 id 会把全部
//     事件当「其他会话」丢弃，静默恢复等于无声断流，宁可交审核者裁决
//   - 重启时正在进行的回合文本累积在内存里已丢失：重建后的回合从 SSE 重放的新
//     快照重新累积（partSeen/partSnap 重新对账），idle 分类的 git 基线以重启
//     时刻的 HEAD 为准——这是 MVP 接受的缝隙，由 e2e 清单「agentd 重启」项
//     实测观察
//   - 与 Start 的对称性：Stop（done 归档）对恢复出来的运行态同样有效，
//     会按进程组 Kill 回收执行者资源
func (a *Adapter) Resume(req executor.ResumeReq) (out executor.ResumeOutcome, err error) {
	a.log.Info("adapter 恢复任务执行", "task", req.TaskID, "session", req.SessionID)
	defer func() {
		if err != nil {
			a.log.Error("adapter 恢复任务失败", "task", req.TaskID, "cause", err)
		} else if out.Alive {
			a.log.Info("adapter 任务已恢复", "task", req.TaskID, "session", out.SessionID)
		}
	}()

	// 冷恢复互斥（spec §6）：先在 runs 上占位再拉进程，后到者直接返回
	// 「恢复进行中」。两个 serve 抢同一个会话是数据损坏级别的后果
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

	if req.SessionID == "" {
		return executor.ResumeOutcome{}, fmt.Errorf("任务 %s 缺 executor_session，无法重建订阅", req.TaskID)
	}
	si, err := readProcInfo(req.TaskDir)
	if err != nil {
		return executor.ResumeOutcome{}, err
	}
	// ServeLogPath 从 taskDir 推导（proc.json 不持久化它）：serve 死亡诊断的
	// serve.log 尾部读取需要路径，重启恢复的任务同样要能用
	proc := &Proc{Handle: si.Handle, Port: si.Port, Password: si.Password,
		ServeLogPath: filepath.Join(req.TaskDir, serveLogFileName)}

	mode := executor.ResumeModeReattach
	if !proc.Alive() {
		// 回收残留进程：Alive() 为假只说明 serve 进程没了，shim 可能还在收尸途中。
		// 冷恢复要新建 shim，而 proc.lock 可能仍被旧的持有，不先回收会直接撞锁
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("回收已死执行者失败，可能需人工清理",
				"task", req.TaskID, "shim_pid", proc.Handle.PID, "cause", kerr)
		}
		if !req.Cold {
			a.log.Info("serve 已不在且不允许冷恢复，判不可恢复",
				"task", req.TaskID, "shim_pid", proc.Handle.PID)
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
		// 冷恢复重写 proc.json：沿用盘上已有的水位与 armed 值，不在这里改动。
		// why：armed 的语义是「会话是否本版本 agentd 亲手新建」。冷恢复只是重起
		// serve 进程、会话本身还在（sqlite 持久）——它不是新会话，armed 必须保持
		// 原值：legacy 任务保持 unarmed（升级保护完整），本版本出生的任务保持 armed
		if werr := writeProcInfo(req.TaskDir, &procInfo{
			Handle: newProc.Handle, Port: newProc.Port, Password: newProc.Password,
			LastTurnMsgID: si.LastTurnMsgID, WatermarkArmed: si.WatermarkArmed,
		}); werr != nil {
			a.log.Warn("冷恢复写 proc.json 失败，下次重启恢复将不可用",
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
				a.drop(req.TaskID) // newRun 已登记运行态，失败必须注销否则占位清理不到
				return executor.ResumeOutcome{Alive: false,
					Note: fmt.Sprintf("原会话已不在且新建失败：%v", nerr)}, nil
			}
			a.log.Warn("原会话已不在，已新开会话", "task", req.TaskID,
				"old", sessionID, "new", newID)
			sessionID, mode = newID, executor.ResumeModeFresh
			// 新会话让旧水位失去意义：不清零的话下次对账会拿旧会话的消息 id
			// 去比新会话的尾部，必然判「有未消费的回合」而补出一条假终态。
			// armed 保持 true：新会话同样处于 B38 持续水位维护之下，空水位
			// 意味着「新会话的第一个回合还没被消费」，该补发时必须补发
			if werr := writeProcInfo(req.TaskDir, &procInfo{
				Handle: proc.Handle, Port: proc.Port, Password: proc.Password,
				WatermarkArmed: true,
			}); werr != nil {
				a.log.Warn("新会话清零对账水位失败", "task", req.TaskID, "cause", werr)
			}
		}
		r.session = sessionID
	}
	r.captureStartCommit(a)
	go r.subscribeLoop(a)
	go a.watchdog(r)
	// 对账（B38）：断连窗口内完成的回合，其终态事件在 /event 上永久丢失
	// （无重放语义），不对账就会冻死在 running。fresh 模式不对账——那是新会话，
	// 没有「错过的进展」，且水位已随新会话失去意义
	if mode != executor.ResumeModeFresh {
		go a.reconcileAfterRecovery(context.Background(), req.TaskID, "startup")
	}
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
