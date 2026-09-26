// Package ledger's rebuildable open-ticket read model and bounded detail/count queries.
package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/diag"
)

const openTicketProjectionVersion = 1

type ticketProjectionQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

type storedOpenTicket struct{ ticket OpenTicket }

type openTicketTaskKey struct {
	cardID, target, taskID string
}

// OpenTickets reads only active ticket rows. The left join also makes a missing
// projection marker distinguishable from a valid empty projection.
// B409.6：查询经 timedReadQuery 分离阶段，日志带关联 id；旧签名委托 Context 入口。
func (s *Store) OpenTickets() ([]OpenTicket, error) {
	return s.OpenTicketsContext(context.Background())
}

// OpenTicketsContext 是 OpenTickets 的可取消入口（B409.6：card wait / cards 读
// 路径的取消传播与阶段诊断）。语义与旧入口一致。
func (s *Store) OpenTicketsContext(ctx context.Context) ([]OpenTicket, error) {
	started := time.Now()
	if err := s.ensureOpenTicketProjection(); err != nil {
		return nil, fmt.Errorf("校验未决工单投影: %w", err)
	}
	const query = `SELECT st.open_count, p.card_id, p.source_target, p.source_task,
		p.ticket_id, p.task_type, p.payload
		FROM open_ticket_projection_state st
		LEFT JOIN open_ticket_projection p ON 1 = 1
		WHERE st.id = 1
		ORDER BY p.card_id, p.ticket_id, p.source_target, p.source_task`
	out := make([]OpenTicket, 0)
	var expected int64
	var stateSeen bool
	stage, err := s.timedReadQuery(ctx, query, nil, func(rows *sql.Rows) (int, int64, error) {
		var resultRows, payloadBytes int64
		for rows.Next() {
			var cardID, target, taskID, ticketID, taskType, payload sql.NullString
			if err := rows.Scan(&expected, &cardID, &target, &taskID, &ticketID, &taskType, &payload); err != nil {
				return int(resultRows), payloadBytes, fmt.Errorf("扫未决工单投影: %w", err)
			}
			stateSeen = true
			resultRows++
			if !cardID.Valid {
				continue // Valid empty projection sentinel from the LEFT JOIN.
			}
			if !target.Valid || !taskID.Valid || !ticketID.Valid || !taskType.Valid || !payload.Valid {
				return int(resultRows), payloadBytes, fmt.Errorf("未决工单投影存在空字段")
			}
			body := json.RawMessage(payload.String)
			var bodyTicket ticketPayload
			if err := json.Unmarshal(body, &bodyTicket); err != nil || bodyTicket.TicketID != ticketID.String {
				if err == nil {
					err = fmt.Errorf("投影 ticket_id 与正文不一致")
				}
				return int(resultRows), payloadBytes, fmt.Errorf("校验未决工单投影正文: %w", err)
			}
			out = append(out, OpenTicket{CardID: cardID.String, Target: target.String, TaskID: taskID.String,
				TicketID: ticketID.String, TaskType: taskType.String, Payload: append(json.RawMessage(nil), body...)})
			payloadBytes += int64(len(body))
		}
		return int(resultRows), payloadBytes, rows.Err()
	})
	if err != nil {
		log().Error("读取未决工单投影失败", append(diag.Attrs(ctx),
			"error_class", readErrorClass(err, stage.failedStage), "failed_stage", stage.failedStage,
			"rows_read", stage.rowsRead, "cause", err)...)
		return nil, err
	}
	if !stateSeen {
		err := fmt.Errorf("未决工单投影状态记录缺失")
		log().Error("未决工单投影状态缺失", "cause", err)
		return nil, err
	}
	if int64(len(out)) != expected {
		err := fmt.Errorf("未决工单投影行数不一致: state=%d rows=%d", expected, len(out))
		log().Error("未决工单投影校验失败", "cause", err, "expected_rows", expected, "actual_rows", len(out))
		return nil, err
	}
	// payload_bytes 保留既有字段名（U1 传输边界测试按它取数）；与 result_bytes 同值。
	log().Debug("读取未决工单投影完成", append(append(append(diag.Attrs(ctx), "tickets", len(out),
		"payload_bytes", stage.resultBytes), stage.logAttrs()...),
		"elapsed_ns", time.Since(started).Nanoseconds())...)
	return out, nil
}

// OpenTicketCounts aggregates in SQL and never returns ticket bodies to Go.
// B409.6：旧签名委托 Context 入口，两入口同一把尺。
func (s *Store) OpenTicketCounts() (map[string]int, error) {
	return s.OpenTicketCountsContext(context.Background())
}

// OpenTicketCountsContext 是 OpenTicketCounts 的可取消入口（B409.6：cards 读
// 路径的 HTTP context 取消传播；阶段诊断见完成/失败日志）。语义与旧入口一致。
func (s *Store) OpenTicketCountsContext(ctx context.Context) (map[string]int, error) {
	started := time.Now()
	if err := s.ensureOpenTicketProjection(); err != nil {
		return nil, fmt.Errorf("校验未决工单投影: %w", err)
	}
	const query = `SELECT st.open_count, p.card_id, COUNT(p.ticket_id)
		FROM open_ticket_projection_state st
		LEFT JOIN open_ticket_projection p ON 1 = 1
		WHERE st.id = 1
		GROUP BY st.open_count, p.card_id
		ORDER BY p.card_id`
	counts := make(map[string]int)
	var expected, total int64
	var stateSeen bool
	stage, err := s.timedReadQuery(ctx, query, nil, func(rows *sql.Rows) (int, int64, error) {
		var resultRows int64
		for rows.Next() {
			var cardID sql.NullString
			var cardCount int64
			if err := rows.Scan(&expected, &cardID, &cardCount); err != nil {
				return int(resultRows), 0, fmt.Errorf("扫未决工单聚合: %w", err)
			}
			stateSeen = true
			resultRows++
			total += cardCount
			if cardID.Valid {
				counts[cardID.String] = int(cardCount)
			}
		}
		return int(resultRows), 0, rows.Err()
	})
	if err != nil {
		log().Error("聚合未决工单投影失败", append(diag.Attrs(ctx),
			"error_class", readErrorClass(err, stage.failedStage), "failed_stage", stage.failedStage,
			"rows_read", stage.rowsRead, "cause", err)...)
		return nil, err
	}
	if !stateSeen {
		err := fmt.Errorf("未决工单投影状态记录缺失")
		log().Error("未决工单投影状态缺失", "cause", err)
		return nil, err
	}
	if total != expected {
		err := fmt.Errorf("未决工单投影计数不一致: state=%d aggregate=%d", expected, total)
		log().Error("未决工单投影计数校验失败", "cause", err, "expected_rows", expected, "actual_rows", total)
		return nil, err
	}
	log().Info("聚合未决工单投影完成", append(append(append(diag.Attrs(ctx), "cards", len(counts),
		"payload_bytes", stage.resultBytes), stage.logAttrs()...),
		"elapsed_ns", time.Since(started).Nanoseconds())...)
	return counts, nil
}

// ensureOpenTicketProjection upgrades an old ledger once, and repairs projection
// state when an older writer appended a mirror row without maintaining it.
func (s *Store) ensureOpenTicketProjection() error {
	needsRebuild, err := s.openTicketProjectionNeedsRebuild(s.db)
	if err != nil {
		log().Error("检查未决工单投影水位失败", "cause", err)
		return err
	}
	if !needsRebuild {
		return nil
	}
	started := time.Now()
	log().Info("开始重建未决工单投影")
	if err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		stale, err := s.openTicketProjectionNeedsRebuild(tx)
		if err != nil || !stale {
			return err
		}
		return s.rebuildOpenTicketProjectionTx(tx)
	}); err != nil {
		log().Error("重建未决工单投影失败", "cause", err)
		return err
	}
	log().Info("未决工单投影重建完成", "duration_ms", time.Since(started).Milliseconds())
	return nil
}

func (s *Store) openTicketProjectionNeedsRebuild(q ticketProjectionQueryer) (bool, error) {
	var version int
	var ledgerSeq int64
	err := q.QueryRow(`SELECT version, ledger_seq FROM open_ticket_projection_state WHERE id = 1`).Scan(&version, &ledgerSeq)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取未决工单投影状态: %w", err)
	}
	var currentSeq int64
	// Historical room-history mirrors may have a valid source identity but NULL
	// card_id. They cannot key a card ticket, so exclude them alongside malformed
	// rows missing source_task from this projection's rebuild watermark.
	if err := q.QueryRow(s.q(`SELECT COALESCE(MAX(seq), 0) FROM card_events
		WHERE type = ? AND card_id IS NOT NULL
		AND source_target IS NOT NULL AND source_task IS NOT NULL`), EvTaskMirrored).Scan(&currentSeq); err != nil {
		return false, fmt.Errorf("读取镜像事件水位: %w", err)
	}
	return version != openTicketProjectionVersion || ledgerSeq != currentSeq, nil
}

// rebuildOpenTicketProjection exists for recovery and deterministic tests. The
// canonical card_events replay and projection replacement commit atomically.
func (s *Store) rebuildOpenTicketProjection() error {
	log().Info("按事件流显式重建未决工单投影")
	if err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		return s.rebuildOpenTicketProjectionTx(tx)
	}); err != nil {
		log().Error("显式重建未决工单投影失败", "cause", err)
		return err
	}
	return nil
}

func (s *Store) rebuildOpenTicketProjectionTx(tx *sql.Tx) error {
	if _, err := tx.Exec(`DELETE FROM open_ticket_projection`); err != nil {
		return fmt.Errorf("清空未决工单投影: %w", err)
	}
	rows, err := tx.Query(s.q(`SELECT seq, card_id, source_target, source_task, payload
		FROM card_events WHERE type = ? AND card_id IS NOT NULL
		AND source_target IS NOT NULL AND source_task IS NOT NULL ORDER BY seq ASC`), EvTaskMirrored)
	if err != nil {
		return fmt.Errorf("读取投影重建事件: %w", err)
	}
	open := make(map[openTicketKey]storedOpenTicket)
	byTask := make(map[openTicketTaskKey]map[string]struct{})
	var sourceSeq, eventRows int64
	for rows.Next() {
		var seq int64
		var cardID, target, taskID, raw string
		if err := rows.Scan(&seq, &cardID, &target, &taskID, &raw); err != nil {
			rows.Close()
			return fmt.Errorf("扫描投影重建事件: %w", err)
		}
		var event mirroredTaskPayload
		if err := json.Unmarshal([]byte(raw), &event); err != nil {
			rows.Close()
			return fmt.Errorf("解码投影重建事件 seq=%d: %w", seq, err)
		}
		if err := applyTicketEventToMemory(open, byTask, cardID, target, taskID, event); err != nil {
			rows.Close()
			return fmt.Errorf("应用投影重建事件 seq=%d: %w", seq, err)
		}
		sourceSeq = seq
		eventRows++
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("读取投影重建事件结束: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("关闭投影重建事件: %w", err)
	}

	for _, stored := range open {
		ticket := stored.ticket
		if _, err := tx.Exec(s.q(`INSERT INTO open_ticket_projection
			(card_id, source_target, source_task, ticket_id, task_type, payload)
			VALUES (?, ?, ?, ?, ?, ?)`), ticket.CardID, ticket.Target, ticket.TaskID,
			ticket.TicketID, ticket.TaskType, string(ticket.Payload)); err != nil {
			return fmt.Errorf("写入重建工单 %s/%s: %w", ticket.CardID, ticket.TicketID, err)
		}
	}
	if _, err := tx.Exec(s.q(`INSERT INTO open_ticket_projection_state (id, version, ledger_seq, open_count)
		VALUES (1, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET version = excluded.version,
			ledger_seq = excluded.ledger_seq, open_count = excluded.open_count`),
		openTicketProjectionVersion, sourceSeq, len(open)); err != nil {
		return fmt.Errorf("写入未决工单投影水位: %w", err)
	}
	log().Info("从权威事件流重建未决工单投影", "mirror_events", eventRows, "open_tickets", len(open), "ledger_seq", sourceSeq)
	return nil
}

func applyTicketEventToMemory(open map[openTicketKey]storedOpenTicket, byTask map[openTicketTaskKey]map[string]struct{},
	cardID, target, taskID string, event mirroredTaskPayload) error {
	keyFor := func(ticketID string) openTicketKey {
		return openTicketKey{cardID: cardID, target: target, taskID: taskID, ticketID: ticketID}
	}
	taskKey := openTicketTaskKey{cardID: cardID, target: target, taskID: taskID}
	switch event.TaskType {
	case evTicketCreated, evTicketQuestion:
		var payload ticketPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("解码工单创建正文: %w", err)
		}
		if payload.TicketID != "" {
			key := keyFor(payload.TicketID)
			if _, exists := open[key]; !exists {
				if byTask[taskKey] == nil {
					byTask[taskKey] = make(map[string]struct{})
				}
				byTask[taskKey][payload.TicketID] = struct{}{}
			}
			open[key] = storedOpenTicket{ticket: OpenTicket{
				CardID: cardID, Target: target, TaskID: taskID, TicketID: payload.TicketID,
				TaskType: event.TaskType, Payload: append(json.RawMessage(nil), event.Payload...),
			}}
		}
	case evTicketAnswered:
		var payload ticketPayload
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return fmt.Errorf("解码工单答复正文: %w", err)
		}
		key := keyFor(payload.TicketID)
		delete(open, key)
		delete(byTask[taskKey], payload.TicketID)
		if len(byTask[taskKey]) == 0 {
			delete(byTask, taskKey)
		}
	case evTicketsVoided, "completed", "failed", "archived":
		for ticketID := range byTask[taskKey] {
			delete(open, keyFor(ticketID))
		}
		delete(byTask, taskKey)
	}
	return nil
}

func (s *Store) applyOpenTicketMirrorTx(tx *sql.Tx, cardID string, ev MirroredEvent, ledgerSeq int64) error {
	var delta int64
	switch ev.Type {
	case evTicketCreated, evTicketQuestion:
		var payload ticketPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			return fmt.Errorf("解码工单创建正文: %w", err)
		}
		if payload.TicketID != "" {
			var exists int
			err := tx.QueryRow(s.q(`SELECT 1 FROM open_ticket_projection
				WHERE card_id = ? AND source_target = ? AND source_task = ? AND ticket_id = ?`),
				cardID, ev.Target, ev.Task, payload.TicketID).Scan(&exists)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return fmt.Errorf("检查已有未决工单: %w", err)
			}
			if errors.Is(err, sql.ErrNoRows) {
				delta = 1
			}
			if _, err := tx.Exec(s.q(`INSERT INTO open_ticket_projection
				(card_id, source_target, source_task, ticket_id, task_type, payload)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT (card_id, source_target, source_task, ticket_id) DO UPDATE SET
					task_type = excluded.task_type, payload = excluded.payload`),
				cardID, ev.Target, ev.Task, payload.TicketID, ev.Type, string(ev.Payload)); err != nil {
				return fmt.Errorf("更新未决工单投影: %w", err)
			}
		}
	case evTicketAnswered:
		var payload ticketPayload
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			return fmt.Errorf("解码工单答复正文: %w", err)
		}
		if payload.TicketID != "" {
			res, err := tx.Exec(s.q(`DELETE FROM open_ticket_projection
				WHERE card_id = ? AND source_target = ? AND source_task = ? AND ticket_id = ?`),
				cardID, ev.Target, ev.Task, payload.TicketID)
			if err != nil {
				return fmt.Errorf("关闭已答复工单投影: %w", err)
			}
			delta, err = rowsAffected(res)
			if err != nil {
				return fmt.Errorf("读取答复关单影响行数: %w", err)
			}
			delta = -delta
		}
	case evTicketsVoided, "completed", "failed", "archived":
		res, err := tx.Exec(s.q(`DELETE FROM open_ticket_projection
			WHERE card_id = ? AND source_target = ? AND source_task = ?`), cardID, ev.Target, ev.Task)
		if err != nil {
			return fmt.Errorf("关闭任务未决工单投影: %w", err)
		}
		affected, err := rowsAffected(res)
		if err != nil {
			return fmt.Errorf("读取任务关单影响行数: %w", err)
		}
		delta = -affected
	}
	res, err := tx.Exec(s.q(`UPDATE open_ticket_projection_state
		SET ledger_seq = ?, open_count = open_count + ? WHERE id = 1 AND version = ?`),
		ledgerSeq, delta, openTicketProjectionVersion)
	if err != nil {
		return fmt.Errorf("更新未决工单投影水位: %w", err)
	}
	updated, err := rowsAffected(res)
	if err != nil || updated != 1 {
		if err == nil {
			err = fmt.Errorf("投影水位行数为 %d", updated)
		}
		return fmt.Errorf("未决工单投影状态未就绪: %w", err)
	}
	log().Debug("应用镜像工单投影事件", "card", cardID, "target", ev.Target, "task", ev.Task,
		"task_type", ev.Type, "ledger_seq", ledgerSeq, "open_delta", delta)
	return nil
}

// rowsAffected normalizes the result so a failed driver call cannot become a silent projection drift.
func rowsAffected(result sql.Result) (int64, error) {
	count, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("读取 SQL 影响行数: %w", err)
	}
	return count, nil
}
