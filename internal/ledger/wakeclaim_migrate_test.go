// B389 §4-36 旧单键 wake_claims 表迁移的锁（SQLite 侧；PG 侧同构分支由
// TestPGSchema 之外的迁移逻辑覆盖，需真 PG DSN 才跑）。
package ledger

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// seedLegacyWakeClaimsTable 在目标路径建出基线形态的 wake_claims(seq) 单键表
// （含一行存量在飞认领），模拟升级前的旧库。
func seedLegacyWakeClaimsTable(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("打开旧库: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE wake_claims (
		seq INTEGER PRIMARY KEY,
		card TEXT NOT NULL,
		holder TEXT NOT NULL,
		lease_until TEXT NOT NULL,
		done_at TEXT)`); err != nil {
		t.Fatalf("建旧单键表: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO wake_claims (seq, card, holder, lease_until, done_at)
		VALUES (?, ?, ?, ?, NULL)`, 7, "B-legacy", "old#1", time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("写旧存量行: %v", err)
	}
}

// TestWakeClaimsLegacySingleKeyTableMigrates 锁 §4-36：旧库的 seq 单键表在 Open
// 时被安全重建为 (card,seq) 复合键表，三个方法随即在新结构上工作——尤其
// ClaimWake 的 WHERE card=? 不撞上无 card 主键的旧表。
func TestWakeClaimsLegacySingleKeyTableMigrates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	seedLegacyWakeClaimsTable(t, path)

	s, err := Open(path)
	if err != nil {
		t.Fatalf("升级旧库: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// 列结构已含 card，且 card 在主键里（PRAGMA 的 pk 序号非 0）。
	rows, err := s.db.Query(`PRAGMA table_info(wake_claims)`)
	if err != nil {
		t.Fatalf("读新表列: %v", err)
	}
	pkCols := map[string]int{}
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
			t.Fatalf("扫描新表列: %v", err)
		}
		if pk > 0 {
			pkCols[name] = pk
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("遍历新表列: %v", err)
	}
	rows.Close()
	if pkCols["card"] == 0 || pkCols["seq"] == 0 {
		t.Fatalf("迁移后主键应为 (card,seq)，实得 %v", pkCols)
	}

	// 三个方法在新结构上工作：两张卡同 seq 各自可认领，收尾按 (card,seq) 定位。
	if got, err := s.ClaimWake(11, "B1", "h#1", time.Minute); err != nil || !got {
		t.Fatalf("迁移后 ClaimWake B1: got=%v err=%v", got, err)
	}
	if got, err := s.ClaimWake(11, "B2", "h#1", time.Minute); err != nil || !got {
		t.Fatalf("迁移后不同卡同 seq 应可各自认领: got=%v err=%v", got, err)
	}
	if err := s.CompleteWake(11, "B1", "h#1"); err != nil {
		t.Fatalf("迁移后 CompleteWake: %v", err)
	}
	inFlight, err := s.WakeClaimsBefore(12)
	if err != nil {
		t.Fatalf("迁移后 WakeClaimsBefore: %v", err)
	}
	if len(inFlight) != 1 || inFlight[0] != 11 {
		t.Fatalf("迁移后 B2 在飞应仍可见: %v", inFlight)
	}
}

// TestWakeClaimsCompositeTableReopenIdempotent 锁迁移幂等：已迁移库重复 Open
// 不重建、不报错（新库路径与已升级库都要满足）。
func TestWakeClaimsCompositeTableReopenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	s, err := Open(path)
	if err != nil {
		t.Fatalf("首开: %v", err)
	}
	if _, err := s.ClaimWake(3, "B1", "h#1", time.Minute); err != nil {
		t.Fatalf("首次认领: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("关闭: %v", err)
	}
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("重复 Open: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	inFlight, err := s2.WakeClaimsBefore(4)
	if err != nil {
		t.Fatalf("重复 Open 后读在飞: %v", err)
	}
	if len(inFlight) != 1 || inFlight[0] != 3 {
		t.Fatalf("重复 Open 不应清空已迁移库的认领行: %v", inFlight)
	}
}
