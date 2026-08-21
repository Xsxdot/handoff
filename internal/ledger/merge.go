// 合并/拆回/拆分：账本域一等操作。合并 = 关系不是销毁——被并卡的
// 判据、事件、B 号全部保留，拆回只删一条边。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// MergeCards 把 ids 并入承载卡 into。校验（任一不过全拒，ErrBadMerge）：
// 卡都存在且非终态；不含 into 自身；无重复并入；无链式（into 自身未被并
// 入、ids 里没有正在承载别人的卡）；全体有效基线一致。
func (s *Store) MergeCards(ids []string, into, actor string) error {
	if len(ids) == 0 {
		return fmt.Errorf("合并: 成员为空")
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		carrier, err := getCardTx(s, tx, into)
		if err != nil {
			return fmt.Errorf("合并: 承载卡 %s: %w", into, err)
		}
		if carrier.Status == StatusClosed || carrier.Status == StatusDone {
			log().Warn("合并被拒：承载卡终态", "card", into, "status", carrier.Status)
			return fmt.Errorf("承载卡 %s 已处于终态 %s: %w", into, carrier.Status, ErrBadMerge)
		}
		if current, err := s.mergedIntoTx(tx, into); err != nil {
			return err
		} else if current != "" {
			log().Warn("合并被拒：链式", "into", into, "its_carrier", current)
			return fmt.Errorf("承载卡 %s 自身已并入 %s，不许链式: %w", into, current, ErrBadMerge)
		}
		intoBase, err := s.effectiveBaseTx(tx, into)
		if err != nil {
			return err
		}
		seen := make(map[string]bool, len(ids))
		for _, id := range ids {
			if seen[id] {
				log().Warn("合并被拒：成员重复", "card", id, "into", into)
				return fmt.Errorf("卡 %s 在成员列表中重复: %w", id, ErrBadMerge)
			}
			seen[id] = true
			if id == into {
				log().Warn("合并被拒：自并入", "card", id)
				return fmt.Errorf("卡 %s 不能并入自己: %w", id, ErrBadMerge)
			}
			member, err := getCardTx(s, tx, id)
			if err != nil {
				return fmt.Errorf("合并: 成员 %s: %w", id, err)
			}
			if member.Status == StatusClosed || member.Status == StatusDone {
				log().Warn("合并被拒：成员终态", "card", id, "status", member.Status)
				return fmt.Errorf("成员 %s 已处于终态 %s: %w", id, member.Status, ErrBadMerge)
			}
			if current, err := s.mergedIntoTx(tx, id); err != nil {
				return err
			} else if current != "" {
				log().Warn("合并被拒：重复并入", "card", id, "already", current)
				return fmt.Errorf("卡 %s 已并入 %s: %w", id, current, ErrBadMerge)
			}
			var carrierCount int
			if err := tx.QueryRow(s.q(`SELECT COUNT(*) FROM card_relations WHERE to_id = ? AND type = ?`),
				id, RelMergedInto).Scan(&carrierCount); err != nil {
				return fmt.Errorf("查承载: %w", err)
			}
			if carrierCount > 0 {
				log().Warn("合并被拒：成员在承载别人", "card", id)
				return fmt.Errorf("卡 %s 正承载着别的卡，不许链式: %w", id, ErrBadMerge)
			}
			base, err := s.effectiveBaseTx(tx, id)
			if err != nil {
				return err
			}
			if base != intoBase {
				log().Warn("合并被拒：跨基线", "card", id, "base", base, "into_base", intoBase)
				return fmt.Errorf("卡 %s 基线 %q ≠ 承载卡基线 %q: %w", id, base, intoBase, ErrBadMerge)
			}
		}
		for _, id := range ids {
			if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
				VALUES (?,?,?,?)`), id, into, RelMergedInto, s.tval(time.Now())); err != nil {
				return fmt.Errorf("写并入边 %s: %w", id, err)
			}
		}
		_, err = s.appendEvent(tx, sink, into, EvMerged, actor, map[string]any{"members": ids})
		return err
	})
}

// UnmergeCard 拆回：删 merged_into 边，恢复自主流转。判据/事件无损
// （它们从未被动过）。
func (s *Store) UnmergeCard(id, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		carrier, err := s.mergedIntoTx(tx, id)
		if err != nil {
			return err
		}
		if carrier == "" {
			return fmt.Errorf("卡 %s 未并入任何卡: %w", id, ErrNotFound)
		}
		if _, err := tx.Exec(s.q(`DELETE FROM card_relations WHERE from_id = ? AND type = ?`),
			id, RelMergedInto); err != nil {
			return fmt.Errorf("删并入边: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvUnmerged, actor, map[string]any{"from_carrier": carrier})
		return err
	})
}

// SplitCard 拆子卡：建子卡（点号 id、同工作流、同 project）+ split_from 边。
func (s *Store) SplitCard(parent, title, actor string) (Card, error) {
	parentCard, err := s.GetCard(parent)
	if err != nil {
		return Card{}, fmt.Errorf("拆分: 父卡 %s: %w", parent, err)
	}
	child, err := s.CreateCard(NewCard{Title: title, Project: parentCard.Project,
		Workflow: parentCard.WorkflowName, Parent: parent, Actor: actor})
	if err != nil {
		return Card{}, err
	}
	err = s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
			VALUES (?,?,?,?)`), child.ID, parent, RelSplitFrom, s.tval(time.Now())); err != nil {
			return fmt.Errorf("写 split_from: %w", err)
		}
		_, err := s.appendEvent(tx, sink, parent, EvSplit, actor,
			map[string]any{"child": child.ID, "title": title})
		return err
	})
	return child, err
}

// mergedIntoTx 查卡当前并入的承载卡（"" = 未并入）。也承担 SQLite 侧
// 「一卡至多一条 merged_into」的应用层校验职责（PG 有 partial unique
// index 兜底，SQLite 靠 MergeCards 的先查后插 + 全局写锁保证）。
func (s *Store) mergedIntoTx(tx *sql.Tx, id string) (string, error) {
	var carrier string
	err := tx.QueryRow(s.q(`SELECT to_id FROM card_relations WHERE from_id = ? AND type = ?`),
		id, RelMergedInto).Scan(&carrier)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("查并入: %w", err)
	}
	return carrier, nil
}
