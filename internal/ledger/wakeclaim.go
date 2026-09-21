// 唤醒认领（B389 契约 §3.1）：共库下同一张卡的同一事件只允许一台机器处理。
//
// 认领键取 (card, card_events.seq) 复合键：seq 全局唯一，但一条事件可合法扇出
// 到多张卡（room_message 按寻址每卡一条 wake），单键会让第二张卡认领失败并被
// 静默跳过、游标越过、永久丢唤醒（契约 §3.1.1/§6-2）。排他语义因此收窄为
// 「同一张卡的同一事件只允许一个持有者」；不同卡的同 seq 互不排他。
// 排他靠 mutate 的事务串行化（PG 上先取 pg_advisory_xact_lock），不是概率性
// 去重。
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

// ClaimWake 尝试认领某张卡的某条唤醒事件：拿到返回 true，别人持有且未过期
// 返回 false（调用方据此直接跳过，不占名额、不试跑）。同持有者可续期；过期可
// 被接管。已终局（done_at 非空）的事件永远不再被认领。冲突判定与过期接管都
// 以 (card,seq) 定位行：不同卡的同 seq 不是竞争关系。
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
		err := tx.QueryRow(s.q(`SELECT holder, lease_until, done_at FROM wake_claims
			WHERE card = ? AND seq = ?`), card, seq).Scan(&existingHolder, &leaseUntil, &doneAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			// 首认领
		case err != nil:
			return fmt.Errorf("读唤醒认领 card=%s seq=%d: %w", card, seq, err)
		default:
			if doneAt != nil {
				return nil
			}
			if existingHolder != holder && toTime(leaseUntil).After(s.timeNow()) {
				return nil
			}
		}
		if _, err := tx.Exec(s.q(`INSERT INTO wake_claims (card, seq, holder, lease_until, done_at)
			VALUES (?, ?, ?, ?, NULL)
			ON CONFLICT(card, seq) DO UPDATE SET holder = excluded.holder,
				lease_until = excluded.lease_until`),
			card, seq, holder, s.tval(s.timeNow().Add(ttl))); err != nil {
			return fmt.Errorf("写唤醒认领 card=%s seq=%d: %w", card, seq, err)
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

// CompleteWake 收尾一张卡上一条已认领的事件；只允许持有者调用，非持有者返回
// ErrCASConflict 且不改任何行。key 为 (card,seq)：对同一 seq 的另一张卡收尾
// 不影响本卡认领行。收尾是幂等的（重复收尾仍成功）。
func (s *Store) CompleteWake(seq int64, card, holder string) error {
	if seq <= 0 || card == "" || holder == "" {
		return fmt.Errorf("唤醒收尾参数不完整（seq=%d card=%q holder=%q）: %w",
			seq, card, holder, ErrBadState)
	}
	updated := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`UPDATE wake_claims SET done_at = ?
			WHERE card = ? AND seq = ? AND holder = ?`),
			s.tval(s.timeNow()), card, seq, holder)
		if err != nil {
			return fmt.Errorf("收尾唤醒认领 card=%s seq=%d: %w", card, seq, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读收尾影响行数 card=%s seq=%d: %w", card, seq, err)
		}
		updated = n > 0
		return nil
	})
	if err != nil {
		log().Warn("唤醒收尾失败", "seq", seq, "card", card, "holder", holder, "cause", err)
		return err
	}
	if !updated {
		err := fmt.Errorf("唤醒认领 card=%s seq=%d 不属于 %s: %w", card, seq, holder, ErrCASConflict)
		log().Warn("唤醒收尾被拒：非持有者", "seq", seq, "card", card, "holder", holder, "cause", err)
		return err
	}
	log().Info("唤醒收尾完成", "seq", seq, "card", card, "holder", holder)
	return nil
}

// WakeClaimsBefore 列出 seq 小于水位的仍在飞认领（升序去重）。游标只能推进到
// 这些 seq 的最小值之前——否则对端崩溃或超时后该事件被永久跳过。同一 seq 在
// 多张卡上有多个认领行时，任一行在飞就挡住对应水位（DISTINCT seq 聚合）。
func (s *Store) WakeClaimsBefore(seq int64) ([]int64, error) {
	rows, err := s.db.Query(s.q(`SELECT DISTINCT seq FROM wake_claims
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
// 内允许推进到的最大值——窗口内一旦有在飞认领（同一 seq 任一卡的认领行在飞即
// 算），就停在那条之前。单调性由调用方保证（from 只增不减）；candidate ≤ from
// 时原样返回。
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
