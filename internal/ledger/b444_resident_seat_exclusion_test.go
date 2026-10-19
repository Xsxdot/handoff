// b444_resident_seat_exclusion_test.go —— B444 P2 返工三 裁决 2（真机 R6：push 驻场者 `card bind` 把自己
// 绑成卡 D4 的席位、亲手跑完整条流）：同一 CLI 会话不能既是 push 驻场者又坐卡席位，由事实主人账本执法。
//
// 观察点：真账本（SQLite；PG 孪生设 LEDGER_TEST_PG_DSN 启用）。判据：① 坐席位的两个写面（BindSeatMode
// 含 bind/coordinate 两种来源、RebindSeatMode）遇到席位会话 id 等于某 push 驻场行 push_session 时拒绝，
// 文案「该 CLI 会话是会话 <id> 的驻场者，不能坐卡席位」，席位不变；按会话 id 判，不看 CLI 标签；
// ② 反向：SitResident(push) 与 SetResidentPushSession 遇到 push_session 正是某卡席位会话时拒绝，零写入；
// ③ 驻场者请下后同一会话可坐席位（约束只看现状，不留疤）。
package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB444ResidentSeatExclusion(t *testing.T) {
	b444ResidentSeatExclusionScenario(t, seedStore(t))
}

func TestB444ResidentSeatExclusionPG(t *testing.T) {
	dsn := os.Getenv("LEDGER_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("未设 LEDGER_TEST_PG_DSN，跳过 B444 驻场/席位互斥 PG 腿")
	}
	schema := fmt.Sprintf("b444_exclusion_%d", time.Now().UnixNano())
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
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	s, err := Open(dsn + sep + "search_path=" + schema)
	if err != nil {
		t.Fatalf("Open PG 隔离库: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if err := seedTestWorkflows(t, s); err != nil {
		t.Fatal(err)
	}
	b444ResidentSeatExclusionScenario(t, s)
}

func b444ResidentSeatExclusionScenario(t *testing.T, s *Store) {
	t.Helper()
	residentSession := b435ResidentSession(t, s)
	resident := b444PushResident(residentSession)
	resident.PushSession = "ses_b444_resident"
	if _, err := s.SitResident(resident); err != nil {
		t.Fatalf("坐 push 驻场者: %v", err)
	}
	wantText := "该 CLI 会话是会话 " + residentSession + " 的驻场者，不能坐卡席位"
	assertConflict := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, ErrResidentSeatConflict) || !strings.Contains(err.Error(), wantText) {
			t.Fatalf("%s 应以 ErrResidentSeatConflict 拒绝且文案含 %q，得 %v", what, wantText, err)
		}
	}
	card := b435Card(t, s, "驻场者不得坐的卡")
	residentSeat := "cli:opencode#ses_b444_resident"

	// ① bind 来源（R6 原样：card bind 出示驻场载体自己的会话）。
	assertConflict("bind 坐驻场会话", s.BindSeatMode(card.ID, residentSeat, proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}))
	// 按会话 id 判：换个 CLI 标签同一会话 id 也拒。
	assertConflict("换 CLI 标签坐驻场会话", s.BindSeatMode(card.ID, "cli:claude#ses_b444_resident", proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}))
	// coordinate 来源（push 席位写面同样执法）。
	assertConflict("coordinate 坐驻场会话", s.BindSeatMode(card.ID, residentSeat, proto.SeatSourceCoordinate, proto.SeatModePush,
		SeatBearing{Carrier: "coord-carrier", Machine: "linux-01"}))
	if got := b435Seat(t, s, card.ID); got != (b435SeatRow{ID: card.ID}) {
		t.Fatalf("被拒后卡席位必须仍空: %+v", got)
	}

	// ① 换绑写面：先由别的会话坐下，再试图换绑成驻场会话。
	cardSeat := "cli:opencode#ses_b444_card"
	if err := s.BindSeatMode(card.ID, cardSeat, proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}); err != nil {
		t.Fatalf("普通会话坐下: %v", err)
	}
	assertConflict("换绑成驻场会话", s.RebindSeatMode(card.ID, residentSeat, proto.SeatSourceBind, proto.SeatModePull, cardSeat, SeatBearing{}))
	if got := b435Seat(t, s, card.ID); got.Session != cardSeat {
		t.Fatalf("换绑被拒后席位应不变，得 %+v", got)
	}

	// ② 反向：push 驻场行的 push_session 不得是某卡的席位会话。
	reverseText := "该 CLI 会话正坐卡 " + card.ID + " 的席位"
	assertReverse := func(what string, err error) {
		t.Helper()
		if !errors.Is(err, ErrResidentSeatConflict) || !strings.Contains(err.Error(), reverseText) {
			t.Fatalf("%s 应以 ErrResidentSeatConflict 拒绝且文案含 %q，得 %v", what, reverseText, err)
		}
	}
	other := b435ResidentSession(t, s)
	seated := b444PushResident(other)
	seated.PushSession = "ses_b444_card"
	_, err := s.SitResident(seated)
	assertReverse("SitResident(push) 坐卡席位会话", err)
	if _, ok, err := s.SessionResident(other); err != nil || ok {
		t.Fatalf("被拒的 SitResident 必须零写入: ok=%v err=%v", ok, err)
	}
	inflight := b444PushResident(other)
	inflight.PushSession = ""
	if _, err := s.SitResident(inflight); err != nil {
		t.Fatalf("坐首轮在途 push 驻场者: %v", err)
	}
	assertReverse("SetResidentPushSession 补写卡席位会话", s.SetResidentPushSession(other, inflight.Identity, "ses_b444_card"))
	if got := b435MustResident(t, s, other); got.PushSession != "" {
		t.Fatalf("被拒的补写不得改 push_session，得 %q", got.PushSession)
	}

	// ③ 驻场者请下后，同一 CLI 会话可以坐席位（约束只看现状）。
	if err := s.StandResident(residentSession, resident.Identity); err != nil {
		t.Fatalf("请下驻场者: %v", err)
	}
	other2 := b435Card(t, s, "请下后可坐")
	if err := s.BindSeatMode(other2.ID, residentSeat, proto.SeatSourceBind, proto.SeatModePull, SeatBearing{}); err != nil {
		t.Fatalf("驻场者请下后同会话坐席位应成功: %v", err)
	}
}
