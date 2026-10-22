package cmd

// b464_seg3_cli_test.go —— 段 3 的 CLI 面（B464 plan C2、S9）：`handoff account discover
// [--machine]` 每机一行、全部成功退出 0 / 任一失败退出 1；`handoff squad list` 载体行账号格
// 「账号名 · 机器 HOME」。
//
// 边界：stub agentd（与 account/squad 测试同款 harness）；远端结果由夹具给，不起真执行器。

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestAccountDiscoverPrintsPerMachineAndExitCodes(t *testing.T) {
	dir := t.TempDir()
	fixture := `{"machines":[` +
		`{"machine":"mac-mini","ok":true,"at":"2026-10-22T10:30:00Z"},` +
		`{"machine":"linux-01","ok":false,"error":"转发到 linux-01 失败: 连接被拒绝"}]}`
	var gotMethod, gotPath, gotQuery string
	stubSquadAgentd(t, dir, http.StatusOK, fixture, func(r *http.Request, _ string) {
		gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
	})
	out, errOut, err := runLedgerCLI(t, dir, "account", "discover")
	if gotMethod != http.MethodPost || gotPath != "/api/accounts/discover" || gotQuery != "" {
		t.Fatalf("请求 = %s %s?%s", gotMethod, gotPath, gotQuery)
	}
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("任一机失败应退出码 1：err=%v code=%d stderr=%s", err, ExitCode(err), errOut)
	}
	lines := nonEmptyLines(out)
	if len(lines) != 2 {
		t.Fatalf("应每机一行，得 %d 行：%s", len(lines), out)
	}
	if !strings.Contains(lines[0], "mac-mini") || !strings.Contains(lines[0], "成功") {
		t.Fatalf("成功机行不符：%q", lines[0])
	}
	if !strings.Contains(lines[1], "linux-01") || !strings.Contains(lines[1], "连接被拒绝") {
		t.Fatalf("失败机行应带原因：%q", lines[1])
	}

	allOK := t.TempDir()
	stubSquadAgentd(t, allOK, http.StatusOK, `{"machines":[{"machine":"mac-mini","ok":true,"at":"2026-10-22T10:30:00Z"}]}`, nil)
	if _, errOut, err := runLedgerCLI(t, allOK, "account", "discover"); err != nil || ExitCode(err) != 0 {
		t.Fatalf("全部成功应退出码 0：err=%v stderr=%s", err, errOut)
	}
}

func TestAccountDiscoverMachineFlagScopesRequest(t *testing.T) {
	dir := t.TempDir()
	var gotQuery string
	stubSquadAgentd(t, dir, http.StatusOK, `{"machines":[{"machine":"linux-01","ok":true,"at":"2026-10-22T10:30:00Z"}]}`,
		func(r *http.Request, _ string) { gotQuery = r.URL.RawQuery })
	out, _, err := runLedgerCLI(t, dir, "account", "discover", "--machine", "linux-01")
	if err != nil {
		t.Fatalf("account discover --machine: %v", err)
	}
	if gotQuery != "machine=linux-01" {
		t.Fatalf("--machine 应带出 machine 查询：%q", gotQuery)
	}
	if lines := nonEmptyLines(out); len(lines) != 1 || !strings.Contains(lines[0], "linux-01") {
		t.Fatalf("应只打指定机一行：%s", out)
	}
}

// TestSquadListCarrierAccountShowsMachineHome 锁 S9 的账号格：「账号名 · 机器 HOME」；无账号与
// 未选保持既有文案（无账号 / -）。
func TestSquadListCarrierAccountShowsMachineHome(t *testing.T) {
	dir := t.TempDir()
	fixture := `{"carriers":[` +
		`{"name":"c1","machine":"mac-mini","cli":"opencode","home_dir":"/Users/dev/.handoff/home/oc","version":1,"login":"login-1","account_name":"og-main","login_state":"ok"},` +
		`{"name":"c2","machine":"mac-mini","cli":"opencode","home_dir":"","version":1,"login":"login-2","account_name":"og-main","login_state":"ok"},` +
		`{"name":"c3","machine":"mac-mini","cli":"agy","home_dir":"","version":1,"no_account":true}],"squads":[],"running":[]}`
	stubSquadAgentd(t, dir, http.StatusOK, fixture, nil)
	out, _, err := runLedgerCLI(t, dir, "squad", "list")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.Contains(line, "c1"):
			if !strings.Contains(line, "og-main · mac-mini /Users/dev/.handoff/home/oc") {
				t.Fatalf("c1 账号格应为「账号名 · 机器 HOME」：%q", line)
			}
		case strings.Contains(line, "c2"):
			if !strings.Contains(line, "og-main · mac-mini ~") {
				t.Fatalf("主 HOME 登录应显示 ~：%q", line)
			}
		case strings.Contains(line, "c3"):
			if !strings.HasSuffix(strings.TrimSpace(line), "无账号") {
				t.Fatalf("无账号载体行保持「无账号」：%q", line)
			}
		}
	}
	// --json 载体行仍是 wire 形状（不因表格改动而多塞派生字串）。
	out2, _, err := runLedgerCLI(t, dir, "squad", "list", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var carrier proto.CarrierView
	if err := json.Unmarshal([]byte(nonEmptyLines(out2)[0]), &carrier); err != nil {
		t.Fatalf("--json 首行不是 CarrierView：%v", err)
	}
	if carrier.AccountName != "og-main" || carrier.Machine != "mac-mini" {
		t.Fatalf("--json 应原样带 account_name/machine：%+v", carrier)
	}
}
