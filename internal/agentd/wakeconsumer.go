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
	"sort"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

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
	case ledger.EvNeedsHuman, ledger.EvNeedsCleared,
		ledger.EvDecisionOpened, ledger.EvDecisionAnswered:
		return keystone.WakeEvent{
			Kind: keystone.WakeTaskTerminal, Card: ev.CardID,
			Summary: fmt.Sprintf("%s: %s", ev.Type, truncateRunes(string(ev.Payload), 400)),
		}, true, nil
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
// 寻址分流（条 29/30）；其余事件类型的映射沿用 automationWakeEvent，其中
// 无卡事件是结构事实（session_*/driver_* 等），条 31 不唤醒任何人。
func (s *Server) automationWakeEvents(ev proto.LedgerEvent) ([]keystone.WakeEvent, error) {
	if ev.Type == ledger.EvRoomMessage {
		return s.roomMessageWakeEvents(ev)
	}
	if ev.CardID == "" {
		s.log.Debug("无卡结构事件不唤醒", "seq", ev.Seq, "type", ev.Type)
		return nil, nil
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

// roomMessageWakeEvents 是会话消息的寻址分流（契约 §3.8、条 47/48/50）。
// 只对会话房间（room.IsSessionRoom）的人类形状消息做寻址判定；判定唯一入口
// collab.Service.MessageWakeTargets（agentd 不解析 mentions，不建外部身份
// 注册表）。分流：
//   - target = 会话内某卡当前 driver_session → WakeMessage{Card:该卡} 恰一次
//     （同卡多命中去重）；
//   - 其余 target（外部身份/非成员/旧席位/不存在的卡）→ 零 keystone Wake，
//     Info 日志可查，未读由事件+游标天然承载（推醒走 session wait 订阅通道）。
//
// 非会话房间的消息一律不唤醒：旧房间只读归档（S1），广播形状已删（条 30）。
// 寻址判定读账本失败上抛；会话本体读失败宁缺毋滥（Error 日志 + 不唤醒，
// 同 AddressesCard 的装配缺失纪律）。
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
	if !room.IsSessionRoom(msg.Room) {
		s.log.Debug("非会话房间消息不唤醒（旧房间只读归档，广播形状已删）",
			"seq", ev.Seq, "room", msg.Room)
		return nil, nil
	}
	targets, err := s.rooms.MessageWakeTargets(msg)
	if err != nil {
		return nil, fmt.Errorf("事件 %d 的会话寻址判定失败: %w", ev.Seq, err)
	}
	if len(targets) == 0 {
		s.log.Debug("会话消息无寻址，不唤醒", "seq", ev.Seq, "session", msg.Room)
		return nil, nil
	}
	session, err := s.autoLedger.GetSession(msg.Room)
	if err != nil {
		s.log.Error("会话消息所在会话读取失败，不唤醒",
			"seq", ev.Seq, "session", msg.Room, "cause", err)
		return nil, nil
	}
	bySeat := make(map[string]string, len(session.Cards))
	for _, cardID := range session.Cards {
		card, err := s.autoLedger.GetCard(cardID)
		if err != nil {
			s.log.Error("会话卡席位读取失败，该卡不参与本次唤醒",
				"seq", ev.Seq, "session", msg.Room, "card", cardID, "cause", err)
			continue
		}
		if card.DriverSession != "" {
			bySeat[card.DriverSession] = cardID
		}
	}
	var wakes []keystone.WakeEvent
	seenCard := make(map[string]bool, len(targets))
	external := make([]string, 0, len(targets))
	for _, target := range targets {
		cardID, ok := bySeat[target]
		if !ok {
			external = append(external, target)
			continue
		}
		if seenCard[cardID] {
			continue
		}
		seenCard[cardID] = true
		wakes = append(wakes, keystone.WakeEvent{
			Kind: keystone.WakeMessage, Card: cardID,
			Summary: truncateRunes(msg.Body, 400),
		})
	}
	if len(external) > 0 {
		s.log.Info("会话消息外部身份命中：不经 keystone，推醒走 session wait 订阅通道",
			"seq", ev.Seq, "session", msg.Room, "targets", external)
	}
	return wakes, nil
}

const wakeParentWalkLimit = 32

// resolveWakeCard 把唤醒目标从事件卡收到真正有 coordinate 席位的卡。
// 事件卡自己有 coordinate 席位 → 自己；事件卡是 bind → 空（bind 靠 CLI wait）；
// 空座沿 parent_id 上走，bind 祖先跳过，coordinate 祖先接手。不改账本 card_id。
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
		occupied := card.DriverSession != "" || card.DriverSource != ""
		if occupied {
			if card.DriverSource == string(proto.SeatSourceBind) {
				if cardID == origin {
					return ""
				}
				cardID = card.ParentID
				continue
			}
			if proto.ValidateSeat(card.DriverSession, proto.SeatSource(card.DriverSource)) == nil {
				return cardID
			}
		}
		cardID = card.ParentID
	}
	return ""
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
		if ev.Seq > maxProcessed {
			maxProcessed = ev.Seq
		}
		if len(wakes) > 0 {
			queued := 0
			for _, wake := range wakes {
				target := s.resolveWakeCard(wake.Card)
				if target == "" {
					s.log.Debug("自动化事件无 coordinate 席位可叫醒", "card", wake.Card, "seq", ev.Seq)
					continue
				}
				if target != wake.Card {
					s.log.Info("自动化唤醒冒泡到祖先席位", "from", wake.Card, "to", target, "seq", ev.Seq)
				}
				wake.Card = target
				pendingByCard[target] = append(pendingByCard[target], pending{seq: ev.Seq, typ: ev.Type, ev: wake})
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
		evs := make([]keystone.WakeEvent, 0, len(batch))
		for _, item := range batch {
			evs = append(evs, item.ev)
		}
		decision := s.keystone.Decide(evs[0])
		if !decision.Wake {
			s.log.Info("自动化事件因 attach 暂缓", "card", card,
				"event_count", len(evs), "reason", decision.Reason)
			for _, item := range batch {
				s.log.Debug("自动化 pending 事件因 attach 暂缓", "seq", item.seq,
					"card", card, "type", item.typ, "cursor", from,
					"cursor_candidate", maxProcessed, "reason", decision.Reason)
			}
			return processed, escalated, nil
		}
		result, wakeErr := s.wakeCoordinatorRound(ctx, card, evs)
		if wakeErr != nil {
			s.log.Error("自动化事件批次唤醒失败", "card", card,
				"event_count", len(evs), "cause", wakeErr)
			// 失败也推进游标：同一条用户消息重试会反复 launchRound，失败前若
			// 再落指针就把房间刷爆（B274）。attach 暂缓走上面的 early return，
			// 不经过这里。
			// 双失败会在账本里落一条 needs_human；它是本轮失败的结果，不是
			// 新的唤醒请求。把这条自生事件一并标记，避免转等人后立即再次
			// Launch 形成自激重试；其它并发事件仍留给下一轮读取。
			if result.Escalated {
				generated, readErr := s.autoLedger.EventsFromAsc(nil, maxProcessed, 500)
				if readErr != nil {
					s.log.Warn("升级后读取自生等人事件失败", "card", card, "cause", readErr)
				} else {
					for _, generatedEvent := range generated {
						if generatedEvent.Type != ledger.EvNeedsHuman || generatedEvent.CardID != card {
							continue
						}
						s.automationMu.Lock()
						s.automationSeen[generatedEvent.Seq] = struct{}{}
						s.automationMu.Unlock()
						if generatedEvent.Seq > maxProcessed {
							maxProcessed = generatedEvent.Seq
						}
					}
				}
			}
			for _, item := range batch {
				s.log.Debug("自动化 pending 事件唤醒失败，推进 cursor", "seq", item.seq,
					"card", card, "type", item.typ, "cursor", from,
					"cursor_candidate", maxProcessed, "cause", wakeErr)
			}
			cursorErr := s.advanceAutomationCursor(maxProcessed)
			if cursorErr != nil {
				return processed, escalated || result.Escalated, errors.Join(wakeErr, cursorErr)
			}
			return processed, escalated || result.Escalated, wakeErr
		}
		if s.automationRoundHook != nil {
			s.automationRoundHook(card, result)
		}
		if result.Escalated {
			escalated = true
		}
		for _, item := range batch {
			s.automationMu.Lock()
			s.automationSeen[item.seq] = struct{}{}
			s.automationMu.Unlock()
			processed++
			s.log.Debug("自动化 pending 事件标记 seen", "seq", item.seq,
				"card", card, "type", item.typ, "cursor", from,
				"cursor_candidate", maxProcessed, "reason", "wake_succeeded")
		}
		s.log.Info("自动化事件批次已唤醒", "card", card,
			"event_count", len(evs), "session", result.SessionID,
			"rebuilt", result.Rebuilt, "escalated", result.Escalated)
	}
	if cursorErr := s.advanceAutomationCursor(maxProcessed); cursorErr != nil {
		return processed, escalated, cursorErr
	}
	s.log.Info("自动化事件消费轮完成", "from_cursor", from,
		"to_cursor", maxProcessed, "processed", processed, "escalated", escalated)
	return processed, escalated, nil
}
