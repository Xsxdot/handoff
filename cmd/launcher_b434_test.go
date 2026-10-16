// launcher_b434_test.go —— B434.2：handoff launcher 命令族（契约 §3.4、§3.10、条 139–143）
// 与 handoff status「拉起器」段（条 159–161、§11.2）。
//
// 职责：走 root command + 真实 SQLite 账本锁登记/覆盖/注销/不落卡事件与 stdout 线格式；
// status 段用 httptest 假 agentd（本机 + 两个 target）锁逐项目列与可达判定。
// 边界：账本写闸（形状校验）在 internal/ledger 锁。必须 env -u CLAUDE_CODE_SESSION_ID 下跑。
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
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
