// B389 承载记录的行为锁（契约 docs/superpowers/specs/b389-contract.md §4 第 1-13 条）。
// 席位真源仍是 cards.driver_session/driver_source；本文件锁的是"承载记录随席位
// 同事务落盘、随换绑整体覆写、随清座删除"这几条不变量。
package ledger

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// 契约 §4-1/§4-2：两个方言都必须登记同一张新表（TestDDLDialectParity 之外再点名，
// 缺表时报错能直接指到本卡）。
func TestSeatBearingDDLRegisteredBothDialects(t *testing.T) {
	for _, pg := range []bool{true, false} {
		joined := strings.Join(ddlStatements(pg), "\n")
		for _, want := range []string{
			"CREATE TABLE IF NOT EXISTS seat_bearings",
			"CREATE TABLE IF NOT EXISTS wake_claims",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("方言 pg=%v 缺语句 %q", pg, want)
			}
		}
	}
}

// 契约 §4-3：旧库重复 Open 不因新表失败（CREATE IF NOT EXISTS 幂等）。
func TestSeatBearingReopenIdempotent(t *testing.T) {
	s := seedStore(t)
	for _, tbl := range []string{"seat_bearings", "wake_claims"} {
		if _, err := s.db.Exec("SELECT * FROM " + tbl + " LIMIT 0"); err != nil {
			t.Fatalf("表 %s 不存在: %v", tbl, err)
		}
	}
	s2, err := Open(filepath.Join(filepath.Dir(s.path), "ledger.db"))
	if err != nil {
		t.Fatalf("二次 Open 应幂等: %v", err)
	}
	s2.Close()
}

// 契约 §4-4/5/6/8/9/10/12/13：承载记录的形态执法与随席位同事务的覆写/清空。
func TestSeatBearingFollowsSeatLifecycle(t *testing.T) {
	s := seedStore(t)
	c, err := s.CreateCard(NewCard{Title: "承载", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	bearing := SeatBearing{
		Carrier: "coord-carrier", Machine: "linux-01",
		HomeDir: "/home/coordinator", Workdir: "/srv/repo", Model: "m1",
	}

	// §4-4：叫机器人不传承载 = 显式失败，且席位没被写进去。
	seat, _ := proto.EncodeSeatIdentity("opencode", "sess-coord")
	if err := s.BindSeat(c.ID, seat, proto.SeatSourceCoordinate, SeatBearing{}); !errors.Is(err, ErrBadState) {
		t.Fatalf("叫机器人缺承载应 ErrBadState，得 %v", err)
	}
	if got, _ := s.GetCard(c.ID); got.DriverSession != "" || got.DriverSource != "" {
		t.Fatalf("失败后席位必须保持空座: %+v", got)
	}

	// §4-5：人尺度坐下不许带承载。
	bindSeat, _ := proto.EncodeSeatIdentity("codex", "mine")
	if err := s.BindSeat(c.ID, bindSeat, proto.SeatSourceBind, bearing); !errors.Is(err, ErrBadState) {
		t.Fatalf("bind 带承载应 ErrBadState，得 %v", err)
	}
	if got, _ := s.GetCard(c.ID); got.DriverSession != "" || got.DriverSource != "" {
		t.Fatalf("失败后席位必须保持空座: %+v", got)
	}

	// §4-12：无记录的读面。
	if _, ok, err := s.SeatBearingOf(c.ID); err != nil || ok {
		t.Fatalf("无记录应 ok=false 且不报错: ok=%v err=%v", ok, err)
	}

	// §4-6/§4-13：叫机器人成功 = 席位与承载同事务落；identity 见证与席位一致。
	if err := s.BindSeat(c.ID, seat, proto.SeatSourceCoordinate, bearing); err != nil {
		t.Fatalf("叫机器人坐下: %v", err)
	}
	got, ok, err := s.SeatBearingOf(c.ID)
	if err != nil || !ok {
		t.Fatalf("读承载: ok=%v err=%v", ok, err)
	}
	if got != bearing {
		t.Fatalf("承载记录 = %+v，期望 %+v", got, bearing)
	}
	var witness, session string
	if err := s.db.QueryRow(`SELECT identity, (SELECT driver_session FROM cards WHERE id = ?)
		FROM seat_bearings WHERE card_id = ?`, c.ID, c.ID).Scan(&witness, &session); err != nil {
		t.Fatalf("读见证列: %v", err)
	}
	if witness != session || witness != seat {
		t.Fatalf("见证 identity=%q 与席位 %q 不一致", witness, session)
	}

	// §4-9：换绑 CAS 不符时席位与承载都不变。
	next, _ := proto.EncodeSeatIdentity("opencode", "sess-next")
	other := SeatBearing{Carrier: "other-carrier", Machine: "mbp", HomeDir: "/h", Workdir: "/w", Model: "m2"}
	if err := s.RebindSeat(c.ID, next, proto.SeatSourceCoordinate, "cli:opencode#wrong", other); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("CAS 不符应 ErrCASConflict，得 %v", err)
	}
	if after, _, _ := s.SeatBearingOf(c.ID); after != bearing {
		t.Fatalf("CAS 失败后承载被改: %+v", after)
	}

	// §4-8：换绑成功 = 承载整体覆写（机器/载体/环境三样都换）。
	if err := s.RebindSeat(c.ID, next, proto.SeatSourceCoordinate, seat, other); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	if after, _, _ := s.SeatBearingOf(c.ID); after != other {
		t.Fatalf("换绑后承载 = %+v，期望 %+v", after, other)
	}

	// §4-10：清座删承载行，且幂等。
	if err := s.ClearSeat(c.ID); err != nil {
		t.Fatalf("清座: %v", err)
	}
	if card, _ := s.GetCard(c.ID); card.DriverSession != "" || card.DriverSource != "" {
		t.Fatalf("清座后席位非空: %+v", card)
	}
	if _, ok, _ := s.SeatBearingOf(c.ID); ok {
		t.Fatal("清座后承载行应不存在")
	}
	if err := s.ClearSeat(c.ID); err != nil {
		t.Fatalf("清座应幂等: %v", err)
	}

	// §4-4 反向：清座后可重新叫机器人（承载缺失不再拦住新席位）。
	if err := s.BindSeat(c.ID, next, proto.SeatSourceCoordinate, other); err != nil {
		t.Fatalf("清座后重新坐下: %v", err)
	}
}

// 契约 §4-7：承载写入失败时席位不得留下——两件事在同一事务里。
// 用"把承载表拆掉"制造真实失败，验证的是事务边界而不是分支。
func TestSeatBearingWriteFailureRollsBackSeat(t *testing.T) {
	s := seedStore(t)
	c, err := s.CreateCard(NewCard{Title: "回滚", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE seat_bearings`); err != nil {
		t.Fatalf("拆承载表: %v", err)
	}
	seat, _ := proto.EncodeSeatIdentity("opencode", "sess-rollback")
	err = s.BindSeat(c.ID, seat, proto.SeatSourceCoordinate,
		SeatBearing{Carrier: "c", Machine: "m"})
	if err == nil {
		t.Fatal("承载写入失败时 BindSeat 必须报错")
	}
	card, err := s.GetCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.DriverSession != "" || card.DriverSource != "" {
		t.Fatalf("承载失败后席位必须回滚为空: %+v", card)
	}
}
