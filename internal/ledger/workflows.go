// Workflow 聚合：不可变版本化的状态机形状。只插新版本、永不 UPDATE
// 旧行——钉版本的卡随时能取回当时的形状，这是审计链的前提。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PutWorkflow 写入 name 的下一个版本并返回版本号。def 原样存 JSON。
func (s *Store) PutWorkflow(name string, def WorkflowDef) (int, error) {
	raw, err := json.Marshal(def)
	if err != nil {
		return 0, fmt.Errorf("编码工作流定义: %w", err)
	}
	var version int
	err = s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		row := tx.QueryRow(s.q(`SELECT COALESCE(MAX(version),0) FROM workflows WHERE name = ?`), name)
		if err := row.Scan(&version); err != nil {
			return fmt.Errorf("查最大版本: %w", err)
		}
		version++
		_, err := tx.Exec(s.q(`INSERT INTO workflows (name, version, definition, created_at) VALUES (?,?,?,?)`),
			name, version, string(raw), s.tval(time.Now()))
		if err != nil {
			return fmt.Errorf("写工作流 %s v%d: %w", name, version, err)
		}
		return nil
	})
	return version, err
}

// GetWorkflow 取指定版本；version==0 取最新版。找不到返回 ErrNotFound。
func (s *Store) GetWorkflow(name string, version int) (Workflow, error) {
	q := `SELECT name, version, definition, created_at FROM workflows WHERE name = ?`
	args := []any{name}
	if version > 0 {
		q += ` AND version = ?`
		args = append(args, version)
	}
	q += ` ORDER BY version DESC LIMIT 1`
	row := s.db.QueryRow(s.q(q), args...)
	var workflow Workflow
	var raw string
	var createdAt any
	if err := row.Scan(&workflow.Name, &workflow.Version, &raw, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Workflow{}, fmt.Errorf("工作流 %s v%d: %w", name, version, ErrNotFound)
		}
		return Workflow{}, fmt.Errorf("读工作流: %w", err)
	}
	if err := jsonUnmarshal(raw, &workflow.Def); err != nil {
		return Workflow{}, fmt.Errorf("解码工作流定义: %w", err)
	}
	workflow.CreatedAt = toTime(createdAt)
	return workflow, nil
}

// EnsureDefaultWorkflows 幂等 seed 出厂工作流：feature 流（带「已出spec」
// 插入状态与两道 gate，对齐 💡→📋→🔨→✅ 生命周期）与 bug 流（无门直流）。
// 已存在同名工作流则不动（不覆盖用户改过的版本）。
func (s *Store) EnsureDefaultWorkflows() error {
	defaults := map[string]WorkflowDef{
		"feature": {
			States: []string{StatusTodo, "已出spec", StatusDoing, StatusReview, "待合并", StatusDone},
			Gates: map[string]Gate{
				"已出spec": {RequireAttachment: "spec"},
				"待合并":    {RequireAcceptance: true},
			},
		},
		"bug": {
			States: []string{StatusTodo, StatusDoing, StatusReview, StatusDone},
		},
	}
	for name, def := range defaults {
		if _, err := s.GetWorkflow(name, 0); err == nil {
			continue // 已存在，不覆盖
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := s.PutWorkflow(name, def); err != nil {
			return err
		}
		log().Info("seed 默认工作流", "name", name)
	}
	return nil
}

// ListWorkflowNames 全部工作流名（去重升序）。
func (s *Store) ListWorkflowNames() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT name FROM workflows ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("列工作流名: %w", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// MigrateCardWorkflow 把卡显式迁到本工作流的另一个版本。防悬空校验：
// 卡的当前状态必须存在于目标版本的 States（否则拒绝，让人先挪状态）。
func (s *Store) MigrateCardWorkflow(cardID string, toVersion int, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, cardID)
		if err != nil {
			return fmt.Errorf("迁工作流: 卡 %s: %w", cardID, err)
		}
		target, err := s.getWorkflowTx(tx, card.WorkflowName, toVersion)
		if err != nil {
			return err
		}
		found := card.Status == StatusClosed // 终止态卡不受 States 约束
		for _, state := range target.Def.States {
			if state == card.Status {
				found = true
				break
			}
		}
		if !found {
			log().Warn("迁移被拒：状态悬空", "card", cardID, "status", card.Status, "to_version", toVersion)
			return fmt.Errorf("卡 %s 当前状态 %q 不在 %s v%d 中，先转移状态再迁: %w",
				cardID, card.Status, card.WorkflowName, toVersion, ErrBadState)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET workflow_version = ?, updated_at = ? WHERE id = ?`),
			toVersion, s.tval(time.Now()), cardID); err != nil {
			return fmt.Errorf("写迁移: %w", err)
		}
		_, err = s.appendEvent(tx, sink, cardID, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("工作流迁至 %s v%d", card.WorkflowName, toVersion)})
		return err
	})
}

// jsonUnmarshal 统一 JSON 解码错误措辞。
func jsonUnmarshal(raw string, v any) error {
	if err := json.Unmarshal([]byte(raw), v); err != nil {
		return fmt.Errorf("解码 JSON 定义: %w", err)
	}
	return nil
}
