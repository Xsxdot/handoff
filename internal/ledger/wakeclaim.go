// 唤醒认领（B389 契约 §3.1）：共库下同一条事件只允许一台机器处理。
//
// 认领键取 card_events.seq 本身（全局唯一），不叠加卡号——加上卡号等于给同一
// 事件造出两个可认领键，排他就没了。排他靠 mutate 的事务串行化（PG 上先取
// pg_advisory_xact_lock），不是概率性去重。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WakeClaim 一条唤醒认领行；Done 为空表示在飞。
type WakeClaim struct {
	Seq        int64
	Card       string
	Holder     string
	LeaseUntil time.Time
	Done       bool
}

// ClaimWake 尝试认领某条唤醒事件：拿到返回 true，别人持有且未过期返回 false
// （调用方据此直接跳过，不占名额、不试跑）。同持有者可续期；过期可被接管。
// 已终局（done_at 非空）的事件永远不再被认领。
func (s *Store) ClaimWake(seq int64, card, holder string, ttl time.Duration) (bool, error) {
	if seq <= 0 || card == "" || holder == "" {
		return false, fmt.Errorf("唤醒认领参数不完整（seq=%d card=%q holder=%q）: %w",
			seq, card, holder, ErrBadState)
	}
	got := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		var existingHolder string
		var leaseUntil any
		var doneAt any
		err := tx.QueryRow(s.q(`SELECT holder, lease_until, done_at FROM wake_claims WHERE seq = ?`),
			seq).Scan(&existingHolder, &leaseUntil, &doneAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// 首认领
		case err != nil:
			return fmt.Errorf("读唤醒认领 %d: %w", seq, err)
		default:
			if doneAt != nil {
				return nil
			}
			if existingHolder != holder && toTime(leaseUntil).After(s.timeNow()) {
				return nil
			}
		}
		if _, err := tx.Exec(s.q(`INSERT INTO wake_claims (seq, card, holder, lease_until, done_at)
			VALUES (?, ?, ?, ?, NULL)
			ON CONFLICT(seq) DO UPDATE SET card = excluded.card, holder = excluded.holder,
				lease_until = excluded.lease_until`),
			seq, card, holder, s.tval(s.timeNow().Add(ttl))); err != nil {
			return fmt.Errorf("写唤醒认领 %d: %w", seq, err)
		}
		got = true
		return nil
	})
	if err != nil {
		log().Warn("唤醒认领失败", "seq", seq, "card", card, "holder", holder, "cause", err)
		return false, err
	}
	if got {
		log().Info("唤醒认领成功", "seq", seq, "card", card, "holder", holder)
	} else {
		log().Info("唤醒认领让过", "seq", seq, "card", card, "holder", holder)
	}
	return got, nil
}

// CompleteWake 收尾一条已认领的事件；只允许持有者调用，非持有者返回
// ErrCASConflict 且不改任何行。收尾是幂等的（重复收尾仍成功）。
func (s *Store) CompleteWake(seq int64, holder string) error {
	if seq <= 0 || holder == "" {
		return fmt.Errorf("唤醒收尾参数不完整（seq=%d holder=%q）: %w", seq, holder, ErrBadState)
	}
	updated := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`UPDATE wake_claims SET done_at = ? WHERE seq = ? AND holder = ?`),
			s.tval(s.timeNow()), seq, holder)
		if err != nil {
			return fmt.Errorf("收尾唤醒认领 %d: %w", seq, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读收尾影响行数 %d: %w", seq, err)
		}
		updated = n > 0
		return nil
	})
	if err != nil {
		log().Warn("唤醒收尾失败", "seq", seq, "holder", holder, "cause", err)
		return err
	}
	if !updated {
		err := fmt.Errorf("唤醒认领 %d 不属于 %s: %w", seq, holder, ErrCASConflict)
		log().Warn("唤醒收尾被拒：非持有者", "seq", seq, "holder", holder, "cause", err)
		return err
	}
	log().Info("唤醒收尾完成", "seq", seq, "holder", holder)
	return nil
}

// WakeClaimsBefore 列出 seq 小于水位的仍在飞认领（升序）。游标只能推进到
// 这些 seq 的最小值之前——否则对端崩溃或超时后该事件被永久跳过。
func (s *Store) WakeClaimsBefore(seq int64) ([]int64, error) {
	rows, err := s.db.Query(s.q(`SELECT seq FROM wake_claims
		WHERE seq < ? AND done_at IS NULL ORDER BY seq`), seq)
	if err != nil {
		return nil, fmt.Errorf("读在飞唤醒认领: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var got int64
		if err := rows.Scan(&got); err != nil {
			return nil, fmt.Errorf("扫描在飞唤醒认领: %w", err)
		}
		out = append(out, got)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读在飞唤醒认领结束: %w", err)
	}
	return out, nil
}

// CursorWatermark 把候选水位收紧成"终局前缀水位"：返回窗口 (from, candidate]
// 内允许推进到的最大值——窗口内一旦有在飞认领，就停在那条之前。
// 单调性由调用方保证（from 只增不减）；candidate ≤ from 时原样返回。
func (s *Store) CursorWatermark(from, candidate int64) (int64, error) {
	if candidate <= from {
		return from, nil
	}
	inFlight, err := s.WakeClaimsBefore(candidate + 1)
	if err != nil {
		return from, err
	}
	for _, seq := range inFlight {
		if seq > from {
			return seq - 1, nil
		}
	}
	return candidate, nil
}
