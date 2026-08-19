// PG 冒烟：真 PG 上跑 schema + 基本读写。默认 skip，设
// LEDGER_TEST_PG_DSN 后启用（判据⑩落地前审核者本地跑一次）。
package ledger

import (
	"os"
	"testing"
)

func newPGStore(t *testing.T) *Store {
	t.Helper()
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 PG 冒烟")
	}
	s, err := Open(dsn)
	if err != nil {
		t.Fatalf("open pg: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPGSchema(t *testing.T) {
	s := newPGStore(t)
	if _, err := s.db.Exec("SELECT * FROM cards LIMIT 0"); err != nil {
		t.Fatalf("cards 表: %v", err)
	}
}
