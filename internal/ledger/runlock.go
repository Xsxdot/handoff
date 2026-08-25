// 运行锁：一张卡一轮节点编排的占用记录（运行尺度），与 cards.driver_session
// 承载的归属锁（人尺度）分立。权威在账本：取得、续租、释放、过期抢占全部
// 经 mutate 事务收口在本文件；agentd 的进程内在飞集合只是本进程快速去重。
//
// 边界：运行锁的时间判定、事务写入与抢占事件都在 Store 内完成；消费方只读方法
// 返回的事实，不在本包外复制过期判定或持有者竞争规则。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// RunLock 运行锁一行。回答四件事：哪张卡、哪个节点、谁持有、租期到几点。
// Holder 是承载编排的那次运行（运行标识），不是人尺度归属身份；归属仍在
// cards.driver_session，两者互不代写。
type RunLock struct {
	CardID     string    `json:"card_id"`
	Node       string    `json:"node"`
	Holder     string    `json:"holder"`
	AcquiredAt time.Time `json:"acquired_at"`
	ExpiresAt  time.Time `json:"expires_at"`
}

const (
	// RunLockTTL 是节点运行锁的默认租期。
	RunLockTTL = 5 * time.Minute
	// RunLockRenewInterval 是生产续租 goroutine 的默认节拍。
	RunLockRenewInterval = 2 * time.Minute
)

const runLockPreemptReason = "上一轮运行的租期已过，本轮编排接管这张卡的运行锁"

// AcquireRunLock 取得运行锁。无行、持有者是自己、或已过期 → 得到锁（过期时
// 覆盖并在卡上落抢占事件）；他主持有且未过期 → 拒绝，acquired=false 且返回
// 现存锁（谁在跑、哪个节点、租期到几点）。过期判定用 Store 可注入时钟。
func (s *Store) AcquireRunLock(cardID, node, holder string, ttl time.Duration) (RunLock, bool, error) {
	var out RunLock
	acquired := false
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("取得运行锁: 卡 %s: %w", cardID, err)
		}
		now := s.timeNow()
		var row RunLock
		var acquiredAt, expiresAt any
		err := tx.QueryRow(s.q(`SELECT card_id, node, holder, acquired_at, expires_at
			FROM card_run_locks WHERE card_id = ?`), cardID).
			Scan(&row.CardID, &row.Node, &row.Holder, &acquiredAt, &expiresAt)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			lock := RunLock{CardID: cardID, Node: node, Holder: holder,
				AcquiredAt: now, ExpiresAt: now.Add(ttl)}
			if _, err := tx.Exec(s.q(`INSERT INTO card_run_locks
				(card_id, node, holder, acquired_at, expires_at) VALUES (?,?,?,?,?)`),
				cardID, lock.Node, lock.Holder, s.tval(lock.AcquiredAt), s.tval(lock.ExpiresAt)); err != nil {
				return fmt.Errorf("首建运行锁: %w", err)
			}
			log().Info("运行锁首取", "card", cardID, "node", node, "holder", holder, "ttl", ttl.String())
			out, acquired = lock, true
			return nil
		case err != nil:
			return fmt.Errorf("读运行锁: %w", err)
		}
		row.AcquiredAt, row.ExpiresAt = toTime(acquiredAt), toTime(expiresAt)
		switch {
		case row.Holder == holder:
			row.ExpiresAt = now.Add(ttl)
			if _, err := tx.Exec(s.q(`UPDATE card_run_locks SET expires_at = ? WHERE card_id = ?`),
				s.tval(row.ExpiresAt), cardID); err != nil {
				return fmt.Errorf("刷新运行锁: %w", err)
			}
			out, acquired = row, true
			return nil
		case row.ExpiresAt.After(now):
			log().Info("运行锁被他方持有", "card", cardID, "holder", row.Holder,
				"expires_at", row.ExpiresAt.Format(time.RFC3339))
			out = row
			return nil
		default:
			prev := row.Holder
			row.Node, row.Holder, row.AcquiredAt, row.ExpiresAt = node, holder, now, now.Add(ttl)
			if _, err := tx.Exec(s.q(`UPDATE card_run_locks SET node = ?, holder = ?, acquired_at = ?, expires_at = ? WHERE card_id = ?`),
				node, holder, s.tval(now), s.tval(row.ExpiresAt), cardID); err != nil {
				return fmt.Errorf("抢占运行锁: %w", err)
			}
			if _, err := s.appendEvent(tx, sink, cardID, EvDriverTakeover, holder,
				map[string]string{"from": prev, "to": holder, "reason": runLockPreemptReason}); err != nil {
				return fmt.Errorf("抢占落事件: %w", err)
			}
			log().Info("运行锁过期接管", "card", cardID, "from", prev, "to", holder)
			out, acquired = row, true
			return nil
		}
	})
	if err != nil {
		return RunLock{}, false, err
	}
	return out, acquired, nil
}

// RenewRunLock 续租：只有当前持有者可续。返回 false = 已失去（被抢或从未
// 持有），调用方必须停止对这张卡的一切写。
func (s *Store) RenewRunLock(cardID, holder string, ttl time.Duration) (bool, error) {
	renewed := false
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`UPDATE card_run_locks SET expires_at = ?
			WHERE card_id = ? AND holder = ?`), s.tval(s.timeNow().Add(ttl)), cardID, holder)
		if err != nil {
			return fmt.Errorf("续租运行锁: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("读续租影响行数: %w", err)
		}
		renewed = n > 0
		return nil
	})
	if err != nil {
		return false, err
	}
	return renewed, nil
}

// ReleaseRunLock 释放运行锁（回合结束的尽力而为清理；非持有者是 no-op，
// 失去锁的权威信号在 RenewRunLock 的 false）。
func (s *Store) ReleaseRunLock(cardID, holder string) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		res, err := tx.Exec(s.q(`DELETE FROM card_run_locks WHERE card_id = ? AND holder = ?`),
			cardID, holder)
		if err != nil {
			return fmt.Errorf("释放运行锁: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			log().Info("运行锁已释放", "card", cardID, "holder", holder)
		}
		return nil
	})
}

// RunLockOf 读单卡的运行锁行；第二个返回值 = 行是否存在。**不过滤过期**：
// 「是否正在跑」由消费侧按 ExpiresAt 与同一注入时钟判定。
func (s *Store) RunLockOf(cardID string) (RunLock, bool, error) {
	var lock RunLock
	var acquiredAt, expiresAt any
	err := s.db.QueryRow(s.q(`SELECT card_id, node, holder, acquired_at, expires_at
		FROM card_run_locks WHERE card_id = ?`), cardID).
		Scan(&lock.CardID, &lock.Node, &lock.Holder, &acquiredAt, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return RunLock{}, false, nil
	}
	if err != nil {
		return RunLock{}, false, fmt.Errorf("读运行锁 %s: %w", cardID, err)
	}
	lock.AcquiredAt, lock.ExpiresAt = toTime(acquiredAt), toTime(expiresAt)
	return lock, true, nil
}

// AllRunLocks 全部运行锁行（看板批量判定用）。同样不过滤过期。
func (s *Store) AllRunLocks() ([]RunLock, error) {
	rows, err := s.db.Query(`SELECT card_id, node, holder, acquired_at, expires_at
		FROM card_run_locks ORDER BY card_id`)
	if err != nil {
		return nil, fmt.Errorf("读全部运行锁: %w", err)
	}
	defer rows.Close()
	var out []RunLock
	for rows.Next() {
		var lock RunLock
		var acquiredAt, expiresAt any
		if err := rows.Scan(&lock.CardID, &lock.Node, &lock.Holder, &acquiredAt, &expiresAt); err != nil {
			return nil, err
		}
		lock.AcquiredAt, lock.ExpiresAt = toTime(acquiredAt), toTime(expiresAt)
		out = append(out, lock)
	}
	return out, rows.Err()
}
