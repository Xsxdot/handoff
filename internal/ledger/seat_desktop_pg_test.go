// seat_desktop_pg_test.go —— B434.1 PG 方言往返：seat_desktops / desktop_launchers
// 在真 PG 上的时间列（TIMESTAMPTZ）与空串列往返、生命周期联动、列表字节序。
//
// 职责：补 SQLite 测不到的方言差异（$N 占位、timestamptz 精度与时区、collation）。
// 边界：默认 skip，设 LEDGER_TEST_PG_DSN 后启用（newPGStore，store_pg_test.go:22）；
// 清理段只删本测试项目名下的数据，DSN 仍须指向专用测试库。
package ledger

import (
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// b434PGProject 是本测试造数据的项目名，同时是清理段的作用域边界。
const b434PGProject = "b434-ledger-pg-临时数据"

func TestB434SeatDesktopPostgresRoundTrip(t *testing.T) {
	s := newPGStore(t)
	if err := seedTestWorkflows(t, s); err != nil {
		t.Fatalf("写测试工作流: %v", err)
	}
	cleanup := func() {
		for _, stmt := range []string{
			`DELETE FROM seat_desktops WHERE card_id IN (SELECT id FROM cards WHERE project = $1)`,
			`DELETE FROM card_events WHERE card_id IN (SELECT id FROM cards WHERE project = $1)`,
			`DELETE FROM cards WHERE project = $1`,
			`DELETE FROM desktop_launchers WHERE project LIKE $1 || '%'`,
		} {
			if _, err := s.db.Exec(stmt, b434PGProject); err != nil {
				t.Logf("清理 B434 PG 数据: %v", err)
			}
		}
	}
	cleanup()
	t.Cleanup(cleanup)

	// timestamptz 是微秒精度：纳秒位必须为 0，否则 Equal 因截断假红。
	fixed := time.Date(2026, 10, 16, 19, 30, 1, 123456000, time.FixedZone("CST", 8*3600))
	s.now = func() time.Time { return fixed }

	c, err := s.CreateCard(NewCard{Title: "pg 桌面端席位", Project: b434PGProject, Workflow: "bug", Actor: "pgtest"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	seat, _ := proto.EncodeSeatIdentity("claude", "cli-pg")
	if err := s.BindSeat(c.ID, seat, proto.SeatSourceBind, SeatBearing{}); err != nil {
		t.Fatalf("bind 坐下: %v", err)
	}
	if err := s.SetSeatDesktop(c.ID, seat, SeatDesktop{Session: b434DesktopSession, Machine: "", Via: proto.SeatDesktopViaPage}); err != nil {
		t.Fatalf("SetSeatDesktop: %v", err)
	}
	got, ok, err := s.SeatDesktopOf(c.ID)
	if err != nil || !ok {
		t.Fatalf("读回: ok=%v err=%v", ok, err)
	}
	if got.Session != b434DesktopSession || got.Machine != "" || got.Via != proto.SeatDesktopViaPage || !got.BoundAt.Equal(fixed) {
		t.Fatalf("PG 往返 %+v，期望 Session=%q Machine=\"\" Via=page BoundAt=%v", got, b434DesktopSession, fixed)
	}

	next, _ := proto.EncodeSeatIdentity("claude", "cli-pg-next")
	if err := s.RebindSeat(c.ID, next, proto.SeatSourceBind, seat, SeatBearing{}); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	if _, ok, _ := s.SeatDesktopOf(c.ID); ok {
		t.Fatal("PG 上换绑后桌面端记录必须不存在")
	}

	// 拉起器：时间往返 + 字节序（PG 非 C collation 下 "b…-a" 会排在 "B…-Z" 前；
	// 契约要求按 project 升序，本实现按字节序，两方言一致）。
	upper, lower := b434PGProject+"-Z", b434PGProject+"-a"
	for _, p := range []string{lower, upper} {
		if err := s.RegisterDesktopLauncher(DesktopLauncher{Project: p, DesktopSession: b434DesktopSession,
			CLISession: "cli-pg-launcher", Machine: "", Actor: "pgtest"}); err != nil {
			t.Fatalf("登记 %s: %v", p, err)
		}
	}
	l, ok, err := s.DesktopLauncherOf(lower)
	if err != nil || !ok || l.Machine != "" || !l.RegisteredAt.Equal(fixed) {
		t.Fatalf("PG 拉起器往返 %+v ok=%v err=%v，期望 Machine=\"\" RegisteredAt=%v", l, ok, err, fixed)
	}
	list, err := s.ListDesktopLaunchers()
	if err != nil {
		t.Fatalf("列登记: %v", err)
	}
	var mine []string
	for _, item := range list {
		if item.Project == upper || item.Project == lower {
			mine = append(mine, item.Project)
		}
	}
	if len(mine) != 2 || mine[0] != upper || mine[1] != lower {
		t.Fatalf("PG 列表本测试两行顺序 = %v，期望字节序 [%s %s]", mine, upper, lower)
	}
}
