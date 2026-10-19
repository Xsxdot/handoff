// B435 R 驻场者写面（C-414 一房间至多一名、Q6 同事务递增席位版本、Q7 条件删除、Q8 第二次 sit
// 一律拒绝）。夹具：真 SQLite 账本；会话由 CreateSession 建。PG 孪生见 b435_resident_write_pg_test.go。
package ledger

import (
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func b435ResidentSession(t *testing.T, s *Store) string {
	t.Helper()
	session, err := s.CreateSession("驻场写面", "user:owner", "user:owner")
	if err != nil {
		t.Fatal(err)
	}
	return session.ID
}

func b435MustResident(t *testing.T, s *Store, session string) SessionResident {
	t.Helper()
	r, ok, err := s.SessionResident(session)
	if err != nil || !ok {
		t.Fatalf("应读到驻场者: ok=%v err=%v", ok, err)
	}
	return r
}

func TestB435SitResidentWritesRow(t *testing.T) {
	s := newTestStore(t)
	sat := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return sat }
	full := b435ResidentSession(t, s)
	bare := b435ResidentSession(t, s)
	got, err := s.SitResident(SessionResident{Session: full, Identity: "user:r",
		DesktopSession: "local_abc", CLISession: "cli-1", Machine: "mac-pro"})
	if err != nil {
		t.Fatalf("sit: %v", err)
	}
	want := SessionResident{Session: full, Identity: "user:r", DesktopSession: "local_abc",
		CLISession: "cli-1", Machine: "mac-pro", SatAt: sat}
	if !got.SatAt.Equal(sat) {
		t.Fatalf("返回 sat_at=%v want %v", got.SatAt, sat)
	}
	got.SatAt = sat
	if got != want {
		t.Fatalf("返回行不符:\n got=%+v\nwant=%+v", got, want)
	}
	read := b435MustResident(t, s, full)
	if !read.SatAt.Equal(sat) {
		t.Fatalf("读回 sat_at=%v", read.SatAt)
	}
	read.SatAt = sat
	if read != want {
		t.Fatalf("读回不符:\n got=%+v\nwant=%+v", read, want)
	}
	if _, err := s.SitResident(SessionResident{Session: bare, Identity: "agent:b"}); err != nil {
		t.Fatalf("无桌面端 sit: %v", err)
	}
	r := b435MustResident(t, s, bare)
	if r.Identity != "agent:b" || r.DesktopSession != "" || r.CLISession != "" || r.Machine != "" {
		t.Fatalf("无桌面端三列应为空串: %+v", r)
	}
}

func TestB435SitResidentSecondSitRejected(t *testing.T) {
	s := newTestStore(t)
	session := b435ResidentSession(t, s)
	if _, err := s.SitResident(SessionResident{Session: session, Identity: "user:first", DesktopSession: "local_a"}); err != nil {
		t.Fatal(err)
	}
	before := b435MustResident(t, s, session)
	for _, identity := range []string{"agent:second", "user:first"} { // Q8 甲：同身份再 sit 亦拒
		_, err := s.SitResident(SessionResident{Session: session, Identity: identity})
		if !errors.Is(err, ErrResidentOccupied) {
			t.Fatalf("第二次 sit（%s）应为 ErrResidentOccupied，得 %v", identity, err)
		}
		if after := b435MustResident(t, s, session); after != before {
			t.Fatalf("被拒 sit 改了原登记:\n before=%+v\n after=%+v", before, after)
		}
	}
}

// 并发：两个 Store 句柄（两条独立连接，模拟两个 CLI 进程）上 N 个 goroutine 同时 sit 不同身份，
// 恰一成功、其余为哨兵错误、表中恰一行（库层主键兑现 C-414，P3 甲）。
func TestB435SitResidentConcurrentExactlyOne(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ledger.db")
	a, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.Close() })
	b, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { b.Close() })
	session := b435ResidentSession(t, a)
	const n = 8
	errs := make([]error, n)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			st := a
			if i%2 == 1 {
				st = b
			}
			<-start
			_, errs[i] = st.SitResident(SessionResident{Session: session, Identity: fmt.Sprintf("agent:a%d", i)})
		}(i)
	}
	close(start)
	wg.Wait()
	ok := 0
	for i, err := range errs {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrResidentOccupied):
		default:
			t.Fatalf("goroutine %d 应成功或 ErrResidentOccupied，得 %v", i, err)
		}
	}
	if ok != 1 {
		t.Fatalf("恰一成功，得 %d 成功（%v）", ok, errs)
	}
	var rows int
	if err := a.db.QueryRow(`SELECT COUNT(*) FROM session_residents WHERE session = ?`, session).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("表中应恰一行: rows=%d err=%v", rows, err)
	}
}

func TestB435SitResidentRejectsMissingOrArchivedSession(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.SitResident(SessionResident{Session: "session:999", Identity: "user:r"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("会话不存在应 ErrNotFound，得 %v", err)
	}
	archived := b435ResidentSession(t, s)
	if err := s.ArchiveSession(archived, "user:owner"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SitResident(SessionResident{Session: archived, Identity: "user:r"}); !errors.Is(err, ErrBadState) {
		t.Fatalf("已归档应 ErrBadState，得 %v", err)
	}
	for _, bad := range []SessionResident{{Session: "", Identity: "user:r"}, {Session: archived, Identity: ""}} {
		if _, err := s.SitResident(bad); !errors.Is(err, ErrBadState) {
			t.Fatalf("空入参应 ErrBadState: %+v → %v", bad, err)
		}
	}
	var rows int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM session_residents`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("被拒 sit 应零写入: rows=%d err=%v", rows, err)
	}
}

func TestB435StandResidentRequiresIdentity(t *testing.T) {
	s := newTestStore(t)
	session := b435ResidentSession(t, s)
	if err := s.StandResident(session, "user:r"); !errors.Is(err, ErrResidentNotSeated) {
		t.Fatalf("无人在座 stand 应 ErrResidentNotSeated，得 %v", err)
	}
	if _, err := s.SitResident(SessionResident{Session: session, Identity: "user:r"}); err != nil {
		t.Fatal(err)
	}
	before := b435MustResident(t, s, session)
	if err := s.StandResident(session, "user:other"); !errors.Is(err, ErrResidentNotSeated) {
		t.Fatalf("身份不符 stand 应 ErrResidentNotSeated，得 %v", err)
	}
	if after := b435MustResident(t, s, session); after != before {
		t.Fatalf("被拒 stand 改了登记: %+v → %+v", before, after)
	}
	if err := s.StandResident(session, "user:r"); err != nil {
		t.Fatalf("身份相符 stand: %v", err)
	}
	if _, ok, err := s.SessionResident(session); err != nil || ok {
		t.Fatalf("stand 后应 ok=false: ok=%v err=%v", ok, err)
	}
}

func TestB435ResidentWritesBumpSeatRevision(t *testing.T) {
	s := newTestStore(t)
	session := b435ResidentSession(t, s)
	rev := func() int64 {
		t.Helper()
		v, err := s.SeatRevision()
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	r0 := rev()
	if _, err := s.SitResident(SessionResident{Session: session, Identity: "user:r"}); err != nil {
		t.Fatal(err)
	}
	r1 := rev()
	if r1 <= r0 {
		t.Fatalf("sit 应递增席位版本: %d → %d", r0, r1)
	}
	if _, err := s.SitResident(SessionResident{Session: session, Identity: "user:x"}); !errors.Is(err, ErrResidentOccupied) {
		t.Fatal(err)
	}
	if err := s.StandResident(session, "user:x"); !errors.Is(err, ErrResidentNotSeated) {
		t.Fatal(err)
	}
	if r := rev(); r != r1 {
		t.Fatalf("被拒的 sit/stand 不得递增版本: %d → %d", r1, r)
	}
	if err := s.StandResident(session, "user:r"); err != nil {
		t.Fatal(err)
	}
	if r2 := rev(); r2 <= r1 {
		t.Fatalf("stand 应递增席位版本: %d → %d", r1, r2)
	}
}

func TestB435StandKeepsDeliveryCursor(t *testing.T) {
	s := newTestStore(t)
	session := b435ResidentSession(t, s)
	key := "resident:" + session + "/user:r"
	if _, err := s.SitResident(SessionResident{Session: session, Identity: "user:r"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AdvanceSessionDeliveryCursor(key, 42); err != nil {
		t.Fatal(err)
	}
	if err := s.StandResident(session, "user:r"); err != nil {
		t.Fatal(err)
	}
	seq, err := s.SessionDeliveryCursor(key)
	if err != nil || seq != 42 {
		t.Fatalf("stand 不得动驻场者地址游标行（S8 续读）: seq=%d err=%v", seq, err)
	}
}
