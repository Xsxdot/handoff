// b435_session_resident_test.go —— B435 R：房间驻场者（session sit/stand、session wait <session>）。
//
// 夹具：真 SQLite 账本 + 真 collab 匹配函数（roomServiceFor，CLI 组装点）+ 真 cobra 入口
// （runLedgerCLI）。会话与卡沿用 card wait 房间命中段夹具 newB435WaitEnv。
package cmd

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// b435UnsetSessionEnv 清掉宿主会话键（HANDOFF 注入对与 grok/claude 宿主键），让「当前会话」
// 出示只取决于测试显式设的值；结束后由 t.Setenv 还原。
func b435UnsetSessionEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HANDOFF_SESSION_CLI", "HANDOFF_SESSION_ID", "GROK_SESSION_ID", "CLAUDE_CODE_SESSION_ID"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}

func b435JSONKeys(t *testing.T, line string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		t.Fatalf("stdout 行不是 JSON 对象: %v %q", err, line)
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

func b435SingleLine(t *testing.T, out string) string {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("stdout 应恰一行，得 %d 行: %q", len(lines), out)
	}
	return lines[0]
}

func (e *b435WaitEnv) resident(t *testing.T) (ledger.SessionResident, bool) {
	t.Helper()
	r, ok, err := e.st.SessionResident(e.session)
	if err != nil {
		t.Fatal(err)
	}
	return r, ok
}

// b435NonZeroNot124 断言命令失败且退出码不是超时档。
func b435NonZeroNot124(t *testing.T, err error, what string) {
	t.Helper()
	if code := b435ExitCode(err); code == 0 || code == ExitTimeout {
		t.Fatalf("%s 应非 0 且 ≠124，得退出码 %d（%v）", what, code, err)
	}
}

func TestB435SessionSitOutputsResident(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	out, _, err := runLedgerCLI(t, e.dir, "session", "sit", e.session, "--as", "agent:r")
	if err != nil {
		t.Fatalf("session sit: %v", err)
	}
	line := b435SingleLine(t, out)
	if keys := b435JSONKeys(t, line); keys != "identity,sat_at,session" {
		t.Fatalf("无桌面端 sit 的 stdout 键集应为 C-406 第二形态，得 %s", keys)
	}
	var got proto.SessionResident
	if err := json.Unmarshal([]byte(line), &got); err != nil || got.Session != e.session || got.Identity != "agent:r" || got.SatAt.IsZero() {
		t.Fatalf("stdout 内容不符: %+v err=%v", got, err)
	}
	if r, ok := e.resident(t); !ok || r.Identity != "agent:r" {
		t.Fatalf("账本应有驻场者 agent:r: %+v ok=%v", r, ok)
	}
	// 枚举新值过既有输出：session detail 文本把 resident 种类原样打出。
	detail, _, err := runLedgerCLI(t, e.dir, "session", "detail", e.session)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range strings.Split(detail, "\n") {
		if strings.Contains(l, "agent:r") && strings.Contains(l, proto.SessionMemberResident) {
			found = true
		}
	}
	if !found {
		t.Fatalf("session detail 文本缺 resident 行:\n%s", detail)
	}
}

func TestB435SessionSitRequiresAs(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	_, _, err := runLedgerCLI(t, e.dir, "session", "sit", e.session)
	b435NonZeroNot124(t, err, "缺 --as 的 sit")
	_, _, err = runLedgerCLI(t, e.dir, "session", "sit", e.session, "--as", "cli:x#y")
	b435NonZeroNot124(t, err, "非统一记法 --as 的 sit")
	if _, ok := e.resident(t); ok {
		t.Fatal("被拒 sit 不得写入")
	}
}

func TestB435SessionSitSecondRejected(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	if _, _, err := runLedgerCLI(t, e.dir, "session", "sit", e.session, "--as", "agent:r"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runLedgerCLI(t, e.dir, "session", "sit", e.session, "--as", "agent:x")
	b435NonZeroNot124(t, err, "第二次 sit")
	if !strings.Contains(stderr, "handoff session stand "+e.session+" --as agent:r") {
		t.Fatalf("stderr 应给出可行动的下一条命令（session stand … --as 现驻场者），得:\n%s", stderr)
	}
	out, _, err := runLedgerCLI(t, e.dir, "session", "detail", e.session, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var detail proto.SessionDetail
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &detail); err != nil {
		t.Fatal(err)
	}
	residents := 0
	for _, m := range detail.Summary.Members {
		if m.Kind == proto.SessionMemberResident {
			residents++
			if m.Identity != "agent:r" {
				t.Fatalf("被拒 sit 改了驻场者: %+v", m)
			}
		}
	}
	if residents != 1 {
		t.Fatalf("会话详情应恰一名驻场者，得 %d", residents)
	}
}

func TestB435SessionSitDesktopFields(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	const desk = "local_0a1b-2c3d"
	// 校验在任何写入之前：取不到 claude 会话 id、来源是 grok、--desktop-session 形状非法 → 非 0、零写入。
	_, _, err := runLedgerCLI(t, e.dir, "session", "sit", e.session, "--as", "agent:org", "--desktop-session", desk)
	b435NonZeroNot124(t, err, "无当前会话来源时带 --desktop-session 的 sit")
	t.Setenv("GROK_SESSION_ID", "g1")
	_, _, err = runLedgerCLI(t, e.dir, "session", "sit", e.session, "--as", "agent:org", "--desktop-session", desk)
	b435NonZeroNot124(t, err, "grok 来源带 --desktop-session 的 sit")
	if err := os.Unsetenv("GROK_SESSION_ID"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "abc")
	_, _, err = runLedgerCLI(t, e.dir, "session", "sit", e.session, "--as", "agent:org", "--desktop-session", "bogus")
	b435NonZeroNot124(t, err, "非法 --desktop-session 的 sit")
	if _, ok := e.resident(t); ok {
		t.Fatal("被拒 sit 不得写入")
	}

	out, _, err := runLedgerCLI(t, e.dir, "session", "sit", e.session, "--as", "agent:org", "--desktop-session", desk)
	if err != nil {
		t.Fatalf("带桌面端的 sit: %v", err)
	}
	line := b435SingleLine(t, out)
	if keys := b435JSONKeys(t, line); keys != "cli_session,desktop_session,identity,machine,sat_at,session" {
		t.Fatalf("带桌面端 sit 的 stdout 键集应为 C-406 全量，得 %s", keys)
	}
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	r, ok := e.resident(t)
	if !ok || r.CLISession != "abc" || r.DesktopSession != desk || r.Machine != host {
		t.Fatalf("桌面端三列不符（cli_session=当前 claude 会话、machine=hostname）: %+v", r)
	}
	if !scheduling.IsLocalMachine(r.Machine) {
		t.Fatalf("machine=%q 应被本机判据认作本机（同 os.Hostname 来源，N3）", r.Machine)
	}
}

func TestB435SessionStandCommand(t *testing.T) {
	b435UnsetSessionEnv(t)
	e := newB435WaitEnv(t)
	card := e.card(t)
	seat := e.pull(t, card, "r9") // S9：驻场者升格坐下一张卡
	if _, _, err := runLedgerCLI(t, e.dir, "session", "sit", e.session, "--as", "agent:r"); err != nil {
		t.Fatal(err)
	}
	_, stderr, err := runLedgerCLI(t, e.dir, "session", "stand", e.session, "--as", "agent:x")
	b435NonZeroNot124(t, err, "身份不符的 stand")
	if !strings.Contains(stderr, "agent:r") {
		t.Fatalf("身份不符 stand 的 stderr 应指出现驻场者，得:\n%s", stderr)
	}
	if r, ok := e.resident(t); !ok || r.Identity != "agent:r" {
		t.Fatalf("被拒 stand 改了登记: %+v %v", r, ok)
	}
	out, _, err := runLedgerCLI(t, e.dir, "session", "stand", e.session, "--as", "agent:r")
	if err != nil {
		t.Fatalf("stand: %v", err)
	}
	if strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("stand stdout 应为 {\"ok\":true}，得 %q", out)
	}
	if _, ok := e.resident(t); ok {
		t.Fatal("stand 后应无驻场者")
	}
	got, err := e.st.GetCard(card)
	if err != nil {
		t.Fatal(err)
	}
	if got.DriverSession != seat || got.DriverMode != string(proto.SeatModePull) {
		t.Fatalf("stand 不得动卡席位列: driver_session=%q driver_mode=%q", got.DriverSession, got.DriverMode)
	}
	_, _, err = runLedgerCLI(t, e.dir, "session", "stand", e.session)
	b435NonZeroNot124(t, err, "缺 --as 的 stand")
}

// TestB435ResidentDefaultRecipientViaCLIAssembly 是段 0b 前提：库里有驻场者行时，经 CLI 组装点
// roomServiceFor 拿到的匹配函数把「人类无 @」判给驻场者地址、不判给同会话根卡席位（K0 判定表
// 第 5 行先于第 6 行，C-219/C-220）。行由测试直写——证明 R 只需写面与 wait 改接。
func TestB435ResidentDefaultRecipientViaCLIAssembly(t *testing.T) {
	e := newB435WaitEnv(t)
	root := e.card(t)
	seat := e.pull(t, root, "rootpull")
	e.exec(t, `INSERT INTO session_residents (session, identity, sat_at) VALUES (?, ?, ?)`,
		e.session, "user:r", "2026-10-18T00:00:00Z")
	svc := roomServiceFor(e.st)
	msg := proto.RoomMessage{Room: e.session, Kind: proto.RoomMsgUser, Body: "大家好"}
	resident := proto.DeliveryAddress{Kind: proto.DeliveryResident, Session: e.session, Identity: "user:r"}
	hit, err := svc.Matches(msg, "user:x", resident)
	if err != nil || !hit {
		t.Fatalf("人类无 @ 应命中驻场者地址: hit=%v err=%v", hit, err)
	}
	rootAddr := proto.DeliveryAddress{Kind: proto.DeliverySeat, Session: e.session, Card: root, Identity: seat}
	hit, err = svc.Matches(msg, "user:x", rootAddr)
	if err != nil || hit {
		t.Fatalf("有驻场者时根卡席位不应命中: hit=%v err=%v", hit, err)
	}
}
