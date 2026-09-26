// 收件箱消费链的限域读（B409 U5；B156.2 Pending/Mentions/Consume 语义的取数面）。
//
// 消费者是 internal/collab#Service：Pending / Mentions / Consume /
// consumeRoomMentions。旧实现把 ReadAllEvents 全流读进 collab 再逐行过滤；
// 本文件把「按 member/room/card/seq 地址键取候选」下沉到账本边界，过滤裁决
// （mentions 语义、kind、SameRoom、幂等消费）仍在 collab——账本只执行字段
// 级投影，不解释 RoomMessage 业务语义（ledger/rooms.go 文件头同款边界）。
//
// U4/U5 共享 seam 清点（plan 闸）：U4 落的是 room/session/card 范围投影读
// （RoomMessagesBeforeContext/RoomMessageSnapshotsContext/SessionProjectionEventsContext/
// LatestNeedsEventsContext）；本文件落的是 recipient-address 候选、consumed
// 批量与 seq 点读，两组生产消费者的过滤键与结果语义不同，无共同新增方法或 DTO。
package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// candidateLimitMax 单页候选上限的保守天花板（与会话候选读同一保守值）。
const candidateLimitMax = 10000

// 方言 JSON 表达式（session_candidates.go 同款方言点；禁止 jsonb 的 ? 运算符）。
const (
	mentionMatchOnlyPG     = `EXISTS (SELECT 1 FROM jsonb_array_elements_text(payload->'mentions') m WHERE m = ?)`
	mentionMatchOnlySQLite = `EXISTS (SELECT 1 FROM json_each(payload, '$.mentions') m WHERE m.value = ?)`
	kindExprPG             = `payload->>'kind'`
	kindExprSQLite         = `json_extract(payload, '$.kind')`
)

// mentionMatchExpr 只按身份精确匹配 mentions（收件箱语义：无席位解算）。
func (s *Store) mentionMatchExpr() string {
	if s.dialect == dialectSQLite {
		return mentionMatchOnlySQLite
	}
	return mentionMatchOnlyPG
}

// roomExpr 房间身份表达式（无卡事件的 payload.room；rooms.go 同款）。
func (s *Store) roomExpr() string {
	if s.dialect == dialectSQLite {
		return `json_extract(payload, '$.room')`
	}
	return `payload->>'room'`
}

// kindExpr 消息 kind 表达式。
func (s *Store) kindExpr() string {
	if s.dialect == dialectSQLite {
		return kindExprSQLite
	}
	return kindExprPG
}

// MentionCandidatesContext 读一页 @member 的 room_message 候选超集（mentions
// 数组精确含 member），seq 升序。
//
// 参数：member 必填；roomID 非空时限房间——返回 SameRoom 的 SQL 超集（挂卡行
// 按 card_id、无卡行按 payload.room），SameRoom 的权威判定由 collab 执行；
// cardlessOnly=true 只取无卡行（Pending 的群级源）；afterSeq 排他；limit>0。
// kind、by_system 不在 SQL 过滤——那是 collab 的裁决，不在账本复制。
func (s *Store) MentionCandidatesContext(ctx context.Context, member, roomID string, cardlessOnly bool, afterSeq int64, limit int) ([]Event, error) {
	if member == "" {
		return nil, fmt.Errorf("提及候选读需要 member")
	}
	if limit <= 0 || limit > candidateLimitMax {
		return nil, fmt.Errorf("提及候选读 limit 必须在 (0,%d]: %d", candidateLimitMax, limit)
	}
	started := time.Now()
	query := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events WHERE type = 'room_message' AND seq > ?`
	args := []any{afterSeq}
	if cardlessOnly {
		query += ` AND card_id IS NULL`
	}
	if roomID != "" {
		query += ` AND (card_id = ? OR (card_id IS NULL AND ` + s.roomExpr() + ` = ?))`
		args = append(args, roomID, roomID)
	}
	query += ` AND ` + s.mentionMatchExpr() + ` ORDER BY seq ASC LIMIT ?`
	args = append(args, member, limit)
	if err := ctx.Err(); err != nil {
		log().Info("提及候选读已取消", "member", member, "room_restricted", roomID != "", "cause", err)
		return nil, err
	}
	out, err := s.queryEvents(ctx, "提及候选", query, args...)
	if err != nil {
		return nil, err
	}
	log().Info("提及候选读完成", "member", member, "room_restricted", roomID != "",
		"cardless_only", cardlessOnly, "after_seq", afterSeq,
		"rows_returned", len(out), "payload_bytes", eventsPayloadBytes(out),
		"elapsed_ns", time.Since(started).Nanoseconds())
	return out, nil
}

// CardRoomUserMessagesContext 读给定卡集合上 kind=user 的房间消息行（Pending
// 第二源：绑定卡房间内未消费用户留言）。cardIDs 为空返回空集；afterSeq 排他；
// limit>0。mentions/kind 之外的裁决仍在 collab。
func (s *Store) CardRoomUserMessagesContext(ctx context.Context, cardIDs []string, afterSeq int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > candidateLimitMax {
		return nil, fmt.Errorf("绑定卡用户消息 limit 必须在 (0,%d]: %d", candidateLimitMax, limit)
	}
	if len(cardIDs) == 0 {
		return []Event{}, nil
	}
	started := time.Now()
	var out []Event
	// 与 rooms.go 的 400 参数分块同形：单条 SQL 绑定参数留在两方言保守上限内。
	// 每块只取剩余配额；块间各自升序，最终归并排序由调用方（collab）负责——
	// 本方法保证的是「各块内升序 + 总量不超过 limit」。
	const cardChunkSize = 400
	for start := 0; start < len(cardIDs) && len(out) < limit; start += cardChunkSize {
		end := start + cardChunkSize
		if end > len(cardIDs) {
			end = len(cardIDs)
		}
		chunk := cardIDs[start:end]
		remaining := limit - len(out)
		query := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
			FROM card_events WHERE type = 'room_message' AND card_id IN (?` + strings.Repeat(",?", len(chunk)-1) + `)
			AND seq > ? AND ` + s.kindExpr() + ` = ?
			ORDER BY seq ASC LIMIT ?`
		args := make([]any, 0, len(chunk)+3)
		for _, id := range chunk {
			args = append(args, id)
		}
		args = append(args, afterSeq, proto.RoomMsgUser, remaining)
		if err := ctx.Err(); err != nil {
			log().Info("绑定卡用户消息读取已取消", "cards", len(chunk), "cause", err)
			return nil, err
		}
		events, err := s.queryEvents(ctx, "绑定卡用户消息", query, args...)
		if err != nil {
			return nil, err
		}
		out = append(out, events...)
	}
	log().Info("绑定卡用户消息读取完成", "cards", len(cardIDs), "after_seq", afterSeq,
		"rows_returned", len(out), "payload_bytes", eventsPayloadBytes(out),
		"elapsed_ns", time.Since(started).Nanoseconds())
	return out, nil
}

// ConsumedMessageSeqsContext 读 consumer 已消费的 message_seq 集合（批量，供
// Pending/Mentions/consumeRoomMentions 的「未消费」过滤复用）。actor 列粗筛 +
// 载荷精确匹配（载荷才是权威，RecordMessageConsumed 查重同款）；afterSeq 是
// 安全上界——消费标记事件 seq 必大于被标记消息 seq，故 seq>afterSeq 的标记
// 覆盖了全部 message_seq>afterSeq 的相关标记，不会漏。
func (s *Store) ConsumedMessageSeqsContext(ctx context.Context, consumer string, afterSeq int64) ([]int64, error) {
	if consumer == "" {
		return nil, fmt.Errorf("consumed 批量读需要 consumer")
	}
	started := time.Now()
	rows, err := s.db.QueryContext(ctx, s.q(`SELECT payload FROM card_events
		WHERE type = ? AND actor = ? AND seq > ?`), EvMessageConsumed, consumer, afterSeq)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			log().Info("consumed 批量读已取消", "consumer", consumer, "cause", err)
			return nil, err
		}
		log().Warn("consumed 批量读 SQL 失败", "consumer", consumer, "cause", err)
		return nil, fmt.Errorf("读消费标记: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			log().Warn("consumed 批量读行扫描失败", "consumer", consumer, "rows_read", len(out), "cause", err)
			return nil, fmt.Errorf("扫消费标记行: %w", err)
		}
		var marker consumedMarker
		if err := json.Unmarshal([]byte(raw), &marker); err != nil {
			continue // 非法载荷不该出现；跳过不破坏消费集（ConsumedSeqs 同款）
		}
		if marker.Consumer != consumer {
			continue
		}
		out = append(out, marker.MessageSeq)
	}
	if err := rows.Err(); err != nil {
		log().Warn("consumed 批量读遍历失败", "consumer", consumer, "cause", err)
		return nil, fmt.Errorf("遍历消费标记: %w", err)
	}
	log().Info("consumed 批量读完成", "consumer", consumer, "after_seq", afterSeq,
		"rows_returned", len(out), "elapsed_ns", time.Since(started).Nanoseconds())
	return out, nil
}

// EventBySeq 按全局 seq 点读单条事件（Consume 定位消息所属卡的限域替身，不再
// 全流扫描）。ok=false 表示 seq 不存在。
func (s *Store) EventBySeq(seq int64) (Event, bool, error) {
	row := s.db.QueryRow(s.q(`SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events WHERE seq = ?`), seq)
	var event Event
	var cardID, sourceTarget, sourceTask sql.NullString
	var sourceSeq sql.NullInt64
	var raw string
	var createdAt any
	err := row.Scan(&event.Seq, &cardID, &event.Type, &event.Actor, &raw,
		&sourceTarget, &sourceTask, &sourceSeq, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, false, nil
	}
	if err != nil {
		return Event{}, false, fmt.Errorf("点读事件 %d: %w", seq, err)
	}
	event.CardID = cardID.String
	event.SourceTarget = sourceTarget.String
	event.SourceTask = sourceTask.String
	event.SourceSeq = sourceSeq.Int64
	event.Payload = json.RawMessage(raw)
	event.CreatedAt = toTime(createdAt)
	return event, true, nil
}

// queryEvents 统一执行候选类查询并扫描行（错误日志带查询名，不记正文）。
func (s *Store) queryEvents(ctx context.Context, name, query string, args ...any) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			log().Info(name+"已取消", "cause", err)
			return nil, err
		}
		log().Warn(name+" SQL 失败", "cause", err)
		return nil, fmt.Errorf("读%s: %w", name, err)
	}
	defer rows.Close()
	out, scanErr := scanProjectionEvents(rows)
	if scanErr != nil {
		log().Warn(name+"行扫描失败", "rows_read", len(out), "cause", scanErr)
		return nil, fmt.Errorf("扫%s: %w", name, scanErr)
	}
	return out, nil
}

func eventsPayloadBytes(events []Event) int {
	total := 0
	for _, event := range events {
		total += len(event.Payload)
	}
	return total
}
