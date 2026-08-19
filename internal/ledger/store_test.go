// store 层测试基座：所有领域操作测试共用 newTestStore。
// SQLite 全量跑；PG 冒烟在 store_pg_test.go 由环境变量门控。
package ledger

import (
	"path/filepath"
	"testing"
)

// newTestStore 返回临时 SQLite 账本库。后续所有 *_test.go 复用。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "ledger.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestOpenCreatesSchema(t *testing.T) {
	s := newTestStore(t)
	// 全部表都建出来了：逐表 SELECT 不报错即证明 DDL 幂等执行成功
	for _, tbl := range []string{"cards", "card_relations", "card_tasks",
		"card_events", "workflows", "dispatch_templates", "decisions",
		"mirror_lease", "mirror_cursors", "ledger_meta"} {
		if _, err := s.db.Exec("SELECT * FROM " + tbl + " LIMIT 0"); err != nil {
			t.Fatalf("表 %s 不存在: %v", tbl, err)
		}
	}
	// 幂等：重开不报错
	s2, err := Open(filepath.Join(filepath.Dir(s.path), "ledger.db"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s2.Close()
}

func TestQRebind(t *testing.T) {
	s := &Store{dialect: dialectPG}
	got := s.q("SELECT ? , ?")
	if got != "SELECT $1 , $2" {
		t.Fatalf("rebind: %q", got)
	}
	s2 := &Store{dialect: dialectSQLite}
	if s2.q("SELECT ?") != "SELECT ?" {
		t.Fatal("sqlite 不应重写")
	}
}

func TestRedactDSN(t *testing.T) {
	got := redactDSN("postgres://user:secret@example.test:5432/handoff", dialectPG)
	if got != "postgres://user@example.test:5432/handoff" {
		t.Fatalf("DSN 脱敏: %q", got)
	}
	if got := redactDSN("/tmp/ledger.db", dialectSQLite); got != "/tmp/ledger.db" {
		t.Fatalf("SQLite 路径不应改写: %q", got)
	}
}
