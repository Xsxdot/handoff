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
// Ordinary comments and unrelated card/task histories are excluded at the query boundary.
func (s *Store) SessionRoleActivityContext(ctx context.Context, cardIDs []string, limit int) ([]Event, []string, error) {
	cardIDs = uniqueSorted(cardIDs)
	if limit <= 0 || limit > 200 {
		return nil, nil, fmt.Errorf("角色动态窗口必须在 1..200: %d", limit)
	}
	if len(cardIDs) == 0 {
		return []Event{}, []string{}, nil
	}
	const cardBatchSize = 400
	const mandatoryFactsPerCard = 6 // dispatch + wake + two seat boundaries + current raw/lifecycle mirrors
	var events []Event
	var truncated []string
	for start := 0; start < len(cardIDs); start += cardBatchSize {
		end := start + cardBatchSize
		if end > len(cardIDs) {
			end = len(cardIDs)
		}
		chunk := cardIDs[start:end]
		query, args := buildSessionRoleActivityQuery(s.dialect, chunk, limit)
		stage, err := s.timedReadQuery(ctx, query, args, func(rows *sql.Rows) (int, int64, error) {
			count := 0
			var resultBytes int64
			for rows.Next() {
				var ev Event
				var cardID, sourceTarget, sourceTask sql.NullString
				var sourceSeq sql.NullInt64
				var raw string
				var createdAt any
				var rank, cardTotal int
				if err := rows.Scan(&ev.Seq, &cardID, &ev.Type, &ev.Actor, &raw, &sourceTarget, &sourceTask, &sourceSeq, &createdAt, &rank, &cardTotal); err != nil {
					return count, 0, err
				}
				ev.CardID, ev.SourceTarget, ev.SourceTask, ev.SourceSeq = cardID.String, sourceTarget.String, sourceTask.String, sourceSeq.Int64
				ev.Payload, ev.CreatedAt = json.RawMessage(raw), toTime(createdAt)
				events = append(events, ev)
				resultBytes += int64(len(raw))
				if cardTotal > limit && !containsString(truncated, ev.CardID) {
					truncated = append(truncated, ev.CardID)
				}
				count++
			}
			return count, resultBytes, rows.Err()
		})
		if err != nil {
			log().Error("读取角色动态失败", append(diag.Attrs(ctx), "cards", len(chunk), "limit", limit,
				"max_rows_per_card", limit+mandatoryFactsPerCard, "error_class", readErrorClass(err, stage.failedStage),
				"failed_stage", stage.failedStage, "cause", err)...)
			return nil, nil, fmt.Errorf("读取角色动态卡范围 %d..%d: %w", start, end, err)
		}
		log().Debug("读取角色动态完成", append(append(diag.Attrs(ctx), "cards", len(chunk), "limit", limit,
			"max_rows_per_card", limit+mandatoryFactsPerCard, "truncated_cards", len(truncated)), stage.logAttrs()...)...)
	}
	return events, truncated, nil
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

// buildSessionRoleActivityQuery constructs one card-scoped window query. latest dispatch and
// the two current-task mirror facts are computed once as CTEs, never by correlated per-row lookups.
func buildSessionRoleActivityQuery(dialect dialect, cardIDs []string, limit int) (string, []any) {
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(cardIDs)), ",")
	commentKey := `json_extract(e.payload, '$.dedupe_key')`
	dispatchTarget, dispatchTask := `d.payload->>'target'`, `d.payload->>'task_id'`
	mirrorTaskType := `m.payload->>'task_type'`
	if dialect == dialectSQLite {
		dispatchTarget, dispatchTask = `json_extract(d.payload, '$.target')`, `json_extract(d.payload, '$.task_id')`
		mirrorTaskType = `json_extract(m.payload, '$.task_type')`
	} else {
		commentKey = `e.payload->>'dedupe_key'`
	}
	query := `WITH dispatch_ranked AS (
			SELECT d.card_id, d.seq, ` + dispatchTarget + ` AS target, ` + dispatchTask + ` AS task,
				ROW_NUMBER() OVER (PARTITION BY d.card_id ORDER BY d.seq DESC) AS rn
			FROM card_events d WHERE d.card_id IN (` + placeholders + `) AND d.type='dispatched'
		), current_dispatch AS (
			SELECT card_id, seq, target, task FROM dispatch_ranked WHERE rn=1
		), current_mirror_ranked AS (
			SELECT m.card_id, m.seq, m.source_seq,
				ROW_NUMBER() OVER (PARTITION BY m.card_id ORDER BY m.source_seq DESC) AS raw_rn,
				MAX(CASE WHEN ` + mirrorTaskType + ` IN ('permission_request','question','ticket_answered','completed','turn_failed','failed','archived','delivery_failed') THEN m.source_seq END)
					OVER (PARTITION BY m.card_id) AS latest_lifecycle_seq
			FROM card_events m JOIN current_dispatch d ON d.card_id=m.card_id
				AND d.target=m.source_target AND d.task=m.source_task
			WHERE m.type='task_mirrored'
		), role_ranked AS (
			SELECT e.seq, e.card_id, e.type, e.actor, e.payload, e.source_target, e.source_task, e.source_seq, e.created_at,
				ROW_NUMBER() OVER (PARTITION BY e.card_id ORDER BY e.seq DESC) AS rn,
				COUNT(*) OVER (PARTITION BY e.card_id) AS card_total,
				CASE WHEN e.type='comment' THEN ROW_NUMBER() OVER (PARTITION BY e.card_id, e.type ORDER BY e.seq DESC) ELSE 0 END AS wake_rn,
				CASE WHEN e.type IN ('driver_seat_bound','driver_takeover') THEN ROW_NUMBER() OVER (PARTITION BY e.card_id, e.type ORDER BY e.seq DESC) ELSE 0 END AS seat_rn
			FROM card_events e WHERE e.card_id IN (` + placeholders + `)
				AND e.type IN ('comment','dispatched','task_mirrored','room_message','needs_human','needs_cleared','status_moved','driver_seat_bound','driver_takeover')
				AND (e.type <> 'comment' OR ` + commentKey + ` LIKE 'wake_round:%')
		), mandatory(card_id, seq) AS (
			SELECT card_id, seq FROM current_dispatch
			UNION SELECT card_id, seq FROM role_ranked WHERE type='comment' AND wake_rn=1
			UNION SELECT card_id, seq FROM role_ranked WHERE type IN ('driver_seat_bound','driver_takeover') AND seat_rn=1
			UNION SELECT card_id, seq FROM current_mirror_ranked WHERE raw_rn=1
			UNION SELECT card_id, seq FROM current_mirror_ranked WHERE source_seq=latest_lifecycle_seq AND latest_lifecycle_seq IS NOT NULL
		)
		SELECT r.seq, r.card_id, r.type, r.actor, r.payload, r.source_target, r.source_task, r.source_seq, r.created_at, r.rn, r.card_total
		FROM role_ranked r LEFT JOIN mandatory k ON k.card_id=r.card_id AND k.seq=r.seq
		WHERE r.rn <= ? OR k.seq IS NOT NULL ORDER BY r.card_id, r.seq DESC`
	// The card list occurs once in dispatch_ranked and once in role_ranked.
	queryArgs := make([]any, 0, 2*len(cardIDs)+1)
	queryArgs = append(queryArgs, cardIDsToAny(cardIDs)...)
	queryArgs = append(queryArgs, cardIDsToAny(cardIDs)...)
	queryArgs = append(queryArgs, limit)
	return query, queryArgs
}

func cardIDsToAny(ids []string) []any {
	values := make([]any, len(ids))
	for i, id := range ids {
		values[i] = id
	}
	return values
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
