// 账本事件单流：同事务 append、PG 事务内 pg_notify 推送、游标升序读。
// 追加语义与 internal/store 的 events 表同源：append-only、seq 全局
// 自增（单卡内稀疏）、升序读截断尾部保证游标只越过真正收到的事件。
package ledger

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// appendEvent 在事务内落一条账本事件并（PG）推送通知。cardID 可空 =
// 项目级事件。返回 seq。所有领域操作共用此入口，禁止绕过它裸 INSERT。
func (s *Store) appendEvent(tx *sql.Tx, sink *eventSink, cardID, typ, actor string, payload any) (int64, error) {
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
			cid, typ, actor, string(raw), s.tval(time.Now())).Scan(&seq)
		if err == nil {
			_, err = tx.Exec(`SELECT pg_notify('card_events', $1)`, fmt.Sprint(seq))
		}
	} else {
		var result sql.Result
		result, err = tx.Exec(s.q(`INSERT INTO card_events (card_id, type, actor, payload, created_at)
			VALUES (?,?,?,?,?)`),
			cid, typ, actor, string(raw), s.tval(time.Now()))
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
