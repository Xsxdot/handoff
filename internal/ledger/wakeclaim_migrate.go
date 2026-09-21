// wake_claims 旧表迁移（B389 契约 §4-36）。
//
// 职责：把基线遗留的 wake_claims(seq) 单键表在 Open 时安全重建成 (card,seq)
// 复合键表，避免后续 ClaimWake/CompleteWake/WakeClaimsBefore 用 WHERE card=?
// 撞上无 card 主键的旧表产生静默错配。
// 边界：只处理这一张表；不做数据搬迁（认领是短暂租约，重建即丢弃在飞行——
// 事件会在下一轮被重新认领，不会永久丢失）；PG 与 SQLite 各一条探测分支。
package ledger

import (
	"fmt"
)

// wakeClaimsHasCompositeKey 报告 wake_claims 表是否已是 (card,seq) 复合键形态
// ——判据是 card 列属于主键，而不是"存在 card 列"：基线单键表同样有 card 列
// （只是不在主键里），只查列存在会把旧表当成已迁移而静默漏迁。
// 表不存在返回 (false, false, nil)：调用方据此跳过迁移，交由 DDL 建新表。
func (s *Store) wakeClaimsHasCompositeKey() (exists, composite bool, err error) {
	if s.dialect == dialectPG {
		var n int
		if err := s.db.QueryRow(`SELECT count(*) FROM information_schema.tables
			WHERE table_name = 'wake_claims' AND table_schema = current_schema()`).Scan(&n); err != nil {
			return false, false, fmt.Errorf("探测 wake_claims 表: %w", err)
		}
		if n == 0 {
			return false, false, nil
		}
		var cols int
		if err := s.db.QueryRow(`SELECT count(*) FROM pg_index i
			JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = ANY(i.indkey)
			WHERE i.indrelid = 'wake_claims'::regclass AND i.indisprimary
			AND a.attname = 'card'`).Scan(&cols); err != nil {
			return true, false, fmt.Errorf("探测 wake_claims 主键含 card: %w", err)
		}
		return true, cols > 0, nil
	}
	var n int
	if err := s.db.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'wake_claims'`).Scan(&n); err != nil {
		return false, false, fmt.Errorf("探测 wake_claims 表: %w", err)
	}
	if n == 0 {
		return false, false, nil
	}
	rows, err := s.db.Query(`PRAGMA table_info(wake_claims)`)
	if err != nil {
		return true, false, fmt.Errorf("读 wake_claims 列: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name      string
			typ       string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			return true, false, fmt.Errorf("扫描 wake_claims 列: %w", err)
		}
		if name == "card" && pk > 0 {
			composite = true
		}
	}
	if err := rows.Err(); err != nil {
		return true, false, fmt.Errorf("遍历 wake_claims 列: %w", err)
	}
	return true, composite, nil
}

// migrateWakeClaimsCompositeKey 在 Open 时把旧单键表重建成复合键表。新库与
// 已迁移库都是 no-op。重建成败直接决定后续三个认领方法能否工作，故失败上抛，
// 不做静默降级。
func (s *Store) migrateWakeClaimsCompositeKey() error {
	exists, composite, err := s.wakeClaimsHasCompositeKey()
	if err != nil {
		return fmt.Errorf("迁移 wake_claims 探测: %w", err)
	}
	if !exists || composite {
		return nil
	}
	// 单键表：DROP + CREATE（含索引）。旧表没有 card 列，无数据可迁移；
	// 在飞认领会被清空，事件在下一轮被重新认领，不永久丢。
	if _, err := s.db.Exec(`DROP TABLE wake_claims`); err != nil {
		return fmt.Errorf("迁移 wake_claims 删旧表: %w", err)
	}
	seqType := "INTEGER"
	tsType := "TEXT"
	if s.dialect == dialectPG {
		seqType, tsType = "BIGINT", "TIMESTAMPTZ"
	}
	create := fmt.Sprintf(`CREATE TABLE wake_claims (
		card TEXT NOT NULL,
		seq %s NOT NULL,
		holder TEXT NOT NULL,
		lease_until %s NOT NULL,
		done_at %s,
		PRIMARY KEY (card, seq))`, seqType, tsType, tsType)
	if _, err := s.db.Exec(create); err != nil {
		return fmt.Errorf("迁移 wake_claims 建新表: %w", err)
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_wake_claims_seq ON wake_claims(seq)`); err != nil {
		return fmt.Errorf("迁移 wake_claims 建索引: %w", err)
	}
	log().Info("唤醒认领表已迁移为 (card,seq) 复合键", "dialect", map[bool]string{true: "postgres", false: "sqlite"}[s.dialect == dialectPG])
	return nil
}
