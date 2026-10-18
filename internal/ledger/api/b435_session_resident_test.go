// B435 Facade.SessionResident 映射（C-406 键集；P2 甲：行由测试直写）。
package api

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestB435FacadeSessionResidentWire(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resident.db")
	st, err := ledger.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	lc := New(st)

	if _, ok, err := lc.SessionResident("session:1"); ok || err != nil {
		t.Fatalf("无驻场者应 ok=false、err=nil: %v %v", ok, err)
	}

	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	sat := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC).Format(time.RFC3339Nano)
	if _, err := raw.Exec(`INSERT INTO session_residents
		(session, identity, desktop_session, cli_session, machine, sat_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"session:1", "user:boss", "local_abc", "cli-1", "mac-pro", sat); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`INSERT INTO session_residents (session, identity, sat_at) VALUES (?, ?, ?)`,
		"session:2", "agent:org", sat); err != nil {
		t.Fatal(err)
	}
	keys := func(session string) []string {
		t.Helper()
		r, ok, err := lc.SessionResident(session)
		if err != nil || !ok {
			t.Fatalf("%s 应读到驻场者: %v %v", session, ok, err)
		}
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		var out []string
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	if got, want := keys("session:1"), []string{"cli_session", "desktop_session", "identity", "machine", "sat_at", "session"}; !slices.Equal(got, want) {
		t.Fatalf("全量键集不符: %v", got)
	}
	if got, want := keys("session:2"), []string{"identity", "sat_at", "session"}; !slices.Equal(got, want) {
		t.Fatalf("无桌面端地址键集不符: %v", got)
	}
}
