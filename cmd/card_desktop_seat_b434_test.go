// card_desktop_seat_b434_test.go —— B434.2 命令级缝测试：card bind --desktop-session 与
// card seat desktop set（契约 §3.10、§4.4 条 42–48/60–61/64–65、拍板 D2）。
//
// 职责：走 root command + 真实 SQLite 账本，锁「坐下 + 记桌面端 id」两步提交的可观察
// 结果；半态（第二步失败）用 raw sqlite 触发器从账本侧真实注入，不造代码缝。
// 边界：ledger 写闸自身（CAS、via 词表、id 形状）在 internal/ledger/seat_desktop_test.go 锁；
// 本文件只锁 CLI 一侧的执法与投影。必须在 env -u CLAUDE_CODE_SESSION_ID 下跑，
// 每支测试开头 clearSeatSourceEnv。本文件的 b434* helper 供同包其它 B434 测试复用。
package cmd

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// b434Ledger 打开 dir 下的测试账本（调用方负责 Close）。
func b434Ledger(t *testing.T, dir string) *ledger.Store {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("打开测试账本: %v", err)
	}
	return st
}

// b434SeatState 读卡（席位两列）与桌面端记录。
func b434SeatState(t *testing.T, dir, id string) (ledger.Card, ledger.SeatDesktop, bool) {
	t.Helper()
	st := b434Ledger(t, dir)
	defer st.Close()
	card, err := st.GetCard(id)
	if err != nil {
		t.Fatalf("读卡 %s: %v", id, err)
	}
	desk, ok, err := st.SeatDesktopOf(id)
	if err != nil {
		t.Fatalf("读桌面端记录 %s: %v", id, err)
	}
	return card, desk, ok
}

// b434Events 返回该卡指定类型的事件（seq 升序）。
func b434Events(t *testing.T, dir, id, typ string) []ledger.Event {
	t.Helper()
	st := b434Ledger(t, dir)
	defer st.Close()
	evs, err := st.EventsFromAsc([]string{id}, 0, 1000)
	if err != nil {
		t.Fatalf("读事件 %s: %v", id, err)
	}
	var out []ledger.Event
	for _, ev := range evs {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// b434Payload 把事件载荷解成字符串 map（seat_desktop_set 载荷四键均为字符串）。
func b434Payload(t *testing.T, ev ledger.Event) map[string]string {
	t.Helper()
	var m map[string]string
	if err := json.Unmarshal(ev.Payload, &m); err != nil {
		t.Fatalf("解析事件 %d 载荷 %s: %v", ev.Seq, ev.Payload, err)
	}
	return m
}

// b434MaxSeq 读账本全局最大 seq（「零新事件」反面断言用）。
func b434MaxSeq(t *testing.T, dir string) int64 {
	t.Helper()
	st := b434Ledger(t, dir)
	defer st.Close()
	n, err := st.MaxSeq()
	if err != nil {
		t.Fatalf("读最大 seq: %v", err)
	}
	return n
}

// b434Hostname 是 --machine 的 CLI 缺省值（契约条 46）。
func b434Hostname(t *testing.T) string {
	t.Helper()
	h, err := os.Hostname()
	if err != nil {
		t.Fatalf("取 hostname: %v", err)
	}
	return h
}

// 条 42–44：不带 --desktop-session 的 bind 与基线 e020380a 逐字相同——stdout、
// 席位两列、恰一条 driver_seat_bound、零条 seat_desktop_set、无桌面端记录。
func TestB434CardBindWithoutDesktopUnchanged(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-plain")
	dir := t.TempDir()
	id := mustAddCard(t, dir, "无桌面端坐下卡")
	out, errOut, err := runLedgerCLI(t, dir, "card", "bind", id)
	if err != nil {
		t.Fatalf("bind: %v stderr=%s", err, errOut)
	}
	if out != "{\"ok\":true}\n" {
		t.Fatalf("stdout = %q，基线为 %q", out, "{\"ok\":true}\n")
	}
	card, _, has := b434SeatState(t, dir, id)
	if card.DriverSession != "cli:claude#claude-plain" || card.DriverSource != "bind" {
		t.Fatalf("席位两列 = %q/%q", card.DriverSession, card.DriverSource)
	}
	if has {
		t.Fatal("不带 --desktop-session 不得写桌面端记录")
	}
	if n := len(b434Events(t, dir, id, ledger.EvDriverSeatBound)); n != 1 {
		t.Fatalf("driver_seat_bound = %d 条，want 1", n)
	}
	if n := len(b434Events(t, dir, id, ledger.EvSeatDesktopSet)); n != 0 {
		t.Fatalf("seat_desktop_set = %d 条，want 0", n)
	}
}

// 条 45–47、64、65 + 序列化边界（CLI → 账本载荷）：带 --desktop-session、不给
// --machine/--via → 记录命中；machine = os.Hostname()、via = manual；席位仍是
// bind / cli:claude#<CLAUDE_CODE_SESSION_ID>；seat_bearings 无行；载荷四值与记录一致。
func TestB434CardBindDesktopSessionRecordsSeatDesktop(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-desk-01")
	const session = "local_31eda11a-a6fe-4a5c-a229-fc12d682d1b6"
	dir := t.TempDir()
	id := mustAddCard(t, dir, "桌面端坐下卡")
	out, errOut, err := runLedgerCLI(t, dir, "card", "bind", id, "--desktop-session", session)
	if err != nil {
		t.Fatalf("bind --desktop-session: %v stderr=%s", err, errOut)
	}
	if out != "{\"ok\":true}\n" {
		t.Fatalf("stdout = %q", out)
	}
	host := b434Hostname(t)
	card, desk, has := b434SeatState(t, dir, id)
	if !has || desk.Session != session {
		t.Fatalf("SeatDesktopOf = %+v ok=%v, want Session=%s", desk, has, session)
	}
	if desk.Machine != host {
		t.Fatalf("Machine = %q, want os.Hostname() %q", desk.Machine, host)
	}
	if desk.Via != proto.SeatDesktopViaManual {
		t.Fatalf("Via = %q, want manual", desk.Via)
	}
	if card.DriverSource != "bind" || card.DriverSession != "cli:claude#claude-desk-01" {
		t.Fatalf("席位 = %q/%q，want bind / cli:claude#claude-desk-01", card.DriverSession, card.DriverSource)
	}
	sets := b434Events(t, dir, id, ledger.EvSeatDesktopSet)
	if len(sets) != 1 {
		t.Fatalf("seat_desktop_set = %d 条，want 1", len(sets))
	}
	want := map[string]string{"identity": "cli:claude#claude-desk-01", "desktop_session": session, "machine": host, "via": "manual"}
	got := b434Payload(t, sets[0])
	if len(got) != len(want) {
		t.Fatalf("载荷键集 = %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("载荷 %s = %q, want %q（全载荷 %v）", k, got[k], v, got)
		}
	}
	st := b434Ledger(t, dir)
	defer st.Close()
	if _, hasBearing, err := st.SeatBearingOf(id); err != nil || hasBearing {
		t.Fatalf("桌面端 bind 席位不得有 seat_bearings 行: has=%v err=%v", hasBearing, err)
	}
}

// 条 48 + 拆解稿 §5.2 枚举表驱动：--via 三值照记；词表外与显式空串在坐下前被拒。
func TestB434CardBindDesktopViaVocabulary(t *testing.T) {
	cases := []struct {
		via string
		ok  bool
	}{
		{"page", true}, {"launcher", true}, {"manual", true},
		{"bogus", false}, {"", false}, {"Page", false},
	}
	for _, tc := range cases {
		t.Run("via="+tc.via, func(t *testing.T) {
			clearSeatSourceEnv(t)
			t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-via")
			dir := t.TempDir()
			id := mustAddCard(t, dir, "via 卡")
			_, _, err := runLedgerCLI(t, dir, "card", "bind", id, "--desktop-session", "local_via-1", "--via", tc.via)
			card, desk, has := b434SeatState(t, dir, id)
			if tc.ok {
				if err != nil || !has || desk.Via != tc.via {
					t.Fatalf("--via %q 应照记: err=%v has=%v via=%q", tc.via, err, has, desk.Via)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "--via") {
				t.Fatalf("--via %q 应被拒且点名 --via: %v", tc.via, err)
			}
			if card.DriverSession != "" || has {
				t.Fatalf("词表外 --via 必须在坐下前被拒: seat=%q has=%v", card.DriverSession, has)
			}
		})
	}
}

// 条 60：席位 cli 段非 claude → 报错且不写桌面端记录（本卡在坐下前校验，故也不坐下）。
func TestB434CardBindDesktopRequiresClaudeSeat(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("HANDOFF_SESSION_CLI", "codex")
	t.Setenv("HANDOFF_SESSION_ID", "thread-desk")
	dir := t.TempDir()
	id := mustAddCard(t, dir, "非 claude 卡")
	_, _, err := runLedgerCLI(t, dir, "card", "bind", id, "--desktop-session", "local_abc")
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Fatalf("非 claude 席位带 --desktop-session 应报错并点名 claude: %v", err)
	}
	card, _, has := b434SeatState(t, dir, id)
	if has {
		t.Fatal("非 claude 席位不得写桌面端记录")
	}
	if card.DriverSession != "" {
		t.Fatalf("入参被拒时不得坐下，席位 = %q", card.DriverSession)
	}
}

// 条 61：--machine 或 --via 不带 --desktop-session → 报错且不坐下（反面断言查席位列）。
func TestB434CardBindDesktopFlagsRequireSession(t *testing.T) {
	for _, extra := range [][]string{{"--machine", "mac-01"}, {"--via", "page"}} {
		t.Run(extra[0], func(t *testing.T) {
			clearSeatSourceEnv(t)
			t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-orphan")
			dir := t.TempDir()
			id := mustAddCard(t, dir, "孤立 flag 卡")
			args := append([]string{"card", "bind", id}, extra...)
			_, _, err := runLedgerCLI(t, dir, args...)
			if err == nil || !strings.Contains(err.Error(), "--desktop-session") {
				t.Fatalf("%v 不带 --desktop-session 应报错: %v", extra, err)
			}
			card, _, has := b434SeatState(t, dir, id)
			if card.DriverSession != "" || has {
				t.Fatalf("孤立 flag 不得坐下: seat=%q has=%v", card.DriverSession, has)
			}
		})
	}
}

// 条 54 的 CLI 侧前置：非法桌面端会话 id 在坐下前被拒。
func TestB434CardBindDesktopRejectsBadSessionID(t *testing.T) {
	for _, bad := range []string{"31eda11a-a6fe", "local_", "local_a b", "local_a/b", "LOCAL_abc"} {
		t.Run(bad, func(t *testing.T) {
			clearSeatSourceEnv(t)
			t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-bad-id")
			dir := t.TempDir()
			id := mustAddCard(t, dir, "非法 id 卡")
			_, _, err := runLedgerCLI(t, dir, "card", "bind", id, "--desktop-session", bad)
			// 断言「--desktop-session 非法」而非仅「--desktop-session」：基线的 unknown flag 报文
			// 也含 flag 名，只断言 flag 名会在基线假绿（台账 C7 红跑读数）。
			if err == nil || !strings.Contains(err.Error(), "--desktop-session 非法") {
				t.Fatalf("非法 id %q 应报错: %v", bad, err)
			}
			card, _, has := b434SeatState(t, dir, id)
			if card.DriverSession != "" || has {
				t.Fatalf("非法 id 不得坐下: seat=%q has=%v", card.DriverSession, has)
			}
		})
	}
}

// 拍板 D2 + 缺陷族·静默失败：第二步 SetSeatDesktop 失败（sqlite 触发器真实注入）→
// 非零退出、stdout 不报成功、错误含补救命令（原样带 --via，契约 r10 第 5 条：不降成 manual，
// 保故事 9 的来源信息）；席位保持已坐下；去掉注入后按补救命令补写成功。
func TestB434CardBindDesktopSecondStepFailureKeepsSeat(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-half")
	dir := t.TempDir()
	id := mustAddCard(t, dir, "半态卡")
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("打开 raw 账本: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER b434_seat_desktop_fail BEFORE INSERT ON seat_desktops
		BEGIN SELECT RAISE(ABORT, '注入：桌面端记录写入失败'); END`); err != nil {
		t.Fatalf("建注入触发器: %v", err)
	}
	out, errOut, err := runLedgerCLI(t, dir, "card", "bind", id, "--desktop-session", "local_half-1", "--via", "page")
	if err == nil || ExitCode(err) == 0 {
		t.Fatalf("第二步失败必须非零退出: err=%v", err)
	}
	msg := err.Error() + errOut
	for _, want := range []string{"已坐下", "handoff card seat desktop set " + id + " --desktop-session local_half-1 --via page"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("错误应含 %q，得 %q", want, msg)
		}
	}
	if strings.Contains(out, `"ok"`) {
		t.Fatalf("半态不得在 stdout 报成功: %q", out)
	}
	card, _, has := b434SeatState(t, dir, id)
	if card.DriverSession != "cli:claude#claude-half" || has {
		t.Fatalf("半态应为「已坐下、无桌面端记录」: seat=%q has=%v", card.DriverSession, has)
	}
	if _, err := db.Exec(`DROP TRIGGER b434_seat_desktop_fail`); err != nil {
		t.Fatalf("撤注入触发器: %v", err)
	}
	out, errOut, err = runLedgerCLI(t, dir, "card", "seat", "desktop", "set", id, "--desktop-session", "local_half-1", "--via", "page")
	if err != nil || out != "{\"ok\":true}\n" {
		t.Fatalf("按补救命令补写应成功: out=%q err=%v stderr=%s", out, err, errOut)
	}
	_, desk, has := b434SeatState(t, dir, id)
	if !has || desk.Session != "local_half-1" || desk.Via != proto.SeatDesktopViaPage {
		t.Fatalf("补写后记录 = %+v has=%v", desk, has)
	}
}

// §11.2：--machine 缺省取 hostname，且本机配置有 targets 却无同名项 → stderr 提示、照常坐下；
// 显式 --machine 不提示。
func TestB434CardBindDesktopMachineHint(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-hint")
	dir := t.TempDir()
	cfg := &config.Config{
		Listen: "127.0.0.1:0", Token: "t", DataDir: dir, StallTimeout: 2 * time.Hour,
		Ledger:  config.LedgerConfig{Enabled: true},
		Targets: map[string]config.Target{"b434-other-box": {Addr: "10.0.0.9:7777", Token: "x"}},
	}
	if err := config.Save(filepath.Join(dir, "config.yaml"), cfg); err != nil {
		t.Fatalf("写带 targets 的配置: %v", err)
	}
	id := mustAddCard(t, dir, "提示卡")
	_, errOut, err := runLedgerCLI(t, dir, "card", "bind", id, "--desktop-session", "local_hint-1")
	if err != nil {
		t.Fatalf("提示不应影响成败: %v", err)
	}
	if !strings.Contains(errOut, "提示：--machine 缺省取本机 hostname") {
		t.Fatalf("应提示 target 名对齐，stderr=%q", errOut)
	}
	id2 := mustAddCard(t, dir, "显式机器卡")
	_, errOut, err = runLedgerCLI(t, dir, "card", "bind", id2, "--desktop-session", "local_hint-2", "--machine", "b434-other-box")
	if err != nil || strings.Contains(errOut, "提示：--machine") {
		t.Fatalf("显式 --machine 不应提示: err=%v stderr=%q", err, errOut)
	}
	if _, desk, _ := b434SeatState(t, dir, id2); desk.Machine != "b434-other-box" {
		t.Fatalf("显式 --machine 应照记，得 %q", desk.Machine)
	}
}

// 契约 r10 第 5 条（信箱键安全）：席位 CLI 会话 id 是信箱文件名
// ~/.handoff/desktop-wake/<CLI id>.<ns>.md 的一段——含路径分隔符或以 . 开头的 id
// 在坐下前被拒（# 与空白已被席位编码规则拒绝，不会走到这里）。
func TestB434CardBindDesktopRejectsUnsafeMailboxKey(t *testing.T) {
	for _, bad := range []string{"a/b", `a\b`, ".hidden", ".."} {
		t.Run(bad, func(t *testing.T) {
			clearSeatSourceEnv(t)
			t.Setenv("CLAUDE_CODE_SESSION_ID", bad)
			dir := t.TempDir()
			id := mustAddCard(t, dir, "信箱键卡")
			_, _, err := runLedgerCLI(t, dir, "card", "bind", id, "--desktop-session", "local_mbox-1")
			if err == nil || !strings.Contains(err.Error(), "不可作信箱文件名") {
				t.Fatalf("CLI 会话 id %q 应因信箱文件名不安全被拒: %v", bad, err)
			}
			card, _, has := b434SeatState(t, dir, id)
			if card.DriverSession != "" || has {
				t.Fatalf("信箱键不安全时不得坐下: seat=%q has=%v", card.DriverSession, has)
			}
		})
	}
}
