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
	"database/sql"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/diag"
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

// sessionCandidatesBranchSQL 组装单条分支查询与参数（不含 ORDER BY/LIMIT——
// 复合选择只允许末尾一个 ORDER BY；分支单独执行时由调用方补
// ` ORDER BY seq ASC LIMIT ?`）。branch: "mentions"（身份 @ + 当前席位卡 @）
// 或 "reply"（reply_to→member 作者）。单独抽出是为了让方言执行计划测试能对
// 每个分支分别取证，主查询用 UNION 组合。
//
// SQLite 对两分支显式 INDEXED BY idx_room_messages_cardless_seq：优化器在
// idx_events_card(card_id IS NULL, seq) 与 room_message 局部索引之间会选前者，
// 把全部无卡事件（含镜像/结构）拉进扫描域——显式钉死到无卡 room_message 的
// seq 范围（EQP 证据断言扫描域，见 session_candidates_perf_test）。PG 优化器
// 对同一谓词自行选择 room_message 局部索引，无需钉死。
func (s *Store) sessionCandidatesBranchSQL(member string, fromSeq, toSeq int64, branch string) (string, []any) {
	upper := ""
	if toSeq > 0 {
		upper = ` AND seq <= ?`
	}
	indexed := ""
	if s.dialect == dialectSQLite {
		indexed = ` INDEXED BY idx_room_messages_cardless_seq`
	}
	var query string
	var args []any
	switch branch {
	case "mentions":
		mentionMatch, mentionCardMatch := mentionMatchPG, mentionCardMatchPG
		if s.dialect == dialectSQLite {
			mentionMatch, mentionCardMatch = mentionMatchSQLite, mentionCardMatchSQLite
		}
		query = `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
			FROM card_events` + indexed + `
			WHERE type = 'room_message' AND card_id IS NULL AND seq > ?` + upper + `
			AND (` + mentionMatch + ` OR ` + mentionCardMatch + `)`
		args = append(args, fromSeq)
		if toSeq > 0 {
			args = append(args, toSeq)
		}
		// 一参给身份精确匹配，一参给 member 当前席位卡（cards.driver_session）。
		args = append(args, member, member)
	case "reply":
		replyToExpr := replyToExprPG
		if s.dialect == dialectSQLite {
			replyToExpr = replyToExprSQLite
		}
		query = `SELECT e.seq, e.card_id, e.type, e.actor, e.payload, e.source_target, e.source_task, e.source_seq, e.created_at
			FROM card_events e` + indexed + `
			WHERE e.type = 'room_message' AND e.card_id IS NULL AND e.seq > ?` + upper + `
			AND EXISTS (SELECT 1 FROM card_events r WHERE r.seq = ` + fmt.Sprintf(replyToExpr, "e") + `
				AND r.type = 'room_message' AND r.actor = ?)`
		args = append(args, fromSeq)
		if toSeq > 0 {
			args = append(args, toSeq)
		}
		args = append(args, member)
	default:
		return "", nil
	}
	return query, args
}

// sessionCandidatesSQL 组装候选读的两分支 UNION 查询与参数（不含末尾 LIMIT 的
// 参数——调用方取回 args 后 append(limit) 再执行）。fromSeq 排他；toSeq 包含
// （<=0 表示无上界，follow 实时段用）。单独抽出是为了让方言执行计划测试复用
// 同一份 SQL 文本。
func (s *Store) sessionCandidatesSQL(member string, fromSeq, toSeq int64) (string, []any) {
	// UNION 去重：同一消息可能同时命中身份 @ 与 reply 两分支（整体行相同）。
	mentionsQuery, mentionsArgs := s.sessionCandidatesBranchSQL(member, fromSeq, toSeq, "mentions")
	replyQuery, replyArgs := s.sessionCandidatesBranchSQL(member, fromSeq, toSeq, "reply")
	query := mentionsQuery + `
		UNION
		` + replyQuery + `
		ORDER BY seq ASC LIMIT ?`
	return query, append(mentionsArgs, replyArgs...)
}

// SessionMessageCandidates 读一页会话消息候选：member 身份 @、member 当前席位
// 卡号 @、reply_to→member 作者的 room_message 超集，seq 升序。fromSeq 排他；
// toSeq 包含（<=0 无上界）；limit 必须 >0。席位状态一致性由调用方（CLI 扫描器）
// 以 SeatRevision 前后复读保证——本方法不做一致性判断。
func (s *Store) SessionMessageCandidates(member string, fromSeq, toSeq int64, limit int) ([]Event, error) {
	return s.SessionMessageCandidatesContext(context.Background(), member, fromSeq, toSeq, limit)
}

// SessionMessageCandidatesContext 同 SessionMessageCandidates，带取消传播。
// B409.6：查询经 timedReadQuery 分离 pool/sql/row 阶段，日志带关联 id。
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
		log().Info("会话候选读已取消", append(diag.Attrs(ctx), "member", member,
			"from_seq", fromSeq, "to_seq", toSeq,
			"failed_stage", stageFailedStart, "error_class", readErrorClass(err, stageFailedStart), "cause", err)...)
		return nil, err
	}
	var out []Event
	stage, err := s.timedReadQuery(ctx, query, args, func(rows *sql.Rows) (int, int64, error) {
		events, scanErr := scanProjectionEvents(rows)
		if scanErr != nil {
			return 0, 0, scanErr
		}
		out = events
		return len(events), payloadBytesOf(events), nil
	})
	if err != nil {
		class := readErrorClass(err, stage.failedStage)
		level := log().Warn
		if class == "canceled" || class == "deadline_exceeded" {
			level = log().Info
		}
		level("会话候选读 SQL 失败", append(diag.Attrs(ctx), "member", member,
			"from_seq", fromSeq, "to_seq", toSeq,
			"error_class", readErrorClass(err, stage.failedStage), "failed_stage", stage.failedStage,
			"pool_wait_ns", stage.poolWaitNs, "sql_call_ns", stage.sqlCallNs,
			"elapsed_ns", time.Since(started).Nanoseconds(), "cause", err)...)
		return nil, fmt.Errorf("读会话消息候选: %w", err)
	}
	log().Info("会话候选读完成", append(append(append(diag.Attrs(ctx), "member", member,
		"from_seq", fromSeq, "to_seq", toSeq, "rows_returned", len(out)), stage.logAttrs()...),
		"elapsed_ns", time.Since(started).Nanoseconds())...)
	return out, nil
}
