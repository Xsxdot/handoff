// 协作房间域（B156.2）在账本侧的写入与查询：房间消息、消费标记及有界历史读取。
// 房间、成员、白名单等规则归 d_collab；账本只承事件流机制和可重建查询索引，
// 不解释 RoomMessage 的业务字段。
package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// RoomMessagesBeforeContext 只从账本取指定房间最新 limit 条 room_message。
// beforeSeq>0 时使用排他上界；数据库先按倒序截取，返回前再转成升序。
func (s *Store) RoomMessagesBeforeContext(ctx context.Context, roomID string, beforeSeq int64, limit int) ([]Event, error) {
	started := time.Now()
	log().Info("房间历史账本查询开始", "room_id", roomID,
		"before_seq", beforeSeq, "limit", limit)
	if limit <= 0 {
		err := fmt.Errorf("房间历史 limit 必须为正数: %d", limit)
		log().Warn("房间历史账本查询参数无效", "room_id", roomID, "limit", limit, "cause", err)
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		log().Info("房间历史账本查询已取消", "room_id", roomID,
			"before_seq", beforeSeq, "elapsed_ns", time.Since(started).Nanoseconds(), "cause", err)
		return nil, err
	}

	// RoomIDOf 先取非空 card_id，仅无卡事件才使用 payload.Room；拆开查询分支
	// 让卡索引和下方 JSON 表达式索引各自限域，再合并两边各自最多 limit 条。
	roomExpr := `payload->>'room'`
	if s.dialect == dialectSQLite {
		roomExpr = `json_extract(payload, '$.room')`
	}
	upper := ""
	cardArgs := []any{roomID}
	payloadArgs := []any{roomID}
	if beforeSeq > 0 {
		upper = ` AND seq < ?`
		cardArgs = append(cardArgs, beforeSeq)
		payloadArgs = append(payloadArgs, beforeSeq)
	}
	// Keep the event type literal so PostgreSQL can prove the partial-index predicate
	// even if pgx reuses a generic prepared plan.
	cardQuery := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events WHERE type = 'room_message' AND card_id = ?` + upper + ` ORDER BY seq DESC LIMIT ?`
	cardArgs = append(cardArgs, limit)
	payloadQuery := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events WHERE type = 'room_message' AND card_id IS NULL AND ` + roomExpr + ` = ?` + upper + ` ORDER BY seq DESC LIMIT ?`
	payloadArgs = append(payloadArgs, limit)
	// 两个分支的局部 LIMIT 防止收集该房间的全部历史；最终再按全局 seq 截最近窗口。
	query := `WITH card_room AS (` + cardQuery + `), payload_room AS (` + payloadQuery + `)
		SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM (SELECT * FROM card_room UNION ALL SELECT * FROM payload_room) AS room_events
		ORDER BY seq DESC LIMIT ?`
	args := make([]any, 0, len(cardArgs)+len(payloadArgs)+1)
	args = append(args, cardArgs...)
	args = append(args, payloadArgs...)
	args = append(args, limit)

	// 单独获取 *sql.Conn 才能把本请求的池等待与发起 SQL 的耗时分开；
	// DB.Stats().WaitDuration 是全池累计值，在并发场景不能归因给当前请求。
	poolStarted := time.Now()
	conn, err := s.db.Conn(ctx)
	poolWait := time.Since(poolStarted)
	if err != nil {
		level := log().Warn
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			level = log().Info
		}
		level("房间历史账本获取连接失败", "room_id", roomID,
			"before_seq", beforeSeq, "limit", limit,
			"pool_wait_ns", poolWait.Nanoseconds(),
			"elapsed_ns", time.Since(started).Nanoseconds(), "cause", err)
		return nil, fmt.Errorf("获取账本连接: %w", err)
	}
	defer conn.Close()
	dbStarted := time.Now()
	queryStarted := time.Now()
	rows, err := conn.QueryContext(ctx, s.q(query), args...)
	sqlCall := time.Since(queryStarted)
	if err != nil {
		level := log().Warn
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			level = log().Info
		}
		level("房间历史账本 SQL 调用失败", "room_id", roomID,
			"before_seq", beforeSeq, "limit", limit,
			"pool_wait_ns", poolWait.Nanoseconds(),
			"sql_call_ns", sqlCall.Nanoseconds(),
			"elapsed_ns", time.Since(started).Nanoseconds(), "cause", err)
		return nil, fmt.Errorf("读房间消息: %w", err)
	}
	defer rows.Close()

	// QueryContext 时长覆盖 SQL 执行和首批结果到达；逐行传输/扫描另计，
	// 这样慢首包与大量结果的读取成本不会混成一个数字。
	out := make([]Event, 0, limit)
	payloadBytes := 0
	rowReadStarted := time.Now()
	for rows.Next() {
		var event Event
		var cardID, sourceTarget, sourceTask sql.NullString
		var sourceSeq sql.NullInt64
		var raw string
		var createdAt any
		if err := rows.Scan(&event.Seq, &cardID, &event.Type, &event.Actor, &raw,
			&sourceTarget, &sourceTask, &sourceSeq, &createdAt); err != nil {
			log().Warn("房间历史账本行读取失败", "room_id", roomID,
				"before_seq", beforeSeq, "limit", limit, "rows_read", len(out),
				"pool_wait_ns", poolWait.Nanoseconds(),
				"sql_call_ns", sqlCall.Nanoseconds(),
				"row_read_ns", time.Since(rowReadStarted).Nanoseconds(),
				"elapsed_ns", time.Since(started).Nanoseconds(), "cause", err)
			return nil, fmt.Errorf("扫描房间消息行: %w", err)
		}
		event.CardID = cardID.String
		event.SourceTarget = sourceTarget.String
		event.SourceTask = sourceTask.String
		event.SourceSeq = sourceSeq.Int64
		event.Payload = json.RawMessage(raw)
		event.CreatedAt = toTime(createdAt)
		payloadBytes += len(raw)
		out = append(out, event)
	}
	if err := rows.Err(); err != nil {
		level := log().Warn
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			level = log().Info
		}
		level("房间历史账本遍历失败", "room_id", roomID,
			"before_seq", beforeSeq, "limit", limit, "rows_read", len(out),
			"payload_bytes", payloadBytes,
			"pool_wait_ns", poolWait.Nanoseconds(),
			"sql_call_ns", sqlCall.Nanoseconds(),
			"row_read_ns", time.Since(rowReadStarted).Nanoseconds(),
			"elapsed_ns", time.Since(started).Nanoseconds(), "cause", err)
		return nil, fmt.Errorf("遍历房间消息行: %w", err)
	}
	rowRead := time.Since(rowReadStarted)
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}

	dbReadDuration := time.Since(dbStarted)
	log().Info("房间历史账本查询完成", "room_id", roomID,
		"before_seq", beforeSeq, "limit", limit, "rows_returned", len(out),
		"payload_bytes", payloadBytes,
		"pool_wait_ns", poolWait.Nanoseconds(),
		"sql_call_ns", sqlCall.Nanoseconds(),
		"row_read_ns", rowRead.Nanoseconds(),
		"db_read_ns", dbReadDuration.Nanoseconds(),
		"elapsed_ns", time.Since(started).Nanoseconds())
	return out, nil
}

// RecordRoomMessage 落一条房间消息事件。cardID 非空 = 卡会话消息（必须
// 指向存在的卡）；空 = 群级无卡事件（项目群/全员群，天然不进多路 wait）。
// 返回 seq。白名单与书写者执法在 d_collab 的 Send，不在本方法。
func (s *Store) RecordRoomMessage(cardID string, msg proto.RoomMessage, actor string) (int64, error) {
	var seq int64
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if cardID != "" {
			if _, err := getCardTx(s, tx, cardID); err != nil {
				return err
			}
		}
		var err error
		seq, err = s.appendEvent(tx, sink, cardID, EvRoomMessage, actor, msg)
		return err
	})
	return seq, err
}

// consumedMarker 是 message_consumed 事件的载荷 schema（契约 §4 金样键集：
// 恰 message_seq 与 consumer 两键）。actor 列另存 consumer 一份供查重与
// 读侧扫描先按列粗筛；载荷才是权威。collab 侧不得 import 本包，其本地
// 镜像 struct 的等值由两侧字面量测试钉住（TestRoomEventTypeLiteralMatchesLedger
// 同形）。刻意不放 proto：d_protocol 本轮零触碰。
type consumedMarker struct {
	MessageSeq int64  `json:"message_seq"`
	Consumer   string `json:"consumer"`
}

// RecordMessageConsumed 落消息消费标记：同一 mutate 事务内查重后写
// （ClearNeedsHumanFrom 同形，events.go），同 (msgSeq, consumer) 重复消费
// 是幂等 no-op。恰好一次由 mutate 的单写者串行化免费获得（store.go mutate
// 注释）——查重与写入之间的窗口被事务吃掉，这是拍板 5.4「权威在账本事件」
// 的机制兑现。
//
// 语义边界（都有测试钉着）：
//   - cardID 非空时必须指向存在的卡（ErrNotFound），标记挂同一张卡的流上；
//     cardID=""=群级消息的项目级标记。cardID 不参与查重键——seq 全局唯一，
//     传错 cardID 时幂等性优先于报错。
//   - 不校验 msgSeq 是否指向存在的 room_message：目标态「已消费」对不存在
//     消息天然成立（breakdown 岔口六方案甲，未知 seq 同样真落一条标记），
//     静默面由 Pending/Mentions 只列未消费兜底。要改这个选择先回 contract。
//   - consumer 为空直接报错：「谁消费了哪条」没有「谁」无意义。
//
// 返回 nil 含三种情形：首次写入成功、本人重复消费跳过、他人已消费后再写
// 自己的标记（各消费者一条，互不顶替）——前两种全流恰一条本人标记，第三种
// 是新消费者的首次写入。
func (s *Store) RecordMessageConsumed(cardID string, msgSeq int64, consumer string) error {
	if consumer == "" {
		return fmt.Errorf("消费标记必须带 consumer")
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if cardID != "" {
			if _, err := getCardTx(s, tx, cardID); err != nil {
				return fmt.Errorf("消费标记: 卡 %s: %w", cardID, err)
			}
		}
		// 查重：先按 type+actor 列粗筛出本人的全部标记，再解载荷精确比对
		// message_seq。载荷匹配无法用跨 SQLite(TEXT)/PG(JSONB) 方言的 SQL
		// 表达，Go 解析是唯一可移植路径；单消费者的标记量以「他消费过的
		// 消息数」为界，全扫可承受。
		rows, err := tx.Query(s.q(`SELECT payload FROM card_events WHERE type = ? AND actor = ?`),
			EvMessageConsumed, consumer)
		if err != nil {
			return fmt.Errorf("查消费标记: %w", err)
		}
		defer rows.Close()
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return fmt.Errorf("扫消费标记行: %w", err)
			}
			var marker consumedMarker
			if err := json.Unmarshal([]byte(raw), &marker); err != nil {
				continue // 非法载荷不该出现；跳过不中断幂等判定
			}
			if marker.MessageSeq == msgSeq && marker.Consumer == consumer {
				log().Info("消费标记幂等跳过：同参标记已存在",
					"msg_seq", msgSeq, "consumer", consumer, "card", cardID)
				return nil
			}
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("遍历消费标记: %w", err)
		}
		seq, err := s.appendEvent(tx, sink, cardID, EvMessageConsumed, consumer,
			consumedMarker{MessageSeq: msgSeq, Consumer: consumer})
		if err != nil {
			return err
		}
		log().Info("消息消费标记已落账", "msg_seq", msgSeq, "consumer", consumer,
			"card", cardID, "event_seq", seq)
		return nil
	})
}
