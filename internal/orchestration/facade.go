// facade.go —— B233.28：gateway 任务查询/应答/回迁与项目位置查询的编排门面。
//
// 职责：把 handler 今日对 store 的直打翻译成编排门面上的精确签名（契约见
// docs/superpowers/specs/b233.28-contract.md）。读路径与项目位置是**直通镜像**
// （逐行照抄既有同形接线，如 Manager.ListProjects → m.st.ListProjectLocations）；
// 应答 + 回迁在编排侧收口：原 gateway.handleReply 的三步与 gateway.resumeIfIdle
// 的 CAS 循环迁入本文件，gateway 不再持一份 CAS 循环。
//
// 边界：
//   - 不放 HTTP 面；不新增 HTTP 路径、CLI 命令、事件类型
//   - **不引 Session/Workbench**：登录/工作台继续打 store，不属于编排契约
//     （spec §要变的事实 3 / Out of Scope）
//   - 不碰 target.json 预算，不改 best.json 容器归属（B233.28 C 裁决：会话/
//     工作台命中记容器粒度债，无独立容器可改挂）
package orchestration

import (
	"context"
	"errors"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// —— 任务查询（直通镜像，逐行照抄 store 同形方法）——

// ListTasks 是 store.ListTasks 的直通镜像（契约 §2.2）。
func (m *Manager) ListTasks() ([]proto.Task, error) { return m.st.ListTasks() }

// GetTask 是 store.GetTask 的直通镜像。
func (m *Manager) GetTask(taskID string) (*proto.Task, error) { return m.st.GetTask(taskID) }

// PendingTickets 是 store.PendingTickets 的直通镜像。
func (m *Manager) PendingTickets(taskID string) ([]proto.Ticket, error) {
	return m.st.PendingTickets(taskID)
}

// EventsFrom 是 store.EventsFrom 的直通镜像（任务详情的最近事件）。
func (m *Manager) EventsFrom(taskID string, fromSeq int64, limit int) ([]proto.Event, error) {
	return m.st.EventsFrom(taskID, fromSeq, limit)
}

// EventsFromAsc 是 store.EventsFromAsc 的直通镜像（WS 补发历史，截尾部）。
func (m *Manager) EventsFromAsc(taskID string, fromSeq int64, limit int) ([]proto.Event, error) {
	return m.st.EventsFromAsc(taskID, fromSeq, limit)
}

// LatestEvent 是 store.LatestEvent 的直通镜像（WS 截断基线快照）。
func (m *Manager) LatestEvent(taskID string) (*proto.Event, error) { return m.st.LatestEvent(taskID) }

// CountEvents 是 store.CountEvents 的直通镜像（WS 截断缺口核对）。
func (m *Manager) CountEvents(taskID string, afterSeq, throughSeq int64) (int, error) {
	return m.st.CountEvents(taskID, afterSeq, throughSeq)
}

// ListMirrorTasks 是 store.ListMirrorTasks 的直通镜像（跨机任务汇总快照）。
func (m *Manager) ListMirrorTasks() ([]store.MirrorTask, error) { return m.st.ListMirrorTasks() }

// MirrorTaskTarget 是 store.MirrorTaskTarget 的直通镜像（透明路由索引）。
func (m *Manager) MirrorTaskTarget(taskID string) (string, bool, error) {
	return m.st.MirrorTaskTarget(taskID)
}

// MirrorEventsFrom 是 store.MirrorEventsFrom 的直通镜像（镜像任务历史重放）。
func (m *Manager) MirrorEventsFrom(taskID string, fromSeq int64, limit int) ([]proto.Event, error) {
	return m.st.MirrorEventsFrom(taskID, fromSeq, limit)
}

// —— 项目位置（直通镜像）——

// ListProjectLocations 是 store.ListProjectLocations 的直通镜像（未探测状态）。
// 与 ListProjects 的区别：后者现场探测填 Status，本方法是登记表原样读出。
func (m *Manager) ListProjectLocations() ([]proto.ProjectLocation, error) {
	return m.st.ListProjectLocations()
}

// GetProjectLocationByName 是 store.GetProjectLocationByName 的直通镜像
// （按引用名查单条位置；不存在返回 store.ErrNotFound）。
func (m *Manager) GetProjectLocationByName(name string) (proto.ProjectLocation, error) {
	return m.st.GetProjectLocationByName(name)
}

// UpdateProjectLocation 是 store.UpdateProjectLocation 的直通镜像
// （改引用名与/或路径；ErrProjectDuplicate / ErrNotFound 原样透出）。
func (m *Manager) UpdateProjectLocation(name, newName, newPath string) (proto.ProjectLocation, error) {
	return m.st.UpdateProjectLocation(name, newName, newPath)
}

// —— 应答与回迁 ——

// AnswerTicket 在编排侧收口「应答一张工单」：工单归属校验、AnswerTicketApplied
// 落库（answer IS NULL 幂等守卫）、ticket_answered 事件。
//
// 参数：
//   - ctx: 预留上下文；本方法为同进程同库同步调用，不发起网络
//   - taskID: 工单必须归属的任务（跨任务按不存在处理，不泄露信息）
//   - ticketID / answer: 工单与应答原文
//
// 返回：
//   - applied=true：本次真正从 NULL 写入新答案（且已追加 ticket_answered 事件）
//   - applied=false, err=nil：同值重答（幂等），调用方按 200 + idempotent 应答
//   - store.ErrTicketConflict / store.ErrNotFound：原样透出（不许重包装，会断 errors.Is）
//
// 为什么 AppendEvent 成功后补 hub.Publish（B380 C-1）：ticket_answered 在客户端
// 不可交付（client.WaitDeliveryPolicy 已列为 false），发布它不会唤醒任何
// wait/唤醒消费；但账本镜像只镜像 hub 实时流（internal/ledgermirror），不发布
// 就会让关单镜像断流，OpenTickets 重放出幽灵未决单。唤醒 executor 仍由 gateway
// 侧 hub.NotifyAnswer 承担（contract C-3）。
func (m *Manager) AnswerTicket(ctx context.Context, taskID, ticketID, answer string) (applied bool, err error) {
	m.log.Info("工单应答门面进入", "task", taskID, "ticket", ticketID)
	tk, err := m.st.GetTicket(ticketID)
	if err != nil {
		return false, err // store.ErrNotFound 原样透出
	}
	if tk.TaskID != taskID {
		// 跨任务工单按不存在处理，不泄露（与现状 handleReply tk.TaskID != taskID → 404 同款）
		return false, store.ErrNotFound
	}
	applied, err = m.st.AnswerTicketApplied(ticketID, answer)
	if err != nil {
		return false, err // ErrTicketConflict / ErrNotFound 原样透出
	}
	if !applied {
		m.log.Info("工单应答幂等（同值重答）", "task", taskID, "ticket", ticketID)
		return false, nil
	}
	evt, aerr := m.st.AppendEvent(taskID, proto.EventTypeTicketAnswered,
		TicketAnsweredPayload{TicketID: ticketID, Answer: answer})
	if aerr != nil {
		// 事件追加失败只 Warn，不阻断应答成功；发布也一并跳过（无事件可发）
		m.log.Warn("追加工单答复事件失败", "task", taskID, "ticket", ticketID, "cause", aerr)
	} else {
		m.hub.Publish(evt)
	}
	m.log.Info("工单应答完成", "task", taskID, "ticket", ticketID)
	return true, nil
}

// ResumeIfIdle 在编排侧收口「无待办则回迁 running」：任务处于 waiting_answer 且
// PendingTickets 为空时，经 store.UpdateTaskState 的 CAS 迁移到 running；CAS 撞
// ErrBadTransit（并发赢家已迁移）时有界重读重试（3 次），重试耗尽只 Warn。
//
// 为什么有界重试：两个工单被并发回答时，两个请求都可能读到「已无工单」，先执行者
// 成功、后执行者收到 ErrBadTransit；重试让意图在最新快照上重新评估（contract §4）。
// 无返回值：与现状 gateway.resumeIfIdle 一致——回迁失败只记日志，不影响 reply 成功。
func (m *Manager) ResumeIfIdle(ctx context.Context, taskID string) {
	for attempt := 0; attempt < 3; attempt++ {
		task, err := m.st.GetTask(taskID)
		if err != nil {
			m.log.Error("reply 后读取任务失败", "task", taskID, "cause", err)
			return
		}
		if task.State != proto.TaskStateWaitingAnswer {
			return // 任务已不在等待应答，无需回迁
		}
		pending, err := m.st.PendingTickets(taskID)
		if err != nil {
			m.log.Error("reply 后查询待办工单失败", "task", taskID, "cause", err)
			return
		}
		if len(pending) > 0 {
			return // 仍有未答工单，任务保持 waiting_answer
		}
		if err := m.st.UpdateTaskState(taskID, proto.TaskStateRunning); err == nil {
			m.log.Info("reply 后任务回迁 running", "task", taskID)
			return
		} else if !errors.Is(err, store.ErrBadTransit) {
			m.log.Error("reply 后恢复任务运行失败", "task", taskID, "cause", err)
			return
		}
		// ErrBadTransit：状态被并发变更（如另一 reply 已回迁），重读最新快照重试
	}
	m.log.Warn("reply 后恢复任务运行重试耗尽", "task", taskID)
}
