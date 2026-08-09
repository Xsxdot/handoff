// agentd control_stream：桌面控制流的独立实时路由与控制流 WS。
//
// 职责：
//   - ControlHub：control_revision 维度的实时扇出（与 task Hub 独立）
//   - handleDesktopControlStream：先订阅再补发、按 revision 去重、慢客户端有界断开
//
// 边界：
//   - 不复用 task Hub：desktop 高频 catalog 流与任务事件流语义不同
//   - bootstrap/stream 无窗口竞态：先 bootstrap 得 R，再 after=R 订阅；
//     快照期间产生的事件经 durable control_events 补发
package agentd

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/desktopapi"
)

// controlStreamReplayLimit 是控制流单连接补发上限（同旧 WS 语义：超出由
// 客户端凭更大 after 续拉）。
const controlStreamReplayLimit = 10000

// controlStreamLiveLimit 是控制流实时事件待写缓冲上限（越限断开，无损重连）。
const controlStreamLiveLimit = 1000

// ControlHub 是 control_revision 维度的实时扇出。
//
// 并发安全：与 task Hub 同构（锁内订阅/发布，取消移除并关闭通道）。
type ControlHub struct {
	mu   sync.Mutex
	subs map[string][]chan desktopapi.ControlEventEnvelope
	log  *slog.Logger
}

// NewControlHub 创建控制流 Hub。
func NewControlHub() *ControlHub {
	return &ControlHub{subs: map[string][]chan desktopapi.ControlEventEnvelope{}, log: slog.Default()}
}

// Subscribe 订阅控制流（key=global，全部事件）。
func (h *ControlHub) Subscribe() (<-chan desktopapi.ControlEventEnvelope, func()) {
	ch := make(chan desktopapi.ControlEventEnvelope, controlStreamLiveLimit/2)
	h.mu.Lock()
	h.subs["global"] = append(h.subs["global"], ch)
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		subs := h.subs["global"]
		for i, c := range subs {
			if c == ch {
				subs = append(subs[:i], subs[i+1:]...)
				if len(subs) == 0 {
					delete(h.subs, "global")
				} else {
					h.subs["global"] = subs
				}
				close(ch)
				return
			}
		}
	}
}

// Publish 把控制事件广播给订阅者（永不阻塞，慢订阅者丢弃并记日志）。
func (h *ControlHub) Publish(ev desktopapi.ControlEventEnvelope) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range h.subs["global"] {
		select {
		case ch <- ev:
		default:
			h.log.Warn("控制流慢订阅者丢弃事件", "revision", ev.Revision, "kind", ev.Kind)
		}
	}
}

// handleDesktopControlStream 处理 WS /v1/control/stream?after=<revision>。
//
// 流程：校验 after → 先订阅 hub → 补发 durable control_events(after 之后) →
// 实时循环。订阅与补发并发运行，窗口期事件经 revision 去重不丢不重。
func (s *Server) handleDesktopControlStream(w http.ResponseWriter, r *http.Request) {
	after, err := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	if err != nil || after < 0 {
		s.log.Warn("desktop 控制流 after 参数非法", "after", r.URL.Query().Get("after"))
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "after 必须是大于等于 0 的整数",
		})
		return
	}

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("desktop 控制流握手失败", "after", after, "err", err)
		return
	}
	defer conn.CloseNow()
	ctx := conn.CloseRead(r.Context())

	// 先订阅再补发（与旧 /ws/events 同原则）：补发期间的事件进 live 队列。
	ch, cancel := s.controlHub.Subscribe()
	defer cancel()

	var (
		liveMu sync.Mutex
		live   []desktopapi.ControlEventEnvelope
		notify = make(chan struct{}, 1)
	)
	go func() {
		for {
			select {
			case ev, ok := <-ch:
				if !ok {
					return
				}
				liveMu.Lock()
				if len(live) >= controlStreamLiveLimit {
					liveMu.Unlock()
					notifyNotify(notify)
					return
				}
				live = append(live, ev)
				liveMu.Unlock()
				notifyNotify(notify)
			case <-ctx.Done():
				return
			}
		}
	}()

	// 补发 durable events（after 之后，revision 升序）。
	replays, err := s.st.ControlEventsAfter(ctx, after, controlStreamReplayLimit)
	if err != nil {
		s.log.Error("desktop 控制流补发失败", "after", after, "cause", err)
		return
	}
	a := &desktopapi.CatalogAssembler{}
	maxReplayed := after
	replayEnvelopes := make([]desktopapi.ControlEventEnvelope, 0, len(replays))
	for _, ev := range replays {
		env, err := a.ToControlEvent(ev)
		if err != nil {
			continue
		}
		replayEnvelopes = append(replayEnvelopes, env)
		maxReplayed = env.Revision
	}
	for _, env := range replayEnvelopes {
		if err := writeControlEnvelope(ctx, conn, env); err != nil {
			s.log.Warn("desktop 控制流补发写入失败", "revision", env.Revision, "err", err)
			return
		}
	}
	s.log.Info("desktop 控制流连接建立", "after", after, "replayed", len(replayEnvelopes))

	// 归并补发期间收集的实时事件（revision > maxReplayed 才写）。
	liveMu.Lock()
	pending := live
	live = nil
	liveMu.Unlock()
	for _, env := range pending {
		if env.Revision > maxReplayed {
			if err := writeControlEnvelope(ctx, conn, env); err != nil {
				return
			}
			maxReplayed = env.Revision
		}
	}

	// 实时循环。
	for {
		select {
		case <-notify:
			liveMu.Lock()
			pending := live
			live = nil
			over := len(pending) >= controlStreamLiveLimit
			liveMu.Unlock()
			for _, env := range pending {
				if env.Revision <= maxReplayed {
					continue // 重复
				}
				if err := writeControlEnvelope(ctx, conn, env); err != nil {
					return
				}
				maxReplayed = env.Revision
			}
			if over {
				s.log.Warn("desktop 控制流缓冲越限，断开（客户端凭 after 重连补拉）",
					"after", after, "last_revision", maxReplayed)
				return
			}
		case <-ctx.Done():
			return
		}
	}
}

// notifyNotify 向通知通道发送一次唤醒（非阻塞）。
func notifyNotify(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// writeControlEnvelope 写出一个控制事件信封。
func writeControlEnvelope(ctx context.Context, conn *websocket.Conn, env desktopapi.ControlEventEnvelope) error {
	b, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return conn.Write(ctx, websocket.MessageText, b)
}
