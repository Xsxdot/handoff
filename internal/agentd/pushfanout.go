// pushfanout.go —— 「需要你」事实到 APNs 的内部扇出（B432 spec §契约面 2）。
//
// 职责：
//   - 把两侧事件库的事件分类成「需要你」通知（decision / ticket / mention /
//     needs_human，与收件箱同源词表）
//   - 经非阻塞队列投递到该成员全部已登记 iOS 设备，badge 取收件箱同源计数
//   - APNs 410 → 删设备不重试；任何失败只 Warn，接口层不感知（不报假送达）
//
// 边界：
//   - **旁路**：只入队，不挡事件写；队满即丢（写事件永远优先）
//   - **不重造「需要你」判定**：分类只做事件类型 → 词表映射，判定口径归
//     collectInboxItems / 收件箱既有实现
//   - **静默降级**：无 APNs 配置（sender==nil）、无登记设备、成员未解析——
//     一律不发送、不报错，站内铃铛兜底（spec 验收③）
//   - **日志不落 token 明文**，只落 device_id（承重安全属性）
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// PushSender 是 fanout 与 APNs 之间的唯一接缝：把一条通知投给一个 APNs token。
// 生产实现是 APNsSender（apns.go）；测试注入 fake。
// 返回错误必须可分类：ErrPushUnregistered 表示 token 失效（APNs 410）。
type PushSender interface {
	Send(ctx context.Context, token string, n proto.PushNotification, badge int) error
}

// ErrPushUnregistered 表示 APNs 回 410（token 已失效）——fanout 据此删设备，不重试。
var ErrPushUnregistered = errors.New("push: 设备 token 已失效")

// pushEventTypeNeedsHuman 是「需要你」第四源的 event_type。前三源直接复用
// proto.InboxOrigin*（与收件箱同源词表），等人标记没有 inbox origin，
// 用本常量避免散落字面量。
const pushEventTypeNeedsHuman = "needs_human"

// pushQueueCap 是通知队列容量。64 条 ≈ 一次事件风暴的量级；满了就丢——
// 推送是尽力而为的旁路，绝不能反压事件写。
const pushQueueCap = 64

// PushFanout 是「需要你」事实到 APNs 的扇出器。
//
// 并发安全：badgeCounter 经 badgeMu 读写，其余字段构造后只读；
// ch 是唯一跨 goroutine 的通信点（生产者 Notify、消费者 worker）。
type PushFanout struct {
	ch           chan proto.PushNotification
	st           *store.Store
	sender       PushSender
	memberOf     func() string
	log          *slog.Logger
	badgeMu      sync.RWMutex
	badgeCounter func(member string) int
}

// NewPushFanout 构造扇出器。sender 可为 nil（未配 APNs）——投递时静默降级。
// memberOf 返回当前控制台成员（user:<name>）；返回空串表示身份未解析，
// 分类即不产出通知。badgeCounter 缺省恒 0，由装配点后置注入同源计数。
func NewPushFanout(st *store.Store, sender PushSender, memberOf func() string, log *slog.Logger) *PushFanout {
	if log == nil {
		log = slog.Default()
	}
	return &PushFanout{
		ch:           make(chan proto.PushNotification, pushQueueCap),
		st:           st,
		sender:       sender,
		memberOf:     memberOf,
		log:          log,
		badgeCounter: func(string) int { return 0 },
	}
}

// SetBadgeCounter 由装配点注入收件箱同源计数（badge 与未处理数一致的来源）。
func (f *PushFanout) SetBadgeCounter(fn func(member string) int) {
	f.badgeMu.Lock()
	defer f.badgeMu.Unlock()
	if fn == nil {
		f.badgeCounter = func(string) int { return 0 }
		return
	}
	f.badgeCounter = fn
}

// badge 读当前计数器（nil 安全）。
func (f *PushFanout) badge(member string) int {
	f.badgeMu.RLock()
	fn := f.badgeCounter
	f.badgeMu.RUnlock()
	if fn == nil {
		return 0
	}
	return fn(member)
}

// Start 起投递 worker；ctx 取消即退出。可重复调用（各自一个 worker）。
func (f *PushFanout) Start(ctx context.Context) {
	go func() {
		f.log.Debug("推送扇出 worker 启动")
		defer f.log.Debug("推送扇出 worker 退出", "cause", ctx.Err())
		for {
			select {
			case <-ctx.Done():
				return
			case n := <-f.ch:
				f.deliver(ctx, n)
			}
		}
	}()
}

// Notify 非阻塞入队；队满丢弃并 Warn（不挡事件写）。
func (f *PushFanout) Notify(n proto.PushNotification) {
	select {
	case f.ch <- n:
	default:
		f.log.Warn("推送队列已满，丢弃", "event_type", n.EventType, "member", n.Member)
	}
}

// OnLedgerEvent 是 card_events 侧的分类入口（decision / mention / needs_human）。
// 分类未命中不打日志（高频事件流）。
func (f *PushFanout) OnLedgerEvent(ev proto.LedgerEvent) {
	n, ok := classifyLedgerEvent(ev, f.member())
	if ok {
		f.Notify(n)
	}
}

// OnTaskEvent 是 store.events 侧的分类入口（permission / question 工单）。
func (f *PushFanout) OnTaskEvent(e proto.Event) {
	n, ok := classifyTaskEvent(e, f.member())
	if ok {
		f.Notify(n)
	}
}

// member 解析当前控制台成员；未配置返回空串（分类据此静默不推）。
func (f *PushFanout) member() string {
	if f.memberOf == nil {
		return ""
	}
	return f.memberOf()
}

// deliver 把一条通知投给该成员全部登记设备。
//
// 失败语义（承重）：任何一步失败都只 Warn + 继续，绝不向调用方回传「已送达」；
// ErrPushUnregistered → 删设备（第二次删不存在被忽略），不再重试。
func (f *PushFanout) deliver(ctx context.Context, n proto.PushNotification) {
	if f.sender == nil {
		f.log.Debug("未配置 APNs，静默降级站内", "event_type", n.EventType, "member", n.Member)
		return
	}
	if f.st == nil {
		f.log.Warn("推送扇出缺少存储，丢弃通知", "event_type", n.EventType, "member", n.Member)
		return
	}
	devices, err := f.st.ListPushDevices(n.Member)
	if err != nil {
		f.log.Warn("查询推送设备失败，降级站内", "member", n.Member,
			"event_type", n.EventType, "cause", err)
		return
	}
	if len(devices) == 0 {
		f.log.Debug("无登记推送设备，静默降级站内", "member", n.Member, "event_type", n.EventType)
		return
	}
	badge := f.badge(n.Member)
	for _, dev := range devices {
		f.log.Debug("投递推送通知", "device", dev.DeviceID, "platform", dev.Platform,
			"event_type", n.EventType, "member", n.Member)
		if err := f.sender.Send(ctx, dev.APNSToken, n, badge); err != nil {
			if errors.Is(err, ErrPushUnregistered) {
				f.log.Info("APNs 标记设备失效，删除登记",
					"member", n.Member, "device", dev.DeviceID, "event_type", n.EventType)
				if derr := f.st.DeletePushDevice(n.Member, dev.DeviceID); derr != nil &&
					!errors.Is(derr, store.ErrNotFound) {
					f.log.Warn("删除失效推送设备失败", "member", n.Member,
						"device", dev.DeviceID, "cause", derr)
				}
				continue
			}
			f.log.Warn("APNs 投递失败", "device", dev.DeviceID,
				"event_type", n.EventType, "cause", err)
			continue
		}
		f.log.Info("APNs 已受理", "device", dev.DeviceID,
			"event_type", n.EventType, "member", n.Member)
	}
}

// decisionOpenedPayload 是 EvDecisionOpened 的载荷（ledger/decisions.go 写入处）。
type decisionOpenedPayload struct {
	DecisionID int64  `json:"decision_id"`
	Body       string `json:"body"`
}

// cardDeepLink 把卡号折成控制台深链；无卡落工作台（plan 决策 D4）。
func cardDeepLink(cardID string) string {
	if cardID == "" {
		return "/"
	}
	return "/cards?card=" + cardID
}

// classifyLedgerEvent 把 card_events 侧事件映射成「需要你」通知。
//
// 参数：ev 账本事件；consoleMember 当前控制台成员（user:<name>，空串则不推）。
// 返回：(通知, 是否触发)。触发面 = decision_opened / room_message(@我) /
// needs_human；其余类型与空成员一律 false——**未知类型不默认推送**，防未来
// 新事件类型静默变成推送（plan §8 缺陷族 7）。
//
// 映射表对齐收件箱三源 + needs_human（spec §架构边界：别只挂 needs_human）；
// 标题复用 roomsapi.go 的 bodyTitle（首行+截断），横幅与工作台标题同源。
func classifyLedgerEvent(ev proto.LedgerEvent, consoleMember string) (proto.PushNotification, bool) {
	if consoleMember == "" {
		return proto.PushNotification{}, false
	}
	switch ev.Type {
	case "decision_opened":
		var p decisionOpenedPayload
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			return proto.PushNotification{}, false
		}
		return proto.PushNotification{
			EventType: proto.InboxOriginDecision, Member: consoleMember,
			Title: bodyTitle(p.Body), CardID: ev.CardID,
			RefID: strconv.FormatInt(p.DecisionID, 10), DeepLink: cardDeepLink(ev.CardID),
		}, true
	case "room_message":
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			return proto.PushNotification{}, false
		}
		if !containsMember(msg.Mentions, consoleMember) {
			return proto.PushNotification{}, false
		}
		return proto.PushNotification{
			EventType: proto.InboxOriginMention, Member: consoleMember,
			Title: "@你：" + bodyTitle(msg.Body), CardID: ev.CardID,
			RefID: strconv.FormatInt(ev.Seq, 10), DeepLink: cardDeepLink(ev.CardID),
		}, true
	case pushEventTypeNeedsHuman:
		return proto.PushNotification{
			EventType: pushEventTypeNeedsHuman, Member: consoleMember,
			Title: "卡等待人工", CardID: ev.CardID,
			RefID: strconv.FormatInt(ev.Seq, 10), DeepLink: cardDeepLink(ev.CardID),
		}, true
	default:
		return proto.PushNotification{}, false
	}
}

// classifyTaskEvent 把 store.events 侧事件映射成「需要你」通知。
//
// 触发面 = permission_request / question（与收件箱 ticket 源同源）；其余
// （approver_decision / ticket_answered 等「已被处理」的事件）一律 false。
// 事件载荷不含卡号，故深链落工作台「需要你处理」（spec 验收② 兜底路径）。
func classifyTaskEvent(e proto.Event, consoleMember string) (proto.PushNotification, bool) {
	if consoleMember == "" {
		return proto.PushNotification{}, false
	}
	switch e.Type {
	case proto.EventTypePermissionRequest:
		return proto.PushNotification{
			EventType: proto.InboxOriginTicket, Member: consoleMember,
			Title: "权限工单待答复", RefID: e.TaskID, DeepLink: "/",
		}, true
	case proto.EventTypeQuestion:
		return proto.PushNotification{
			EventType: proto.InboxOriginTicket, Member: consoleMember,
			Title: "提问工单待答复", RefID: e.TaskID, DeepLink: "/",
		}, true
	default:
		return proto.PushNotification{}, false
	}
}

// containsMember 判定 mentions 是否含目标成员（统一记法精确比对，不模糊匹配）。
func containsMember(mentions []string, member string) bool {
	for _, m := range mentions {
		if m == member {
			return true
		}
	}
	return false
}
