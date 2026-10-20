// b444_resident_push_pg_test.go —— B444 P1 驻场者 push 列迁移的 PG 孪生（plan §5 A1.6 PG 腿、R4）。
// 默认 skip；设 LEDGER_TEST_PG_DSN 后启用。只在本测试新建的隔离 schema 内建表写行，用后
// DROP SCHEMA CASCADE，不碰库内其它数据。
package ledger

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// A1.6 PG 腿：旧库先建无新列的 session_residents 并插一行 → Open 迁移成功，旧行 driver_mode==""，
// 重复 Open 幂等；随后 push 行写读往返。
func TestB444ResidentPushMigrationPG(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 B444 PG 迁移腿")
	}
	schema := fmt.Sprintf("b444_resident_%d", time.Now().UnixNano())
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
		`CREATE TABLE ` + schema + `.session_residents (
			session TEXT PRIMARY KEY,
			identity TEXT NOT NULL,
			desktop_session TEXT NOT NULL DEFAULT '',
			cli_session TEXT NOT NULL DEFAULT '',
			machine TEXT NOT NULL DEFAULT '',
			sat_at TIMESTAMPTZ NOT NULL)`,
		// 旧行绕过会话号分配器直写，取 session:900 避开后面 CreateSession 从计数器分到的 session:1（B448 R9 首跑撞主键）。
		`INSERT INTO ` + schema + `.sessions (id, title, owner, created_at, updated_at)
			VALUES ('session:900', '旧会话', 'user:owner', now(), now())`,
		`INSERT INTO ` + schema + `.session_residents (session, identity, desktop_session, cli_session, machine, sat_at)
			VALUES ('session:900', 'agent:org', 'local_old', 'old-cli', '', now())`,
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
		r, ok, err := s.SessionResident("session:900")
		if err != nil || !ok || r.DriverMode != ResidentDriverDesktop || r.PushSession != "" || r.CLISession != "old-cli" {
			s.Close()
			t.Fatalf("第 %d 次 Open 后旧行应 desktop 且零回填: ok=%v err=%v r=%+v", round, ok, err, r)
		}
		s.Close()
	}
	s, err := Open(isolated)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session, err := s.CreateSession("push 驻场", "user:owner", "user:owner")
	if err != nil {
		t.Fatal(err)
	}
	want := b444PushResident(session.ID)
	if _, err := s.SitResident(want); err != nil {
		t.Fatalf("PG 登记 push 驻场者: %v", err)
	}
	got, ok, err := s.SessionResident(session.ID)
	if err != nil || !ok {
		t.Fatalf("PG 读 push 驻场行: ok=%v err=%v", ok, err)
	}
	got.SatAt = time.Time{}
	if got != want {
		t.Fatalf("PG push 驻场行读回不符:\n得 %+v\n应 %+v", got, want)
	}
}
