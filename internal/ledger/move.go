// CAS 状态转移 + workflow gate。一期不限制转移方向（人工回退是真实
// 需求），只做四重校验：目标状态在钉住版本的 States 内、当前非终态、
// CAS 前值、gate 条件。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MoveCard 状态转移。expect 空 = 以事务内读到的当前值为前值（交互场景）；
// 非空 = 显式 CAS（脚本场景钉死前值）。冲突返回 ErrCASConflict，
// gate 不过返回 ErrGateBlocked（错误文案指明缺什么）。
func (s *Store) MoveCard(id, to, expect, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		return s.moveCardTx(tx, sink, id, to, expect, actor)
	})
}

// moveCardTx 事务内的转移实现。状态读取、workflow gate 与状态写入必须共用
// 一个事务，避免并发转移依据过期状态放行。
func (s *Store) moveCardTx(tx *sql.Tx, sink *eventSink, id, to, expect, actor string) error {
	{
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
		if err := s.checkWorkflowGateTx(tx, card, workflow, to, "转移"); err != nil {
			return err
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
	}
}

// checkWorkflowGateTx 检查进入 workflow 的 to 列所需的全部 gate。
// moveCardTx 与 MigrateCardWorkflow 必须共用这里，避免新增 gate 时两条入口
// 分裂；action 只区分日志前缀，检查顺序、错误文案和 ErrGateBlocked 保持一致。
func (s *Store) checkWorkflowGateTx(tx *sql.Tx, card Card, workflow Workflow, to, action string) error {
	gate, ok := workflow.Def.Gates[to]
	if !ok {
		return nil
	}
	if gate.RequireAttachment != "" && !cardHasAttachmentKind(card, gate.RequireAttachment) {
		log().Warn(action+"被拒：gate 缺附件", "card", card.ID, "to", to, "need", gate.RequireAttachment)
		return fmt.Errorf("进 %q 需要 %s 附件（当前没有）: %w", to, gate.RequireAttachment, ErrGateBlocked)
	}
	// 择一门与上面的单值门是 AND：两个都设就两个都要过。
	if len(gate.RequireAttachmentAny) > 0 {
		matched := false
		for _, kind := range gate.RequireAttachmentAny {
			if cardHasAttachmentKind(card, kind) {
				matched = true
				break
			}
		}
		if !matched {
			need := strings.Join(gate.RequireAttachmentAny, " 或 ")
			log().Warn(action+"被拒：gate 缺附件（择一）", "card", card.ID, "to", to, "need", need)
			return fmt.Errorf("进 %q 需要 %s 附件之一（当前都没有）: %w", to, need, ErrGateBlocked)
		}
	}
	if gate.RequireAcceptance && card.AcceptanceCriteria == "" {
		log().Warn(action+"被拒：gate 缺判据", "card", card.ID, "to", to)
		return fmt.Errorf("进 %q 需要验收判据非空: %w", to, ErrGateBlocked)
	}
	if gate.RequireChildrenDone {
		pending, err := s.pendingChildrenTx(tx, card.ID)
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			log().Warn(action+"被拒：聚合闸有未完结子卡", "card", card.ID, "to", to, "pending", pending)
			return fmt.Errorf("进 %q 需全部子卡完结，未完结: %s: %w",
				to, strings.Join(pending, ", "), ErrGateBlocked)
		}
	}
	return nil
}

// pendingChildrenTx 事务内取未完结（非 已完成/终止）的直接子卡 id 列表。
// 与转移同事务读：闸判定和状态写之间不留「子卡刚好在窗口里完结/复活」的缝。
func (s *Store) pendingChildrenTx(tx *sql.Tx, id string) ([]string, error) {
	rows, err := tx.Query(s.q(`SELECT id, status FROM cards WHERE parent_id = ?`), id)
	if err != nil {
		return nil, fmt.Errorf("聚合闸读子卡: %w", err)
	}
	defer rows.Close()
	var pending []string
	for rows.Next() {
		var childID, status string
		if err := rows.Scan(&childID, &status); err != nil {
			return nil, err
		}
		if status != StatusDone && status != StatusClosed {
			pending = append(pending, childID)
		}
	}
	return pending, rows.Err()
}

// ClaimCard 保留人尺度旧签名，但不再改变协调者席位；新流程使用 BindSeat。
func (s *Store) ClaimCard(id, owner string) error {
	return s.ClaimCardAs(id, owner, "")
}

// ReleaseCard 释放驱动归属（幂等；只动自己持有的那份）。
//
// 参数：id 卡号；session 仅用于兼容日志。空座幂等成功，非空席位拒绝且保留原值。
func (s *Store) ReleaseCard(id, session string) error {
	log().Info("开始释放归属", "card", id, "session", session)
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("释放: 卡 %s: %w", id, err)
		}
		switch {
		case card.DriverSession == "" && card.DriverSource == "":
			log().Info("释放无操作：卡无主", "card", id)
			return nil
		default:
			err := fmt.Errorf("卡 %s 当前有席位，请使用 rebind（release 不清席位）: %w", id, ErrBadState)
			log().Warn("释放被拒：席位由新流程管理", "card", id, "has_session", card.DriverSession != "", "has_source", card.DriverSource != "", "cause", err)
			return err
		}
	})
	if err != nil {
		log().Warn("释放归属失败", "card", id, "session", session, "cause", err)
		return err
	}
	return nil
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

// cardHasAttachmentKind 判断卡是否挂了某种 kind 的附件。
// 抽出来是因为单值门与择一门（Gate.RequireAttachmentAny）用的是同一条判定，
// 两处各写一遍的话，将来改附件匹配语义（比如加大小写归一）必然漏改一处。
func cardHasAttachmentKind(card Card, kind string) bool {
	for _, attachment := range card.Attachments {
		if attachment.Kind == kind {
			return true
		}
	}
	return false
}
