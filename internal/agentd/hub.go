// Package agentd 是 handoff agentd 服务的进程内实时路由层。
//
// 职责：
//   - 按 taskID 维度做事件实时扇出（Subscribe/Publish），供 HTTP/WS 层推送
//   - 提供 ticket 应答的一次性等待/通知路由（WaitAnswer/NotifyAnswer）
//
// 边界：
//   - 不做持久化：事件落库在 store，可靠性由 events 表 seq + cursor 承担，
//     本层只做实时扇出，不保证送达（慢订阅者直接丢弃），历史回放由 server 层用 store.EventsFrom 拼接
//   - 不参与业务决策（状态迁移、审批），仅提供进程内路由原语
package agentd

import (
	"context"
	"log/slog"
	"sync"

	"github.com/xushixin/handoff/internal/proto"
)

// eventBufferSize 是每个事件订阅者通道的缓冲长度。
//
// 为什么选「带缓冲 + select-default」：Publish 对缓冲已满的订阅者走 default 分支直接丢弃，
// 防止一个断连/慢速的 WS 客户端让 Publish 阻塞，进而卡死全局事件扇出。
const eventBufferSize = 16

// Hub 是进程内实时路由层，管理事件订阅与 ticket 应答等待。
//
// 并发安全：subs/answers 两个 map 的全部访问都在 mu 保护下；
// Publish/NotifyAnswer 的通道发送也在锁内进行，与 cancel 的「移除+关闭」互斥，
// 从而避免出现向已关闭通道发送的 panic。
type Hub struct {
	mu      sync.Mutex
	subs    map[string][]chan proto.Event // taskID -> 订阅者事件通道
	answers map[string][]chan string      // ticketID -> 等待者应答通道（各缓冲 1）
	log     *slog.Logger
}

// NewHub 创建空的 Hub。
//
// 注意：
//   - 日志取创建时的 slog.Default()；如需统一格式/级别，调用方应先 slog.SetDefault(logx.Setup(...)) 再 NewHub
func NewHub() *Hub {
	return &Hub{
		subs:    make(map[string][]chan proto.Event),
		answers: make(map[string][]chan string),
		log:     slog.Default(),
	}
}

// Subscribe 订阅指定 task 的实时事件流。
//
// 参数：
//   - taskID: 要订阅的任务 ID
//
// 返回：
//   - ch: 事件通道（带缓冲）；仅包含订阅后新产生的事件，历史回放由 server 层用 store.EventsFrom 拼接
//   - cancel: 取消订阅函数，可重复调用；取消后本通道不再接收新事件并被关闭，
//     消费方可直接 range 到结束，Publish 也不会再向其发送
//
// 注意：
//   - 订阅者消费不及时（缓冲写满）时事件会被 Publish 丢弃，本层不保证每条事件都投递
func (h *Hub) Subscribe(taskID string) (<-chan proto.Event, func()) {
	ch := make(chan proto.Event, eventBufferSize)
	h.mu.Lock()
	h.subs[taskID] = append(h.subs[taskID], ch)
	n := len(h.subs[taskID])
	h.mu.Unlock()

	h.log.Debug("事件订阅", "taskID", taskID, "subscribers", n)
	return ch, func() { h.unsubscribe(taskID, ch) }
}

// unsubscribe 将订阅者从表中移除并关闭其通道。
//
// 为什么「移除+关闭」都在锁内完成：Publish 也在锁内向通道发送，
// 锁保证了不存在「已从表移除但 Publish 正要发送」的窗口，不会发生向已关闭通道发送的 panic。
// 通道不在表中（重复取消）时直接返回，避免重复 close。
func (h *Hub) unsubscribe(taskID string, ch chan proto.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	subs := h.subs[taskID]
	for i, c := range subs {
		if c != ch {
			continue
		}
		// 移除该订阅者；删空后连 taskID 的 key 一起清理，避免 map 无界增长
		subs = append(subs[:i], subs[i+1:]...)
		if len(subs) == 0 {
			delete(h.subs, taskID)
		} else {
			h.subs[taskID] = subs
		}
		close(ch)
		h.log.Debug("取消事件订阅", "taskID", taskID, "subscribers", len(subs))
		return
	}
}

// Publish 将事件广播给该 task 的所有订阅者，永不阻塞。
//
// 参数：
//   - ev: 待广播的事件；Seq/TaskID 需已由调用方（store 落库后）赋值
//
// 注意：
//   - 无订阅者时直接丢弃（持久化在 store，不靠 hub）
//   - 对缓冲已满的慢订阅者走 select-default 丢弃并打 Warn（taskID、seq），
//     这是「事件为什么没到」的第一排查点
func (h *Hub) Publish(ev proto.Event) {
	h.mu.Lock()
	defer h.mu.Unlock()

	for _, ch := range h.subs[ev.TaskID] {
		select {
		case ch <- ev:
		default:
			// 订阅者消费不及时（典型为断连的 WS 客户端），丢弃本次事件防止卡住全局扇出
			h.log.Warn("慢订阅者丢弃事件", "taskID", ev.TaskID, "seq", ev.Seq)
		}
	}
}

// WaitAnswer 等待指定 ticket 的应答，直到收到应答或 ctx 取消。
//
// 参数：
//   - ctx: 控制等待生命周期，取消时立即返回
//   - ticketID: 要等待应答的工单 ID
//
// 返回：
//   - 应答内容
//   - ctx 取消时返回 ctx.Err()（context.Canceled / context.DeadlineExceeded）
//
// 注意：
//   - 应答是一次性的：NotifyAnswer 只投递给正在等待的调用者，先 Notify 后 Wait 拿不到旧应答
//   - 多个调用者可同时等待同一 ticket，均会收到应答
//   - 等待者退出时会从表里移除，不会泄漏
func (h *Hub) WaitAnswer(ctx context.Context, ticketID string) (string, error) {
	ch := make(chan string, 1)

	h.mu.Lock()
	// 先查 ctx 是否已取消：已取消则直接返回，不注册等待者，避免泄漏
	if err := ctx.Err(); err != nil {
		h.mu.Unlock()
		return "", err
	}
	h.answers[ticketID] = append(h.answers[ticketID], ch)
	h.mu.Unlock()

	// 无论以何种方式返回，都把自己从等待表移除
	defer func() {
		h.mu.Lock()
		h.removeAnswerWaiter(ticketID, ch)
		h.mu.Unlock()
	}()

	select {
	case ans := <-ch:
		return ans, nil
	case <-ctx.Done():
		// 应答与取消可能同时到达：优先交出已就绪的应答，避免「明明有答案却报超时」
		select {
		case ans := <-ch:
			return ans, nil
		default:
		}
		return "", ctx.Err()
	}
}

// removeAnswerWaiter 将指定等待者通道从 ticketID 的等待表中移除。
//
// NotifyAnswer 已把整个等待表清空时，这里找不到该通道，直接返回即可（幂等）。
func (h *Hub) removeAnswerWaiter(ticketID string, ch chan string) {
	waiters := h.answers[ticketID]
	for i, c := range waiters {
		if c != ch {
			continue
		}
		waiters = append(waiters[:i], waiters[i+1:]...)
		if len(waiters) == 0 {
			delete(h.answers, ticketID)
		} else {
			h.answers[ticketID] = waiters
		}
		return
	}
}

// NotifyAnswer 将应答投递给等待该 ticket 的所有 WaitAnswer 调用者。
//
// 参数：
//   - ticketID: 工单 ID
//   - answer: 应答内容
//
// 注意：
//   - 无人等待时不 panic，应答直接丢弃（应答已由 store 持久化，等待方超时后自行重查）
//   - 投递后该 ticket 的等待表被清空，后续 WaitAnswer 不会拿到旧应答
func (h *Hub) NotifyAnswer(ticketID, answer string) {
	h.mu.Lock()
	waiters := h.answers[ticketID]
	delete(h.answers, ticketID)
	h.mu.Unlock()

	for _, ch := range waiters {
		// 等待者通道缓冲为 1 且仅本函数写入，发送不会阻塞
		ch <- answer
	}
	h.log.Info("ticket 应答已投递", "ticketID", ticketID)
}
