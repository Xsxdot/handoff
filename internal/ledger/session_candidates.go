// 会话订阅的有界候选读（B409 U5；冻结语义见
// docs/superpowers/specs/2026-09-25-session-bounded-candidate-read-contract.md）。
//
// 消费者是 cmd/session.go#runSessionWait：候选页在席位状态版本护栏内交给
// collab.Service.MessageWakeTargets 做唯一裁决。本文件只按「地址键 + seq 范围」
// 枚举可能命中的超集，绝不复制寻址规则（契约第 19/20 条）：
//
//   - mentions 精确含 member（外部身份）或含 driver_session=member 的卡号
//     （当前席位键是账本自有的 cards 列查询，不是第二份 @ 解析规则）；
//   - reply_to 指向 actor=member 的 room_message（同房间校验留给权威判定）；
//   - 两条分支都限 card_id IS NULL——会话订阅通道只对会话房间消息判定
//     （room.IsSessionRoom 前置，B358 §3.8），挂卡历史行永远不可能命中；
//     project:/global 等无卡房间按超集放行，由权威判定拒绝。
//
// 由此刻意不做 SQL 过滤的行（by_system/pointer、无卡但非会话房间、reply 跨
// 房间）会出现在候选里——这是超集契约的一部分，internal/ledger 的金样测试
// 逐行钉住。mentions 精确等值依赖写入侧归一化（collab normalizeMentions 去首尾
// 空白后落账），历史存量已按同一写边界产生。
//
// 物理读面：两条分支都走 room_message 局部索引 + seq 范围（ddl 的
// idx_room_messages_seq/idx_room_reply_to_seq/idx_room_messages_actor_seq），
// 镜像/结构等无关事件不进入扫描域；增长证据与执行计划由
// session_candidates_perf_test 断言。
package ledger

import (
	"context"
	"fmt"
	"time"
)

// sessionCandidatesLimitMax 单页候选上限的保守天花板；调用方（CLI 扫描器）
// 用更小的页步进，这里只防一次性搬回超大结果。
const sessionCandidatesLimitMax = 10000

// 方言表达式：PG 用 jsonb 数组展开，SQLite 用 json_each（store.go 文件头允许
// 的 JSON 表达式方言点，rooms.go roomExpr 同款先例）。占位符统一写 ?，经 q()
// 重写为 $N——禁止使用 jsonb 的 ? 存在性运算符（会被 q() 吃掉）。
const (
	mentionMatchPG = `EXISTS (SELECT 1 FROM jsonb_array_elements_text(payload->'mentions') m WHERE m = ?)`
	mentionCardMatchPG = `EXISTS (
		SELECT 1 FROM jsonb_array_elements_text(payload->'mentions') m
		JOIN cards c ON c.id = m
		WHERE c.driver_session = ?)`
	replyToExprPG = `(%s.payload->>'reply_to')::bigint`

	mentionMatchSQLite = `EXISTS (SELECT 1 FROM json_each(payload, '$.mentions') m WHERE m.value = ?)`
	mentionCardMatchSQLite = `EXISTS (
		SELECT 1 FROM json_each(payload, '$.mentions') m
		JOIN cards c ON c.id = m.value
		WHERE c.driver_session = ?)`
	replyToExprSQLite = `json_extract(%s.payload, '$.reply_to')`
)

// sessionCandidatesSQL 组装候选读的两分支 UNION 查询与参数（不含末尾 LIMIT 的
// 参数——调用方取回 args 后 append(limit) 再执行）。fromSeq 排他；toSeq 包含
// （<=0 表示无上界，follow 实时段用）。单独抽出是为了让方言执行计划测试复用
// 同一份 SQL 文本。
func (s *Store) sessionCandidatesSQL(member string, fromSeq, toSeq int64) (string, []any) {
	upperCardless := ""
	upperAliased := ""
	if toSeq > 0 {
		upperCardless = ` AND seq <= ?`
		upperAliased = ` AND e.seq <= ?`
	}
	mentionMatch, mentionCardMatch, replyToExpr := mentionMatchPG, mentionCardMatchPG, replyToExprPG
	if s.dialect == dialectSQLite {
		mentionMatch, mentionCardMatch, replyToExpr = mentionMatchSQLite, mentionCardMatchSQLite, replyToExprSQLite
	}
	// UNION 去重：同一消息可能同时命中身份 @ 与 reply 两分支（整体行相同）。
	query := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events
		WHERE type = 'room_message' AND card_id IS NULL AND seq > ?` + upperCardless + `
		AND (` + mentionMatch + ` OR ` + mentionCardMatch + `)
		UNION
		SELECT e.seq, e.card_id, e.type, e.actor, e.payload, e.source_target, e.source_task, e.source_seq, e.created_at
		FROM card_events e
		WHERE e.type = 'room_message' AND e.card_id IS NULL AND e.seq > ?` + upperAliased + `
		AND EXISTS (SELECT 1 FROM card_events r WHERE r.seq = ` + fmt.Sprintf(replyToExpr, "e") + `
			AND r.type = 'room_message' AND r.actor = ?)
		ORDER BY seq ASC LIMIT ?`
	var args []any
	args = append(args, fromSeq)
	if toSeq > 0 {
		args = append(args, toSeq)
	}
	// mentions 两分支共用 member：一是 mentions∋member 身份精确匹配，二是
	// member 当前席位卡号（cards.driver_session 查询）。
	args = append(args, member, member, fromSeq)
	if toSeq > 0 {
		args = append(args, toSeq)
	}
	args = append(args, member)
	return query, args
}

// SessionMessageCandidates 读一页会话消息候选：member 身份 @、member 当前席位
// 卡号 @、reply_to→member 作者的 room_message 超集，seq 升序。fromSeq 排他；
// toSeq 包含（<=0 无上界）；limit 必须 >0。席位状态一致性由调用方（CLI 扫描器）
// 以 SeatRevision 前后复读保证——本方法不做一致性判断。
func (s *Store) SessionMessageCandidates(member string, fromSeq, toSeq int64, limit int) ([]Event, error) {
	return s.SessionMessageCandidatesContext(context.Background(), member, fromSeq, toSeq, limit)
}

// SessionMessageCandidatesContext 同 SessionMessageCandidates，带取消传播。
func (s *Store) SessionMessageCandidatesContext(ctx context.Context, member string, fromSeq, toSeq int64, limit int) ([]Event, error) {
	if member == "" {
		return nil, fmt.Errorf("会话候选读需要 member")
	}
	if limit <= 0 || limit > sessionCandidatesLimitMax {
		return nil, fmt.Errorf("会话候选读 limit 必须在 (0,%d]: %d", sessionCandidatesLimitMax, limit)
	}
	started := time.Now()
	query, args := s.sessionCandidatesSQL(member, fromSeq, toSeq)
	args = append(args, limit)
	if err := ctx.Err(); err != nil {
		log().Info("会话候选读已取消", "member", member, "from_seq", fromSeq, "to_seq", toSeq, "cause", err)
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, s.q(query), args...)
	if err != nil {
		log().Warn("会话候选读 SQL 失败", "member", member, "from_seq", fromSeq, "to_seq", toSeq, "cause", err)
		return nil, fmt.Errorf("读会话消息候选: %w", err)
	}
	defer rows.Close()
	out, scanErr := scanProjectionEvents(rows)
	if scanErr != nil {
		log().Warn("会话候选读行扫描失败", "member", member, "rows_read", len(out), "cause", scanErr)
		return nil, fmt.Errorf("扫会话消息候选: %w", scanErr)
	}
	payloadBytes := 0
	for _, event := range out {
		payloadBytes += len(event.Payload)
	}
	log().Info("会话候选读完成", "member", member, "from_seq", fromSeq, "to_seq", toSeq,
		"rows_returned", len(out), "payload_bytes", payloadBytes,
		"elapsed_ns", time.Since(started).Nanoseconds())
	return out, nil
}
