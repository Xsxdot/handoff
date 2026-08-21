// 查询期派生标记：blocked / 跟随 / 等人。不落列（spec §2）——账面永远
// 从边表 + 事件流现算，不存在「派生列忘更新」这类说谎方式。实现取三次
// 全量小表 + 内存组装：卡量级在数百张，正确性与可读性优先，慢了再谈索引。
package ledger

import (
	"database/sql"
	"fmt"
	"sort"
)

// ListCards 过滤 + 派生。排序：待人处理的在前（needs/blocked），其余按
// id 升序——CLI 领活与看板共用此序。
func (s *Store) ListCards(filter CardFilter) ([]CardView, error) {
	query := `SELECT ` + cardColumns + ` FROM cards WHERE 1=1`
	var args []any
	if filter.Project != "" {
		query += ` AND project = ?`
		args = append(args, filter.Project)
	}
	if filter.Status != "" {
		query += ` AND status = ?`
		args = append(args, filter.Status)
	}
	if !filter.IncludeTerminal {
		query += ` AND status NOT IN (?, ?)`
		args = append(args, StatusDone, StatusClosed)
	}
	rows, err := s.db.Query(s.q(query), args...)
	if err != nil {
		return nil, fmt.Errorf("列卡: %w", err)
	}
	var cards []Card
	for rows.Next() {
		card, err := scanCard(rows)
		if err != nil {
			rows.Close()
			return nil, fmt.Errorf("扫卡行: %w", err)
		}
		cards = append(cards, card)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	relations, err := s.allRelations()
	if err != nil {
		return nil, err
	}
	statusOf, err := s.allStatuses()
	if err != nil {
		return nil, err
	}
	needs, err := s.needsMap()
	if err != nil {
		return nil, err
	}
	openDecisions, err := s.openDecisionCount()
	if err != nil {
		return nil, err
	}
	parents, err := s.allParents()
	if err != nil {
		return nil, err
	}
	type childStat struct{ total, done int }
	childStats := map[string]*childStat{}
	for child, parent := range parents {
		stat := childStats[parent]
		if stat == nil {
			stat = &childStat{}
			childStats[parent] = stat
		}
		stat.total++
		// 完结 = 已完成或终止，与聚合闸同一把尺（types.go Gate 注释）
		if status := statusOf[child]; status == StatusDone || status == StatusClosed {
			stat.done++
		}
	}

	var out []CardView
	for _, card := range cards {
		view := CardView{Card: card, OpenDecisions: openDecisions[card.ID], NeedsReason: needs[card.ID]}
		if stat := childStats[card.ID]; stat != nil {
			view.ChildrenTotal, view.ChildrenDone = stat.total, stat.done
		}
		for _, relation := range relations {
			switch {
			case relation.Type == RelMergedInto && relation.From == card.ID:
				view.Following = relation.To
			case relation.Type == RelMergedInto && relation.To == card.ID:
				view.MergedCount++
			case relation.Type == RelBlocks && relation.To == card.ID:
				// blocker 到「已完成」才解除；终止不解除且派生等人（判据③）
				status := statusOf[relation.From]
				if status != StatusDone {
					view.Blocked = true
					view.BlockedBy = append(view.BlockedBy, relation.From)
					if status == StatusClosed && view.NeedsReason == "" {
						view.NeedsReason = "前置 " + relation.From + " 已终止"
					}
				}
			}
		}
		if filter.BaseBranch != "" {
			effective, err := s.EffectiveBaseBranch(card.ID)
			if err != nil {
				return nil, err
			}
			if effective != filter.BaseBranch {
				continue
			}
		}
		if filter.Blocked && !view.Blocked {
			continue
		}
		if filter.Needs && view.NeedsReason == "" && view.OpenDecisions == 0 {
			continue
		}
		out = append(out, view)
	}
	sort.Slice(out, func(i, j int) bool {
		iPriority := out[i].NeedsReason != "" || out[i].Blocked
		jPriority := out[j].NeedsReason != "" || out[j].Blocked
		if iPriority != jPriority {
			return iPriority
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// allParents 全量 child→parent 映射（只含有父的卡）。与 allStatuses 同为
// 「全量小表 + 内存组装」——卡量级数百张，正确性优先（见文件头）。
func (s *Store) allParents() (map[string]string, error) {
	rows, err := s.db.Query(s.q(`SELECT id, parent_id FROM cards WHERE parent_id IS NOT NULL`))
	if err != nil {
		return nil, fmt.Errorf("读父子表: %w", err)
	}
	defer rows.Close()
	parents := map[string]string{}
	for rows.Next() {
		var id, parent string
		if err := rows.Scan(&id, &parent); err != nil {
			return nil, err
		}
		parents[id] = parent
	}
	return parents, rows.Err()
}

func (s *Store) allRelations() ([]Relation, error) {
	rows, err := s.db.Query(`SELECT from_id, to_id, type FROM card_relations`)
	if err != nil {
		return nil, fmt.Errorf("读边表: %w", err)
	}
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		var relation Relation
		if err := rows.Scan(&relation.From, &relation.To, &relation.Type); err != nil {
			return nil, err
		}
		out = append(out, relation)
	}
	return out, rows.Err()
}

func (s *Store) allStatuses() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT id, status FROM cards`)
	if err != nil {
		return nil, fmt.Errorf("读状态表: %w", err)
	}
	defer rows.Close()
	statuses := map[string]string{}
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		statuses[id] = status
	}
	return statuses, rows.Err()
}

// needsMap 每卡最后一条 needs_human/needs_cleared 决定当前等人态。
// 单卡最多几十条事件、卡数百张，直接扫两类事件按 seq 归并即可。
func (s *Store) needsMap() (map[string]string, error) {
	rows, err := s.db.Query(s.q(`SELECT card_id, type, payload FROM card_events
		WHERE type IN (?, ?) AND card_id IS NOT NULL ORDER BY seq ASC`), EvNeedsHuman, EvNeedsCleared)
	if err != nil {
		return nil, fmt.Errorf("读等人事件: %w", err)
	}
	defer rows.Close()
	needs := map[string]string{}
	for rows.Next() {
		var cardID, typ, payload string
		if err := rows.Scan(&cardID, &typ, &payload); err != nil {
			return nil, err
		}
		if typ == EvNeedsCleared {
			delete(needs, cardID)
			continue
		}
		var data struct {
			Reason string `json:"reason"`
		}
		if err := jsonUnmarshal(payload, &data); err != nil {
			return nil, err
		}
		needs[cardID] = data.Reason
	}
	return needs, rows.Err()
}

// openDecisionCount 每卡 open 裁决数（decisions 表在 Task 10 前恒空，
// 查询天然返回空 map，不需要桩）。
func (s *Store) openDecisionCount() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT card_id, COUNT(*) FROM decisions
		WHERE status = 'open' AND card_id IS NOT NULL GROUP BY card_id`)
	if err != nil {
		return nil, fmt.Errorf("读裁决计数: %w", err)
	}
	defer rows.Close()
	counts := map[string]int{}
	for rows.Next() {
		var cardID sql.NullString
		var count int
		if err := rows.Scan(&cardID, &count); err != nil {
			return nil, err
		}
		if cardID.Valid {
			counts[cardID.String] = count
		}
	}
	return counts, rows.Err()
}

// NeedsOf 取一张卡当前的等人原因；没打标记（或已清除）返回空串。
//
// why 单卡要有自己的取法：needsMap 是为列表视图一次性扫全表的，详情抽屉
// 只关心一张卡。看板卡片上有「需要你」角标、点进抽屉却看不到原因，等于把
// 「卡的一切只在抽屉一处看」拆成了两处（2026-08-19 真机看到）。
//
// 语义与 needsMap 一致：按 seq 顺序回放，needs_cleared 抵消先前的 needs_human。
func (s *Store) NeedsOf(cardID string) (string, error) {
	rows, err := s.db.Query(s.q(`SELECT type, payload FROM card_events
		WHERE card_id = ? AND type IN (?, ?) ORDER BY seq ASC`), cardID, EvNeedsHuman, EvNeedsCleared)
	if err != nil {
		return "", fmt.Errorf("读卡 %s 的等人事件: %w", cardID, err)
	}
	defer rows.Close()
	reason := ""
	for rows.Next() {
		var typ, payload string
		if err := rows.Scan(&typ, &payload); err != nil {
			return "", err
		}
		if typ == EvNeedsCleared {
			reason = ""
			continue
		}
		var data struct {
			Reason string `json:"reason"`
		}
		if err := jsonUnmarshal(payload, &data); err != nil {
			return "", err
		}
		reason = data.Reason
	}
	return reason, rows.Err()
}
