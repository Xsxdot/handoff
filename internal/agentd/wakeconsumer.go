// wakeconsumer.go —— K5 账本事件到 keystone 唤醒的唯一消费者。
//
// 职责：读取 card_events 全流，解包 task_mirrored/room_message，过滤非唤醒事件，
// 按卡合并为一次 Wake；成功后推进 seq 并记录 seen，防游标回退重复唤醒。
// 边界：不写 ledger schema、不解释 task 状态、不决定 attach/rebuild。
package agentd

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

type mirroredTaskEnvelope struct {
	TaskType string          `json:"task_type"`
	Payload  json.RawMessage `json:"payload"`
}

func mirroredTaskTypeAndPayload(ev proto.LedgerEvent) (string, json.RawMessage, error) {
	if ev.Type != ledger.EvTaskMirrored {
		return "", nil, fmt.Errorf("事件 %d 不是 task_mirrored: %s", ev.Seq, ev.Type)
	}
	var envelope mirroredTaskEnvelope
	if err := json.Unmarshal(ev.Payload, &envelope); err != nil {
		return "", nil, fmt.Errorf("事件 %d 的 task_mirrored envelope 解码失败: %w", ev.Seq, err)
	}
	if envelope.TaskType == "" || len(envelope.Payload) == 0 {
		return "", nil, fmt.Errorf("事件 %d 的 task_mirrored 缺 task_type/payload", ev.Seq)
	}
	return envelope.TaskType, envelope.Payload, nil
}

// automationWakeEvent 的 false 表示合法但不唤醒；摘要最多 400 rune，原事件仍在账本。
func automationWakeEvent(ev proto.LedgerEvent) (keystone.WakeEvent, bool, error) {
	if ev.CardID == "" {
		return keystone.WakeEvent{}, false, nil
	}
	switch ev.Type {
	case ledger.EvTaskMirrored:
		taskType, payload, err := mirroredTaskTypeAndPayload(ev)
		if err != nil {
			return keystone.WakeEvent{}, false, err
		}
		var kind keystone.WakeKind
		switch proto.EventType(taskType) {
		case proto.EventTypeCompleted, proto.EventTypeFailed, proto.EventTypeTurnFailed:
			kind = keystone.WakeTaskTerminal
		case proto.EventTypePermissionRequest, proto.EventTypeQuestion:
			kind = keystone.WakeTicket
		default:
			return keystone.WakeEvent{}, false, nil
		}
		return keystone.WakeEvent{
			Kind: kind, Card: ev.CardID,
			Summary: fmt.Sprintf("%s: %s", taskType, truncateRunes(string(payload), 400)),
		}, true, nil
	case ledger.EvRoomMessage:
		var msg proto.RoomMessage
		if err := json.Unmarshal(ev.Payload, &msg); err != nil {
			return keystone.WakeEvent{}, false,
				fmt.Errorf("事件 %d 的 room_message 解码失败: %w", ev.Seq, err)
		}
		if msg.Kind != proto.RoomMsgUser || msg.BySystem {
			return keystone.WakeEvent{}, false, nil
		}
		return keystone.WakeEvent{
			Kind: keystone.WakeMessage, Card: ev.CardID,
			Summary: truncateRunes(msg.Body, 400),
		}, true, nil
	case ledger.EvNeedsHuman, ledger.EvNeedsCleared:
		return keystone.WakeEvent{
			Kind: keystone.WakeTaskTerminal, Card: ev.CardID,
			Summary: fmt.Sprintf("%s: %s", ev.Type, truncateRunes(string(ev.Payload), 400)),
		}, true, nil
	default:
		return keystone.WakeEvent{}, false, nil
	}
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
		ev  keystone.WakeEvent
	}
	pendingByCard := map[string][]pending{}
	maxProcessed := from
	for _, ev := range events {
		if ev.Seq <= from {
			continue
		}
		s.automationMu.Lock()
		_, duplicate := s.automationSeen[ev.Seq]
		s.automationMu.Unlock()
		if duplicate {
			if ev.Seq > maxProcessed {
				maxProcessed = ev.Seq
			}
			continue
		}
		wake, yes, mapErr := automationWakeEvent(ev)
		if mapErr != nil {
			s.log.Error("自动化账本事件映射失败", "seq", ev.Seq, "card", ev.CardID,
				"type", ev.Type, "cause", mapErr)
			return processed, escalated, mapErr
		}
		if ev.Seq > maxProcessed {
			maxProcessed = ev.Seq
		}
		if yes {
			pendingByCard[ev.CardID] = append(pendingByCard[ev.CardID], pending{seq: ev.Seq, ev: wake})
			continue
		}
		s.automationMu.Lock()
		s.automationSeen[ev.Seq] = struct{}{}
		s.automationMu.Unlock()
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
			return processed, escalated, nil
		}
		result, wakeErr := s.wakeCoordinatorRound(ctx, card, evs)
		if wakeErr != nil {
			s.log.Error("自动化事件批次唤醒失败", "card", card,
				"event_count", len(evs), "cause", wakeErr)
			// 失败也推进游标：同一条用户消息重试会反复 launchRound，失败前若
			// 再落指针就把房间刷爆（B274）。attach 暂缓走上面的 early return，
			// 不经过这里。
			s.automationMu.Lock()
			if maxProcessed > s.automationCursor {
				s.automationCursor = maxProcessed
			}
			s.automationMu.Unlock()
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
		}
		s.log.Info("自动化事件批次已唤醒", "card", card,
			"event_count", len(evs), "session", result.SessionID,
			"rebuilt", result.Rebuilt, "escalated", result.Escalated)
	}
	s.automationMu.Lock()
	if maxProcessed > s.automationCursor {
		s.automationCursor = maxProcessed
	}
	s.automationMu.Unlock()
	s.log.Info("自动化事件消费轮完成", "from_cursor", from,
		"to_cursor", maxProcessed, "processed", processed, "escalated", escalated)
	return processed, escalated, nil
}
