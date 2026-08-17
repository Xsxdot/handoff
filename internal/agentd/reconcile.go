// reconcile.go —— 任务运行态与 executor 实际存活性的对账。
//
// 职责：
//   - reconcileExecutorGone：「executor 已不在」这一事实的唯一收尾实现，
//     三个到达口（启动探活 / 事件通道关闭 / 协调者动作撞上失配）共用
//
// 边界：
//   - 不探活：本文件只负责「已经知道 executor 没了之后怎么办」，
//     「怎么知道的」属各到达口自己（spec §2.2 明确不加周期性探活）
//   - 不碰 adapter：收尾只动 store 与 hub
package agentd

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// noteStopping 标记「接下来这次事件通道关闭是我们自己发起的」。
//
// 必须在 ad.Stop() **之前**调用。
//
// why（为什么需要这个标记）：Manager.Stop 先调 ad.Stop() 再落 failed，两步之间
// adapter 已经关掉了事件通道，mediate 随之退出并对账——此时任务状态还是 running，
// 于是补一条它不该有的 failed 事件，并造成 running→waiting_review→failed 的
// 状态抖动（末跳合法，所以不硬失败，但事件是噪音）。
//
// why（为什么不改 Stop 的顺序）：先落 failed 再 ad.Stop() 会让 executor 在状态
// 已定型后仍可能产出事件，各 handler 的「已终结则丢弃」判断要散在更多路径上，
// 风险大于收益。显式标记说的正是「这次关闭是我们自己关的」，诚实且局部。
func (m *Manager) noteStopping(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stopping[taskID] = struct{}{}
}

// takeStopping 取走并清空标记，返回本次关闭是否为主动停止。
//
// why（取走式而非常驻）：标记的生命周期就是一次主动停止。若它长期驻留，下一次
// executor 猝死会被上一次的主动停止误抑制——真出事时反而没人对账。与 grok
// adapter 的 takeAskedViaTool、opencode 的 takeTurnRejected 同源。
func (m *Manager) takeStopping(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.stopping[taskID]
	delete(m.stopping, taskID)
	return ok
}

// reconcileExecutorGone 是包级同名函数的方法薄包装（省去调用点重复传 st/hub/log）。
func (m *Manager) reconcileExecutorGone(taskID, reason string) proto.TaskState {
	return reconcileExecutorGone(m.st, m.hub, taskID, reason, m.log, m.SweepTaskProcs)
}

// stopExecutor 停 executor，并在「没有内存运行态」时按恢复凭据兜底回收。
//
// 参数：
//   - taskID: 目标任务
//   - ad: 已解析的 adapter（调用方已做 adapterFor）
//
// 返回：
//   - nil：executor 已停止，或已通过 reaper 兜底回收
//   - 非 nil：停止或兜底回收失败；调用方据此决定是否可以宣布任务已回收
//
// 注意：
//   - 调用前必须已 noteStopping（本函数会关掉事件通道，mediate 随之退出）
//   - Stop/Done 等人工主动收尾可以忽略返回值；ForceReclaim 必须据此保持活跃态，
//     不能把「executor 未停」写成成功
func (m *Manager) stopExecutor(taskID string, ad executor.Adapter) error {
	m.noteStopping(taskID)
	err := ad.Stop(taskID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		// executor 还在，只是这次没停掉：兜底回收对它无意义——
		// 真去 kill 进程反而可能杀掉正在收尾的进程
		m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
		// 唯独「已发 SIGKILL 但复核仍存活」要惊动人：这是唯一一种不提示就会
		// 留下长期孤儿的失败（B20 现场存活了 11.5 小时，正是因为完全静默）。
		// 其余 Stop 失败五花八门（ctx 取消、内部状态不一致），全发事件等于
		// 把协调者淹了，那样这条提示就没人看了。
		if errors.Is(err, prochost.ErrStillAlive) {
			m.notifyOrphanRisk(taskID, fmt.Sprintf(
				"executor 进程可能残留（已发 SIGKILL 但复核仍存活），"+
					"请先 handoff status 确认，再 handoff stop %s 回收（原因：%v）", taskID, err))
		}
		return err
	}
	rp, ok := ad.(reaper)
	if !ok {
		m.log.Warn("executor 无内存运行态且 adapter 不支持兜底回收", "task", taskID, "cause", err)
		return err
	}
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	m.log.Info("executor 无内存运行态，按恢复凭据兜底回收", "task", taskID)
	if rerr := rp.Reap(taskID, taskDir); rerr != nil {
		m.log.Error("兜底回收失败，留事件提示人工", "task", taskID, "cause", rerr)
		// 给协调者的是「下一步做什么」，不是「出了什么错」——旧文案让人去
		// tmux kill-session，那个命令现在不存在了，照做只会更困惑
		m.notifyOrphanRisk(taskID, fmt.Sprintf("executor 进程可能残留，请先 handoff status 确认，"+
			"再 handoff stop %s 回收（原因：%v）", taskID, rerr))
		return rerr
	}
	m.log.Info("按恢复凭据兜底回收成功", "task", taskID)
	return nil
}

// notifyOrphanRisk 追加一条「executor 可能残留」的 progress 事件并广播。
//
// 参数：
//   - taskID: 目标任务
//   - text: 面向协调者的正文；给的必须是「下一步做什么」而不是「出了什么错」
//
// 注意：
//   - 追加失败只记日志、不返回错误：调用方全都处在归档/中止的收尾路径上，
//     那件事本身已经达成，不该因为发不出提示而中断
func (m *Manager) notifyOrphanRisk(taskID, text string) {
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: text})
	if err != nil {
		m.log.Error("追加 executor 残留提示事件失败", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
	m.log.Info("已向协调者发出 executor 残留提示", "task", taskID)
}

// reconcileExecutorGone 收尾一个 executor 已不在的任务。
//
// 参数：
//   - sweep: 残留进程清扫回调；**无条件调用**，与任务状态无关（why 见下）
//
// 为什么清扫是无条件后置动作：本函数对非 running/waiting_answer 的状态提前返回，
// 那条分支的理由是「待审核终态与已终结态不需要状态收尾」——它说的是状态，不是
// 资源。executor 已经不在了，残留进程该不该收与任务停在哪个状态无关；已经 done
// 过的任务同样可能有残留（那次 Kill 正因锁已释放而空转）。2026-08-12 事故里两个
// 任务最终都停在 waiting_review，恰好是提前返回会跳过的形态。
//
// 顺序：状态收尾在前、清扫在后。协调者的工作流（任务进 waiting_review）不受
// 清扫成败影响——清扫失败只上报，绝不回头改状态。
func reconcileExecutorGone(st *store.Store, hub *Hub, taskID, reason string,
	log *slog.Logger, sweep func(taskID string)) proto.TaskState {

	cur, err := st.GetTask(taskID)
	if err != nil {
		log.Error("对账读取任务失败", "task", taskID, "reason", reason, "cause", err)
		return ""
	}
	log.Info("executor 已不在，开始对账", "task", taskID, "state", cur.State, "reason", reason)
	if cur.State != proto.TaskStateRunning && cur.State != proto.TaskStateWaitingAnswer {
		log.Info("任务无需状态对账，仅清扫残留", "task", taskID, "state", cur.State)
		sweep(taskID) // 无条件：见上方 why
		return cur.State
	}

	// 复用终态收口的同一个助手（B63）：这条路径迁的是 waiting_review（非终态），
	// 走不到 transit 的终态分支，但「executor 已死 ⇒ 挂起工单不可能再被回答」的
	// 语义与终态一致，审计痕迹也该一致
	voidTicketsWithAudit(st, taskID, reason, log)
	// 对账路径没有 git 实况可带（executor 已不在，查不了回合起点）
	evt, err := st.AppendEvent(taskID, proto.EventTypeFailed, newFailedPayload(reason, "", ""))
	if err != nil {
		log.Error("对账追加 failed 事件失败，不迁移状态", "task", taskID, "cause", err)
		sweep(taskID) // 状态没迁成不代表 executor 还活着，残留照收
		return cur.State
	}
	if err := recoverTransit(st, taskID, cur.State); err != nil {
		log.Error("对账迁移 waiting_review 失败", "task", taskID, "cause", err)
		sweep(taskID)
		return cur.State
	}
	hub.Publish(evt)
	log.Info("对账完成", "task", taskID, "from", cur.State, "to", proto.TaskStateWaitingReview)
	sweep(taskID)
	return proto.TaskStateWaitingReview
}

// TaskProcCount 数一个任务名下当前有几个进程。
//
// 参数：taskID 为完整任务 id
//
// 返回：(n, ok)。**ok 为 false 时 n 无意义**，调用方必须什么都不做——
// 取不到句柄、adapter 不支持、Footprint 判定不可信（Verdict 非 OK）都归此类。
// 把「量不出来」当成「超了」会误杀，当成「没超」会让告警的置位状态错乱。
//
// 导出是因为 watchdog 的接线点在 cmd/agentd.go（与 SweepTaskProcs 同理），
// 不是给外部当通用 API 用。
func (m *Manager) TaskProcCount(taskID string) (int, bool) {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Debug("点名解析执行者失败", "task", taskID, "cause", err)
		return 0, false
	}
	fp, ok := ad.(footprinter)
	if !ok {
		m.log.Debug("adapter 不支持进程句柄，跳过点名", "task", taskID)
		return 0, false
	}
	h, err := fp.ProcHandle(taskID, filepath.Join(m.cfg.DataDir, "tasks", taskID))
	if err != nil {
		m.log.Debug("点名取进程句柄失败", "task", taskID, "cause", err)
		return 0, false
	}
	members, v, err := prochost.Footprint(h)
	if err != nil || v != prochost.VerdictOK {
		m.log.Debug("点名读数不可信", "task", taskID, "verdict", string(v), "cause", err)
		return 0, false
	}
	return len(members), true
}

// footprinter 是「交出任务进程句柄」的可选 adapter 能力（四个真实 adapter 均实现，
// fake 不实现）。
//
// 为什么是可选接口而不是加进 executor.Adapter：不支持的 adapter 一律按「无凭据」
// 降级是自然语义，五动作核心契约不该为一个诊断/回收功能扩面——与 reaper /
// prober / restorer / volatilePermitter 同一套路数。
type footprinter interface {
	ProcHandle(taskID, taskDir string) (prochost.Handle, error)
}

// SweepTaskProcs 清扫一个任务的残留进程，best-effort。
//
// 参数：taskID 为目标任务
//
// 注意：
//   - 无返回值：调用方全都处在收尾路径上，清扫成败不该反过来影响那件事
//   - 只有「确实有残留但我们没敢动」才发事件提示人工；成功与无残留只进日志。
//     这是尊重 stopExecutor 已经想清楚过的事——「其余失败五花八门，全发事件
//     等于把协调者淹了，那样这条提示就没人看了」
//   - 导出是因为 RecoverOnStartup 的接线点在 cmd/agentd.go（与 ResumeTask 同理），
//     不是给外部当通用 API 用
func (m *Manager) SweepTaskProcs(taskID string) {
	ad, err := m.adapterFor(taskID)
	if err != nil {
		m.log.Error("清扫解析执行者失败", "task", taskID, "cause", err)
		return
	}
	fp, ok := ad.(footprinter)
	if !ok {
		m.log.Debug("adapter 不支持进程句柄，跳过清扫", "task", taskID)
		return
	}
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	h, err := fp.ProcHandle(taskID, taskDir)
	if err != nil {
		m.log.Error("清扫取进程句柄失败", "task", taskID, "dir", taskDir, "cause", err)
		return
	}
	killed, verdict, err := prochost.Sweep(h)
	switch {
	case errors.Is(err, prochost.ErrExecutorAlive):
		// executor 仍存活 → Sweep 降级为只做名册点名（B119 §2.1）。改前这里打的是
		// 「交由常规回收路径」，而对失控任务而言那条路径并不存在；现在报告实际
		// 回收数，0 与非 0 都是有意义的结论。
		m.log.Info("执行者存活，已降级为点名回收", "task", taskID, "pid", h.PID, "killed", killed)
	case err != nil:
		m.log.Error("清扫失败", "task", taskID, "pid", h.PID, "cause", err)
		m.notifyOrphanRisk(taskID, fmt.Sprintf(
			"残留进程清扫失败（pid=%d，原因：%v），请先 handoff footprint 确认再人工处理", h.PID, err))
	case verdict != prochost.VerdictOK:
		m.log.Warn("清扫放弃", "task", taskID, "pid", h.PID, "verdict", string(verdict))
		m.notifyOrphanRisk(taskID, fmt.Sprintf(
			"残留进程未清扫（判定：%s），请先 handoff footprint 确认再人工处理", verdict))
	case killed > 0:
		m.log.Info("残留进程已清扫", "task", taskID, "pid", h.PID, "killed", killed)
	default:
		m.log.Info("无残留进程", "task", taskID, "pid", h.PID)
	}
}
