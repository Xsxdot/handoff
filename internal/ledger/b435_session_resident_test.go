// B435 驻场者登记只读面（P2 甲 / P3 甲；K0 无写面，行由测试直写）。
package ledger

import (
	"testing"
	"time"
)

func TestB435SessionResidentReadEmpty(t *testing.T) {
	s := newTestStore(t)
	r, ok, err := s.SessionResident("session:1")
	if err != nil || ok || r != (SessionResident{}) {
		t.Fatalf("空表应 ok=false、err=nil、零值，得 %+v %v %v", r, ok, err)
	}
}

func TestB435SessionResidentReadRow(t *testing.T) {
	s := newTestStore(t)
	sat := time.Date(2026, 10, 18, 9, 30, 0, 0, time.UTC)
	if _, err := s.db.Exec(`INSERT INTO session_residents
		(session, identity, desktop_session, cli_session, machine, sat_at) VALUES (?, ?, ?, ?, ?, ?)`,
		"session:7", "user:boss", "local_abc", "cli-uuid-1", "mac-pro", s.tval(sat)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO session_residents (session, identity, sat_at) VALUES (?, ?, ?)`,
		"session:8", "agent:org", s.tval(sat)); err != nil {
		t.Fatal(err)
	}
	full, ok, err := s.SessionResident("session:7")
	if err != nil || !ok {
		t.Fatalf("应读到一行: %v %v", ok, err)
	}
	want := SessionResident{Session: "session:7", Identity: "user:boss", DesktopSession: "local_abc",
		CLISession: "cli-uuid-1", Machine: "mac-pro", SatAt: sat}
	if !full.SatAt.Equal(sat) {
		t.Fatalf("sat_at 读回不符: %v", full.SatAt)
	}
	full.SatAt = sat
	if full != want {
		t.Fatalf("字段不齐:\n got=%+v\nwant=%+v", full, want)
	}
	bare, ok, err := s.SessionResident("session:8")
	if err != nil || !ok || bare.Identity != "agent:org" || bare.DesktopSession != "" || bare.CLISession != "" || bare.Machine != "" {
		t.Fatalf("无桌面端地址的行读回不符: %+v %v %v", bare, ok, err)
	}
}
