// card_tasks 弱引用（账本 → 执行域的唯一通道）与卡的 driver 归属。
// 弱引用无外键校验 task 真实存在——执行域在别的机器的 SQLite 里，
// 账本只记指针；指针悬空由镜像/看板 join 时显性化，不在写入时拦。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TaskLink card_tasks 一行。JSON tag 服务直接编码账本结构的 CLI；HTTP 详情使用
// proto 投影并刻意保留 PascalCase 线格式。
type TaskLink struct {
	CardID string `json:"card_id"`
	// Node 是工作流节点名；空值表示非工作流直派或旧挂账。
	Node string `json:"node,omitempty"`
	// Attempt 是本次节点尝试的稳定身份；B233.6 约定由派发 task id 形成。
	Attempt   string    `json:"attempt,omitempty"`
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

// TasksOf 列一张卡挂的全部 task，并从同卡的最新 dispatched 快照投影
// workflow Node/Attempt。card_tasks 保持五列兼容形状，旧挂账没有匹配快照
// 时返回空投影，不从 task id 或 purpose 猜身份。
func (s *Store) TasksOf(cardID string) ([]TaskLink, error) {
	rows, err := s.db.Query(s.q(`SELECT card_id, target, task_id, purpose, created_at
		FROM card_tasks WHERE card_id = ? ORDER BY created_at`), cardID)
	if err != nil {
		log().Warn("读挂账失败", "card", cardID, "cause", err)
		return nil, fmt.Errorf("读挂账: %w", err)
	}
	defer rows.Close()
	var out []TaskLink
	for rows.Next() {
		var link TaskLink
		var createdAt any
		if err := rows.Scan(&link.CardID, &link.Target, &link.TaskID, &link.Purpose, &createdAt); err != nil {
			log().Warn("扫描挂账失败", "card", cardID, "cause", err)
			return nil, err
		}
		link.CreatedAt = toTime(createdAt)
		out = append(out, link)
	}
	if err := rows.Err(); err != nil {
		log().Warn("读挂账行失败", "card", cardID, "cause", err)
		return nil, fmt.Errorf("读挂账行: %w", err)
	}
	return s.projectTaskLinks(cardID, out)
}

// AllTaskLinks 全部挂账行（镜像对账用），并投影每行所属卡的最新
// dispatched 快照中的 workflow Node/Attempt。
func (s *Store) AllTaskLinks() ([]TaskLink, error) {
	rows, err := s.db.Query(`SELECT card_id, target, task_id, purpose, created_at
		FROM card_tasks ORDER BY target, task_id`)
	if err != nil {
		log().Warn("读全部挂账失败", "cause", err)
		return nil, fmt.Errorf("读全部挂账: %w", err)
	}
	defer rows.Close()
	var out []TaskLink
	for rows.Next() {
		var link TaskLink
		var createdAt any
		if err := rows.Scan(&link.CardID, &link.Target, &link.TaskID, &link.Purpose, &createdAt); err != nil {
			log().Warn("扫描全部挂账失败", "cause", err)
			return nil, err
		}
		link.CreatedAt = toTime(createdAt)
		out = append(out, link)
	}
	if err := rows.Err(); err != nil {
		log().Warn("读全部挂账行失败", "cause", err)
		return nil, fmt.Errorf("读全部挂账行: %w", err)
	}
	return s.projectTaskLinks("", out)
}

type dispatchProjection struct {
	Node    string
	Attempt string
}

// projectTaskLinks 把 workflow 身份作为读取投影加入 card_tasks 行。
// 为什么按卡事件逐条取最新快照：card_tasks 是跨机弱引用，不能扩列或把派发
// 事实复制成第二权威；事件顺序同时保证重试后最新快照会覆盖旧身份。
func (s *Store) projectTaskLinks(cardID string, links []TaskLink) ([]TaskLink, error) {
	if len(links) == 0 {
		return links, nil
	}
	query := `SELECT card_id, payload FROM card_events WHERE type = ?`
	args := []any{EvDispatched}
	if cardID != "" {
		query += ` AND card_id = ?`
		args = append(args, cardID)
	}
	query += ` ORDER BY seq ASC`
	rows, err := s.db.Query(s.q(query), args...)
	if err != nil {
		log().Warn("读派发快照投影失败", "card", cardID, "cause", err)
		return nil, fmt.Errorf("读派发快照投影: %w", err)
	}
	defer rows.Close()
	projections := make(map[string]dispatchProjection)
	for rows.Next() {
		var eventCard, raw string
		if err := rows.Scan(&eventCard, &raw); err != nil {
			log().Warn("扫描派发快照投影失败", "card", cardID, "cause", err)
			return nil, fmt.Errorf("扫描派发快照投影: %w", err)
		}
		var snapshot DispatchSnapshot
		if err := json.Unmarshal([]byte(raw), &snapshot); err != nil {
			log().Warn("派发快照投影解码失败，保留空身份", "card", eventCard,
				"cause", err)
			continue
		}
		key := eventCard + "\x00" + snapshot.Target + "\x00" + snapshot.TaskID
		projection := dispatchProjection{}
		if snapshot.Node != "" && snapshot.Attempt != "" {
			projection = dispatchProjection{Node: snapshot.Node, Attempt: snapshot.Attempt}
		}
		projections[key] = projection
	}
	if err := rows.Err(); err != nil {
		log().Warn("读取派发快照投影行失败", "card", cardID, "cause", err)
		return nil, fmt.Errorf("读派发快照投影: %w", err)
	}
	for i := range links {
		key := links[i].CardID + "\x00" + links[i].Target + "\x00" + links[i].TaskID
		projection, ok := projections[key]
		if !ok {
			log().Debug("挂账没有匹配派发快照，保留空身份", "card", links[i].CardID,
				"target", links[i].Target, "task", links[i].TaskID)
			continue
		}
		links[i].Node = projection.Node
		links[i].Attempt = projection.Attempt
	}
	log().Debug("挂账派发身份投影完成", "card", cardID, "links", len(links))
	return links, nil
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
