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
