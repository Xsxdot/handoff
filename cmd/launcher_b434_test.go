// launcher_b434_test.go —— B434.2：handoff launcher 命令族（契约 §3.4、§3.10、条 139–143）
// 与 handoff status「拉起器」段（条 159–161、§11.2）。
//
// 职责：走 root command + 真实 SQLite 账本锁登记/覆盖/注销/不落卡事件与 stdout 线格式；
// status 段用 httptest 假 agentd（本机 + 两个 target）锁逐项目列与可达判定。
// 边界：账本写闸（形状校验）在 internal/ledger 锁。必须 env -u CLAUDE_CODE_SESSION_ID 下跑。
package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// b434LauncherWireKeys 是 launcher register/show 的 stdout 键集（与 desktop_launchers 列名同名）。
var b434LauncherWireKeys = []string{"project", "desktop_session", "cli_session", "machine", "actor", "registered_at"}

// b434DecodeLauncherWire 把一行 JSON 对象解成 map 并断言键集恰为 b434LauncherWireKeys。
func b434DecodeLauncherWire(t *testing.T, line string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(line)), &m); err != nil {
		t.Fatalf("stdout 不是 JSON 对象: %q: %v", line, err)
	}
	if len(m) != len(b434LauncherWireKeys) {
		t.Fatalf("键集 = %v, want %v", m, b434LauncherWireKeys)
	}
	for _, k := range b434LauncherWireKeys {
		if _, ok := m[k]; !ok {
			t.Fatalf("缺键 %q: %v", k, m)
		}
	}
	return m
}

// 条 139、140、142、143 + 序列化边界：register 写入并回显登记值；再登记覆盖；show 两形；
// unregister 幂等；全程不落任何卡事件。
func TestB434LauncherRegisterShowUnregister(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "launch-cli-01")
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "card", "list"); err != nil {
		t.Fatalf("预热账本: %v", err)
	}
	before := b434MaxSeq(t, dir)
	host := b434Hostname(t)

	out, errOut, err := runLedgerCLI(t, dir, "launcher", "register", "--project", "demo", "--desktop-session", "local_l1")
	if err != nil {
		t.Fatalf("register: %v stderr=%s", err, errOut)
	}
	m := b434DecodeLauncherWire(t, out)
	for k, v := range map[string]string{"project": "demo", "desktop_session": "local_l1", "cli_session": "launch-cli-01",
		"machine": host, "actor": "cli:claude#launch-cli-01"} {
		if m[k] != v {
			t.Fatalf("register 回显 %s = %v, want %q", k, m[k], v)
		}
	}
	if ts, _ := m["registered_at"].(string); ts == "" {
		t.Fatalf("registered_at 应为非空时间串: %v", m["registered_at"])
	} else if at, err := time.Parse(time.RFC3339Nano, ts); err != nil || at.IsZero() {
		t.Fatalf("registered_at 解析失败或为零: %q err=%v", ts, err)
	}
	st := b434Ledger(t, dir)
	l, ok, err := st.DesktopLauncherOf("demo")
	st.Close()
	if err != nil || !ok || l.DesktopSession != "local_l1" || l.CLISession != "launch-cli-01" || l.Machine != host {
		t.Fatalf("DesktopLauncherOf = %+v ok=%v err=%v", l, ok, err)
	}

	t.Setenv("CLAUDE_CODE_SESSION_ID", "launch-cli-02")
	if _, errOut, err := runLedgerCLI(t, dir, "launcher", "register", "--project", "demo",
		"--desktop-session", "local_l2", "--machine", "mac-02"); err != nil {
		t.Fatalf("再登记: %v stderr=%s", err, errOut)
	}
	out, _, err = runLedgerCLI(t, dir, "launcher", "show", "--project", "demo")
	if err != nil {
		t.Fatalf("show --project: %v", err)
	}
	m = b434DecodeLauncherWire(t, out)
	if m["desktop_session"] != "local_l2" || m["cli_session"] != "launch-cli-02" || m["machine"] != "mac-02" {
		t.Fatalf("再登记应覆盖: %v", m)
	}
	out, _, err = runLedgerCLI(t, dir, "launcher", "show")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &rows); err != nil || len(rows) != 1 {
		t.Fatalf("show 应为恰一行的数组: %q err=%v", out, err)
	}

	for i := 0; i < 2; i++ {
		out, errOut, err := runLedgerCLI(t, dir, "launcher", "unregister", "--project", "demo")
		if err != nil || out != "{\"ok\":true}\n" {
			t.Fatalf("第 %d 次 unregister 应幂等成功: out=%q err=%v stderr=%s", i+1, out, err, errOut)
		}
	}
	if _, _, err := runLedgerCLI(t, dir, "launcher", "show", "--project", "demo"); err == nil ||
		!strings.Contains(err.Error(), "launcher register") {
		t.Fatalf("未登记的 show --project 应非零退出并指路 register: %v", err)
	}
	out, _, err = runLedgerCLI(t, dir, "launcher", "show")
	if err != nil || out != "[]\n" {
		t.Fatalf("无登记时 show 应为 []: out=%q err=%v", out, err)
	}
	if after := b434MaxSeq(t, dir); after != before {
		t.Fatalf("拉起器登记不得落卡事件: seq %d → %d", before, after)
	}
}

// 条 141：当前会话出示不出 claude 身份（非 claude 物种 / 无身份）→ 报错且不写。
func TestB434LauncherRegisterRequiresClaudeSession(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"codex 会话": func(t *testing.T) {
			t.Setenv("HANDOFF_SESSION_CLI", "codex")
			t.Setenv("HANDOFF_SESSION_ID", "thread-l")
		},
		"无会话身份": func(t *testing.T) {},
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			clearSeatSourceEnv(t)
			setup(t)
			dir := t.TempDir()
			if _, _, err := runLedgerCLI(t, dir, "card", "list"); err != nil {
				t.Fatalf("预热账本: %v", err)
			}
			_, _, err := runLedgerCLI(t, dir, "launcher", "register", "--project", "demo", "--desktop-session", "local_l1")
			if err == nil || !strings.Contains(err.Error(), "claude") {
				t.Fatalf("非 claude 会话登记应报错并点名 claude: %v", err)
			}
			st := b434Ledger(t, dir)
			defer st.Close()
			if _, ok, err := st.DesktopLauncherOf("demo"); err != nil || ok {
				t.Fatalf("被拒时不得写登记: ok=%v err=%v", ok, err)
			}
		})
	}
}

// b434StatusOK 是最小可用的 /api/status 回包（无活跃任务）。
const b434StatusOK = `{"listen":"0.0.0.0:7777","data_dir":"/data","started_at":"2026-08-10T00:00:00Z",
	"version":{},"executors":["opencode"],"default_executor":"opencode","task_counts":{}}`

func b434StatusServer(t *testing.T) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(b434StatusOK))
	}))
	t.Cleanup(ts.Close)
	return ts
}

// 条 159–161 + §11.2 + 契约 r10 第 6 条：逐项目列 project / desktop_session / machine / 可达，
// 按 project 字节序；target 可达 / 不可达、本机 hostname 可达、本机别名 local / 本机 探本机
// （可达，并告警别名不利于异机寻址）、其它名字「不可探（非本机）」并告警。
func TestB434StatusListsLaunchers(t *testing.T) {
	targetName = ""
	resetFlags(t)
	local := b434StatusServer(t)
	up := b434StatusServer(t)
	down := httptest.NewServer(http.NotFoundHandler())
	downAddr := strings.TrimPrefix(down.URL, "http://")
	down.Close()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	body := "listen: 127.0.0.1:7777\ntoken: " + testToken + "\ndatadir: " + dir + "\n" +
		"targets:\n" +
		"  mac-up:\n    addr: \"" + strings.TrimPrefix(up.URL, "http://") + "\"\n    token: \"" + testToken + "\"\n" +
		"  mac-down:\n    addr: \"" + downAddr + "\"\n    token: \"" + testToken + "\"\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	host := b434Hostname(t)
	lst, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("开账本: %v", err)
	}
	for _, l := range []ledger.DesktopLauncher{
		{Project: "gamma", DesktopSession: "local_g1", CLISession: "cli-g", Machine: "nowhere", Actor: "cli:claude#cli-g"},
		{Project: "alpha", DesktopSession: "local_a1", CLISession: "cli-a", Machine: "mac-up", Actor: "cli:claude#cli-a"},
		{Project: "demo", DesktopSession: "local_d1", CLISession: "cli-d", Machine: host, Actor: "cli:claude#cli-d"},
		{Project: "beta", DesktopSession: "local_b1", CLISession: "cli-b", Machine: "mac-down", Actor: "cli:claude#cli-b"},
		{Project: "epsilon", DesktopSession: "local_e1", CLISession: "cli-e", Machine: "local", Actor: "cli:claude#cli-e"},
		{Project: "zeta", DesktopSession: "local_z1", CLISession: "cli-z", Machine: "本机", Actor: "cli:claude#cli-z"},
	} {
		if err := lst.RegisterDesktopLauncher(l); err != nil {
			t.Fatalf("种拉起器 %s: %v", l.Project, err)
		}
	}
	if err := lst.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := runStatus(t, cfgPath, local.URL)
	if err != nil {
		t.Fatalf("status 应成功: %v\n%s", err, out)
	}
	idx := strings.Index(out, "\n拉起器\n")
	if idx < 0 {
		t.Fatalf("应有「拉起器」段:\n%s", out)
	}
	lines := nonEmptyLines(out[idx+len("\n拉起器\n"):])
	if len(lines) != 6 {
		t.Fatalf("拉起器段应恰 6 行，得 %q", lines)
	}
	wantOrder := []struct{ project, session, machine string }{
		{"alpha", "local_a1", "mac-up"}, {"beta", "local_b1", "mac-down"},
		{"demo", "local_d1", host}, {"epsilon", "local_e1", "local"},
		{"gamma", "local_g1", "nowhere"}, {"zeta", "local_z1", "本机"},
	}
	for i, w := range wantOrder {
		for _, part := range []string{w.project, w.session, w.machine} {
			if !strings.Contains(lines[i], part) {
				t.Fatalf("第 %d 行应含 %q: %q", i+1, part, lines[i])
			}
		}
	}
	if !strings.HasSuffix(lines[0], "  可达") || !strings.HasSuffix(lines[2], "  可达") {
		t.Fatalf("alpha（target 可达）与 demo（本机）应以「可达」收尾: %q / %q", lines[0], lines[2])
	}
	if !strings.Contains(lines[1], "不可达（") {
		t.Fatalf("beta（target 已关）应不可达: %q", lines[1])
	}
	for _, i := range []int{3, 5} {
		if !strings.Contains(lines[i], "  可达") || strings.Contains(lines[i], "不可") || !strings.Contains(lines[i], "⚠") {
			t.Fatalf("本机别名（local / 本机）应探本机得可达并告警: %q", lines[i])
		}
	}
	if !strings.Contains(lines[4], "不可探（非本机）") || !strings.Contains(lines[4], "⚠") {
		t.Fatalf("gamma（未知机器名）应「不可探（非本机）」并告警: %q", lines[4])
	}
}

// 无登记时不打段：既有 status 输出不变。
func TestB434StatusOmitsLauncherSectionWhenNone(t *testing.T) {
	targetName = ""
	resetFlags(t)
	ts := b434StatusServer(t)
	out, err := runStatus(t, writeStatusConfig(t), ts.URL)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if strings.Contains(out, "拉起器") {
		t.Fatalf("无登记时不应出现拉起器段:\n%s", out)
	}
}
