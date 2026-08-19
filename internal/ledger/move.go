// CAS 状态转移 + workflow gate。一期不限制转移方向（人工回退是真实
// 需求），只做四重校验：目标状态在钉住版本的 States 内、当前非终态、
// CAS 前值、gate 条件。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MoveCard 状态转移。expect 空 = 以事务内读到的当前值为前值（交互场景）；
// 非空 = 显式 CAS（脚本场景钉死前值）。冲突返回 ErrCASConflict，
// gate 不过返回 ErrGateBlocked（错误文案指明缺什么）。
func (s *Store) MoveCard(id, to, expect, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("转移: 卡 %s: %w", id, err)
		}
		if card.Status == StatusDone || card.Status == StatusClosed {
			log().Warn("转移被拒：终态卡", "card", id, "status", card.Status)
			return fmt.Errorf("卡 %s 已处于终态 %s: %w", id, card.Status, ErrBadState)
		}
		if expect != "" && card.Status != expect {
			log().Warn("转移被拒：CAS 冲突", "card", id, "expect", expect, "actual", card.Status)
			return fmt.Errorf("卡 %s 当前是 %q 非 %q: %w", id, card.Status, expect, ErrCASConflict)
		}
		workflow, err := s.getWorkflowTx(tx, card.WorkflowName, card.WorkflowVersion)
		if err != nil {
			return fmt.Errorf("转移取工作流: %w", err)
		}
		found := false
		for _, state := range workflow.Def.States {
			if state == to {
				found = true
				break
			}
		}
		if !found {
			// wrap ErrBadState：API 层（Plan D）靠哨兵翻译成 409，裸 error 会变 500
			log().Warn("转移被拒：未知状态", "card", id, "to", to)
			return fmt.Errorf("状态 %q 不在工作流 %s v%d 中: %w", to, workflow.Name, workflow.Version, ErrBadState)
		}
		if gate, ok := workflow.Def.Gates[to]; ok {
			if gate.RequireAttachment != "" {
				hasAttachment := false
				for _, attachment := range card.Attachments {
					if attachment.Kind == gate.RequireAttachment {
						hasAttachment = true
						break
					}
				}
				if !hasAttachment {
					log().Warn("转移被拒：gate 缺附件", "card", id, "to", to, "need", gate.RequireAttachment)
					return fmt.Errorf("进 %q 需要 %s 附件（当前没有）: %w", to, gate.RequireAttachment, ErrGateBlocked)
				}
			}
			if gate.RequireAcceptance && card.AcceptanceCriteria == "" {
				log().Warn("转移被拒：gate 缺判据", "card", id, "to", to)
				return fmt.Errorf("进 %q 需要验收判据非空: %w", to, ErrGateBlocked)
			}
		}
		// CAS 写：前值进 WHERE，被并发抢先则 0 行（照抄 store.UpdateTaskState 模式）
		result, err := tx.Exec(s.q(`UPDATE cards SET status = ?, updated_at = ? WHERE id = ? AND status = ?`),
			to, s.tval(time.Now()), id, card.Status)
		if err != nil {
			return fmt.Errorf("写转移: %w", err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return fmt.Errorf("卡 %s 状态已被并发修改: %w", id, ErrCASConflict)
		}
		_, err = s.appendEvent(tx, sink, id, EvStatusMoved, actor,
			map[string]any{"from": card.Status, "to": to})
		return err
	})
}

// getWorkflowTx 事务内取指定版本工作流（Move 的 gate 判定必须与写同事务）。
func (s *Store) getWorkflowTx(tx *sql.Tx, name string, version int) (Workflow, error) {
	row := tx.QueryRow(s.q(`SELECT name, version, definition, created_at FROM workflows
		WHERE name = ? AND version = ?`), name, version)
	var workflow Workflow
	var raw string
	var createdAt any
	if err := row.Scan(&workflow.Name, &workflow.Version, &raw, &createdAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Workflow{}, fmt.Errorf("工作流 %s v%d: %w", name, version, ErrNotFound)
		}
		return Workflow{}, fmt.Errorf("读工作流 %s v%d: %w", name, version, err)
	}
	if err := jsonUnmarshal(raw, &workflow.Def); err != nil {
		return Workflow{}, err
	}
	workflow.CreatedAt = toTime(createdAt)
	return workflow, nil
}
