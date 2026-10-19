// b444_session_home_pg_test.go —— B444 P2 sessions.home_project 迁移的 PG 孪生（plan §5 A2.8 PG 腿、R4）。
// 默认 skip；设 LEDGER_TEST_PG_DSN 后启用。只在本测试新建的隔离 schema 内建表写行，用后 DROP SCHEMA
// CASCADE，不碰库内其它数据。
package ledger

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// A2.8 PG 腿：旧库 sessions 无 home_project 且有存量行 → Open 迁移成功、旧行 home_project==""，重复 Open
// 幂等；随后项目内会话写读往返、改名 CAS 生效。
func TestB444SessionHomeProjectMigrationPG(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 B444 P2 PG 迁移腿")
	}
	schema := fmt.Sprintf("b444_home_%d", time.Now().UnixNano())
	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("连接 PG: %v", err)
	}
	if _, err := admin.Exec(`CREATE SCHEMA ` + schema); err != nil {
		admin.Close()
		t.Fatalf("建隔离 schema: %v", err)
	}
	t.Cleanup(func() {
		if _, err := admin.Exec(`DROP SCHEMA ` + schema + ` CASCADE`); err != nil {
			t.Errorf("清理隔离 schema %s: %v", schema, err)
		}
		_ = admin.Close()
	})
	for _, stmt := range []string{
		`CREATE TABLE ` + schema + `.sessions (
			id TEXT PRIMARY KEY, title TEXT NOT NULL, owner TEXT NOT NULL,
			archived BOOLEAN NOT NULL DEFAULT false, members JSONB NOT NULL DEFAULT '[]',
			created_at TIMESTAMPTZ NOT NULL, updated_at TIMESTAMPTZ NOT NULL)`,
		`INSERT INTO ` + schema + `.sessions (id, title, owner, created_at, updated_at)
			VALUES ('session:1', '旧会话', 'user:owner', now(), now())`,
		`CREATE TABLE ` + schema + `.ledger_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO ` + schema + `.ledger_meta (key, value) VALUES ('session_seq', '1')`,
	} {
		if _, err := admin.Exec(stmt); err != nil {
			t.Fatalf("建 PG 旧库: %v", err)
		}
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	isolated := dsn + sep + "search_path=" + schema
	for round := 1; round <= 2; round++ {
		s, err := Open(isolated)
		if err != nil {
			t.Fatalf("第 %d 次 Open PG 旧库失败: %v", round, err)
		}
		got, err := s.GetSession("session:1")
		if err != nil || got.HomeProject != "" || got.Title != "旧会话" {
			s.Close()
			t.Fatalf("第 %d 次 Open 后旧会话应零回填: %+v err=%v", round, got, err)
		}
		s.Close()
	}
	s, err := Open(isolated)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	created, err := s.CreateSessionInProject("新会话", "user:o", "handoff", "user:o")
	if err != nil {
		t.Fatalf("PG 建项目内会话: %v", err)
	}
	if _, err := s.RenameSession(created.ID, "PG 改名", "新会话", "user:o"); err != nil {
		t.Fatalf("PG 改名: %v", err)
	}
	got, err := s.GetSession(created.ID)
	if err != nil || got.HomeProject != "handoff" || got.Title != "PG 改名" {
		t.Fatalf("PG 读回: %+v err=%v", got, err)
	}
}
