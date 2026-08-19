// card_tasks 弱引用（账本 → 执行域的唯一通道）与卡的 driver lease。
// 弱引用无外键校验 task 真实存在——执行域在别的机器的 SQLite 里，
// 账本只记指针；指针悬空由镜像/看板 join 时显性化，不在写入时拦。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// driverLeaseTTL 驱动会话心跳有效期。超过即视为无主，可被抢占；
// 看板「无驱动会话」告警也以此为准。
const driverLeaseTTL = 5 * time.Minute

// TaskLink card_tasks 一行。
type TaskLink struct {
	CardID, Target, TaskID, Purpose string
	CreatedAt                       time.Time
}

// LinkTask 把 (target, task) 挂到卡上。purpose ∈ {implement, review, merge}
// 语义由调用方保证（词表不在库层锁死——三期可能扩）。落 comment 事件。
func (s *Store) LinkTask(cardID, target, taskID, purpose, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("挂账: 卡 %s: %w", cardID, err)
		}
		if _, err := tx.Exec(s.q(`INSERT INTO card_tasks (card_id, target, task_id, purpose, created_at)
			VALUES (?,?,?,?,?)`), cardID, target, taskID, purpose, s.tval(time.Now())); err != nil {
			return fmt.Errorf("写挂账（task 可能已挂在别的卡）: %w", err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("挂账 task %s@%s（%s）", taskID, target, purpose)})
		return err
	})
}

// TasksOf 列一张卡挂的全部 task。
func (s *Store) TasksOf(cardID string) ([]TaskLink, error) {
	rows, err := s.db.Query(s.q(`SELECT card_id, target, task_id, purpose, created_at
		FROM card_tasks WHERE card_id = ? ORDER BY created_at`), cardID)
	if err != nil {
		return nil, fmt.Errorf("读挂账: %w", err)
	}
	defer rows.Close()
	var out []TaskLink
	for rows.Next() {
		var link TaskLink
		var createdAt any
		if err := rows.Scan(&link.CardID, &link.Target, &link.TaskID, &link.Purpose, &createdAt); err != nil {
			return nil, err
		}
		link.CreatedAt = toTime(createdAt)
		out = append(out, link)
	}
	return out, rows.Err()
}

// AllTaskLinks 全部挂账行（镜像对账用）。
func (s *Store) AllTaskLinks() ([]TaskLink, error) {
	rows, err := s.db.Query(`SELECT card_id, target, task_id, purpose, created_at
		FROM card_tasks ORDER BY target, task_id`)
	if err != nil {
		return nil, fmt.Errorf("读全部挂账: %w", err)
	}
	defer rows.Close()
	var out []TaskLink
	for rows.Next() {
		var link TaskLink
		var createdAt any
		if err := rows.Scan(&link.CardID, &link.Target, &link.TaskID, &link.Purpose, &createdAt); err != nil {
			return nil, err
		}
		link.CreatedAt = toTime(createdAt)
		out = append(out, link)
	}
	return out, rows.Err()
}

// CardOfTask 反查 task 挂在哪张卡（镜像写入路径的热查询）。
func (s *Store) CardOfTask(target, taskID string) (string, error) {
	var cardID string
	err := s.db.QueryRow(s.q(`SELECT card_id FROM card_tasks WHERE target = ? AND task_id = ?`),
		target, taskID).Scan(&cardID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("task %s@%s 未挂账: %w", taskID, target, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("反查挂账: %w", err)
	}
	return cardID, nil
}

// ClaimDriver 认领驱动权：现驱动为空、为己、或心跳过期才可得；
// 否则 ErrCASConflict（提示对方会话标识）。
func (s *Store) ClaimDriver(cardID, session string) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		card, err := getCardTx(s, tx, cardID)
		if err != nil {
			return fmt.Errorf("认领驱动: 卡 %s: %w", cardID, err)
		}
		expired := card.DriverHeartbeatAt.IsZero() || time.Since(card.DriverHeartbeatAt) > driverLeaseTTL
		if card.DriverSession != "" && card.DriverSession != session && !expired {
			log().Warn("驱动认领被拒", "card", cardID, "holder", card.DriverSession, "claimer", session)
			return fmt.Errorf("卡 %s 正由 %s 驱动: %w", cardID, card.DriverSession, ErrCASConflict)
		}
		if _, err = tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_heartbeat_at = ? WHERE id = ?`),
			session, s.tval(time.Now()), cardID); err != nil {
			return fmt.Errorf("写驱动: %w", err)
		}
		return nil
	})
}

// HeartbeatDriver 续心跳（仅现持有者可续）。
func (s *Store) HeartbeatDriver(cardID, session string) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		result, err := tx.Exec(s.q(`UPDATE cards SET driver_heartbeat_at = ? WHERE id = ? AND driver_session = ?`),
			s.tval(time.Now()), cardID, session)
		if err != nil {
			return fmt.Errorf("续心跳: %w", err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return fmt.Errorf("卡 %s 的驱动不是 %s: %w", cardID, session, ErrCASConflict)
		}
		return nil
	})
}
