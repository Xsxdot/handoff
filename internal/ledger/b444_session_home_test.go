// b444_session_home_test.go —— B444 P2 会话所属项目（home_project）与改名的账本半边（plan §5 A2.5 账本腿、
// A2.8 SQLite 腿；缺陷族 R4/R7/R10）。
//
// 观察点：真 SQLite 账本。① 建会话写入所属项目、读回逐字段；旧入口建的会话所属项目为空；② 旧库（sessions
// 无 home_project 列、有存量行）Open 后迁移加列成功、旧行读出空串、重复 Open 幂等；③ 改名 CAS：成功落
// session_renamed 事件（载荷 {session,title,from}）、expect 不符 → ErrCASConflict 且标题不变、同名零事件、
// 归档拒、改名事件进会话限域投影读面。PG 孪生见 b444_session_home_pg_test.go。
package ledger

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// A2.1 账本腿：所属项目落库读回；旧入口 CreateSession 的会话所属项目为空（S8 不变样）。
func TestB444CreateSessionHomeProjectRoundTrip(t *testing.T) {
	s := newTestStore(t)
	homed, err := s.CreateSessionInProject("项目内会话", "user:o", "handoff", "user:o")
	if err != nil {
		t.Fatalf("建项目内会话: %v", err)
	}
	if homed.HomeProject != "handoff" {
		t.Fatalf("返回值所属项目应为 handoff，得 %q", homed.HomeProject)
	}
	legacy, err := s.CreateSession("旧入口会话", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSession(homed.ID)
	if err != nil || got.HomeProject != "handoff" {
		t.Fatalf("GetSession 读回所属项目应为 handoff: %+v err=%v", got, err)
	}
	got, err = s.GetSession(legacy.ID)
	if err != nil || got.HomeProject != "" {
		t.Fatalf("旧入口会话所属项目应为空: %+v err=%v", got, err)
	}
	list, err := s.ListSessions()
	if err != nil {
		t.Fatal(err)
	}
	homes := map[string]string{}
	for _, sess := range list {
		homes[sess.ID] = sess.HomeProject
	}
	if homes[homed.ID] != "handoff" || homes[legacy.ID] != "" {
		t.Fatalf("ListSessions 所属项目投影不符: %v", homes)
	}
}

// A2.8 SQLite 腿：旧库 sessions 无 home_project 列且有存量行 → Open 迁移加列成功，旧行 home_project 为空、
// 其余列不被改写；第二次 Open 幂等；迁移后新建项目内会话可写可读。
func TestB444SessionHomeProjectMigrationOldDB(t *testing.T) {
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
		`INSERT INTO sessions (id, title, owner, created_at, updated_at)
			VALUES ('session:1', '旧会话甲', 'user:owner', '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z')`,
		`INSERT INTO sessions (id, title, owner, created_at, updated_at)
			VALUES ('session:2', '旧会话乙', 'user:owner', '2026-10-01T00:00:00Z', '2026-10-01T00:00:00Z')`,
		`CREATE TABLE ledger_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`,
		`INSERT INTO ledger_meta (key, value) VALUES ('session_seq', '2')`,
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
		for _, id := range []string{"session:1", "session:2"} {
			got, err := s.GetSession(id)
			if err != nil {
				s.Close()
				t.Fatalf("第 %d 次 Open 后读旧会话 %s: %v", round, id, err)
			}
			if got.HomeProject != "" || got.Owner != "user:owner" || !strings.HasPrefix(got.Title, "旧会话") {
				s.Close()
				t.Fatalf("旧会话应零回填且其余列不变，得 %+v", got)
			}
		}
		s.Close()
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	created, err := s.CreateSessionInProject("迁移后新会话", "user:o", "erp", "user:o")
	if err != nil {
		t.Fatalf("迁移后建项目内会话: %v", err)
	}
	got, err := s.GetSession(created.ID)
	if err != nil || got.HomeProject != "erp" {
		t.Fatalf("迁移后新会话所属项目读回: %+v err=%v", got, err)
	}
}

// b444RenameEvents 读全流的 session_renamed 事件。
func b444RenameEvents(t *testing.T, s *Store) []Event {
	t.Helper()
	events, err := s.EventsFromAsc(nil, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var out []Event
	for _, ev := range events {
		if ev.Type == EvSessionRenamed {
			out = append(out, ev)
		}
	}
	return out
}

// A2.5 账本腿：改名成功落一条 session_renamed（载荷 session/title/from、actor 原样）；expect 不符 →
// ErrCASConflict 且标题不变、零新事件；同名零事件；不存在 → ErrNotFound；归档 → ErrBadState。
func TestB444RenameSessionCAS(t *testing.T) {
	s := newTestStore(t)
	sess, err := s.CreateSessionInProject("新会话", "user:o", "handoff", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameSession(sess.ID, "排查登录失败", "新会话", "agent:coord"); err != nil {
		t.Fatalf("CAS 改名应成功: %v", err)
	}
	got, err := s.GetSession(sess.ID)
	if err != nil || got.Title != "排查登录失败" {
		t.Fatalf("改名后标题: %+v err=%v", got, err)
	}
	events := b444RenameEvents(t, s)
	if len(events) != 1 {
		t.Fatalf("应恰一条 session_renamed，得 %d", len(events))
	}
	var payload map[string]string
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["session"] != sess.ID || payload["title"] != "排查登录失败" || payload["from"] != "新会话" ||
		events[0].Actor != "agent:coord" || events[0].CardID != "" {
		t.Fatalf("改名事件形状不符: payload=%v actor=%q card=%q", payload, events[0].Actor, events[0].CardID)
	}

	// 人先改过名：驻场者再按占位名 CAS → 拒，标题保持人的名字（R7）。
	_, err = s.RenameSession(sess.ID, "驻场者起的名", "新会话", "agent:coord")
	if !errors.Is(err, ErrCASConflict) {
		t.Fatalf("expect 不符应 ErrCASConflict，得 %v", err)
	}
	if got, _ := s.GetSession(sess.ID); got.Title != "排查登录失败" {
		t.Fatalf("CAS 失败后标题不得变，得 %q", got.Title)
	}
	if n := len(b444RenameEvents(t, s)); n != 1 {
		t.Fatalf("CAS 失败不得落事件，得 %d 条", n)
	}
	// 同名：幂等零事件。
	if _, err := s.RenameSession(sess.ID, "排查登录失败", "", "user:o"); err != nil {
		t.Fatalf("同名改名应幂等成功: %v", err)
	}
	if n := len(b444RenameEvents(t, s)); n != 1 {
		t.Fatalf("同名改名不得落事件，得 %d 条", n)
	}
	if _, err := s.RenameSession("session:999", "x", "", "user:o"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在会话应 ErrNotFound，得 %v", err)
	}
	if _, err := s.RenameSession(sess.ID, "  ", "", "user:o"); !errors.Is(err, ErrBadState) {
		t.Fatalf("空标题应 ErrBadState，得 %v", err)
	}
	if err := s.ArchiveSession(sess.ID, "user:o"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameSession(sess.ID, "归档后", "", "user:o"); !errors.Is(err, ErrBadState) {
		t.Fatalf("归档会话改名应 ErrBadState，得 %v", err)
	}
}

// A2.5 投影腿：改名事件进会话限域投影读面（详情 timeline 的数据源），且只进本会话。
func TestB444RenameEventInSessionProjection(t *testing.T) {
	s := newTestStore(t)
	a, err := s.CreateSession("甲", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateSession("乙", "user:o", "user:o")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RenameSession(a.ID, "甲改", "", "user:o"); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		session string
		want    int
	}{{a.ID, 1}, {b.ID, 0}} {
		events, err := s.SessionProjectionEventsContext(context.Background(), c.session, nil)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, ev := range events {
			if ev.Type == EvSessionRenamed {
				n++
			}
		}
		if n != c.want {
			t.Fatalf("会话 %s 投影里的改名事件应 %d 条，得 %d", c.session, c.want, n)
		}
	}
}

// 改名事件的限域读走专用部分索引（与 B409 结构事件同口径：不全表扫 card_events）。
func TestB444RenameProjectionUsesScopedIndex(t *testing.T) {
	s := newTestStore(t)
	rows, err := s.db.Query(`EXPLAIN QUERY PLAN SELECT seq FROM card_events WHERE card_id IS NULL
		AND type = 'session_renamed' AND json_extract(payload, '$.session') = ?`, "session:1")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	used := false
	for rows.Next() {
		var id, parent, notUsed int
		var detail string
		if err := rows.Scan(&id, &parent, &notUsed, &detail); err != nil {
			t.Fatal(err)
		}
		used = used || strings.Contains(detail, "idx_session_renamed_session_seq")
	}
	if !used {
		t.Fatal("改名事件限域读未选择 idx_session_renamed_session_seq")
	}
	for _, pg := range []bool{true, false} {
		if !strings.Contains(strings.Join(ddlStatements(pg), "\n"), "idx_session_renamed_session_seq") {
			t.Fatalf("DDL（pg=%v）缺改名事件索引", pg)
		}
	}
}
