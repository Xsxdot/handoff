// wakeconsumer.go —— K5 账本事件到 keystone 唤醒的唯一消费者。
//
// 职责：读取 card_events 全流，解包 task_mirrored/room_message，过滤非唤醒事件，
// 按卡合并为一次 Wake；成功后推进 seq 并记录 seen，防游标回退重复唤醒。
// 边界：不写 ledger schema、不解释 task 状态、不决定 attach/rebuild。
package agentd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// wakeClaimTTL 是唤醒认领租期：短于 agentd stalltimeout（默认 2h），
// 够一台机器崩溃后被另一台接管；不设无限期，避免认领行永久挡住宿主游标。
// 变量而非 const（B399 r2）：测试缩到秒级验证「回合内续租 / 收尾停续」。
var wakeClaimTTL = 5 * time.Minute

// wakeClaimRenewInterval 是回合存活期间认领续租的心跳节拍（B399 r2 §3）：取既有
// DriverLeaseRenewInterval 的量级 2m，远小于 wakeClaimTTL(5m)，保证租约不失效。
var wakeClaimRenewInterval = ledger.DriverLeaseRenewInterval

const (
	// wakeRetryBackoff 是同卡同 seq 失败后的退避窗（契约 §3.5.4）。取 30s：
	// 长于一次 attach/重建的常见完成时间，短到不让人等太久；同 seq 退避期内
	// 不重复试跑，防止失败事件再引发唤醒的自激。
	wakeRetryBackoff = 30 * time.Second
	// wakeRetryMax 是同卡连续失败的重试上限；超过后按失败次数线性延长退避，
	// 不再自动无限试跑。
	wakeRetryMax = 3
	// automationStallEscalateAfter 是同卡连续「协调者准入满员」的升级阈值：达到即
	// 落一次 needs_human，让人看见唤醒链停摆（B390 spec §4.2「不允许静默」）。
	// 节拍沿用既有 automationPollInterval=2s（P4）：不设秒级退避，否则 T1 回路
	// 背靠背 3 轮内转不绿；「按节拍重试」的字面节拍就是轮询本身。
	automationStallEscalateAfter = 3
)

// admissionStalled 只认「协调者准入满员」这一可恢复排队态（B390 R1）：账本故障、
// 承载缺失、席位非法等非准入错误不进重试与升级，避免把真实故障说成满员。
func admissionStalled(err error) bool {
	var adm *coordinatorAdmissionError
	return errors.As(err, &adm) && errors.Is(err, scheduling.ErrNoSlot)
}

// recordAdmissionStall 记一次连续准入失败并留可见痕：每次失败都 Warn（2s 节拍
// 可见），连续失败恰达到阈值那一次落一条 needs_human（按「次数==阈值」去重，
// 之后继续重试不再重复落，避免每次刷屏）。返回累计连续失败次数。
//
// 重试载体是 B389 的认领挡水位，不是本表：准入失败时调用方**不**
// completeWakeBatch、**不**标 seen，认领留在飞挡住终局前缀水位，下一轮重读到、
// 重新尝试（P2）。本表只做升级阈值计数。
func (s *Server) recordAdmissionStall(card string) int {
	s.automationMu.Lock()
	if s.automationStall == nil {
		s.automationStall = make(map[string]int)
	}
	s.automationStall[card]++
	attempts := s.automationStall[card]
	s.automationMu.Unlock()

	s.log.Warn("自动化唤醒准入失败，保留认领按节拍重试", "card", card, "attempts", attempts)
	if attempts != automationStallEscalateAfter {
		return attempts
	}
	reason := fmt.Sprintf("自动化唤醒持续准入失败 %d 次：协调者小队名额被占满，唤醒链停摆，请人工处置"+
		"（可 handoff squad list --json 查看运行位残留、或等待释放）", attempts)
	if err := s.ledger.MarkNeedsHuman(card, reason, "agentd"); err != nil {
		s.log.Error("准入停摆落等人失败", "card", card, "attempts", attempts, "cause", err)
		return attempts
	}
	s.log.Error("准入停摆已落 needs_human", "card", card, "attempts", attempts)
	return attempts
}

// clearWakeStall 在唤醒成功或遇非准入错误时清掉该卡的连续准入失败计数，使下一次
// 停摆重新走一遍「累计到阈值落一次」的节奏。
func (s *Server) clearWakeStall(card string) {
	s.automationMu.Lock()
	delete(s.automationStall, card)
	s.automationMu.Unlock()
}

// wakeBackoff 记录一张卡的退避状态。
type wakeBackoff struct {
	Seq      int64
	Until    time.Time
	Attempts int
}

// maxSeqOf 返回一批 seq 的最大值（三行纯函数，退避按同卡最大 seq 记账）。
func maxSeqOf(seqs []int64) int64 {
	var max int64
	for _, seq := range seqs {
		if seq > max {
			max = seq
		}
	}
	return max
}

// shouldSkipByBackoff 判断该卡本批最大 seq 是否仍在退避窗内（同 seq 不重复试跑）。
func (s *Server) shouldSkipByBackoff(card string, seq int64) bool {
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	state, ok := s.automationBackoff[card]
	if !ok || state.Seq != seq {
		return false
	}
	return time.Now().Before(state.Until)
}

// recordWakeBackoff 记录一次失败退避；超过 wakeRetryMax 后按失败次数线性延长
// 退避窗，且失败已由 keystone 的 Escalated 路径落 needs_human 展示。
func (s *Server) recordWakeBackoff(card string, seq int64) {
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	if s.automationBackoff == nil {
		s.automationBackoff = make(map[string]wakeBackoff)
	}
	state := s.automationBackoff[card]
	if state.Seq != seq {
		state = wakeBackoff{Seq: seq}
	}
	state.Attempts++
	delay := wakeRetryBackoff
	if state.Attempts > wakeRetryMax {
		delay = wakeRetryBackoff * time.Duration(state.Attempts)
	}
	state.Until = time.Now().Add(delay)
	s.automationBackoff[card] = state
	s.log.Info("唤醒失败进入退避", "card", card, "seq", seq,
		"attempts", state.Attempts, "until", state.Until.Format(time.RFC3339))
}

type mirroredTaskEnvelope struct {
	Node     *string         `json:"node"`
	Attempt  *string         `json:"attempt"`
	TaskType string          `json:"task_type"`
	Payload  json.RawMessage `json:"payload"`
}

func decodeMirroredTaskEnvelope(ev proto.LedgerEvent) (mirroredTaskEnvelope, error) {
	var envelope mirroredTaskEnvelope
	if ev.Type != ledger.EvTaskMirrored {
		return envelope, fmt.Errorf("事件 %d 不是 task_mirrored: %s", ev.Seq, ev.Type)
	}
	if err := json.Unmarshal(ev.Payload, &envelope); err != nil {
		return envelope, fmt.Errorf("事件 %d 的 task_mirrored envelope 解码失败: %w", ev.Seq, err)
	}
	if envelope.TaskType == "" || len(envelope.Payload) == 0 {
		return envelope, fmt.Errorf("事件 %d 的 task_mirrored 缺 task_type/payload", ev.Seq)
	}
	return envelope, nil
}

func mirroredTaskTypeAndPayload(ev proto.LedgerEvent) (string, json.RawMessage, error) {
	envelope, err := decodeMirroredTaskEnvelope(ev)
	if err != nil {
		return "", nil, err
	}
	return envelope.TaskType, envelope.Payload, nil
}

// acceptsCurrentWorkflowAttempt 是 task_mirrored 唤醒前的身份闸。
// 判定正文（含 B370 降级三分支）由 internal/client#JudgeMirroredWake 一处承载；
// 本方法只做 envelope 解码、把事件投影成 client.WakeGateEvent，并按 reason 落日志、
// 把判定结果翻译成消费循环的布尔出口。不匹配的事件仍会进入 automationSeen，保留审计
// 但不会唤醒新尝试；缺失与空值使用指针区分，避免旧 envelope 被零值误判为有效身份。
func (s *Server) acceptsCurrentWorkflowAttempt(ev proto.LedgerEvent) (bool, error) {
	envelope, err := decodeMirroredTaskEnvelope(ev)
	if err != nil {
		s.log.Error("task_mirrored envelope 无法进入当前 attempt 闸", "card", ev.CardID,
			"seq", ev.Seq, "type", ev.Type, "cause", err)
		return false, fmt.Errorf("卡 %s task_mirrored seq=%d type=%s: %w", ev.CardID, ev.Seq, ev.Type, err)
	}
	if s.ledger == nil {
		return false, fmt.Errorf("卡 %s task_mirrored seq=%d: 账本未装配", ev.CardID, ev.Seq)
	}
	decision, err := client.JudgeMirroredWake(s.ledger, client.WakeGateEvent{
		CardID: ev.CardID, Seq: ev.Seq, Node: envelope.Node, Attempt: envelope.Attempt,
		TaskType: envelope.TaskType, SourceTask: ev.SourceTask,
		SourceTarget: ev.SourceTarget, SourceSeq: ev.SourceSeq,
	})
	if err != nil {
		s.log.Error("task_mirrored 身份闸判定失败", "card", ev.CardID, "seq", ev.Seq,
			"type", ev.Type, "cause", err)
		return false, err
	}
	s.log.Info("task_mirrored 身份闸判定", "card", ev.CardID, "seq", ev.Seq,
		"type", ev.Type, "task_type", envelope.TaskType, "deliver", decision.Deliver,
		"reason", string(decision.Reason))
	return decision.Deliver, nil
}

// automationWakeEvent 是非 room_message 事件的映射；false 表示合法但不唤醒；
// 摘要最多 400 rune，原事件仍在账本。无卡事件是结构事实（session_*/driver_*
// 等），条 31 不唤醒任何人——该语义由 automationWakeEvents 的非 room_message
// 分支承担，本函数不再设卡闸。
func automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error) {
	switch ev.Type {
	case ledger.EvTaskMirrored:
		taskType, payload, err := mirroredTaskTypeAndPayload(ev)
		if err != nil {
			return keystone.WakeEvent{}, false, err
		}
		if !client.WaitDeliveryPolicy(proto.EventType(taskType)) {
			slog.Default().Debug("task_mirrored 因任务消费策略过滤", "seq", ev.Seq,
				"card", ev.CardID, "type", ev.Type, "task_type", taskType,
				"reason", "delivery_policy_false")
			return keystone.WakeEvent{}, false, nil
		}
		kind := keystone.WakeTaskTerminal
		if proto.EventType(taskType) == proto.EventTypePermissionRequest ||
			proto.EventType(taskType) == proto.EventTypeQuestion {
			kind = keystone.WakeTicket
		}
		return keystone.WakeEvent{
			Kind: kind, Card: ev.CardID,
			Summary: fmt.Sprintf("%s: %s", taskType, truncateRunes(string(payload), 400)),
		}, true, nil
	case ledger.EvStatusMoved:
		var moved struct {
			To string `json:"to"`
		}
		if err := json.Unmarshal(ev.Payload, &moved); err == nil {
			// 终态关 TUI 在消费循环里做，不经 Wake。
			return keystone.WakeEvent{}, false, nil
		}
		return keystone.WakeEvent{}, false, nil
	case ledger.EvNeedsCleared:
		// B394：清标是 needs_human 的状态翻转（注意力平面），与等人同族，不唤醒。
		// 唤醒它会让协调者自己的账务动作把自己叫醒（清→重打的回声乒乓，真机 B382
		// 17726 needs_cleared → 新一轮唤醒 → 17727 keystone 重打 needs_human）。
		// 展示通路（card wait / 会话列表 needsHumanByCard）不经本函数，不受影响。
		slog.Default().Debug("needs_cleared 不唤醒：清标是注意力平面状态翻转，防回声自激",
			"seq", ev.Seq, "card", ev.CardID, "type", ev.Type,
			"reason", "needs_cleared_not_actionable")
		return keystone.WakeEvent{}, false, nil
	case ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
		return keystone.WakeEvent{
			Kind: keystone.WakeTaskTerminal, Card: ev.CardID,
			Summary: fmt.Sprintf("%s: %s", ev.Type, truncateRunes(string(ev.Payload), 400)),
		}, true, nil
	case ledger.EvNeedsHuman, ledger.EvSeatBearingMissing:
		// 判据收口（契约 §3.5.1）：等人与缺承载不唤醒协调者；两条展示通路
		// （卡级订阅 / 会话列表待办）由既有消费者承担，本函数只保证不唤醒。
		return keystone.WakeEvent{}, false, nil
	default:
		return keystone.WakeEvent{}, false, nil
	}
}

// decodeRoomMessageForWake 解码 room_message 并判定是否为可寻址的人类发言形状。
// 返回 human=false 的合法情形：by_system/pointer 系统结构行（恒不唤醒，契约
// 条 26/31）与 by_system:null 旧边界（B353 锁定，TestB353AutomationRoom* 族）。
// kind 不再是唤醒条件——kind 白名单已废止（S1，发言自由）；广播形状已删
// （条 30），唤醒与否由寻址判定决定。解码失败按错误上抛（消费循环报错）。
func decodeRoomMessageForWake(ev proto.LedgerEvent) (proto.RoomMessage, bool, error) {
	var msg proto.RoomMessage
	if err := json.Unmarshal(ev.Payload, &msg); err != nil {
		return proto.RoomMessage{}, false,
			fmt.Errorf("事件 %d 的 room_message 解码失败: %w", ev.Seq, err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(ev.Payload, &fields); err != nil {
		return proto.RoomMessage{}, false,
			fmt.Errorf("事件 %d 的 room_message 字段解码失败: %w", ev.Seq, err)
	}
	if raw, present := fields["by_system"]; present &&
		bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return msg, false, nil
	}
	if msg.BySystem || msg.Kind == proto.RoomMsgPointer {
		return msg, false, nil
	}
	return msg, true, nil
}

// automationWakeEvents 是消费循环的统一映射入口（B358.3 改接寻址后）。
// 会话消息（无卡事件）不再被首行卡闸一刀切拦下——改走 roomMessageWakeEvents
// 寻址分流（条 29/30）；needs_human 先经 desktopNeedsHumanWakes（B434 P5=B：只叫醒
// 桌面端 bind 席位）；其余事件类型的映射沿用 automationWakeEvent，其中
// 无卡事件是结构事实（session_*/driver_* 等），条 31 不唤醒任何人。
func (s *Server) automationWakeEvents(ev proto.LedgerEvent) ([]keystone.WakeEvent, error) {
	if ev.Type == ledger.EvRoomMessage {
		return s.roomMessageWakeEvents(ev)
	}
	if ev.CardID == "" {
		s.log.Debug("无卡结构事件不唤醒", "seq", ev.Seq, "type", ev.Type)
		return nil, nil
	}
	if ev.Type == ledger.EvNeedsHuman {
		// B434 P5=B（契约 §3.8.1）：needs_human 先于纯函数分流——桌面端 bind 席位按排除规则
		// 唤醒；coordinate 与无记录 bind 由 desktopNeedsHumanWakes 返回空、维持 B394。纯函数
		// automationWakeEvent 不改（条 192）。消费循环与转交收件 handleCoordWake 同经本入口。
		return s.desktopNeedsHumanWakes(ev)
	}
	wake, yes, err := automationWakeEvent(ev)
	if err != nil {
		return nil, err
	}
	if !yes {
		return nil, nil
	}
	return []keystone.WakeEvent{wake}, nil
}

// roomMessageWakeEvents 是会话消息按匹配函数地址与席位模式的分流（B435 契约 §4.4
// C-229–C-233；B358 条 47 修订）。判定唯一入口 collab.Service.Recipients（agentd 不解析
// mentions、不自带默认收件人规则）。分流：
//   - 席位地址且该卡 mode=push → 恰一次 WakeMessage{Card:该卡}（同卡去重）；
//   - 席位地址且 mode=pull → 零 keystone Wake（由 card wait 交付）；P4 乙过渡：该卡仍有
//     B434 桌面端记录时照旧产出 WakeMessage，交给 wakeCoordinatorRoundID 的 pull 分支
//     走既有信箱（读记录不读来源词），退役归 X 子卡；
//   - 驻场者地址 → 零 keystone Wake（门铃信桥归 O2 子卡），Info 可查。
//
// 房间消息唤醒不沿 parent 上走（C-232）：产出的唤醒卡即命中地址的卡，消费循环对
// WakeMessage 不调 resolveWakeCard。by_system:null 旧边界在解码层按结构行跳过
// （Recipients 收已解码消息，看不到 null）。匹配函数读账本失败上抛（C-228：水位不越过）。
func (s *Server) roomMessageWakeEvents(ev proto.LedgerEvent) ([]keystone.WakeEvent, error) {
	msg, human, err := decodeRoomMessageForWake(ev)
	if err != nil || !human {
		return nil, err
	}
	if s.rooms == nil {
		s.log.Error("会话消息寻址判定不可用：collab 服务未装配，消息不唤醒",
			"seq", ev.Seq, "room", msg.Room)
		return nil, nil
	}
	delivery, err := s.rooms.Recipients(msg, ev.Actor)
	if err != nil {
		s.log.Warn("会话消息匹配函数判定失败，本轮不越过该消息", "seq", ev.Seq, "room", msg.Room, "cause", err)
		return nil, fmt.Errorf("事件 %d 的会话寻址判定失败: %w", ev.Seq, err)
	}
	if len(delivery.Addresses) == 0 {
		s.log.Debug("会话消息无收件地址，不唤醒", "seq", ev.Seq, "room", msg.Room,
			"reason", delivery.NoRecipient)
		return nil, nil
	}
	var wakes []keystone.WakeEvent
	seenCard := make(map[string]bool, len(delivery.Addresses))
	for _, addr := range delivery.Addresses {
		if addr.Kind != proto.DeliverySeat {
			s.log.Info("会话消息命中驻场者地址：不经 keystone（门铃信桥归后续子卡）",
				"seq", ev.Seq, "session", addr.Session, "identity", addr.Identity, "default", delivery.Default)
			continue
		}
		if seenCard[addr.Card] {
			continue
		}
		seenCard[addr.Card] = true
		wake, ok, err := s.seatAddressWake(ev, msg, addr)
		if err != nil {
			return nil, err
		}
		if ok {
			wakes = append(wakes, wake)
		}
	}
	return wakes, nil
}

// seatAddressWake 按席位地址所在卡的当前模式决定是否产出 keystone 唤醒（见
// roomMessageWakeEvents 分流表）。读卡或读桌面端记录失败上抛：判定读失败不得被当作
// 「不唤醒」吞掉，否则水位越过该消息。
func (s *Server) seatAddressWake(ev proto.LedgerEvent, msg proto.RoomMessage, addr proto.DeliveryAddress) (keystone.WakeEvent, bool, error) {
	card, err := s.autoLedger.GetCard(addr.Card)
	if err != nil {
		s.log.Warn("命中席位地址的卡读取失败，本轮不越过该消息", "seq", ev.Seq, "card", addr.Card, "cause", err)
		return keystone.WakeEvent{}, false, fmt.Errorf("事件 %d 读命中卡 %s: %w", ev.Seq, addr.Card, err)
	}
	if card.DriverSession != addr.Identity {
		s.log.Info("命中席位已换人，本条不唤醒", "seq", ev.Seq, "card", addr.Card)
		return keystone.WakeEvent{}, false, nil
	}
	wake := keystone.WakeEvent{Kind: keystone.WakeMessage, Card: addr.Card, Summary: truncateRunes(msg.Body, 400)}
	switch proto.SeatMode(card.DriverMode) {
	case proto.SeatModePush:
		s.log.Debug("会话消息命中 push 席位", "seq", ev.Seq, "card", addr.Card)
		return wake, true, nil
	case proto.SeatModePull:
		_, hasDesktop, derr := s.ledger.SeatDesktopOf(addr.Card)
		if derr != nil {
			s.log.Warn("pull 席位读桌面端记录失败，本轮不越过该消息", "seq", ev.Seq, "card", addr.Card, "cause", derr)
			return keystone.WakeEvent{}, false, fmt.Errorf("事件 %d 读卡 %s 桌面端记录: %w", ev.Seq, addr.Card, derr)
		}
		if hasDesktop {
			s.log.Info("pull 席位有 B434 桌面端记录：过渡分支沿用信箱", "seq", ev.Seq, "card", addr.Card)
			return wake, true, nil
		}
		s.log.Info("会话消息命中 pull 席位：零回合，由 card wait 交付", "seq", ev.Seq, "card", addr.Card)
		return keystone.WakeEvent{}, false, nil
	default:
		s.log.Warn("命中席位模式非法，不唤醒", "seq", ev.Seq, "card", addr.Card, "mode", card.DriverMode)
		return keystone.WakeEvent{}, false, nil
	}
}

const wakeParentWalkLimit = 32

// WakeRoundDedupePrefix 是唤醒回合注释的 dedupe_key 前缀（B393 spec §4.3：唤醒
// 回合留一行在途读数）。落账走 ledger.Store.EnsureComment（type=EvComment），
// 键形如 "wake_round:start:<roundID>" / "wake_round:end:<roundID>" /
// "wake_round:fail:<roundID>"。
//
// 口径修订（B393 复评 6d85425a，修订号 r2；review-4 确认键=请求身份）：推翻
// 「每次唤醒恰一行」的旧说法。现行定义——**一轮** = 一次逻辑唤醒从打开 roundID
// 到写终态（end/fail 且不再被同一请求重试）的账本痕迹单元。队列路径
// （drainIgnitionRequest）上，**同一 IgnitionRequest** 的失败回填 2s 重试共享
// 同一 roundID（键按 wakeQueueRoundKey 请求身份分组，同卡换节点是新请求、
// 新开一轮），故 fail/start 不随重试增行（一轮一组为上限）；非队列路径
// （事件批次、HTTP 转交端点）每次进入 wakeCoordinatorRoundRaw 即新开一轮，
// 跨轮各得一组、不吞行。
const WakeRoundDedupePrefix = "wake_round"

// wakeRoundSeq 是进程内唤醒轮次序号，与 UnixNano 拼成 roundID。
var wakeRoundSeq atomic.Uint64

// nextWakeRoundID 返回本轮唤醒的轮次标识：墙钟纳秒 + 进程内单调序号。
// 为什么两者都要：只用墙钟在同纳秒内两轮会撞键（测试连续两轮必现）；
// 只用序号在 agentd 重启后会与历史键冲突（EnsureComment 按键幂等，撞键=吞行）。
func nextWakeRoundID() string {
	return strconv.FormatInt(time.Now().UnixNano(), 10) + "-" +
		strconv.FormatUint(wakeRoundSeq.Add(1), 10)
}

// WakeRoundEvent 是写入注释正文的 payload 形状（序列化边界：json.Marshal 进
// EnsureComment 的 body、消费方按需 json.Unmarshal；见 TestB393WakeRoundEventRoundTrip）。
// 字段用指针：nil = 该回合未提供该字段（如未开始就失败无 duration_ms），
// 非 nil 的零值 = 确有该读数且为零——两态不可混。
type WakeRoundEvent struct {
	Phase      string  `json:"phase"`                 // "start" | "end" | "fail"
	Class      *string `json:"class,omitempty"`       // 新增：timeout/session_not_found/other（B399 r2）
	Session    *string `json:"session,omitempty"`     // nil=未知
	Err        *string `json:"err,omitempty"`         // nil=无错误
	DurationMs *int64  `json:"duration_ms,omitempty"` // nil=未计时
}

// ptrInt64 返回 v 的地址，供 WakeRoundEvent.DurationMs 区分「缺失」与「零」。
func ptrInt64(v int64) *int64 { return &v }

// wakeFailClass 把失败的分类映成账本/日志用词（B399 r2 §5③）：超时、会话不存在、
// 其他三态可区分。
func wakeFailClass(err error) string {
	switch {
	case err == nil:
		return wakeFailClassOther
	case errors.Is(err, keysclient.ErrTurnTimeout):
		return wakeFailClassTimeout
	case errors.Is(err, keysclient.ErrSessionNotFound):
		return wakeFailClassSessionNotFound
	default:
		return wakeFailClassOther
	}
}

const (
	wakeFailClassTimeout         = "timeout"
	wakeFailClassSessionNotFound = "session_not_found"
	wakeFailClassOther           = "other"
)

// writeWakeRoundFail 落一行 phase=fail 的 wake_round 注释（键含 roundID，含 class）。
// 写失败只留 Warn，不覆盖唤醒结果本身。恰一次「需要人」（B399 r2 §5）：仅当 class=timeout
// 且本行是本轮**首次**写入（EnsureComment 返回 wrote=true）——同轮 2s 重试的重复写
// (wrote=false) 不重复刷屏。
func (s *Server) writeWakeRoundFail(card, roundID, class, reason string) {
	if s.ledger == nil {
		s.log.Error("唤醒回合失败行未落账：账本未装配", "card", card, "round_id", roundID, "reason", reason)
		return
	}
	payload, err := json.Marshal(WakeRoundEvent{Phase: "fail", Class: &class, Err: &reason})
	if err != nil {
		s.log.Error("唤醒回合失败行序列化失败", "card", card, "round_id", roundID, "cause", err)
		return
	}
	wrote, werr := s.ledger.EnsureComment(card, WakeRoundDedupePrefix+":fail:"+roundID,
		string(payload), "agentd")
	if werr != nil {
		s.log.Warn("唤醒回合失败事件写入失败（不覆盖唤醒结果）", "card", card,
			"round_id", roundID, "cause", werr)
		return
	}
	if class == wakeFailClassTimeout && wrote {
		reasonText := fmt.Sprintf("协调者唤醒回合超时（会话已保留，下一轮仍续接同一会话）：%s", reason)
		if nerr := s.ledger.MarkNeedsHuman(card, reasonText, "agentd"); nerr != nil {
			s.log.Error("唤醒超时落 needs_human 失败", "card", card, "round_id", roundID, "cause", nerr)
		}
	}
}

// resolveWakeCard 把卡自动化事件的唤醒目标从事件卡收到真正能被叫醒的席位卡
// （B435 C-235–C-237：按席位模式判，来源词只作 ValidateSeat 实参）。
// 事件卡自己是 push 席位 → 自己；事件卡是 pull：有 B434 桌面端记录（P4 乙过渡）→ 自己，
// 无记录 → 空（不起回合、不上走，pull 靠 card wait）；空座沿 parent_id 上走，无记录的
// pull 祖先跳过，push 祖先与有桌面端记录的 pull 祖先接手。非空座而模式非法、或 push
// 席位身份非法 → 按非法席位继续上走（Warn）。读桌面端记录失败：起始卡 Warn + 空（同读卡
// 失败）；祖先 Warn 后按无记录继续上走。不改账本 card_id。房间消息唤醒不经本函数（C-232）。
func (s *Server) resolveWakeCard(cardID string) string {
	if s.ledger == nil || cardID == "" {
		return ""
	}
	origin := cardID
	seen := map[string]bool{}
	for i := 0; i < wakeParentWalkLimit && cardID != ""; i++ {
		if seen[cardID] {
			s.log.Warn("自动化唤醒祖先链成环，停止上走", "origin", origin, "card", cardID)
			return ""
		}
		seen[cardID] = true
		card, err := s.ledger.GetCard(cardID)
		if err != nil {
			s.log.Warn("自动化唤醒读卡失败", "origin", origin, "card", cardID, "cause", err)
			return ""
		}
		switch proto.SeatMode(card.DriverMode) {
		case proto.SeatModePull:
			_, hasDesktop, derr := s.ledger.SeatDesktopOf(cardID)
			if derr != nil {
				s.log.Warn("自动化唤醒读桌面端席位记录失败", "origin", origin, "card", cardID, "cause", derr)
				if cardID == origin {
					return ""
				}
				// 祖先 pull 读失败：按无记录处理继续上走，保留「找更上层 push 祖先」语义。
				cardID = card.ParentID
				continue
			}
			if hasDesktop {
				return cardID
			}
			if cardID == origin {
				return ""
			}
		case proto.SeatModePush:
			if proto.ValidateSeat(card.DriverSession, proto.SeatSource(card.DriverSource)) == nil {
				return cardID
			}
			s.log.Warn("自动化唤醒遇非法 push 席位，继续上走", "origin", origin, "card", cardID)
		default:
			if card.DriverSession != "" {
				s.log.Warn("自动化唤醒遇模式非法的非空座，按非法席位继续上走", "origin", origin,
					"card", cardID, "mode", card.DriverMode)
			}
		}
		cardID = card.ParentID
	}
	return ""
}

// wakeClaimHolder 是本机器 + 本 agentd 实例的认领者标识（契约 §3.1 holder）：
// 机器名 + 进程号，供他机日志串联与「同持有者可续期」判定。
func (s *Server) wakeClaimHolder() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "agentd"
	}
	return fmt.Sprintf("%s#%d", host, os.Getpid())
}

// claimWakeBatch 在占名额与试跑之前逐 seq 认领本批事件（契约 §3.1.4）；拿不到
// 的 seq 表示他机持有，本机跳过、不占名额、不发起回合。返回实际拿到的 seq。
func (s *Server) claimWakeBatch(card string, seqs []int64) ([]int64, error) {
	if s.ledger == nil {
		return nil, fmt.Errorf("唤醒认领缺少账本")
	}
	holder := s.wakeClaimHolder()
	claimed := make([]int64, 0, len(seqs))
	for _, seq := range seqs {
		got, err := s.ledger.ClaimWake(seq, card, holder, wakeClaimTTL)
		if err != nil {
			return claimed, fmt.Errorf("认领唤醒事件 seq=%d card=%s: %w", seq, card, err)
		}
		if !got {
			s.log.Info("唤醒事件已被他机认领，本机跳过", "seq", seq, "card", card, "holder", holder)
			continue
		}
		claimed = append(claimed, seq)
	}
	return claimed, nil
}

// completeWakeBatch 收尾某张卡本批认领（key 为 (card,seq)）；失败只留日志，
// 不覆盖唤醒结果（认领行会在租约到期后被他机接管，不会永久挡路）。
func (s *Server) completeWakeBatch(card string, seqs []int64) {
	if s.ledger == nil {
		return
	}
	holder := s.wakeClaimHolder()
	for _, seq := range seqs {
		if err := s.ledger.CompleteWake(seq, card, holder); err != nil {
			s.log.Error("唤醒认领收尾失败", "seq", seq, "card", card, "holder", holder, "cause", err)
			continue
		}
		s.log.Info("唤醒认领收尾完成", "seq", seq, "card", card, "holder", holder)
	}
}

// startWakeClaimRenewal 在回合存续期间按 wakeClaimRenewInterval 续租本批认领，
// 返回停止函数（幂等，必须 defer/显式调用）。回合收尾即停续：认领回到 5m 窗口，
// 崩溃时他机 5m 后可接管（B399 r2 §5，恢复窗口不变）。
// 为什么复用 ClaimWake：它的语义是「同 holder 未终局即 upsert 续期」（wakeclaim.go:52），
// 正是续租所需，无需新增账本方法。
// B399 review-1 race 修复：(a) ttl/interval 在调用方 goroutine 上读包级 var 后
// 捕获为本地副本，续租 goroutine 不再读包级变量——测试 defer/t.Cleanup 还原全局
// 与 goroutine 之间不再有共享写读；(b) stop() 关闭 done 后等待 goroutine 真正
// 退出（stopped 通道）再返回——stop 返回后保证无人再碰 ttl/interval。
func (s *Server) startWakeClaimRenewal(card string, seqs []int64) func() {
	if s.ledger == nil || len(seqs) == 0 {
		return func() {}
	}
	ttl, interval := wakeClaimTTL, wakeClaimRenewInterval
	done := make(chan struct{})
	stopped := make(chan struct{})
	var once sync.Once
	holder := s.wakeClaimHolder()
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				for _, seq := range seqs {
					got, err := s.ledger.ClaimWake(seq, card, holder, ttl)
					if err != nil {
						s.log.Warn("唤醒认领续租失败", "card", card, "seq", seq, "cause", err)
						continue
					}
					if !got {
						s.log.Warn("唤醒认领续租未生效（可能已收尾或被接管）", "card", card, "seq", seq)
					}
				}
			}
		}
	}()
	stop := func() {
		once.Do(func() { close(done) })
		<-stopped
	}
	return stop
}

// advanceAutomationCursorWatermark 把候选水位收紧成终局前缀水位再推进：在飞的
// 认领挡住推进，避免对端崩溃后该事件被永久跳过（契约 §3.1.3）。
func (s *Server) advanceAutomationCursorWatermark(from, candidate int64) error {
	if s.ledger == nil {
		return s.advanceAutomationCursor(candidate)
	}
	w, err := s.ledger.CursorWatermark(from, candidate)
	if err != nil {
		s.log.Error("终局前缀水位计算失败，不推进游标", "from", from, "candidate", candidate, "cause", err)
		return err
	}
	return s.advanceAutomationCursor(w)
}

func (s *Server) consumeAutomationEventsOnce(ctx context.Context) (processed int, escalated bool, err error) {
	if s.autoLedger == nil || s.keystone == nil {
		s.log.Error("自动化事件消费失败：依赖尚未装配",
			"has_ledger", s.autoLedger != nil, "has_keystone", s.keystone != nil)
		return 0, false, fmt.Errorf("自动化事件消费：依赖未装配")
	}
	s.automationMu.Lock()
	from := s.automationCursor
	if s.automationSeen == nil {
		s.automationSeen = make(map[int64]struct{})
	}
	s.automationMu.Unlock()
	var firstWakeErr error
	events, err := s.autoLedger.EventsFromAsc(nil, from, 500)
	if err != nil {
		s.log.Error("读自动化账本事件失败", "cursor", from, "cause", err)
		return 0, false, fmt.Errorf("读自动化账本事件失败 cursor=%d: %w", from, err)
	}
	s.log.Debug("读取自动化账本事件", "cursor", from, "event_count", len(events))
	type pending struct {
		seq int64
		typ string
		ev  keystone.WakeEvent
		raw proto.LedgerEvent
	}
	pendingByCard := map[string][]pending{}
	maxProcessed := from
	for _, ev := range events {
		if ev.Seq <= from {
			s.log.Debug("自动化事件不在游标之后，跳过", "seq", ev.Seq,
				"card", ev.CardID, "type", ev.Type, "cursor", from)
			continue
		}
		s.automationMu.Lock()
		_, duplicate := s.automationSeen[ev.Seq]
		s.automationMu.Unlock()
		if duplicate {
			if ev.Seq > maxProcessed {
				maxProcessed = ev.Seq
			}
			s.log.Debug("自动化事件已见，跳过重复消费", "seq", ev.Seq,
				"card", ev.CardID, "type", ev.Type, "cursor", from,
				"cursor_candidate", maxProcessed, "reason", "seen")
			continue
		}
		if ev.Type == ledger.EvTaskMirrored {
			accepted, gateErr := s.acceptsCurrentWorkflowAttempt(ev)
			if gateErr != nil {
				return processed, escalated, gateErr
			}
			if !accepted {
				if ev.Seq > maxProcessed {
					maxProcessed = ev.Seq
				}
				s.automationMu.Lock()
				s.automationSeen[ev.Seq] = struct{}{}
				s.automationMu.Unlock()
				s.log.Debug("自动化事件因身份闸被标记 seen", "seq", ev.Seq,
					"card", ev.CardID, "type", ev.Type, "cursor", from,
					"cursor_candidate", maxProcessed, "reason", "wake_gate_rejected")
				continue
			}
		}
		wakes, mapErr := s.automationWakeEvents(ev)
		if mapErr != nil {
			s.log.Error("自动化账本事件映射失败", "seq", ev.Seq, "card", ev.CardID,
				"type", ev.Type, "cause", mapErr)
			return processed, escalated, mapErr
		}
		// B434 契约 r12 第 4 条末句：席位换绑（driver_takeover / driver_seat_bound）或桌面端记录
		// 重写（seat_desktop_set）意味着该卡的唤醒去向变了——旧桌面端失败段（退避窗、已落标计数、
		// 段轮次）不得被新席位继承，在此清掉。三者都是不唤醒的结构事件，下面照常标 seen。
		switch {
		case ev.Type == ledger.EvSeatDesktopSet, ev.Type == ledger.EvDriverSeatBound,
			ev.Type == ledger.EvDriverTakeover && s.driverTakeoverChangedSeat(ev):
			s.clearDesktopWakeStall(ev.CardID, "席位换绑或桌面端记录重写（"+ev.Type+"）")
		}
		// B432 旁路（plan 决策 D1）：在唤醒分类与 seen 判定之外喂扇出——
		// needs_human 这类不唤醒协调者的事件也要推。只入队不阻塞，唤醒路径
		// 完全不感知它的存在（队满即丢，见 PushFanout.Notify）。
		if s.pushFanout != nil {
			s.pushFanout.OnLedgerEvent(ev)
		}
		if ev.Seq > maxProcessed {
			maxProcessed = ev.Seq
		}
		if len(wakes) > 0 {
			queued := 0
			for _, wake := range wakes {
				target := wake.Card
				if wake.Kind != keystone.WakeMessage {
					// 卡自动化事件沿 parent 找 push 席位；房间消息唤醒已按匹配函数地址定卡，
					// 不上走（C-232）。
					target = s.resolveWakeCard(wake.Card)
				}
				if target == "" {
					s.log.Debug("自动化事件无 coordinate 席位可叫醒", "card", wake.Card, "seq", ev.Seq)
					continue
				}
				if target != wake.Card {
					s.log.Info("自动化唤醒冒泡到祖先席位", "from", wake.Card, "to", target, "seq", ev.Seq)
				}
				wake.Card = target
				pendingByCard[target] = append(pendingByCard[target], pending{seq: ev.Seq, typ: ev.Type, ev: wake, raw: ev})
				queued++
				s.log.Debug("自动化事件进入 pending", "seq", ev.Seq, "card", wake.Card,
					"type", ev.Type, "kind", string(wake.Kind), "cursor", from,
					"cursor_candidate", maxProcessed)
			}
			if queued == 0 {
				s.automationMu.Lock()
				s.automationSeen[ev.Seq] = struct{}{}
				s.automationMu.Unlock()
			}
			continue
		}
		s.automationMu.Lock()
		s.automationSeen[ev.Seq] = struct{}{}
		s.automationMu.Unlock()
		s.log.Debug("自动化事件标记 seen", "seq", ev.Seq, "card", ev.CardID,
			"type", ev.Type, "cursor", from, "cursor_candidate", maxProcessed,
			"reason", "not_actionable")
	}

	cards := make([]string, 0, len(pendingByCard))
	for card := range pendingByCard {
		cards = append(cards, card)
	}
	sort.Strings(cards)
	for _, card := range cards {
		batch := pendingByCard[card]
		// 终态卡闸（§3.2 第 1 步 / §4-32）：卡已终态仍带席位（MoveCard→已完成
		// 不清席位）时不得再唤醒。必须标 seen 并 continue，不能 return——终态卡
		// 的 pending 若不消费，游标不推进、下一轮重复读到同一条，形成活锁。
		if cardRow, readErr := s.ledger.GetCard(card); readErr == nil &&
			(cardRow.Status == ledger.StatusDone || cardRow.Status == ledger.StatusClosed) {
			s.keystone.Forget(card)
			for _, item := range batch {
				s.automationMu.Lock()
				s.automationSeen[item.seq] = struct{}{}
				s.automationMu.Unlock()
			}
			s.log.Info("终态卡不再唤醒", "card", card, "status", cardRow.Status,
				"event_count", len(batch))
			continue
		}
		// 认领在占名额与试跑之前（§3.1.4）：拿不到认领的 seq 表示他机持有，
		// 本机跳过、不占名额、不发起回合。
		seqs := make([]int64, 0, len(batch))
		for _, item := range batch {
			seqs = append(seqs, item.seq)
		}
		claimed, claimErr := s.claimWakeBatch(card, seqs)
		if claimErr != nil {
			return processed, escalated, claimErr
		}
		if len(claimed) == 0 {
			continue
		}
		claimedSet := make(map[int64]bool, len(claimed))
		for _, seq := range claimed {
			claimedSet[seq] = true
		}
		evs := make([]keystone.WakeEvent, 0, len(claimed))
		raws := make([]proto.LedgerEvent, 0, len(claimed))
		claimedSeqs := make([]int64, 0, len(claimed))
		for _, item := range batch {
			if !claimedSet[item.seq] {
				continue
			}
			evs = append(evs, item.ev)
			raws = append(raws, item.raw)
			claimedSeqs = append(claimedSeqs, item.seq)
		}
		// B434 桌面端失败段退避窗（契约 §3.7、§11.3）：上次「信未落盘」后窗口未到——**不**
		// complete、**不**标 seen，认领留在飞（本轮 claimWakeBatch 已续租），窗口过后再试。
		if until, pending := s.desktopWakeRetryPending(card); pending {
			s.log.Debug("桌面端唤醒退避窗内，保留认领下轮再试", "card", card,
				"retry_at", until, "event_count", len(claimedSeqs))
			continue
		}
		// 退避闸（§3.5.4）：同卡同 seq 在退避窗内不重复试跑，认领照常收尾。
		if s.shouldSkipByBackoff(card, maxSeqOf(claimedSeqs)) {
			s.log.Info("唤醒退避窗内，跳过同 seq 重复试跑", "card", card)
			s.completeWakeBatch(card, claimedSeqs)
			continue
		}
		decision := s.keystone.Decide(evs[0])
		if !decision.Wake {
			// attach 暂缓不试跑、不收尾、不推进游标（与既有行为一致：
			// TestAutomationAttachDefersAndThenWakes 断言暂缓期 cursor 文件不写）。
			// 认领留到租约到期——它同时挡住水位推进，正好是我们要的。
			s.log.Info("自动化事件因 attach 暂缓", "card", card,
				"event_count", len(evs), "reason", decision.Reason)
			for _, item := range batch {
				s.log.Debug("自动化 pending 事件因 attach 暂缓", "seq", item.seq,
					"card", card, "type", item.typ, "cursor", from,
					"cursor_candidate", maxProcessed, "reason", decision.Reason)
			}
			return processed, escalated, nil
		}
		// 轮次标识由本循环给出：桌面端失败段内的重试复用段轮次（B393「同一逻辑唤醒的重试
		// 共享 roundID」），转交失败的 wake_round fail 行一段一行而不是每次重试一行；不在
		// 失败段则每批新开一轮（与原 wakeCoordinatorRoundRaw 同义）。
		roundID := s.desktopWakeRound(card)
		if roundID == "" {
			roundID = nextWakeRoundID()
		}
		stopRenew := s.startWakeClaimRenewal(card, claimedSeqs)
		result, wakeErr := s.wakeCoordinatorRoundID(ctx, card, evs, raws, roundID)
		stopRenew()
		if wakeErr != nil {
			s.log.Error("自动化事件批次唤醒失败", "card", card,
				"event_count", len(evs), "cause", wakeErr)
			if admissionStalled(wakeErr) {
				// B390 P2：准入满员是可恢复排队态——**不** completeWakeBatch、
				// **不**标 seen。认领留在飞挡住终局前缀水位，游标不越过该 seq，
				// 下一轮自然重读到并重试（同持有者可续期认领）。本表只累计连续
				// 失败次数，用于恰一次落 needs_human（P4）。
				s.recordAdmissionStall(card)
				if result.Escalated {
					escalated = true
				}
				if firstWakeErr == nil {
					firstWakeErr = wakeErr
				}
				continue
			}
			if desktopUndelivered(wakeErr) {
				// B434 D5「信未落盘」（本机写信/渲染失败、转交失败、一跳封顶）：与准入满员同为
				// 可恢复排队态——**不** completeWakeBatch、**不**标 seen、**不**记 automationBackoff
				// （它的跳过分支会 complete），认领留在飞挡水位，退避窗过后重试；计入独立的桌面端
				// 失败段（段内首次落 needs_human），**不**调 recordAdmissionStall（条 173/174）。
				// 设 firstWakeErr：本轮对调用方报错（竖切「信箱键不安全」子例依赖它）。
				s.recordDesktopWakeFailure(card, roundID, wakeErr)
				if firstWakeErr == nil {
					firstWakeErr = wakeErr
				}
				continue
			}
			if desktopUnopened(wakeErr) {
				// B434 D5「信已落盘、continue 未发出」：信已保住事件，重试只会多一封同批信——终局
				// 收尾（complete + 标 seen），不记 automationBackoff、不碰 automationStall；计入桌面端
				// 失败段（段内首次落 needs_human，条 109–111、169/170）。
				s.recordDesktopWakeFailure(card, roundID, wakeErr)
				s.completeWakeBatch(card, claimedSeqs)
				for _, seq := range claimedSeqs {
					s.automationMu.Lock()
					s.automationSeen[seq] = struct{}{}
					s.automationMu.Unlock()
				}
				if firstWakeErr == nil {
					firstWakeErr = wakeErr
				}
				continue
			}
			// 非准入错误维持既有终局处置（B389 §3.5.4/§4-30）：单卡失败不断整轮，
			// 退避后继续处理同批与后续卡，轮末统一推进终局前缀水位。失败已
			// CompleteWake，游标不会被它永久挡。
			s.clearWakeStall(card)
			s.completeWakeBatch(card, claimedSeqs)
			s.recordWakeBackoff(card, maxSeqOf(claimedSeqs))
			if result.Escalated {
				escalated = true
			}
			if firstWakeErr == nil {
				firstWakeErr = wakeErr
			}
			for _, seq := range claimedSeqs {
				s.automationMu.Lock()
				s.automationSeen[seq] = struct{}{}
				s.automationMu.Unlock()
			}
			continue
		}
		s.clearWakeStall(card)
		s.clearDesktopWakeStall(card, "唤醒成功")
		s.completeWakeBatch(card, claimedSeqs)
		if s.automationRoundHook != nil {
			s.automationRoundHook(card, result)
		}
		if result.Escalated {
			escalated = true
		}
		for _, seq := range claimedSeqs {
			s.automationMu.Lock()
			s.automationSeen[seq] = struct{}{}
			s.automationMu.Unlock()
			processed++
		}
		s.log.Info("自动化事件批次已唤醒", "card", card,
			"event_count", len(evs), "session", result.SessionID,
			"rebuilt", result.Rebuilt, "escalated", result.Escalated)
	}
	if cursorErr := s.advanceAutomationCursorWatermark(from, maxProcessed); cursorErr != nil {
		return processed, escalated, errors.Join(firstWakeErr, cursorErr)
	}
	s.log.Info("自动化事件消费轮完成", "from_cursor", from,
		"to_cursor", maxProcessed, "processed", processed, "escalated", escalated)
	return processed, escalated, firstWakeErr
}
