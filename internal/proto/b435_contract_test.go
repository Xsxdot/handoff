// b435_contract_test.go —— B435 契约（docs/superpowers/specs/b435-contract.md）的可执行冻结：
// 席位模式字面量与投影键（③）、收件地址游标键与人类作者判据（②）、驻场者来信正文与
// mod 侧对照（④）。改这里的期望值 = 改冻结契约，先回 contract 节点。
package proto

import (
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

func jsonKeys(t *testing.T, v any) []string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("编码 %T: %v", v, err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解码 %T: %v", v, err)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func hasKey(keys []string, k string) bool {
	for _, x := range keys {
		if x == k {
			return true
		}
	}
	return false
}

// 条 C-301/C-302：席位模式字面量逐值冻结。
func TestB435SeatModeLiterals(t *testing.T) {
	if SeatModePush != "push" || SeatModePull != "pull" {
		t.Fatalf("SeatMode 字面量漂移: push=%q pull=%q", SeatModePush, SeatModePull)
	}
	for _, ok := range []string{"push", "pull"} {
		if got, err := ParseSeatMode(ok); err != nil || string(got) != ok {
			t.Fatalf("ParseSeatMode(%q) = %q, %v", ok, got, err)
		}
	}
	for _, bad := range []string{"", "Push", " push", "push ", "launch", "bind", "coordinate"} {
		if _, err := ParseSeatMode(bad); err == nil {
			t.Fatalf("ParseSeatMode(%q) 应拒绝", bad)
		}
	}
}

// 条 C-305/C-306：Card/CardView 的 driver_mode 键——有值出键、空座不出键；不占用 mode 键。
func TestB435CardDriverModeWireKey(t *testing.T) {
	for _, v := range []any{
		Card{DriverSession: "cli:opencode#s1", DriverSource: "coordinate", DriverMode: string(SeatModePush)},
		CardView{DriverSession: "cli:codex#t1", DriverSource: "bind", DriverMode: string(SeatModePull)},
	} {
		keys := jsonKeys(t, v)
		if !hasKey(keys, "driver_mode") {
			t.Fatalf("%T 有席位时缺 driver_mode 键: %v", v, keys)
		}
		if hasKey(keys, "mode") {
			t.Fatalf("%T 不得出现 mode 键（与 CoordinatorRebindReq.Mode 同名异义）: %v", v, keys)
		}
	}
	for _, v := range []any{Card{}, CardView{}} {
		if hasKey(jsonKeys(t, v), "driver_mode") {
			t.Fatalf("%T 空座不应出 driver_mode 键", v)
		}
	}
	raw, _ := json.Marshal(Card{DriverMode: string(SeatModePull)})
	if !strings.Contains(string(raw), `"driver_mode":"pull"`) {
		t.Fatalf("driver_mode 值应逐字为 pull: %s", raw)
	}
}

// 条 C-307：会话详情卡行的 seat_mode 键。
func TestB435SessionCardSeatModeWireKey(t *testing.T) {
	raw, err := json.Marshal(SessionCard{CardID: "B1", Title: "t", Status: "s", Seat: "cli:codex#t1", SeatMode: string(SeatModePull)})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{"card_id":"B1","title":"t","status":"s","seat":"cli:codex#t1","seat_mode":"pull"}`; got != want {
		t.Fatalf("SessionCard wire = %s, want %s", got, want)
	}
	if hasKey(jsonKeys(t, SessionCard{CardID: "B1"}), "seat_mode") {
		t.Fatalf("空座 SessionCard 不应出 seat_mode 键")
	}
}

// 条 C-208/C-406：驻场者成员种类与登记 DTO 键集。
func TestB435SessionResidentWire(t *testing.T) {
	if SessionMemberResident != "resident" {
		t.Fatalf("SessionMemberResident 字面量漂移: %q", SessionMemberResident)
	}
	full := SessionResident{
		Session: "session:3", Identity: "agent:organizer", DesktopSession: "local_x",
		CLISession: "c1", Machine: "mac-pro", SatAt: time.Unix(0, 0).UTC(),
	}
	if got, want := jsonKeys(t, full), []string{"cli_session", "desktop_session", "identity", "machine", "sat_at", "session"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("SessionResident 键集 = %v, want %v", got, want)
	}
	if got, want := jsonKeys(t, SessionResident{Session: "session:3", Identity: "user:sy"}), []string{"identity", "sat_at", "session"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("无桌面端地址的 SessionResident 键集 = %v, want %v", got, want)
	}
}

// 条 C-206/C-207：地址种类字面量与持久游标键编码金样本。
func TestB435DeliveryAddressCursorKey(t *testing.T) {
	if DeliverySeat != "seat" || DeliveryResident != "resident" {
		t.Fatalf("地址种类字面量漂移: %q %q", DeliverySeat, DeliveryResident)
	}
	cases := []struct {
		addr DeliveryAddress
		want string
	}{
		{DeliveryAddress{Kind: DeliverySeat, Session: "session:3", Card: "B1", Identity: "cli:codex#t1"}, "cli:codex#t1"},
		{DeliveryAddress{Kind: DeliveryResident, Session: "session:3", Identity: "user:sy"}, "resident:session:3/user:sy"},
		{DeliveryAddress{Kind: DeliveryResident, Session: "session:3", Identity: "agent:a/b"}, "resident:session:3/agent:a/b"},
		{DeliveryAddress{Kind: DeliverySeat, Session: "session:3", Identity: "cli:codex#t1"}, ""},
		{DeliveryAddress{Kind: DeliverySeat, Session: "session:3", Card: "B1"}, ""},
		{DeliveryAddress{Kind: DeliveryResident, Identity: "user:sy"}, ""},
		{DeliveryAddress{Kind: "external", Session: "session:3", Identity: "user:sy"}, ""},
	}
	for _, c := range cases {
		if got := DeliveryCursorKey(c.addr); got != c.want {
			t.Fatalf("DeliveryCursorKey(%+v) = %q, want %q", c.addr, got, c.want)
		}
	}
}

// 条 C-209/C-210：无收件人原因词表逐值冻结。
func TestB435NoRecipientVocabulary(t *testing.T) {
	want := map[NoRecipientReason]string{
		NoRecipientStructural:          "structural",
		NoRecipientNotSessionRoom:      "not_session_room",
		NoRecipientNonHumanUnaddressed: "non_human_unaddressed",
		NoRecipientAddressedUnresolved: "addressed_unresolved",
		NoRecipientRootNoSeat:          "root_no_seat",
		NoRecipientNeedsMention:        "needs_mention",
	}
	for got, lit := range want {
		if string(got) != lit {
			t.Fatalf("NoRecipientReason 字面量漂移: %q != %q", got, lit)
		}
	}
	if SystemNotifierIdentity != "agent:handoff" {
		t.Fatalf("系统发信身份漂移: %q", SystemNotifierIdentity)
	}
}

// 条 C-201：人类作者判据——user:<name> ∪ CLI 人形 cli:<user>@<host>（spec r3 R3）。
func TestB435IsHumanAuthor(t *testing.T) {
	for _, actor := range []string{"user:sy", "user:a-b_c", "cli:sy@mac-pro", "cli:unknown@linux-01.local"} {
		if !IsHumanAuthor(actor) {
			t.Fatalf("%q 应为人类作者", actor)
		}
	}
	for _, actor := range []string{
		"", "agent:main", SystemNotifierIdentity, "cli:codex#t1", "cli:sy@mac#t1",
		"cli:@host", "cli:sy@", "cli:sy", "cli:a@b@c", "cli:s y@h",
		"web:127.0.0.1", "user:", "user: sy", "sy", "agentd", "node:implement",
	} {
		if IsHumanAuthor(actor) {
			t.Fatalf("%q 不应为人类作者", actor)
		}
	}
}

// 条 C-401/C-402：驻场者来信正文金样本（逐字节）。
func TestB435ResidentLetterGolden(t *testing.T) {
	got := RenderResidentLetter(ResidentLetter{Session: "session:12", Count: 3, MinSeq: 101, MaxSeq: 108})
	want, err := os.ReadFile("testdata/b435_resident_letter.golden")
	if err != nil {
		t.Fatalf("读金样本: %v", err)
	}
	if got != string(want) {
		t.Fatalf("来信正文漂移:\n got %q\nwant %q", got, want)
	}
	lines := strings.Split(strings.TrimSuffix(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("来信应恰两行: %q", got)
	}
	if !strings.HasPrefix(lines[0], DesktopWakeMarker+" ") {
		t.Fatalf("首行须以 %q 开头: %q", DesktopWakeMarker+" ", lines[0])
	}
	if lines[1] != "取信："+ResidentLetterTakeCmd+"session:12" {
		t.Fatalf("第二行取信命令漂移: %q", lines[1])
	}
}

// 条 C-405：mod 侧对照（CDC：读 mod 源码回放 Go 侧字面量）——mod 只认首行标记与
// `<CLI 会话 id>.<19 位数字>.md` 文件名；本卡不改 mod，这两处字面量必须仍在。
func TestB435ModStillMatchesLetterWire(t *testing.T) {
	raw, err := os.ReadFile("../../plugins/claude-desktop-wake/hooks/register.ts")
	if err != nil {
		t.Fatalf("读 mod 源码: %v", err)
	}
	src := string(raw)
	for _, lit := range []string{
		"export const WAKE_MARKER = '" + DesktopWakeMarker + "'",
		"export const WAKE_DIR_REL = '" + DesktopWakeDirRel + "'",
		"const prefix = `${sessionId}.`",
		`/^[0-9]{19}\.md$/`,
		"asUser: true",
	} {
		if !strings.Contains(src, lit) {
			t.Fatalf("mod 源码缺字面量 %q——来信 wire 与 mod 取信规则失配", lit)
		}
	}
}
