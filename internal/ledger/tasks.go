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

// TaskLink card_tasks 一行。JSON tag 服务直接编码账本结构的 CLI；HTTP 详情使用
// proto 投影并刻意保留 PascalCase 线格式。
type TaskLink struct {
	CardID    string    `json:"card_id"`
	Target    string    `json:"target"`
	TaskID    string    `json:"task_id"`
	Purpose   string    `json:"purpose"`
	CreatedAt time.Time `json:"created_at"`
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

// TakeoverCard 保留旧的人尺度命令签名，但不再改变协调者席位或落
// EvDriverTakeover；请使用 BindSeat/RebindSeat 完成三颗按钮语义。
func (s *Store) TakeoverCard(id, session, actor string) error {
	log().Warn("旧接管入口已停用", "card", id, "has_session", session != "", "has_actor", actor != "")
	if _, err := s.GetCard(id); err != nil {
		return fmt.Errorf("接管驱动: 卡 %s: %w", id, err)
	}
	return fmt.Errorf("卡 %s 不再通过 takeover 占座，请使用 bind、coordinate 或 rebind: %w", id, ErrBadState)
}
