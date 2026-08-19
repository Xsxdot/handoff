// 账本侧镜像原语：镜像者（internal/ledgermirror）与看板共用。
// watermark 不设独立游标，从 card_events 的 MAX(source_seq) 派生，
// 让游标与数据同源；幂等写入在 mutate 全局写锁内先查后插，唯一索引作最后防线。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MirroredEvent 一条待镜像的 task 事件（来源三元组 + 原始负载）。
type MirroredEvent struct {
	Target, Task string
	SourceSeq    int64
	Type         string // 原 task 事件类型，存入 payload；账本事件类型恒为 task_mirrored
	Payload      []byte
	CreatedAt    time.Time
}

// MirrorHealthRow per-target 镜像健康行（滞后判定数据源）。
type MirrorHealthRow struct {
	Target    string
	LastSeq   int64
	UpdatedAt time.Time
}

// AppendMirroredEvent 幂等写入镜像事件。返回是否真的写入（false=重放跳过）。
// 账本事件 type=task_mirrored，payload 包原类型与原始负载；来源三元组落
// source_* 列。写入即 NOTIFY（单流消费者不区分事件出身）。
func (s *Store) AppendMirroredEvent(cardID string, ev MirroredEvent) (bool, error) {
	inserted := false
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		var one int
		err := tx.QueryRow(s.q(`SELECT 1 FROM card_events
			WHERE source_target = ? AND source_task = ? AND source_seq = ?`),
			ev.Target, ev.Task, ev.SourceSeq).Scan(&one)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("查幂等键: %w", err)
		}
		inner := string(ev.Payload)
		if len(ev.Payload) == 0 {
			inner = "null"
		}
		payload := fmt.Sprintf(`{"task_type":%q,"payload":%s}`, ev.Type, inner)
		var seq int64
		if s.dialect == dialectPG {
			err = tx.QueryRow(s.q(`INSERT INTO card_events
				(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
				VALUES (?,?,?,?,?,?,?,?) RETURNING seq`),
				cardID, EvTaskMirrored, "mirror", payload,
				ev.Target, ev.Task, ev.SourceSeq, s.tval(ev.CreatedAt)).Scan(&seq)
			if err == nil {
				_, err = tx.Exec(`SELECT pg_notify('card_events', $1)`, fmt.Sprint(seq))
			}
		} else {
			var res sql.Result
			res, err = tx.Exec(s.q(`INSERT INTO card_events
				(card_id, type, actor, payload, source_target, source_task, source_seq, created_at)
				VALUES (?,?,?,?,?,?,?,?)`),
				cardID, EvTaskMirrored, "mirror", payload,
				ev.Target, ev.Task, ev.SourceSeq, s.tval(ev.CreatedAt))
			if err == nil {
				seq, err = res.LastInsertId()
			}
		}
		if err != nil {
			return fmt.Errorf("写镜像事件 %s/%s#%d: %w", ev.Target, ev.Task, ev.SourceSeq, err)
		}
		sink.seqs = append(sink.seqs, seq)
		inserted = true
		return nil
	})
	return inserted && err == nil, err
}

// MirrorWatermark per (target, task) 的续拉起点 = 已镜像的最大原 seq。
func (s *Store) MirrorWatermark(target, task string) (int64, error) {
	var wm sql.NullInt64
	err := s.db.QueryRow(s.q(`SELECT MAX(source_seq) FROM card_events
		WHERE source_target = ? AND source_task = ?`), target, task).Scan(&wm)
	if err != nil {
		return 0, fmt.Errorf("查 watermark: %w", err)
	}
	return wm.Int64, nil
}

// AcquireMirrorLease 取/续镜像 lease（单行表 CAS）。规则：无持有者、
// 持有者是自己、或已过期 → 得到并续期；否则 false。
func (s *Store) AcquireMirrorLease(holder string, ttl time.Duration) (bool, error) {
	got := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		var cur string
		var until any
		err := tx.QueryRow(s.q(`SELECT holder, lease_until FROM mirror_lease WHERE id = 1`)).Scan(&cur, &until)
		now := time.Now()
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if _, err := tx.Exec(s.q(`INSERT INTO mirror_lease (id, holder, lease_until) VALUES (1, ?, ?)`),
				holder, s.tval(now.Add(ttl))); err != nil {
				return fmt.Errorf("首建 lease: %w", err)
			}
			log().Info("镜像 lease 首取", "holder", holder)
			got = true
			return nil
		case err != nil:
			return fmt.Errorf("读 lease: %w", err)
		}
		if cur != holder && toTime(until).After(now) {
			return nil
		}
		if cur != holder {
			log().Info("镜像 lease 接任", "from", cur, "to", holder)
		}
		if _, err := tx.Exec(s.q(`UPDATE mirror_lease SET holder = ?, lease_until = ? WHERE id = 1`),
			holder, s.tval(now.Add(ttl))); err != nil {
			return fmt.Errorf("写 lease: %w", err)
		}
		got = true
		return nil
	})
	return got, err
}

// TouchMirrorHealth per-target 心跳：updated_at 恒更新，last_seq 只升不降。
func (s *Store) TouchMirrorHealth(target string, lastSeq int64) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		_, err := tx.Exec(s.q(`INSERT INTO mirror_cursors (target, last_seq, updated_at)
			VALUES (?,?,?)
			ON CONFLICT (target) DO UPDATE SET
				last_seq = CASE WHEN excluded.last_seq > mirror_cursors.last_seq
					THEN excluded.last_seq ELSE mirror_cursors.last_seq END,
				updated_at = excluded.updated_at`),
			target, lastSeq, s.tval(time.Now()))
		if err != nil {
			return fmt.Errorf("touch 镜像健康 %s: %w", target, err)
		}
		return nil
	})
}

// MirrorHealth 全部 target 的健康行（看板滞后判定：now-UpdatedAt>60s 亮灯）。
func (s *Store) MirrorHealth() ([]MirrorHealthRow, error) {
	rows, err := s.db.Query(`SELECT target, last_seq, updated_at FROM mirror_cursors ORDER BY target`)
	if err != nil {
		return nil, fmt.Errorf("读镜像健康: %w", err)
	}
	defer rows.Close()
	var out []MirrorHealthRow
	for rows.Next() {
		var r MirrorHealthRow
		var ut any
		if err := rows.Scan(&r.Target, &r.LastSeq, &ut); err != nil {
			return nil, err
		}
		r.UpdatedAt = toTime(ut)
		out = append(out, r)
	}
	return out, rows.Err()
}

// MaxSeq 账本单流当前最大 seq（wait 的起点）。
func (s *Store) MaxSeq() (int64, error) {
	var m sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(seq) FROM card_events`).Scan(&m); err != nil {
		return 0, fmt.Errorf("查最大 seq: %w", err)
	}
	return m.Int64, nil
}
