// ask.go —— 提问（ask）面的工单身份与复用语义权威（B233.21 spec §范围 3）。
//
// ask 无政策判断（不经过判据网关、不咨询审批者、不做复用裁决），权威只管两件
// 决策事实：
//   - 工单身份：executor 提供原生提问 id 时按任务命名空间化（与 gate 工单同构、
//     天然幂等，B58）；没有原生 id 时退回 uuid——问题没有天然稳定 id，回答一次
//     即终结
//   - 撞单处置：agentd 重启后 executor 重放同一个未答请求 → 复用既有工单并重挂
//     等待；旧单已答又重发 → 必须另开新单（复用已答工单的 id 会让协调者再也
//     答不了，任务停在 waiting_answer 到 stall，B58）
//
// 边界：提问面的账本机制（建单、事件、waiter、Publish）与应答回程路由
// （OpenCode 走原生 ask 协议、其余走普通消息——那是 adapter 的原生协议转换，
// B233.10 定调 harness 职责）仍归 orchestration.Manager。
package approval

import (
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/google/uuid"
)

// AskTicketID 决定一次提问的工单身份：原生提问 id 按任务命名空间隔离
// （taskID:questionID），executor 没给原生 id 时退回 uuid。
func AskTicketID(taskID, questionID string) string {
	if questionID != "" {
		return executor.NamespacedTicketID(taskID, questionID)
	}
	return uuid.NewString()
}

// AskReissueTicketID 为「旧单已答又重发」的提问另起身份：一律 fresh uuid，
// 与原生 questionID 无关——复用已答工单的 id 会让协调者再也答不了（B58）。
func AskReissueTicketID() string {
	return uuid.NewString()
}

// AskDuplicate 是提问工单 id 撞上既有工单时的处置结论。
type AskDuplicate int

const (
	// AskDuplicateSkip 按既有 id 重读工单失败：按重放跳过——不建单、不发事件、
	// 不重挂等待。
	AskDuplicateSkip AskDuplicate = iota
	// AskDuplicateWait 既有单仍未答：重放——复用既有工单并重挂 waiter；
	// 不建单、不发第二条事件（协调者已经被叫醒过一次）。
	AskDuplicateWait
	// AskDuplicateReissue 旧单已答但 executor 又问了一次：另开新工单。
	AskDuplicateReissue
)

// ClassifyAskDuplicate 对「CreateTicket 报工单已存在」的提问请求给出处置。
//
// 参数：prior/gerr 是按既有工单 id 重读的结果（CreateTicket 幂等返回
// created=false 后由调用方重读）。
func ClassifyAskDuplicate(prior *proto.Ticket, err error) AskDuplicate {
	if err != nil {
		return AskDuplicateSkip
	}
	if prior.Answer == nil {
		return AskDuplicateWait
	}
	return AskDuplicateReissue
}
