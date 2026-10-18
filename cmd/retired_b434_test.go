// retired_b434_test.go —— B435.5（X）B434 退役命令与 flag 的墓碑（plan b435.5 §0 Q3 甲、段 2；
// 拆解 §5.5 判据「改名提示可行动」）与 status 拉起器段退役。
//
// 职责：表驱动锁四处墓碑——命中即非 0、stderr 恰一行且含墓碑提示常量（不在测试里另写文案）、
// 账本零写（席位三列与 MaxSeq 不变）、零 HTTP（假 agentd 计数为 0）；--help 不列。
// 另锁 handoff status 文本在账本留有旧拉起器行时也不再出现「拉起器」段。
// 边界：必须在 env -u CLAUDE_CODE_SESSION_ID 下跑（clearSeatSourceEnv）。
package cmd

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// b434StatusOK 是最小可用的 /api/status 回包（无活跃任务）。B435 X 自已删的 launcher_b434_test.go 迁入。
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

// b435SeatAndSeq 读卡席位三列与账本 MaxSeq（「账本零写」判据）。
func b435SeatAndSeq(t *testing.T, dir, id string) (string, int64) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("打开测试账本: %v", err)
	}
	defer st.Close()
	card, err := st.GetCard(id)
	if err != nil {
		t.Fatalf("读卡 %s: %v", id, err)
	}
	seq, err := st.MaxSeq()
	if err != nil {
		t.Fatalf("读 MaxSeq: %v", err)
	}
	return card.DriverSession + "|" + card.DriverSource + "|" + card.DriverMode, seq
}

func TestB435RetiredB434Tombstones(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "b435-tomb")
	dir := t.TempDir()
	id := mustAddCard(t, dir, "墓碑卡")
	if _, errOut, err := runLedgerCLI(t, dir, "card", "bind", id); err != nil {
		t.Fatalf("前置坐下: %v %s", err, errOut)
	}
	var hits atomic.Int64
	stubSquadAgentd(t, dir, http.StatusOK, `{}`, func(*http.Request, string) { hits.Add(1) })

	cases := []struct {
		name string
		args []string
	}{
		{"bind --desktop-session", []string{"card", "bind", id, "--desktop-session", "local_x"}},
		{"bind --machine", []string{"card", "bind", id, "--machine", "mac-pro"}},
		{"bind --via", []string{"card", "bind", id, "--via", "page"}},
		{"seat desktop set", []string{"card", "seat", "desktop", "set", id, "--desktop-session", "local_x"}},
		{"coordinate --desktop", []string{"card", "coordinate", id, "--desktop"}},
		{"launcher register", []string{"launcher", "register", "--project", "demo", "--desktop-session", "local_x"}},
		{"launcher show", []string{"launcher", "show"}},
		{"launcher unregister", []string{"launcher", "unregister", "--project", "demo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			seatBefore, seqBefore := b435SeatAndSeq(t, dir, id)
			hitsBefore := hits.Load()
			out, errOut, err := runLedgerCLI(t, dir, tc.args...)
			if err == nil {
				t.Fatalf("退役入口必须非 0 退出: stdout=%q stderr=%q", out, errOut)
			}
			lines := nonEmptyLines(errOut)
			if len(lines) != 1 || !strings.Contains(lines[0], retiredB434Hint) {
				t.Fatalf("stderr 应恰一行且含墓碑提示 %q，得 %q", retiredB434Hint, errOut)
			}
			if out != "" {
				t.Fatalf("墓碑不得写 stdout: %q", out)
			}
			if seat, seq := b435SeatAndSeq(t, dir, id); seat != seatBefore || seq != seqBefore {
				t.Fatalf("墓碑不得写账本: 席位 %s→%s, MaxSeq %d→%d", seatBefore, seat, seqBefore, seq)
			}
			if n := hits.Load() - hitsBefore; n != 0 {
				t.Fatalf("墓碑不得发 HTTP，假 agentd 收到 %d 次", n)
			}
		})
	}
	if !strings.Contains(retiredB434Hint, "session sit") || !strings.Contains(retiredB434Hint, "--desktop-session") ||
		!strings.Contains(retiredB434Hint, "project room") {
		t.Fatalf("Q3：提示须指向 session sit --desktop-session 与 project room: %q", retiredB434Hint)
	}
}

func TestB435RetiredB434HiddenFromHelp(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	checks := []struct {
		args []string
		bad  *regexp.Regexp
	}{
		{[]string{"--help"}, regexp.MustCompile(`(?m)^\s+launcher\s`)},
		{[]string{"card", "seat", "--help"}, regexp.MustCompile(`(?m)^\s+desktop\s`)},
		{[]string{"card", "bind", "--help"}, regexp.MustCompile(`--desktop-session|--machine|--via`)},
		{[]string{"card", "coordinate", "--help"}, regexp.MustCompile(`--desktop\b`)},
	}
	for _, c := range checks {
		out, errOut, err := runLedgerCLI(t, dir, c.args...)
		if err != nil {
			t.Fatalf("%v: %v %s", c.args, err, errOut)
		}
		if m := c.bad.FindString(out + errOut); m != "" {
			t.Fatalf("%v 的帮助不得列出退役项 %q:\n%s", c.args, m, out)
		}
	}
}

// status 文本不再有「拉起器」段：账本里残留一条旧拉起器行（旧二进制写入的形态）也不打。
func TestB435StatusHasNoLauncherSection(t *testing.T) {
	targetName = ""
	resetFlags(t)
	ts := b434StatusServer(t)
	cfgPath := writeStatusConfig(t)
	dir := filepath.Dir(cfgPath)
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS desktop_launchers (project TEXT PRIMARY KEY, desktop_session TEXT NOT NULL,
			cli_session TEXT NOT NULL, machine TEXT NOT NULL, actor TEXT NOT NULL, registered_at TEXT NOT NULL)`,
		`INSERT INTO desktop_launchers VALUES ('demo', 'local_d1', 'cli-d', '', 'cli:claude#cli-d', '2026-10-01T00:00:00Z')`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			db.Close()
			t.Fatalf("种旧拉起器行: %v", err)
		}
	}
	db.Close()
	out, err := runStatus(t, cfgPath, ts.URL)
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if strings.Contains(out, "拉起器") {
		t.Fatalf("status 不得再有拉起器段:\n%s", out)
	}
}
