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

// mirrorTaskTerminal 镜像视角的任务终态：订阅应退订、心跳不再要求活连接。
// completed / turn_failed 进 waiting_review，还会再来事件，不算终态。
func mirrorTaskTerminal(typ string) bool {
	return typ == "archived" || typ == "failed"
}

// LiveMirrorTargets 仍有非终态挂账的 target 集合。
//
// 一条挂账算「在飞」当且仅当：还从未镜像过，或最后一条镜像事件不是
// archived/failed。看板用它把「全归档后的静默」从「断链滞后」里剔出去；
// 镜像对账用它决定要不要空 touch 心跳。
func (s *Store) LiveMirrorTargets() (map[string]bool, error) {
	links, err := s.AllTaskLinks()
	if err != nil {
		return nil, err
	}
	if len(links) == 0 {
		return map[string]bool{}, nil
	}
	rows, err := s.db.Query(s.q(`SELECT e.source_target, e.source_task, e.payload
		FROM card_events e
		INNER JOIN (
			SELECT source_target, source_task, MAX(source_seq) AS max_seq
			FROM card_events
			WHERE source_target IS NOT NULL
			GROUP BY source_target, source_task
		) last ON e.source_target = last.source_target
			AND e.source_task = last.source_task
			AND e.source_seq = last.max_seq`))
	if err != nil {
		return nil, fmt.Errorf("读各 task 末条镜像: %w", err)
	}
	defer rows.Close()
	lastType := map[string]string{}
	for rows.Next() {
		var target, task, raw string
		if err := rows.Scan(&target, &task, &raw); err != nil {
			return nil, fmt.Errorf("扫各 task 末条镜像: %w", err)
		}
		var payload struct {
			TaskType string `json:"task_type"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return nil, fmt.Errorf("解码末条镜像 %s@%s: %w", task, target, err)
		}
		lastType[target+"/"+task] = payload.TaskType
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读各 task 末条镜像: %w", err)
	}
	live := map[string]bool{}
	for _, link := range links {
		typ, ok := lastType[link.Target+"/"+link.TaskID]
		if !ok || !mirrorTaskTerminal(typ) {
			live[link.Target] = true
		}
	}
	return live, nil
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
// OpenTicketCounts 的单遍扫描机制，同时读取卡自己的 EvDispatched 与
// EvTaskMirrored：派发事件 payload.task_id 加入已派发集合，镜像事件
// source_task 对应的 archived/failed 加入已收口集合。只有已派发但尚未
// 收口的 task 才算在飞。completed/turn_failed 对应 waiting_review，等裁决
// 仍算在飞；镜像滞后即在飞判定滞后，不另设真相源。
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
	rows, err := q.Query(s.q(`SELECT type, source_task, payload
		FROM card_events
		WHERE card_id = ? AND type IN (?, ?)
		ORDER BY seq ASC`), cardID, EvDispatched, EvTaskMirrored)
	if err != nil {
		err = fmt.Errorf("读卡在飞事件: %w", err)
		log().Error("读取在飞任务事件失败", "card", cardID, "cause", err)
		return false, "", err
	}
	defer rows.Close()

	dispatched := make(map[string]struct{})
	closed := make(map[string]struct{})
	for rows.Next() {
		var eventType, raw string
		var sourceTask sql.NullString
		if err := rows.Scan(&eventType, &sourceTask, &raw); err != nil {
			err = fmt.Errorf("扫卡在飞事件: %w", err)
			log().Error("扫描在飞任务事件失败", "card", cardID, "cause", err)
			return false, "", err
		}
		switch eventType {
		case EvDispatched:
			var dispatch DispatchSnapshot
			if err := json.Unmarshal([]byte(raw), &dispatch); err != nil {
				err = fmt.Errorf("解码卡在飞派发事件: %w", err)
				log().Error("解码在飞任务派发失败", "card", cardID, "cause", err)
				return false, "", err
			}
			if dispatch.TaskID == "" {
				err := fmt.Errorf("卡在飞派发事件缺少 task_id")
				log().Error("解码在飞任务派发失败", "card", cardID, "cause", err)
				return false, "", err
			}
			dispatched[dispatch.TaskID] = struct{}{}
		case EvTaskMirrored:
			if !sourceTask.Valid || sourceTask.String == "" {
				err := fmt.Errorf("卡在飞镜像事件缺少 source_task")
				log().Error("扫描在飞任务镜像失败", "card", cardID, "cause", err)
				return false, "", err
			}
			var event mirroredTaskPayload
			if err := json.Unmarshal([]byte(raw), &event); err != nil {
				err = fmt.Errorf("解码卡在飞镜像事件: %w", err)
				log().Error("解码在飞任务镜像失败", "card", cardID, "cause", err)
				return false, "", err
			}
			if event.TaskType == "archived" || event.TaskType == "failed" {
				closed[sourceTask.String] = struct{}{}
			}
		}
	}
	if err := rows.Err(); err != nil {
		err = fmt.Errorf("读卡在飞事件: %w", err)
		log().Error("读取在飞任务事件失败", "card", cardID, "cause", err)
		return false, "", err
	}
	for taskID := range dispatched {
		if _, done := closed[taskID]; !done {
			log().Debug("卡环节仍在飞", "card", cardID, "task", taskID)
			return true, taskID, nil
		}
	}
	return false, "", nil
}
