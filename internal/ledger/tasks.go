// card_tasks 弱引用（账本 → 执行域的唯一通道）与卡的 driver 归属。
// 弱引用无外键校验 task 真实存在——执行域在别的机器的 SQLite 里，
// 账本只记指针；指针悬空由镜像/看板 join 时显性化，不在写入时拦。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

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

// TakeoverCard 显式替换卡的驱动归属，并在同一事务写可审计事件。
// 参数：id 卡号；session 新驱动会话；actor 发起接管的人/入口标识。
// 注意：这是有意覆盖现有驱动的独立动作，不读取认领时刻，也不自动改变卡状态。
func (s *Store) TakeoverCard(id, session, actor string) error {
	log().Info("开始接管驱动", "card", id, "session", session, "actor", actor)
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("接管驱动: 卡 %s: %w", id, err)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_heartbeat_at = ? WHERE id = ?`),
			session, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("接管写驱动: %w", err)
		}
		if _, err := s.appendEvent(tx, sink, id, EvDriverTakeover, actor,
			map[string]string{"from": card.DriverSession, "to": session}); err != nil {
			return fmt.Errorf("接管落事件: %w", err)
		}
		return nil
	})
	if err != nil {
		log().Warn("接管驱动失败", "card", id, "session", session, "actor", actor, "cause", err)
		return err
	}
	log().Info("驱动已接管", "card", id, "session", session, "actor", actor)
	return nil
}
