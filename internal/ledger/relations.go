// 类型化关系边。方向语义钉死在此文件：
//
//	blocks:          from 阻塞 to（to 被 from 阻塞）
//	merged_into:     from 并入 to（to 是承载卡）——只许经 merge.go 写
//	discovered_from: from 发现自 to
//	split_from:      from 拆分自 to
//	relates:         无向关联（存单向行，查询双向）
//
// 环检测只对 blocks 生效（spec §2）；「parent 树与 blocks 混合成环」的
// 具体解释 = 禁止阻塞自己的祖先或后代 + blocks 图内禁有向环。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var validRelTypes = map[string]bool{
	RelBlocks: true, RelDiscoveredFrom: true, RelSplitFrom: true, RelRelates: true,
	// RelMergedInto 故意不在此表——必须走 MergeCards
}

// AddBlocks 加阻塞边：blocker 阻塞 blocked。事务内做环检测。
func (s *Store) AddBlocks(blocker, blocked, actor string) error {
	if blocker == blocked {
		log().Warn("阻塞边被拒：自阻塞", "card", blocker)
		return fmt.Errorf("不能自阻塞: %w", ErrCycle)
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		for _, id := range []string{blocker, blocked} {
			if _, err := getCardTx(s, tx, id); err != nil {
				return fmt.Errorf("阻塞边: 卡 %s: %w", id, err)
			}
		}
		// 祖先/后代互斥：blocked 的祖先链含 blocker，或 blocker 的祖先链含
		// blocked，都视为 parent 树参与的环
		for _, pair := range [][2]string{{blocker, blocked}, {blocked, blocker}} {
			ancestors, err := s.ancestorsTx(tx, pair[0])
			if err != nil {
				return err
			}
			if ancestors[pair[1]] {
				log().Warn("阻塞边被拒：跨父子", "blocker", blocker, "blocked", blocked)
				return fmt.Errorf("%s 与 %s 是祖先/后代关系: %w", blocker, blocked, ErrCycle)
			}
		}
		// blocks 图有向环：加边后从 blocked 出发（沿「X 阻塞 Y」的 X→Y 方向）
		// 能回到 blocker 即成环。图读写同事务 + 全局写锁 = 判定与写入原子。
		edges, err := s.blocksEdgesTx(tx)
		if err != nil {
			return err
		}
		edges[blocker] = append(edges[blocker], blocked)
		seen := map[string]bool{}
		var visit func(string) bool
		visit = func(node string) bool {
			if node == blocker {
				return true
			}
			if seen[node] {
				return false
			}
			seen[node] = true
			for _, next := range edges[node] {
				if visit(next) {
					return true
				}
			}
			return false
		}
		if visit(blocked) {
			log().Warn("阻塞边被拒：成环", "blocker", blocker, "blocked", blocked)
			return fmt.Errorf("%s→%s 使阻塞图成环: %w", blocker, blocked, ErrCycle)
		}
		if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
			VALUES (?,?,?,?)`), blocker, blocked, RelBlocks, s.tval(time.Now())); err != nil {
			return fmt.Errorf("写阻塞边（可能已存在）: %w", err)
		}
		_, err = s.appendEvent(tx, sink, blocked, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("被 %s 阻塞", blocker), "refs": []string{blocker}})
		return err
	})
}

// AddRelation 加非阻塞、非合并的关系边（discovered_from/split_from/relates）。
func (s *Store) AddRelation(from, to, typ, actor string) error {
	if typ == RelMergedInto {
		return fmt.Errorf("merged_into 必须经 MergeCards 建立（要做基线与链式校验）")
	}
	if typ == RelBlocks {
		return s.AddBlocks(from, to, actor)
	}
	if !validRelTypes[typ] {
		return fmt.Errorf("未知关系类型 %q", typ)
	}
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		for _, id := range []string{from, to} {
			if _, err := getCardTx(s, tx, id); err != nil {
				return fmt.Errorf("关系边: 卡 %s: %w", id, err)
			}
		}
		if _, err := tx.Exec(s.q(`INSERT INTO card_relations (from_id, to_id, type, created_at)
			VALUES (?,?,?,?)`), from, to, typ, s.tval(time.Now())); err != nil {
			return fmt.Errorf("写关系边: %w", err)
		}
		_, err := s.appendEvent(tx, sink, from, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("关系 %s → %s", typ, to), "refs": []string{to}})
		return err
	})
}

// RemoveRelation 删关系边。merged_into 请走 UnmergeCard。
func (s *Store) RemoveRelation(from, to, typ string) error {
	if typ == RelMergedInto {
		return fmt.Errorf("解除并入请走 unmerge")
	}
	return s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		result, err := tx.Exec(s.q(`DELETE FROM card_relations WHERE from_id = ? AND to_id = ? AND type = ?`),
			from, to, typ)
		if err != nil {
			return fmt.Errorf("删关系边: %w", err)
		}
		if count, _ := result.RowsAffected(); count == 0 {
			return fmt.Errorf("关系 %s-%s-%s: %w", from, typ, to, ErrNotFound)
		}
		return nil
	})
}

// RelationsOf 双向取一张卡的全部关系边（from 或 to 命中皆返回）。
func (s *Store) RelationsOf(id string) ([]Relation, error) {
	rows, err := s.db.Query(s.q(`SELECT from_id, to_id, type, created_at FROM card_relations
		WHERE from_id = ? OR to_id = ? ORDER BY created_at`), id, id)
	if err != nil {
		return nil, fmt.Errorf("读关系边: %w", err)
	}
	defer rows.Close()
	var out []Relation
	for rows.Next() {
		var relation Relation
		var createdAt any
		if err := rows.Scan(&relation.From, &relation.To, &relation.Type, &createdAt); err != nil {
			return nil, err
		}
		relation.CreatedAt = toTime(createdAt)
		out = append(out, relation)
	}
	return out, rows.Err()
}

// ancestorsTx 取卡的祖先集合（parent 链，含防御性深度上限）。
func (s *Store) ancestorsTx(tx *sql.Tx, id string) (map[string]bool, error) {
	ancestors := map[string]bool{}
	current := id
	for i := 0; i < 64; i++ { // B 号树实际深度 ≤2，64 是防坏数据死循环
		var parent sql.NullString
		err := tx.QueryRow(s.q(`SELECT parent_id FROM cards WHERE id = ?`), current).Scan(&parent)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ancestors, nil
			}
			return nil, fmt.Errorf("读父链: %w", err)
		}
		if !parent.Valid || parent.String == "" {
			return ancestors, nil
		}
		ancestors[parent.String] = true
		current = parent.String
	}
	return ancestors, fmt.Errorf("父链深度超限（数据疑似成环）: %s", id)
}

// blocksEdgesTx 事务内读全部阻塞边成邻接表（from 阻塞 → to 列表）。
func (s *Store) blocksEdgesTx(tx *sql.Tx) (map[string][]string, error) {
	rows, err := tx.Query(s.q(`SELECT from_id, to_id FROM card_relations WHERE type = ?`), RelBlocks)
	if err != nil {
		return nil, fmt.Errorf("读阻塞图: %w", err)
	}
	defer rows.Close()
	edges := map[string][]string{}
	for rows.Next() {
		var from, to string
		if err := rows.Scan(&from, &to); err != nil {
			return nil, err
		}
		edges[from] = append(edges[from], to)
	}
	return edges, rows.Err()
}

// EffectiveBaseBranch 解析卡的有效基线分支：自身非空即自身；否则沿
// parent 链向上取最近的显式设置；全空返回 ""（= 项目默认主线，由
// 调用方在派发时解析为具体分支名——库不猜 main/master）。
func (s *Store) EffectiveBaseBranch(id string) (string, error) {
	var base string
	err := s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		var err error
		base, err = s.effectiveBaseTx(tx, id)
		return err
	})
	return base, err
}

func (s *Store) effectiveBaseTx(tx *sql.Tx, id string) (string, error) {
	current := id
	for i := 0; i < 64; i++ {
		var base, parent sql.NullString
		err := tx.QueryRow(s.q(`SELECT base_branch, parent_id FROM cards WHERE id = ?`), current).Scan(&base, &parent)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", fmt.Errorf("卡 %s: %w", current, ErrNotFound)
			}
			return "", fmt.Errorf("读基线: %w", err)
		}
		if base.Valid && strings.TrimSpace(base.String) != "" {
			return base.String, nil
		}
		if !parent.Valid || parent.String == "" {
			return "", nil
		}
		current = parent.String
	}
	return "", fmt.Errorf("父链深度超限: %s", id)
}
