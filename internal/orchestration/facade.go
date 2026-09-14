// facade.go —— B233.28 Ticket 0：gateway 任务查询/应答/回迁与项目位置查询的
// 编排门面空壳与直通镜像。
//
// 职责：把 handler 今日对 store 的直打翻译成编排门面上的精确签名（契约见
// docs/superpowers/specs/b233.28-contract.md）。读路径与项目位置是**直通镜像**
// （逐行照抄既有同形接线，如 Manager.ListProjects → m.st.ListProjectLocations）；
// 应答 + 回迁是**空壳**——原 gateway.resumeIfIdle 的 CAS 循环迁入编排侧的行为
// 归 implement 节点，骨架期生产路径不调用，故不改任何对外行为。
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

// ErrTicketAnswerUnwired 是 B233.28 Ticket 0 骨架哨兵：AnswerTicket / ResumeIfIdle
// 的行为（工单归属校验、应答落库、ticket_answered 事件、无待办则回迁 running 的
// CAS 有界重试）与 gateway.resumeIfIdle 的逐行搬运归 implement 节点。骨架期
// 生产路径不调用它们，哨兵只保证接口方法集可编译且不产生可观测行为。
var ErrTicketAnswerUnwired = errors.New("B233.28 Ticket 0：工单应答门面未接线")

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

// —— 应答与回迁（空壳，行为归 implement）——

// AnswerTicket 在编排侧收口「应答一张工单」：工单归属校验、AnswerTicketApplied
// 落库（answer IS NULL 幂等守卫）、ticket_answered 事件。返回 applied=false
// 表示幂等重发（已有相同答案），调用方按 200 + idempotent 应答。
//
// 骨架期：返回 ErrTicketAnswerUnwired；gateway 生产路径仍走 s.st，行为不变。
// implement 节点落地后，gateway 的 handleReply 改为只调本方法 + hub.NotifyAnswer
// / mgr.RelayAnswer，不再自己 AnswerTicketApplied / AppendEvent。
func (m *Manager) AnswerTicket(ctx context.Context, taskID, ticketID, answer string) (applied bool, err error) {
	return false, ErrTicketAnswerUnwired
}

// ResumeIfIdle 在编排侧收口「无待办则回迁 running」：任务处于 waiting_answer 且
// PendingTickets 为空时，经 store.UpdateTaskState 的 CAS 迁移到 running；CAS 撞
// ErrBadTransit（并发赢家已迁移）时有界重读重试，重试耗尽只 Warn。
//
// 骨架期：空实现；gateway 生产路径仍走自己的 resumeIfIdle，行为不变。implement
// 节点把 gateway.resumeIfIdle 的循环逐行搬入此处后，gateway 不再持 CAS 循环。
func (m *Manager) ResumeIfIdle(ctx context.Context, taskID string) {}
