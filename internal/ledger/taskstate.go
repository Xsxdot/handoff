// task 实况推导：从镜像事件流取每个挂账 task 的最后一条事件类型。
// 单一数据源（不跨机拨号），代价是实况滞后于镜像——滞后本身有
// MirrorHealth 显性化，看板不会拿陈旧实况冒充新鲜（信号分层）。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// TaskStateRow 挂账 task 的实况摘要。LastType 空 = 尚无镜像事件（未知）。
type TaskStateRow struct {
	Target, TaskID, Purpose, LastType string
	LastSeq                           int64
}

// LatestTaskStates 一张卡全部挂账 task 的实况。
func (s *Store) LatestTaskStates(cardID string) ([]TaskStateRow, error) {
	links, err := s.TasksOf(cardID)
	if err != nil {
		return nil, err
	}
	out := make([]TaskStateRow, 0, len(links))
	for _, link := range links {
		row := TaskStateRow{Target: link.Target, TaskID: link.TaskID, Purpose: link.Purpose}
		var raw string
		err := s.db.QueryRow(s.q(`SELECT payload, source_seq FROM card_events
			WHERE source_target = ? AND source_task = ?
			ORDER BY source_seq DESC LIMIT 1`), link.Target, link.TaskID).
			Scan(&raw, &row.LastSeq)
		if errors.Is(err, sql.ErrNoRows) {
			out = append(out, row)
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("读 task 实况 %s@%s: %w", link.TaskID, link.Target, err)
		}
		var payload struct {
			TaskType string `json:"task_type"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, fmt.Errorf("解码 task 实况 %s@%s: %w", link.TaskID, link.Target, err)
		}
		row.LastType = payload.TaskType
		out = append(out, row)
	}
	return out, nil
}

// 工单事件类型。permission_request/question 是真实协议中的创建类事件；
// ticket_answered 保留为账本镜像的兼容类型，供已有/未来镜像适配器回放答复。
const (
	evTicketCreated  = "permission_request"
	evTicketQuestion = "question"
	evTicketAnswered = "ticket_answered"
	evTicketsVoided  = "tickets_voided"
)

type openTicketKey struct {
	cardID, target, taskID, ticketID string
}

type mirroredTaskPayload struct {
	TaskType string          `json:"task_type"`
	Payload  json.RawMessage `json:"payload"`
}

type ticketPayload struct {
	TicketID string `json:"ticket_id"`
}

// OpenTicketCounts 每张卡的未决工单数：单遍扫描镜像事件，按 ticket_id
// 回放 创建→答复/作废。镜像滞后即工单滞后——与实况同一显性化通道
// （MirrorHealth），不另设真相源。
func (s *Store) OpenTicketCounts() (map[string]int, error) {
	rows, err := s.db.Query(s.q(`SELECT card_id, source_target, source_task, payload
		FROM card_events WHERE type = ? AND source_target IS NOT NULL ORDER BY seq ASC`), EvTaskMirrored)
	if err != nil {
		return nil, fmt.Errorf("读镜像工单事件: %w", err)
	}
	defer rows.Close()

	open := make(map[openTicketKey]struct{})
	for rows.Next() {
		var cardID, target, taskID, raw string
		if err := rows.Scan(&cardID, &target, &taskID, &raw); err != nil {
			return nil, fmt.Errorf("扫镜像工单事件: %w", err)
		}
		var event mirroredTaskPayload
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			return nil, fmt.Errorf("解码镜像工单事件: %w", err)
		}
		keyPrefix := func(ticketID string) openTicketKey {
			return openTicketKey{cardID: cardID, target: target, taskID: taskID, ticketID: ticketID}
		}
		switch event.TaskType {
		case evTicketCreated, evTicketQuestion:
			var ticket ticketPayload
			if err := json.Unmarshal(event.Payload, &ticket); err != nil {
				return nil, fmt.Errorf("解码镜像工单 payload: %w", err)
			}
			if ticket.TicketID != "" {
				open[keyPrefix(ticket.TicketID)] = struct{}{}
			}
		case evTicketAnswered:
			var ticket ticketPayload
			if err := json.Unmarshal(event.Payload, &ticket); err != nil {
				return nil, fmt.Errorf("解码镜像答复 payload: %w", err)
			}
			if ticket.TicketID != "" {
				delete(open, keyPrefix(ticket.TicketID))
			}
		case evTicketsVoided:
			for key := range open {
				if key.cardID == cardID && key.target == target && key.taskID == taskID {
					delete(open, key)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读镜像工单事件: %w", err)
	}

	counts := make(map[string]int)
	for key := range open {
		counts[key.cardID]++
	}
	return counts, nil
}

// CardStepInFlight 报告卡是否存在仍在运行的环节。
//
// 参数 cardID 是要查询的卡号；返回值为在飞标记和查询错误。实现沿用
// OpenTicketCounts 的同一机制：单遍扫描 EvTaskMirrored，按 source_task 回放
// 任务生命周期。只有 archived/failed 是终态；completed/turn_failed 对应
// waiting_review，等裁决仍算在飞。镜像滞后即在飞判定滞后，不另设真相源。
func (s *Store) CardStepInFlight(cardID string) (bool, error) {
	inFlight, _, err := s.cardStepInFlightQuery(s.db, cardID)
	return inFlight, err
}

type taskEventQueryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// cardStepInFlightTx 是迁移事务使用的同一派生查询，避免把门禁读到事务外
// 形成 TOCTOU 窗口。公开查询与事务查询共享回放实现，保持两个入口同一把尺。
func (s *Store) cardStepInFlightTx(tx *sql.Tx, cardID string) (bool, string, error) {
	return s.cardStepInFlightQuery(tx, cardID)
}

func (s *Store) cardStepInFlightQuery(q taskEventQueryer, cardID string) (bool, string, error) {
	rows, err := q.Query(s.q(`SELECT source_task, payload
		FROM card_events
		WHERE card_id = ? AND type = ? AND source_target IS NOT NULL
		ORDER BY seq ASC`), cardID, EvTaskMirrored)
	if err != nil {
		err = fmt.Errorf("读卡在飞镜像事件: %w", err)
		log().Error("读取在飞任务镜像失败", "card", cardID, "cause", err)
		return false, "", err
	}
	defer rows.Close()

	closed := make(map[string]bool)
	for rows.Next() {
		var taskID, raw string
		if err := rows.Scan(&taskID, &raw); err != nil {
			err = fmt.Errorf("扫卡在飞镜像事件: %w", err)
			log().Error("扫描在飞任务镜像失败", "card", cardID, "cause", err)
			return false, "", err
		}
		var event mirroredTaskPayload
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			err = fmt.Errorf("解码卡在飞镜像事件: %w", err)
			log().Error("解码在飞任务镜像失败", "card", cardID, "cause", err)
			return false, "", err
		}
		closed[taskID] = event.TaskType == "archived" || event.TaskType == "failed"
	}
	if err := rows.Err(); err != nil {
		err = fmt.Errorf("读卡在飞镜像事件: %w", err)
		log().Error("读取在飞任务镜像失败", "card", cardID, "cause", err)
		return false, "", err
	}
	for taskID, done := range closed {
		if !done {
			log().Debug("卡环节仍在飞", "card", cardID, "task", taskID)
			return true, taskID, nil
		}
	}
	return false, "", nil
}
