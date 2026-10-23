package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/diag"
	"github.com/Xsxdot/handoff/internal/proto"
)

// SessionRoleActivityContext returns a bounded latest-facts window for the supplied cards.
// It does not replay the ledger stream; each card contributes at most limit+1 rows.
func (s *Store) SessionRoleActivityContext(ctx context.Context, cardIDs []string, limit int) ([]Event, []string, error) {
	cardIDs = uniqueSorted(cardIDs)
	if limit <= 0 || limit > 200 {
		return nil, nil, fmt.Errorf("角色动态窗口必须在 1..200: %d", limit)
	}
	if len(cardIDs) == 0 {
		return []Event{}, []string{}, nil
	}
	var events []Event
	var truncated []string
	truncatedSeen := map[string]bool{}
	for start := 0; start < len(cardIDs); start += 400 {
		end := start + 400
		if end > len(cardIDs) {
			end = len(cardIDs)
		}
		chunk := cardIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+1)
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, limit+1)
		commentKey := `json_extract(payload, '$.dedupe_key')`
		dispatchTarget := `json_extract(d.payload, '$.target')`
		dispatchTask := `json_extract(d.payload, '$.task_id')`
		mirrorTaskType := `json_extract(m.payload, '$.task_type')`
		if s.dialect != dialectSQLite {
			commentKey = `payload->>'dedupe_key'`
			dispatchTarget = `d.payload->>'target'`
			dispatchTask = `d.payload->>'task_id'`
			mirrorTaskType = `m.payload->>'task_type'`
		}
		query := `WITH ranked AS (SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at,
				ROW_NUMBER() OVER (PARTITION BY card_id ORDER BY seq DESC) AS rn,
				CASE WHEN type = 'dispatched' THEN ROW_NUMBER() OVER (PARTITION BY card_id, type ORDER BY seq DESC) ELSE 0 END AS dispatch_rn,
				CASE WHEN type = 'comment' THEN ROW_NUMBER() OVER (PARTITION BY card_id, type ORDER BY seq DESC) ELSE 0 END AS wake_rn,
				CASE WHEN type IN ('driver_seat_bound','driver_takeover') THEN ROW_NUMBER() OVER (PARTITION BY card_id, type ORDER BY seq DESC) ELSE 0 END AS seat_rn
				FROM card_events WHERE card_id IN (` + placeholders + `)
				AND type IN ('comment','dispatched','task_mirrored','room_message','needs_human','needs_cleared','status_moved','driver_seat_bound','driver_takeover')
				AND (type <> 'comment' OR ` + commentKey + ` LIKE 'wake_round:%'))
			SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at, rn FROM ranked
			WHERE rn <= ? OR (type = 'dispatched' AND dispatch_rn = 1) OR (type = 'comment' AND wake_rn = 1)
			OR (type IN ('driver_seat_bound','driver_takeover') AND seat_rn = 1)
			OR (type = 'task_mirrored' AND seq = (SELECT m.seq FROM card_events m WHERE m.card_id = ranked.card_id AND m.type = 'task_mirrored'
				AND m.source_target = (SELECT ` + dispatchTarget + ` FROM card_events d WHERE d.card_id = ranked.card_id AND d.type = 'dispatched' ORDER BY d.seq DESC LIMIT 1)
				AND m.source_task = (SELECT ` + dispatchTask + ` FROM card_events d WHERE d.card_id = ranked.card_id AND d.type = 'dispatched' ORDER BY d.seq DESC LIMIT 1)
				ORDER BY m.source_seq DESC LIMIT 1))
			OR (type = 'task_mirrored' AND seq = (SELECT m.seq FROM card_events m WHERE m.card_id = ranked.card_id AND m.type = 'task_mirrored'
				AND m.source_target = (SELECT ` + dispatchTarget + ` FROM card_events d WHERE d.card_id = ranked.card_id AND d.type = 'dispatched' ORDER BY d.seq DESC LIMIT 1)
				AND m.source_task = (SELECT ` + dispatchTask + ` FROM card_events d WHERE d.card_id = ranked.card_id AND d.type = 'dispatched' ORDER BY d.seq DESC LIMIT 1)
				AND ` + mirrorTaskType + ` IN ('permission_request','question','ticket_answered','completed','turn_failed','failed','archived','delivery_failed')
				ORDER BY m.source_seq DESC LIMIT 1))
			ORDER BY card_id, seq DESC`
		stage, err := s.timedReadQuery(ctx, query, args, func(rows *sql.Rows) (int, int64, error) {
			count := 0
			var resultBytes int64
			for rows.Next() {
				var ev Event
				var cardID, sourceTarget, sourceTask sql.NullString
				var sourceSeq sql.NullInt64
				var raw string
				var createdAt any
				var rank int
				if err := rows.Scan(&ev.Seq, &cardID, &ev.Type, &ev.Actor, &raw, &sourceTarget, &sourceTask, &sourceSeq, &createdAt, &rank); err != nil {
					return count, 0, err
				}
				ev.CardID, ev.SourceTarget, ev.SourceTask, ev.SourceSeq = cardID.String, sourceTarget.String, sourceTask.String, sourceSeq.Int64
				ev.Payload, ev.CreatedAt = json.RawMessage(raw), toTime(createdAt)
				events = append(events, ev)
				resultBytes += int64(len(raw))
				if rank > limit {
					if !truncatedSeen[ev.CardID] {
						truncated = append(truncated, ev.CardID)
						truncatedSeen[ev.CardID] = true
					}
				}
				count++
			}
			return count, resultBytes, rows.Err()
		})
		if err != nil {
			return nil, nil, fmt.Errorf("读取角色动态卡范围 %d..%d: %w", start, end, err)
		}
		_ = stage
	}
	return events, truncated, nil
}

// SessionRoleTicketsContext reads only the open-ticket projection rows for the supplied cards.
// Ticket payloads are validated and discarded; callers receive identifiers only.
func (s *Store) SessionRoleTicketsContext(ctx context.Context, cardIDs []string, limit int) ([]proto.SessionTicketRef, bool, error) {
	started := time.Now()
	cardIDs = uniqueSorted(cardIDs)
	if limit <= 0 || limit > 1000 {
		return nil, false, fmt.Errorf("角色票据窗口必须在 1..1000: %d", limit)
	}
	if len(cardIDs) == 0 {
		return []proto.SessionTicketRef{}, false, nil
	}
	if err := s.ensureOpenTicketProjection(); err != nil {
		return nil, false, fmt.Errorf("校验未决工单投影: %w", err)
	}
	var out []proto.SessionTicketRef
	for start := 0; start < len(cardIDs); start += 400 {
		end := start + 400
		if end > len(cardIDs) {
			end = len(cardIDs)
		}
		chunk := cardIDs[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, 0, len(chunk)+1)
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, limit+1-len(out))
		query := `SELECT card_id, source_target, source_task, ticket_id, task_type, payload
			FROM open_ticket_projection WHERE card_id IN (` + placeholders + `)
			ORDER BY card_id, ticket_id, source_target, source_task LIMIT ?`
		stage, err := s.timedReadQuery(ctx, query, args, func(rows *sql.Rows) (int, int64, error) {
			var count int
			for rows.Next() {
				var cardID, target, taskID, ticketID, taskType, raw string
				if err := rows.Scan(&cardID, &target, &taskID, &ticketID, &taskType, &raw); err != nil {
					return count, 0, fmt.Errorf("扫描指定卡未决票据: %w", err)
				}
				var payload ticketPayload
				if err := json.Unmarshal([]byte(raw), &payload); err != nil || payload.TicketID != ticketID {
					if err == nil {
						err = fmt.Errorf("投影 id 与票据内容不符")
					}
					return count, 0, fmt.Errorf("校验指定卡未决票据: %w", err)
				}
				out = append(out, proto.SessionTicketRef{CardID: cardID, Target: target, TaskID: taskID, TicketID: ticketID, TaskType: taskType})
				count++
			}
			return count, 0, rows.Err()
		})
		if err != nil {
			log().Error("读取指定卡未决票据失败", append(diag.Attrs(ctx), "error_class", readErrorClass(err, stage.failedStage), "failed_stage", stage.failedStage, "cause", err)...)
			return nil, false, fmt.Errorf("读取指定卡未决票据: %w", err)
		}
		log().Debug("读取指定卡未决票据完成", append(append(diag.Attrs(ctx), "tickets", len(out)), stage.logAttrs()...)...)
		if len(out) > limit {
			return out[:limit], true, nil
		}
	}
	log().Debug("卡范围未决票据读取完成", append(diag.Attrs(ctx), "tickets", len(out), "elapsed_ns", time.Since(started).Nanoseconds())...)
	return out, false, nil
}
