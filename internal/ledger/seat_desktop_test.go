// seat_desktop_test.go —— B434.1 账本层行为锁：桌面端席位记录（seat_desktops）
// 与项目拉起器登记（desktop_launchers）。
//
// 职责：锁契约 docs/superpowers/specs/b434-contract.md §3.1 ValidateDesktopSessionID、
// §3.3、§3.4，与 §4.4 条 49–59/62–63/65、§4.9 条 139/140/142/143 的 Store 一侧。
// 反面断言一律数行数与事件数（count 查询），不只断言 err（拆解稿 §5.1 假绿族）；
// 生命周期测试走真实 RebindSeat / CloseCard / ClearSeat 入口，不手写 SQL 模拟联动。
//
// 边界：CLI 一侧（条 42–48、60–61、141）归 B434.2；PG 方言往返在
// seat_desktop_pg_test.go（LEDGER_TEST_PG_DSN 门控，未设即 skip）。
package ledger

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// b434DesktopSession 是契约条 55 的合法样本 id。
const b434DesktopSession = "local_31eda11a-a6fe-4a5c-a229-fc12d682d1b6"

// TestB434ValidateDesktopSessionID 锁契约条 55–59：前缀 local_ + 余部非空且只含 [A-Za-z0-9-]。
// 拒绝用例同时断言错误不是 ErrDesktopNotImplemented——否则空壳的恒错会让反例假绿。
func TestB434ValidateDesktopSessionID(t *testing.T) {
	cases := []struct {
		name string
		id   string
		ok   bool
	}{
		{"条55 合法 local_uuid", b434DesktopSession, true},
		{"条55 合法 只含字母数字", "local_ABCdef0123", true},
		{"条56 空串", "", false},
		{"条57 只有前缀", "local_", false},
		{"条58 无前缀", "31eda11a-a6fe-4a5c-a229-fc12d682d1b6", false},
		{"条58 前缀大小写不符", "LOCAL_31eda11a", false},
		{"条59 含空格", "local_31eda11a a6fe", false},
		{"条59 含制表符", "local_31eda11a\ta6fe", false},
		{"条59 尾随换行", "local_31eda11a\n", false},
		{"条59 前导空白", " local_31eda11a", false},
		{"条59 含#", "local_31eda11a#a6fe", false},
		{"条59 含/", "local_31eda11a/a6fe", false},
		{"余部含下划线", "local_ab_cd", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := proto.ValidateDesktopSessionID(tc.id)
			if tc.ok && err != nil {
				t.Fatalf("ValidateDesktopSessionID(%q) 应通过，得 %v", tc.id, err)
			}
			if !tc.ok {
				if err == nil {
					t.Fatalf("ValidateDesktopSessionID(%q) 应报错", tc.id)
				}
				if errors.Is(err, proto.ErrDesktopNotImplemented) {
					t.Fatalf("ValidateDesktopSessionID(%q) 返回空壳错误，不是形状判定: %v", tc.id, err)
				}
			}
		})
	}
}

// b434BindCard 建一张卡并以 bind 来源坐下 cli:claude#<cliSession>，返回卡号与席位身份。
func b434BindCard(t *testing.T, s *Store, cliSession string) (string, string) {
	t.Helper()
	c, err := s.CreateCard(NewCard{Title: "桌面端席位", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	seat, err := proto.EncodeSeatIdentity("claude", cliSession)
	if err != nil {
		t.Fatalf("编席位身份: %v", err)
	}
	if err := s.BindSeat(c.ID, seat, proto.SeatSourceBind, SeatBearing{}); err != nil {
		t.Fatalf("bind 坐下: %v", err)
	}
	return c.ID, seat
}

// b434SeatDesktopRows 数 seat_desktops 里该卡的行数。
func b434SeatDesktopRows(t *testing.T, s *Store, card string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(s.q(`SELECT COUNT(*) FROM seat_desktops WHERE card_id = ?`), card).Scan(&n); err != nil {
		t.Fatalf("数 seat_desktops: %v", err)
	}
	return n
}

// b434EventsOfType 返回该卡指定类型的事件（seq 升序）。
func b434EventsOfType(t *testing.T, s *Store, card, typ string) []Event {
	t.Helper()
	events, err := s.EventsFromAsc([]string{card}, 0, 1000)
	if err != nil {
		t.Fatalf("读事件: %v", err)
	}
	var out []Event
	for _, ev := range events {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// TestB434SetSeatDesktopWritesRowAndEvent 锁条 49/50/65 与 §3.3：bind 席位上
// SetSeatDesktop 同事务恰写一行、落恰一条 EvSeatDesktopSet（actor = 席位身份，
// 载荷键集恰为 {identity, desktop_session, machine, via}）；SeatDesktopOf 逐字段
// 读回，BoundAt 取账本时钟（入参被忽略）；同席位再写为覆盖（仍一行、再落一条）。
// 桌面端 bind 席位 driver_source 仍为 bind、seat_bearings 无行（B389 规则 4）。
func TestB434SetSeatDesktopWritesRowAndEvent(t *testing.T) {
	s := seedStore(t)
	// 微秒精度 + 非 UTC 时区：与 PG timestamptz 精度同口径，且逼出时区往返。
	fixed := time.Date(2026, 10, 16, 19, 30, 1, 123456000, time.FixedZone("CST", 8*3600))
	s.now = func() time.Time { return fixed }
	card, seat := b434BindCard(t, s, "cli-sess-1")

	if _, ok, err := s.SeatDesktopOf(card); err != nil || ok {
		t.Fatalf("写入前应 ok=false 且不报错: ok=%v err=%v", ok, err)
	}

	desk := SeatDesktop{Session: b434DesktopSession, Machine: "mbp-01", Via: proto.SeatDesktopViaPage,
		BoundAt: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := s.SetSeatDesktop(card, seat, desk); err != nil {
		t.Fatalf("SetSeatDesktop: %v", err)
	}
	if n := b434SeatDesktopRows(t, s, card); n != 1 {
		t.Fatalf("seat_desktops 行数 = %d，期望 1", n)
	}
	got, ok, err := s.SeatDesktopOf(card)
	if err != nil || !ok {
		t.Fatalf("读桌面端记录: ok=%v err=%v", ok, err)
	}
	if got.Session != desk.Session || got.Machine != desk.Machine || got.Via != desk.Via {
		t.Fatalf("读回 %+v，期望 Session/Machine/Via = %q/%q/%q", got, desk.Session, desk.Machine, desk.Via)
	}
	if !got.BoundAt.Equal(fixed) {
		t.Fatalf("BoundAt = %v，期望账本时钟 %v（入参 BoundAt 应被忽略）", got.BoundAt, fixed)
	}

	events := b434EventsOfType(t, s, card, EvSeatDesktopSet)
	if len(events) != 1 {
		t.Fatalf("seat_desktop_set 事件数 = %d，期望 1", len(events))
	}
	if events[0].Actor != seat {
		t.Fatalf("事件 actor = %q，期望席位身份 %q", events[0].Actor, seat)
	}
	var payload map[string]any
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil {
		t.Fatalf("解载荷: %v", err)
	}
	want := map[string]string{"identity": seat, "desktop_session": desk.Session, "machine": "mbp-01", "via": "page"}
	if len(payload) != len(want) {
		t.Fatalf("载荷键集 = %v，期望恰为 {identity, desktop_session, machine, via}", payload)
	}
	for k, v := range want {
		if payload[k] != v {
			t.Fatalf("载荷 %s = %#v，期望 %q（全载荷 %v）", k, payload[k], v, payload)
		}
	}

	// 席位真源不变：仍是 bind，身份仍是 cli:claude#<CLI 会话 id>（条 64 的 Store 侧）。
	c, err := s.GetCard(card)
	if err != nil {
		t.Fatal(err)
	}
	if c.DriverSource != string(proto.SeatSourceBind) || c.DriverSession != seat {
		t.Fatalf("席位真源被改: source=%q session=%q", c.DriverSource, c.DriverSession)
	}
	// 条 65：桌面端 bind 席位在 seat_bearings 里没有行。
	if _, ok, err := s.SeatBearingOf(card); err != nil || ok {
		t.Fatalf("桌面端 bind 席位不应有承载行: ok=%v err=%v", ok, err)
	}

	// 覆盖：同席位再写（空 Machine = 本机，验证空串往返不变成缺失）。
	desk2 := SeatDesktop{Session: "local_second-0001", Machine: "", Via: proto.SeatDesktopViaManual}
	if err := s.SetSeatDesktop(card, seat, desk2); err != nil {
		t.Fatalf("覆盖写: %v", err)
	}
	if n := b434SeatDesktopRows(t, s, card); n != 1 {
		t.Fatalf("覆盖后 seat_desktops 行数 = %d，期望仍为 1", n)
	}
	got2, ok, err := s.SeatDesktopOf(card)
	if err != nil || !ok || got2.Session != desk2.Session || got2.Machine != "" || got2.Via != desk2.Via {
		t.Fatalf("覆盖后读回 %+v ok=%v err=%v，期望 %+v", got2, ok, err, desk2)
	}
	events = b434EventsOfType(t, s, card, EvSeatDesktopSet)
	if len(events) != 2 {
		t.Fatalf("覆盖后 seat_desktop_set 事件数 = %d，期望 2", len(events))
	}
	var payload2 map[string]any
	if err := json.Unmarshal(events[1].Payload, &payload2); err != nil {
		t.Fatalf("解第二条载荷: %v", err)
	}
	if m, present := payload2["machine"]; !present || m != "" {
		t.Fatalf("空 machine 应以空串出键，得 present=%v value=%#v", present, m)
	}
}

// TestB434SetSeatDesktopRowFailureLeavesNoEvent 锁条 49 的「同事务」：行写失败时
// 事件也不落（事件若在另一个事务里落，这里会读到 1 条）。
func TestB434SetSeatDesktopRowFailureLeavesNoEvent(t *testing.T) {
	s := seedStore(t)
	card, seat := b434BindCard(t, s, "cli-rollback")
	if _, err := s.db.Exec(`DROP TABLE seat_desktops`); err != nil {
		t.Fatalf("拆表: %v", err)
	}
	desk := SeatDesktop{Session: b434DesktopSession, Via: proto.SeatDesktopViaManual}
	if err := s.SetSeatDesktop(card, seat, desk); err == nil {
		t.Fatal("行写失败时 SetSeatDesktop 必须报错")
	}
	if n := len(b434EventsOfType(t, s, card, EvSeatDesktopSet)); n != 0 {
		t.Fatalf("行写失败后 seat_desktop_set 事件数 = %d，期望 0", n)
	}
}

// b434AssertTableDDL 断言 table 在该方言的 ddlStatements 里恰登记一次且含全部列定义。
// 逐语句判定：seat_bearings 也有 bound_at 列，拼接全文做子串判定会假绿。
func b434AssertTableDDL(t *testing.T, pg bool, table string, columns []string) {
	t.Helper()
	var stmts []string
	for _, stmt := range ddlStatements(pg) {
		if strings.HasPrefix(stmt, "CREATE TABLE IF NOT EXISTS "+table+" (") {
			stmts = append(stmts, stmt)
		}
	}
	if len(stmts) != 1 {
		t.Fatalf("方言 pg=%v：表 %s 登记 %d 次，期望恰 1 次", pg, table, len(stmts))
	}
	for _, col := range columns {
		if !strings.Contains(stmts[0], col) {
			t.Fatalf("方言 pg=%v：表 %s 缺列定义 %q\n%s", pg, table, col, stmts[0])
		}
	}
}

// b434TimeType 返回该方言的时间列类型（PG TIMESTAMPTZ / SQLite TEXT，store.go#tval 同口径）。
func b434TimeType(pg bool) string {
	if pg {
		return "TIMESTAMPTZ"
	}
	return "TEXT"
}

// TestB434SeatDesktopsDDLBothDialects 锁 §3.3 DDL：seat_desktops 在两方言各登记一次、
// 列集齐全、bound_at 按方言分型。TestDDLDialectParity 管多重集对等，本测试管列形。
func TestB434SeatDesktopsDDLBothDialects(t *testing.T) {
	for _, pg := range []bool{true, false} {
		b434AssertTableDDL(t, pg, "seat_desktops", []string{"card_id TEXT PRIMARY KEY", "identity TEXT NOT NULL",
			"desktop_session TEXT NOT NULL", "machine TEXT NOT NULL", "via TEXT NOT NULL",
			"bound_at " + b434TimeType(pg) + " NOT NULL"})
	}
}

// TestB434SetSeatDesktopRejectsWithoutSideEffects 锁条 51–54 与写闸：空座、coordinate
// 席位、CAS 不符、非法 id、词表外 via、卡不存在——各自报错，且行数与事件数都不变。
func TestB434SetSeatDesktopRejectsWithoutSideEffects(t *testing.T) {
	s := seedStore(t)
	desk := SeatDesktop{Session: b434DesktopSession, Machine: "mbp-01", Via: proto.SeatDesktopViaPage}
	assertUntouched := func(t *testing.T, card string) {
		t.Helper()
		if n := b434SeatDesktopRows(t, s, card); n != 0 {
			t.Fatalf("拒绝后 seat_desktops 行数 = %d，期望 0", n)
		}
		if n := len(b434EventsOfType(t, s, card, EvSeatDesktopSet)); n != 0 {
			t.Fatalf("拒绝后 seat_desktop_set 事件数 = %d，期望 0", n)
		}
	}

	t.Run("条52 空座", func(t *testing.T) {
		c, err := s.CreateCard(NewCard{Title: "空座", Project: "p", Workflow: "bug", Actor: "t"})
		if err != nil {
			t.Fatal(err)
		}
		// expect="" 与空座的 driver_session 字节相等：只靠 CAS 的实现会在这里漏放。
		for _, expect := range []string{"", "cli:claude#nobody"} {
			if err := s.SetSeatDesktop(c.ID, expect, desk); !errors.Is(err, ErrBadState) {
				t.Fatalf("空座 expect=%q 应 ErrBadState，得 %v", expect, err)
			}
		}
		assertUntouched(t, c.ID)
	})

	t.Run("条51 coordinate 席位", func(t *testing.T) {
		c, err := s.CreateCard(NewCard{Title: "叫机器人", Project: "p", Workflow: "bug", Actor: "t"})
		if err != nil {
			t.Fatal(err)
		}
		seat, _ := proto.EncodeSeatIdentity("opencode", "sess-coord")
		if err := s.BindSeat(c.ID, seat, proto.SeatSourceCoordinate,
			SeatBearing{Carrier: "coord-carrier", Machine: "linux-01"}); err != nil {
			t.Fatalf("叫机器人坐下: %v", err)
		}
		if err := s.SetSeatDesktop(c.ID, seat, desk); !errors.Is(err, ErrBadState) {
			t.Fatalf("coordinate 席位应 ErrBadState，得 %v", err)
		}
		assertUntouched(t, c.ID)
	})

	card, seat := b434BindCard(t, s, "cli-guard")

	t.Run("条53 CAS 不符", func(t *testing.T) {
		if err := s.SetSeatDesktop(card, "cli:claude#someone-else", desk); !errors.Is(err, ErrCASConflict) {
			t.Fatalf("expect 不符应 ErrCASConflict，得 %v", err)
		}
		assertUntouched(t, card)
	})

	t.Run("条54 非法桌面端会话 id", func(t *testing.T) {
		for _, bad := range []string{"", "local_", "31eda11a-a6fe", "local_a b", "local_a#b", "local_a/b"} {
			bd := desk
			bd.Session = bad
			err := s.SetSeatDesktop(card, seat, bd)
			if !errors.Is(err, ErrBadState) || errors.Is(err, proto.ErrDesktopNotImplemented) {
				t.Fatalf("非法 id %q 应 ErrBadState（且非空壳错误），得 %v", bad, err)
			}
		}
		assertUntouched(t, card)
	})

	t.Run("来源词表外", func(t *testing.T) {
		for _, via := range []string{"", "browser", "Page"} {
			bd := desk
			bd.Via = via
			if err := s.SetSeatDesktop(card, seat, bd); !errors.Is(err, ErrBadState) {
				t.Fatalf("via=%q 应 ErrBadState，得 %v", via, err)
			}
		}
		assertUntouched(t, card)
	})

	t.Run("存量态 bind 来源而席位为空", func(t *testing.T) {
		// 生产写面造不出 driver_source=bind 且 driver_session='' 的卡（BindSeat 拒空身份），
		// 但旧库存量数据可能有；此时 expect="" 与卡上空串字节相等，纯 CAS 会漏放——
		// 必须由「席位为空」这道来源闸拦下。
		c, err := s.CreateCard(NewCard{Title: "存量空身份", Project: "p", Workflow: "bug", Actor: "t"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(s.q(`UPDATE cards SET driver_source = 'bind', driver_session = '' WHERE id = ?`), c.ID); err != nil {
			t.Fatalf("造存量态: %v", err)
		}
		if err := s.SetSeatDesktop(c.ID, "", desk); !errors.Is(err, ErrBadState) {
			t.Fatalf("bind 来源空身份 expect=\"\" 应 ErrBadState，得 %v", err)
		}
		assertUntouched(t, c.ID)
	})

	t.Run("卡不存在", func(t *testing.T) {
		if err := s.SetSeatDesktop("P999999", seat, desk); !errors.Is(err, ErrNotFound) {
			t.Fatalf("卡不存在应 ErrNotFound，得 %v", err)
		}
	})

	// 正控制：同一张卡给对的 expect 能成功——证明上面的拒绝不是夹具坏了。
	if err := s.SetSeatDesktop(card, seat, desk); err != nil {
		t.Fatalf("正控制写入失败: %v", err)
	}
	if n := b434SeatDesktopRows(t, s, card); n != 1 {
		t.Fatalf("正控制后 seat_desktops 行数 = %d，期望 1", n)
	}
}

// TestB434RebindSeatDropsSeatDesktop 锁条 62：RebindSeat 成功后 SeatDesktopOf
// ok=false；CAS 失败的换绑不动行；新席位可重新写入；换绑到 coordinate 同样删行。
func TestB434RebindSeatDropsSeatDesktop(t *testing.T) {
	s := seedStore(t)
	card, seat := b434BindCard(t, s, "cli-old")
	desk := SeatDesktop{Session: b434DesktopSession, Machine: "mbp-01", Via: proto.SeatDesktopViaPage}
	if err := s.SetSeatDesktop(card, seat, desk); err != nil {
		t.Fatalf("写桌面端记录: %v", err)
	}

	next, _ := proto.EncodeSeatIdentity("claude", "cli-new")
	if err := s.RebindSeat(card, next, proto.SeatSourceBind, "cli:claude#wrong", SeatBearing{}); !errors.Is(err, ErrCASConflict) {
		t.Fatalf("CAS 不符应 ErrCASConflict，得 %v", err)
	}
	if _, ok, _ := s.SeatDesktopOf(card); !ok {
		t.Fatal("CAS 失败的换绑不应删桌面端记录")
	}

	if err := s.RebindSeat(card, next, proto.SeatSourceBind, seat, SeatBearing{}); err != nil {
		t.Fatalf("换绑: %v", err)
	}
	if _, ok, err := s.SeatDesktopOf(card); err != nil || ok {
		t.Fatalf("换绑后应 ok=false 且不报错: ok=%v err=%v", ok, err)
	}
	if n := b434SeatDesktopRows(t, s, card); n != 0 {
		t.Fatalf("换绑后 seat_desktops 行数 = %d，期望 0", n)
	}

	// 两步提交（D2）：新席位随后可重新登记桌面端记录。
	desk2 := SeatDesktop{Session: "local_new-0001", Via: proto.SeatDesktopViaLauncher}
	if err := s.SetSeatDesktop(card, next, desk2); err != nil {
		t.Fatalf("新席位写桌面端记录: %v", err)
	}

	coord, _ := proto.EncodeSeatIdentity("opencode", "sess-coord")
	if err := s.RebindSeat(card, coord, proto.SeatSourceCoordinate, next,
		SeatBearing{Carrier: "coord-carrier", Machine: "linux-01"}); err != nil {
		t.Fatalf("换绑到 coordinate: %v", err)
	}
	if _, ok, _ := s.SeatDesktopOf(card); ok {
		t.Fatal("换绑到 coordinate 后桌面端记录必须不存在")
	}
}

// TestB434RebindSeatRollsBackWhenSeatDesktopDeleteFails 锁「删行与席位写同事务」
// （拆解稿 §5.1 生命周期族的能变红判据）：删行失败时换绑整体回滚，席位不变、
// 不落 EvDriverTakeover。把删行挪出事务或吞掉删行错误，本测试都会红。
func TestB434RebindSeatRollsBackWhenSeatDesktopDeleteFails(t *testing.T) {
	s := seedStore(t)
	card, seat := b434BindCard(t, s, "cli-old")
	if _, err := s.db.Exec(`DROP TABLE seat_desktops`); err != nil {
		t.Fatalf("拆表: %v", err)
	}
	next, _ := proto.EncodeSeatIdentity("claude", "cli-new")
	if err := s.RebindSeat(card, next, proto.SeatSourceBind, seat, SeatBearing{}); err == nil {
		t.Fatal("删桌面端记录失败时 RebindSeat 必须报错")
	}
	c, err := s.GetCard(card)
	if err != nil {
		t.Fatal(err)
	}
	if c.DriverSession != seat {
		t.Fatalf("回滚后席位应仍为 %q，得 %q", seat, c.DriverSession)
	}
	if n := len(b434EventsOfType(t, s, card, EvDriverTakeover)); n != 0 {
		t.Fatalf("回滚后 driver_takeover 事件数 = %d，期望 0", n)
	}
}

// TestB434ClearSeatPathsDropSeatDesktop 锁条 63：clearSeatTx 的两条入口（终态关单
// CloseCard、人工清座 ClearSeat）之后 SeatDesktopOf 都是 ok=false；清座幂等。
func TestB434ClearSeatPathsDropSeatDesktop(t *testing.T) {
	desk := SeatDesktop{Session: b434DesktopSession, Via: proto.SeatDesktopViaManual}

	t.Run("终态关单", func(t *testing.T) {
		s := seedStore(t)
		card, seat := b434BindCard(t, s, "cli-close")
		if err := s.SetSeatDesktop(card, seat, desk); err != nil {
			t.Fatalf("写桌面端记录: %v", err)
		}
		if err := s.CloseCard(card, CloseShelved, "t"); err != nil {
			t.Fatalf("终止卡: %v", err)
		}
		if _, ok, err := s.SeatDesktopOf(card); err != nil || ok {
			t.Fatalf("终态后应 ok=false 且不报错: ok=%v err=%v", ok, err)
		}
		if n := b434SeatDesktopRows(t, s, card); n != 0 {
			t.Fatalf("终态后 seat_desktops 行数 = %d，期望 0", n)
		}
	})

	t.Run("人工清座", func(t *testing.T) {
		s := seedStore(t)
		card, seat := b434BindCard(t, s, "cli-clear")
		if err := s.SetSeatDesktop(card, seat, desk); err != nil {
			t.Fatalf("写桌面端记录: %v", err)
		}
		if err := s.ClearSeat(card); err != nil {
			t.Fatalf("清座: %v", err)
		}
		if _, ok, err := s.SeatDesktopOf(card); err != nil || ok {
			t.Fatalf("清座后应 ok=false 且不报错: ok=%v err=%v", ok, err)
		}
		if err := s.ClearSeat(card); err != nil {
			t.Fatalf("清座应幂等: %v", err)
		}
	})
}

// TestB434CloseCardRollsBackWhenSeatDesktopDeleteFails 锁终态关单路径的同事务：
// 删桌面端记录失败时整次关单回滚，卡状态与席位都不变。
func TestB434CloseCardRollsBackWhenSeatDesktopDeleteFails(t *testing.T) {
	s := seedStore(t)
	card, seat := b434BindCard(t, s, "cli-close")
	before, err := s.GetCard(card)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DROP TABLE seat_desktops`); err != nil {
		t.Fatalf("拆表: %v", err)
	}
	if err := s.CloseCard(card, CloseShelved, "t"); err == nil {
		t.Fatal("删桌面端记录失败时 CloseCard 必须报错")
	}
	after, err := s.GetCard(card)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != before.Status || after.DriverSession != seat {
		t.Fatalf("回滚后应保持 status=%q seat=%q，得 status=%q seat=%q",
			before.Status, seat, after.Status, after.DriverSession)
	}
}

// TestB434BindSeatLeavesSeatDesktopUntouched 锁 §3.3「BindSeat 不写也不删」与条 44 的
// Store 侧：BindSeat 不落 seat_desktop_set、不建行；空座残留行（只能手写 SQL 构造，
// 生产写面对空座拒绝）也不被 BindSeat 删除——两步提交（D2）的第二步只属于 SetSeatDesktop。
func TestB434BindSeatLeavesSeatDesktopUntouched(t *testing.T) {
	s := seedStore(t)
	card, _ := b434BindCard(t, s, "cli-plain")
	if n := b434SeatDesktopRows(t, s, card); n != 0 {
		t.Fatalf("BindSeat 不应写 seat_desktops，得 %d 行", n)
	}
	if n := len(b434EventsOfType(t, s, card, EvSeatDesktopSet)); n != 0 {
		t.Fatalf("BindSeat 不应落 seat_desktop_set，得 %d 条", n)
	}

	ghost, err := s.CreateCard(NewCard{Title: "残留", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(s.q(`INSERT INTO seat_desktops
		(card_id, identity, desktop_session, machine, via, bound_at) VALUES (?, ?, ?, ?, ?, ?)`),
		ghost.ID, "cli:claude#ghost", "local_ghost", "", proto.SeatDesktopViaManual, s.tval(s.timeNow())); err != nil {
		t.Fatalf("手插残留行: %v", err)
	}
	seat, _ := proto.EncodeSeatIdentity("claude", "cli-fresh")
	if err := s.BindSeat(ghost.ID, seat, proto.SeatSourceBind, SeatBearing{}); err != nil {
		t.Fatalf("bind 坐下: %v", err)
	}
	if n := b434SeatDesktopRows(t, s, ghost.ID); n != 1 {
		t.Fatalf("BindSeat 不应删 seat_desktops 行，得 %d 行", n)
	}
}

// b434LauncherRows 数 desktop_launchers 里该项目的行数。
func b434LauncherRows(t *testing.T, s *Store, project string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(s.q(`SELECT COUNT(*) FROM desktop_launchers WHERE project = ?`), project).Scan(&n); err != nil {
		t.Fatalf("数 desktop_launchers: %v", err)
	}
	return n
}

// b434TotalEvents 数整张 card_events 表（拉起器登记「不落任何卡事件」的反面断言）。
func b434TotalEvents(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM card_events`).Scan(&n); err != nil {
		t.Fatalf("数 card_events: %v", err)
	}
	return n
}

// TestB434DesktopLauncherRegistry 锁条 139/140/142/143 的 Store 一侧与 §3.4：
// 登记后读回逐字段相等（RegisteredAt 取账本时钟）；同项目再登记覆盖（每项目至多
// 一条）；ListDesktopLaunchers 按 project 字节序升序；注销幂等；全程不落任何卡事件。
func TestB434DesktopLauncherRegistry(t *testing.T) {
	s := seedStore(t)
	fixed := time.Date(2026, 10, 16, 20, 0, 0, 654321000, time.FixedZone("CST", 8*3600))
	s.now = func() time.Time { return fixed }
	if _, err := s.CreateCard(NewCard{Title: "事件基线", Project: "proj-b", Workflow: "bug", Actor: "t"}); err != nil {
		t.Fatal(err)
	}
	baseline := b434TotalEvents(t, s)

	if _, ok, err := s.DesktopLauncherOf("proj-b"); err != nil || ok {
		t.Fatalf("未登记应 ok=false 且不报错: ok=%v err=%v", ok, err)
	}
	if list, err := s.ListDesktopLaunchers(); err != nil || len(list) != 0 {
		t.Fatalf("空表列表应为空: %v err=%v", list, err)
	}

	lb := DesktopLauncher{Project: "proj-b", DesktopSession: b434DesktopSession, CLISession: "cli-launcher-b",
		Machine: "mbp-01", Actor: "cli:claude#cli-launcher-b", RegisteredAt: time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := s.RegisterDesktopLauncher(lb); err != nil {
		t.Fatalf("登记: %v", err)
	}
	got, ok, err := s.DesktopLauncherOf("proj-b")
	if err != nil || !ok {
		t.Fatalf("读登记: ok=%v err=%v", ok, err)
	}
	if got.Project != lb.Project || got.DesktopSession != lb.DesktopSession || got.CLISession != lb.CLISession ||
		got.Machine != lb.Machine || got.Actor != lb.Actor || !got.RegisteredAt.Equal(fixed) {
		t.Fatalf("读回 %+v，期望 %+v 且 RegisteredAt=%v", got, lb, fixed)
	}

	// 条 140：同项目再登记覆盖前一条。
	later := fixed.Add(time.Minute)
	s.now = func() time.Time { return later }
	lb2 := DesktopLauncher{Project: "proj-b", DesktopSession: "local_second-0002", CLISession: "cli-launcher-b2",
		Machine: "", Actor: "cli:claude#cli-launcher-b2"}
	if err := s.RegisterDesktopLauncher(lb2); err != nil {
		t.Fatalf("覆盖登记: %v", err)
	}
	if n := b434LauncherRows(t, s, "proj-b"); n != 1 {
		t.Fatalf("覆盖后 proj-b 行数 = %d，期望 1", n)
	}
	got2, ok, err := s.DesktopLauncherOf("proj-b")
	if err != nil || !ok || got2.DesktopSession != lb2.DesktopSession || got2.CLISession != lb2.CLISession ||
		got2.Machine != "" || got2.Actor != lb2.Actor || !got2.RegisteredAt.Equal(later) {
		t.Fatalf("覆盖后读回 %+v ok=%v err=%v，期望 %+v 且 RegisteredAt=%v", got2, ok, err, lb2, later)
	}

	// 字节序升序（"Proj-Z" < "proj-a" < "proj-b"）。
	for _, p := range []string{"proj-a", "Proj-Z"} {
		if err := s.RegisterDesktopLauncher(DesktopLauncher{Project: p, DesktopSession: b434DesktopSession,
			CLISession: "cli-" + strings.ToLower(p), Machine: "mbp-01", Actor: "t"}); err != nil {
			t.Fatalf("登记 %s: %v", p, err)
		}
	}
	list, err := s.ListDesktopLaunchers()
	if err != nil {
		t.Fatalf("列登记: %v", err)
	}
	var order []string
	for _, l := range list {
		order = append(order, l.Project)
	}
	if strings.Join(order, ",") != "Proj-Z,proj-a,proj-b" {
		t.Fatalf("列表顺序 = %v，期望按 project 字节序升序 [Proj-Z proj-a proj-b]", order)
	}

	// 条 142：注销幂等（含从未登记的项目）。
	for i := 0; i < 2; i++ {
		if err := s.UnregisterDesktopLauncher("proj-b"); err != nil {
			t.Fatalf("第 %d 次注销: %v", i+1, err)
		}
	}
	if err := s.UnregisterDesktopLauncher("never-registered"); err != nil {
		t.Fatalf("注销从未登记的项目应成功: %v", err)
	}
	if _, ok, err := s.DesktopLauncherOf("proj-b"); err != nil || ok {
		t.Fatalf("注销后应 ok=false 且不报错: ok=%v err=%v", ok, err)
	}

	// 条 143：登记、覆盖、注销全程不落任何卡事件。
	if n := b434TotalEvents(t, s); n != baseline {
		t.Fatalf("card_events 总数 %d → %d，拉起器登记不应落任何卡事件", baseline, n)
	}
}

// TestB434DesktopLaunchersDDLBothDialects 锁 §3.4 DDL：desktop_launchers 在两方言各登记
// 一次、列集齐全、registered_at 按方言分型。
func TestB434DesktopLaunchersDDLBothDialects(t *testing.T) {
	for _, pg := range []bool{true, false} {
		b434AssertTableDDL(t, pg, "desktop_launchers", []string{"project TEXT PRIMARY KEY",
			"desktop_session TEXT NOT NULL", "cli_session TEXT NOT NULL", "machine TEXT NOT NULL",
			"actor TEXT NOT NULL", "registered_at " + b434TimeType(pg) + " NOT NULL"})
	}
}

// TestB434DesktopTablesReopenIdempotent 锁旧库升级：新表随 ensureSchema 建出，
// 二次 Open 幂等（CREATE TABLE IF NOT EXISTS）。
func TestB434DesktopTablesReopenIdempotent(t *testing.T) {
	s := newTestStore(t)
	for _, tbl := range []string{"seat_desktops", "desktop_launchers"} {
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

// TestB434RegisterDesktopLauncherRejectsInvalid 锁拉起器写闸：项目名、桌面端会话 id、
// CLI 会话 id（信箱文件名的一段）、actor 不合法时报 ErrBadState 且不写行。
func TestB434RegisterDesktopLauncherRejectsInvalid(t *testing.T) {
	s := seedStore(t)
	valid := DesktopLauncher{Project: "proj-x", DesktopSession: b434DesktopSession, CLISession: "cli-ok",
		Machine: "mbp-01", Actor: "cli:claude#cli-ok"}
	cases := []struct {
		name   string
		mutate func(l *DesktopLauncher)
	}{
		{"项目名为空", func(l *DesktopLauncher) { l.Project = "" }},
		{"项目名带首尾空白", func(l *DesktopLauncher) { l.Project = " " + l.Project }},
		{"桌面端会话 id 只有前缀", func(l *DesktopLauncher) { l.DesktopSession = "local_" }},
		{"桌面端会话 id 无前缀", func(l *DesktopLauncher) { l.DesktopSession = "31eda11a" }},
		{"CLI 会话 id 为空", func(l *DesktopLauncher) { l.CLISession = "" }},
		{"CLI 会话 id 含路径分隔符", func(l *DesktopLauncher) { l.CLISession = "../etc" }},
		{"CLI 会话 id 含反斜杠", func(l *DesktopLauncher) { l.CLISession = `a\b` }},
		{"CLI 会话 id 含#", func(l *DesktopLauncher) { l.CLISession = "a#b" }},
		{"CLI 会话 id 含空白", func(l *DesktopLauncher) { l.CLISession = "a b" }},
		{"actor 为空", func(l *DesktopLauncher) { l.Actor = " " }},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// 每个子例独立项目名：共用项目键时，前一子例被误放写进的行会让后面子例的
			// 「行数 = 0」断言连带变红，变异读数失真（review m1）。
			l := valid
			l.Project = fmt.Sprintf("proj-x-%02d", i)
			tc.mutate(&l)
			err := s.RegisterDesktopLauncher(l)
			if !errors.Is(err, ErrBadState) || errors.Is(err, proto.ErrDesktopNotImplemented) {
				t.Fatalf("应 ErrBadState（且非空壳错误），得 %v", err)
			}
			for _, p := range []string{l.Project, strings.TrimSpace(l.Project)} {
				if n := b434LauncherRows(t, s, p); n != 0 {
					t.Fatalf("拒绝后 %q 行数 = %d，期望 0", p, n)
				}
			}
		})
	}
	// 正控制：合法登记能成功。
	if err := s.RegisterDesktopLauncher(valid); err != nil {
		t.Fatalf("正控制登记失败: %v", err)
	}
}
