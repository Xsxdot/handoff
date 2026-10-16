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
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

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
