// b444_resident_push_test.go —— B444 P1（S5）驻场者 push 驱动形态与载体落点的账本半边
// （plan §5 A1.6 账本腿、缺陷族 R4/R10）。
//
// 观察点：真 SQLite 账本。① push 行的驱动形态与 bearing 经 SitResident 落库、SessionResident
// 读回逐字段相等；② 旧库（session_residents 无新列、有存量行）Open 后迁移加列成功，旧行读出
// driver_mode==""（desktop），重复 Open 幂等。PG 孪生见 b444_resident_push_pg_test.go。
package ledger

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// b444PushResident 是一行 push 驻场者的完整形状（全部 bearing 列非空，便于逐字段比对）。
func b444PushResident(session string) SessionResident {
	return SessionResident{
		Session: session, Identity: "agent:coord",
		DriverMode: ResidentDriverPush, PushCLI: "opencode", PushSession: "ses_b444_push_1",
		Carrier: "coord-carrier", CarrierMachine: "linux-01",
		HomeDir: "/tmp/b444-home", Workdir: "/tmp/b444-work", Model: "free-model",
	}
}

// A1.6 账本腿：push 驱动形态与 bearing 落库后逐字段读回；desktop 列保持空。
func TestB444SitResidentPushRoundTrip(t *testing.T) {
	s := newTestStore(t)
	session := b435ResidentSession(t, s)
	want := b444PushResident(session)
	if _, err := s.SitResident(want); err != nil {
		t.Fatalf("登记 push 驻场者: %v", err)
	}
	got := b435MustResident(t, s, session)
	got.SatAt = time.Time{}
	if got != want {
		t.Fatalf("push 驻场行读回不符:\n得 %+v\n应 %+v", got, want)
	}
}

// A1.6 / R4 SQLite 腿：旧库 session_residents 无新列且有存量 desktop 行 → Open 迁移加列成功，
// 旧行 driver_mode=="" 且 bearing 全空（零回填）；第二次 Open 幂等。
func TestB444ResidentPushMigrationOldDB(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE sessions (
			id TEXT PRIMARY KEY, title TEXT NOT NULL, owner TEXT NOT NULL,
			archived INTEGER NOT NULL DEFAULT 0, members TEXT NOT NULL DEFAULT '[]',
			created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`,
		`CREATE TABLE session_residents (
			session TEXT PRIMARY KEY,
			identity TEXT NOT NULL,
			desktop_session TEXT NOT NULL DEFAULT '',
			cli_session TEXT NOT NULL DEFAULT '',
			machine TEXT NOT NULL DEFAULT '',
			sat_at TEXT NOT NULL)`,
		`INSERT INTO sessions (id, title, owner, created_at, updated_at)
			VALUES ('session:1', '旧会话', 'user:owner', '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z')`,
		`INSERT INTO session_residents (session, identity, desktop_session, cli_session, machine, sat_at)
			VALUES ('session:1', 'agent:org', 'local_old', 'old-cli', '', '2026-10-01T00:00:00Z')`,
	} {
		if _, err := raw.Exec(stmt); err != nil {
			t.Fatalf("建旧库 %q: %v", stmt[:20], err)
		}
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		s, err := Open(path)
		if err != nil {
			t.Fatalf("第 %d 次 Open 旧库（迁移）失败: %v", round, err)
		}
		r, ok, err := s.SessionResident("session:1")
		if err != nil || !ok {
			s.Close()
			t.Fatalf("第 %d 次 Open 后读旧驻场行: ok=%v err=%v", round, ok, err)
		}
		if r.DriverMode != ResidentDriverDesktop || r.PushCLI != "" || r.PushSession != "" ||
			r.Carrier != "" || r.CarrierMachine != "" || r.HomeDir != "" || r.Workdir != "" || r.Model != "" {
			s.Close()
			t.Fatalf("旧行应零回填（desktop、bearing 全空），得 %+v", r)
		}
		if r.CLISession != "old-cli" || r.DesktopSession != "local_old" {
			s.Close()
			t.Fatalf("旧行桌面端列不得被迁移改写，得 %+v", r)
		}
		s.Close()
	}
}
