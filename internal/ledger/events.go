// 账本事件单流：同事务 append、PG 事务内 pg_notify 推送、游标升序读。
// 追加语义与 internal/store 的 events 表同源：append-only、seq 全局
// 自增（单卡内稀疏）、升序读截断尾部保证游标只越过真正收到的事件。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// appendEvent 在事务内落一条账本事件并（PG）推送通知。cardID 可空 =
// 项目级事件。返回 seq。所有领域操作共用此入口，禁止绕过它裸 INSERT。
func (s *Store) appendEvent(tx *sql.Tx, sink *eventSink, cardID, typ, actor string, payload any) (int64, error) {
	return s.appendEventAt(tx, sink, cardID, typ, actor, payload, time.Now())
}

// appendEventAt 同 appendEvent，但由调用方给定事件时间——调用方需要把
// 同一个时间戳回填进返回给上层的 Event，否则返回值的 CreatedAt 是零值，
// 机器消费方（CLI stdout、Plan D 的 HTTP API）会读到 0001-01-01。
func (s *Store) appendEventAt(tx *sql.Tx, sink *eventSink, cardID, typ, actor string, payload any, at time.Time) (int64, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("编码事件 payload: %w", err)
	}
	var cid any
	if cardID != "" {
		cid = cardID
	}
	var seq int64
	if s.dialect == dialectPG {
		// PG 用 RETURNING 拿 seq，并在同事务内 NOTIFY——提交即送达 LISTEN 端
		err = tx.QueryRow(s.q(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
			VALUES (?,?,?,?,?) RETURNING seq`),
			cid, typ, actor, string(raw), s.tval(at)).Scan(&seq)
		if err == nil {
			_, err = tx.Exec(`SELECT pg_notify('card_events', $1)`, fmt.Sprint(seq))
		}
	} else {
		var result sql.Result
		result, err = tx.Exec(s.q(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
			VALUES (?,?,?,?,?)`),
			cid, typ, actor, string(raw), s.tval(at))
		if err == nil {
			seq, err = result.LastInsertId()
		}
	}
	if err != nil {
		return 0, fmt.Errorf("落账本事件 %s: %w", typ, err)
	}
	sink.seqs = append(sink.seqs, seq)
	return seq, nil
}

// EventsFromAsc 按 seq 升序读事件。cardIDs 空 = 全流（含项目级）；fromSeq
// 排他；limit<=0 取 1000。升序 + LIMIT 截尾，游标语义与 store.EventsFromAsc
// 一致——绝不能改成降序截头，那会让游标永久跨过缺口。
func (s *Store) EventsFromAsc(cardIDs []string, fromSeq int64, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 1000
	}
	q := `SELECT seq, card_id, type, actor, payload, source_target, source_task, source_seq, created_at
		FROM card_events WHERE seq > ?`
	args := []any{fromSeq}
	if len(cardIDs) > 0 {
		q += ` AND card_id IN (?` + strings.Repeat(",?", len(cardIDs)-1) + `)`
		for _, id := range cardIDs {
			args = append(args, id)
		}
	}
	q += ` ORDER BY seq ASC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(s.q(q), args...)
	if err != nil {
		return nil, fmt.Errorf("读账本事件流: %w", err)
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var event Event
		var cardID, sourceTarget, sourceTask sql.NullString
		var sourceSeq sql.NullInt64
		var raw string
		var createdAt any
		if err := rows.Scan(&event.Seq, &cardID, &event.Type, &event.Actor, &raw,
			&sourceTarget, &sourceTask, &sourceSeq, &createdAt); err != nil {
			return nil, fmt.Errorf("扫描事件行: %w", err)
		}
		event.CardID = cardID.String
		event.SourceTarget = sourceTarget.String
		event.SourceTask = sourceTask.String
		event.SourceSeq = sourceSeq.Int64
		event.Payload = json.RawMessage(raw)
		event.CreatedAt = toTime(createdAt)
		out = append(out, event)
	}
	return out, rows.Err()
}

// ---- 以下为建立在事件流上的领域操作 ----

var cardRefPat = regexp.MustCompile(`#(B\d+(?:\.\d+)*)`)

// DispatchSnapshot 派发事件快照：模板版本 + 纪律块 hash + 落点。
// 「B107 那次派发用的哪版纪律块」从这里答（蓝图 §3.3 取证文化）。
type DispatchSnapshot struct {
	Template        string `json:"template"`
	TemplateVersion int    `json:"template_version"`
	DisciplineHash  string `json:"discipline_hash"`
	Target          string `json:"target"`
	TaskID          string `json:"task_id"`
	Branch          string `json:"branch"`
	PlanPath        string `json:"plan_path,omitempty"`
	Actor           string `json:"-"`
}

// RecordDispatch 落派发事件。
func (s *Store) RecordDispatch(cardID string, snap DispatchSnapshot) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("派发落账: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvDispatched, snap.Actor, snap)
		return err
	})
}

// AddComment 发评论。body 里的 #B 号引用解析出来：存在的卡自动建
// relates 边（幂等），不存在的只留在 refs 里（评论是记录不是校验）。
// kind ∈ {普通, 更正}——「更正」承接 markdown 总账的变更痕迹文化。
func (s *Store) AddComment(cardID, body, kind, actor string) (Event, error) {
	return s.addComment(cardID, body, kind, actor, "")
}

// AddCommentReset 同 AddComment，但 payload 附 human_reset_node=<节点>。
// 这是「人工介入重置回合计数」（spec §5）的唯一落账入口：节点执行器
// （Plan C 的 CountRounds）读到该字段即把对应节点的裁决轮次清零。
func (s *Store) AddCommentReset(cardID, body, kind, actor, resetNode string) (Event, error) {
	if resetNode == "" {
		return Event{}, fmt.Errorf("重置评论: 节点名不能为空")
	}
	return s.addComment(cardID, body, kind, actor, resetNode)
}

func (s *Store) addComment(cardID, body, kind, actor, resetNode string) (Event, error) {
	if kind != "普通" && kind != "更正" {
		return Event{}, fmt.Errorf("评论 kind %q 不在 {普通,更正}", kind)
	}
	var event Event
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("评论: 卡 %s: %w", cardID, err)
		}
		var refs []string
		for _, match := range cardRefPat.FindAllStringSubmatch(body, -1) {
			ref := match[1]
			if ref == cardID {
				continue
			}
			refs = append(refs, ref)
			if _, err := getCardTx(s, tx, ref); err != nil {
				if errors.Is(err, ErrNotFound) {
					continue // 幽灵引用：不建边不报错
				}
				return fmt.Errorf("评论引用查卡 %s: %w", ref, err)
			}
			// 幂等建边：已存在同主键则忽略（两方言都支持 ON CONFLICT DO NOTHING）
			if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
				VALUES (?,?,?,?) ON CONFLICT (from_id, to_id, type) DO NOTHING`),
				cardID, ref, RelRelates, s.tval(time.Now())); err != nil {
				return fmt.Errorf("评论引用建边 %s: %w", ref, err)
			}
		}
		payload := map[string]any{"kind": kind, "body": body, "refs": refs}
		if resetNode != "" {
			payload["human_reset_node"] = resetNode
		}
		now := time.Now()
		seq, err := s.appendEventAt(tx, sink, cardID, EvComment, actor, payload, now)
		if err != nil {
			return err
		}
		event = Event{Seq: seq, CardID: cardID, Type: EvComment, Actor: actor,
			CreatedAt: now.UTC()}
		raw, _ := json.Marshal(payload)
		event.Payload = raw
		return nil
	})
	return event, err
}

// RecordAcceptance 落验收结果事件（verified=true 表示真机已验）。
// 判据文本在卡字段；结果是事件——「已验/待真机验」从最后一条
// acceptance_recorded 推导。
func (s *Store) RecordAcceptance(cardID string, verified bool, evidence, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("验收: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvAcceptanceRecorded, actor,
			map[string]any{"verified_on_real_machine": verified, "evidence": evidence})
		return err
	})
}

// MarkNeedsHuman 打等人标记（reason 必填）；ClearNeedsHuman 清除。
// 等人不落列，从最后一条 needs_human/needs_cleared 事件推导（spec §2）。
func (s *Store) MarkNeedsHuman(cardID, reason, actor string) error {
	if strings.TrimSpace(reason) == "" {
		return fmt.Errorf("等人标记必须带 reason")
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("等人: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvNeedsHuman, actor, map[string]any{"reason": reason})
		return err
	})
}

// ClearNeedsHuman 清除等人标记。
func (s *Store) ClearNeedsHuman(cardID, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("清等人: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvNeedsCleared, actor, map[string]any{})
		return err
	})
}

// Subtree 返回卡树成员 id 集：root + 全部后代（parent 链）+ 并入成员
// （merged_into 指向集内任一成员的卡）。多路 wait 与看板 rollup 共用。
func (s *Store) Subtree(rootID string) ([]string, error) {
	if _, err := s.GetCard(rootID); err != nil {
		return nil, err
	}
	set := map[string]bool{rootID: true}
	frontier := []string{rootID}
	for len(frontier) > 0 {
		q := `SELECT id FROM cards WHERE parent_id IN (?` + strings.Repeat(",?", len(frontier)-1) + `)`
		args := make([]any, len(frontier))
		for i, id := range frontier {
			args[i] = id
		}
		rows, err := s.db.Query(s.q(q), args...)
		if err != nil {
			return nil, fmt.Errorf("读子树: %w", err)
		}
		var next []string
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if !set[id] {
				set[id] = true
				next = append(next, id)
			}
		}
		rowErr := rows.Err()
		rows.Close()
		if rowErr != nil {
			return nil, rowErr
		}
		frontier = next
	}
	ids := make([]string, 0, len(set))
	for id := range set {
		ids = append(ids, id)
	}
	q := `SELECT from_id FROM card_relations WHERE type = 'merged_into' AND to_id IN (?` +
		strings.Repeat(",?", len(ids)-1) + `)`
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	rows, err := s.db.Query(s.q(q), args...)
	if err != nil {
		return nil, fmt.Errorf("读并入成员: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		set[id] = true
	}
	out := make([]string, 0, len(set))
	for id := range set {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, rows.Err()
}
