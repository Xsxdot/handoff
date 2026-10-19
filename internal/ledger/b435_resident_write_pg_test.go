// B435 R 驻场者写面的 PG 孪生（C-414、Q6–Q8）。默认 skip；设 LEDGER_TEST_PG_DSN 后启用（真机 R-PG）。
// 只动本测试新建的会话及其驻场者行，结束时删掉自己的驻场者行。
package ledger

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestB435SessionResidentWritePG(t *testing.T) {
	s := newPGStore(t)
	session := b435ResidentSession(t, s)
	t.Cleanup(func() { _, _ = s.db.Exec(s.q(`DELETE FROM session_residents WHERE session = ?`), session) })

	// 写 + 读回。
	if _, err := s.SitResident(SessionResident{Session: session, Identity: "user:pg", DesktopSession: "local_pg", CLISession: "cli-pg", Machine: "linux-01"}); err != nil {
		t.Fatalf("PG sit: %v", err)
	}
	before := b435MustResident(t, s, session)
	if before.Identity != "user:pg" || before.DesktopSession != "local_pg" || before.CLISession != "cli-pg" || before.Machine != "linux-01" {
		t.Fatalf("PG 读回不符: %+v", before)
	}
	// 拒：第二次 sit，原登记不变。
	if _, err := s.SitResident(SessionResident{Session: session, Identity: "agent:other"}); !errors.Is(err, ErrResidentOccupied) {
		t.Fatalf("PG 第二次 sit 应 ErrResidentOccupied，得 %v", err)
	}
	if after := b435MustResident(t, s, session); !after.SatAt.Equal(before.SatAt) || after.Identity != before.Identity {
		t.Fatalf("PG 被拒 sit 改了登记: %+v → %+v", before, after)
	}
	// 条件删除：身份不符拒、相符删。
	if err := s.StandResident(session, "agent:other"); !errors.Is(err, ErrResidentNotSeated) {
		t.Fatalf("PG 身份不符 stand 应 ErrResidentNotSeated，得 %v", err)
	}
	if err := s.StandResident(session, "user:pg"); err != nil {
		t.Fatalf("PG stand: %v", err)
	}
	if _, ok, err := s.SessionResident(session); err != nil || ok {
		t.Fatalf("PG stand 后应 ok=false: %v %v", ok, err)
	}
	// 并发：恰一成功。
	const n = 6
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = s.SitResident(SessionResident{Session: session, Identity: fmt.Sprintf("agent:pg%d", i)})
		}(i)
	}
	wg.Wait()
	ok := 0
	for i, err := range errs {
		if err == nil {
			ok++
		} else if !errors.Is(err, ErrResidentOccupied) {
			t.Fatalf("PG goroutine %d: %v", i, err)
		}
	}
	if ok != 1 {
		t.Fatalf("PG 并发 sit 应恰一成功，得 %d", ok)
	}
}
