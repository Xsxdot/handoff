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

// moveCardTx 事务内的转移实现。抽出来是为了让「认领」把转状态与落驱动
// 并进同一个事务（见 ClaimCard）——分成两次写会留出一个「进行中但没有
// 驱动」的窗口，并发输家恰好读到它时就报不出认领者是谁。
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
			if gate.RequireChildrenDone {
				pending, err := s.pendingChildrenTx(tx, id)
				if err != nil {
					return err
				}
				if len(pending) > 0 {
					log().Warn("转移被拒：聚合闸有未完结子卡", "card", id, "to", to, "pending", pending)
					return fmt.Errorf("进 %q 需全部子卡完结，未完结: %s: %w",
						to, strings.Join(pending, ", "), ErrGateBlocked)
				}
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
	}
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

// ClaimCard 原子认领：把「CAS 转入 to」与「落驱动 session」并进同一个
// 事务。派发即认领用它——分两次写会留出「进行中但驱动为空」的窗口，
// 并发输家恰好读到那个瞬间就报不出认领者是谁（判据⑥要求报出会话名），
// 进程若在窗口内崩掉还会留下一张没人驱动的「进行中」卡。
// 冲突返回 ErrCASConflict，卡上已有活跃的别家驱动同样拒。
func (s *Store) ClaimCard(id, to, expect, session string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		card, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("认领: 卡 %s: %w", id, err)
		}
		expired := card.DriverHeartbeatAt.IsZero() || time.Since(card.DriverHeartbeatAt) > driverLeaseTTL
		if card.DriverSession != "" && card.DriverSession != session && !expired {
			log().Warn("认领被拒：卡已有活跃驱动", "card", id, "holder", card.DriverSession, "claimer", session)
			return fmt.Errorf("卡 %s 已被 %s 认领: %w", id, card.DriverSession, ErrCASConflict)
		}
		if err := s.moveCardTx(tx, sink, id, to, expect, session); err != nil {
			return err
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET driver_session = ?, driver_heartbeat_at = ? WHERE id = ?`),
			session, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("认领写驱动: %w", err)
		}
		log().Info("卡已认领", "card", id, "to", to, "session", session)
		return nil
	})
}

// ReleaseCard 释放驱动租约（幂等；只动自己持有的那份）。
//
// 参数：id 卡号；session 租约持有者标识，与 ClaimCard 传的同一个。
// 非持有者调用是无操作而不是报错——回滚路径不该因为竞态再抛一次错。
//
// why 需要它：派发失败时回滚只退状态会把卡留在「待办但有主」。驱动身份
// 带 pid，同一个人换个进程重试会被自己的旧租约挡住，要等满 5 分钟 TTL
// 才动得了（2026-08-19 真机踩到：远端缺分支导致派发 400，重试即撞）。
func (s *Store) ReleaseCard(id, session string) error {
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		result, err := tx.Exec(s.q(`UPDATE cards SET driver_session = '', driver_heartbeat_at = ?
			WHERE id = ? AND driver_session = ?`), s.tval(time.Time{}), id, session)
		if err != nil {
			return fmt.Errorf("释放驱动租约: %w", err)
		}
		if n, err := result.RowsAffected(); err == nil && n > 0 {
			log().Info("驱动租约已释放", "card", id, "session", session)
		}
		return nil
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
