package cmd

// account_test.go —— handoff account list/probe 的命令级回归（B453 段 1，spec F11/F12）。
//
// 职责：锁 list --json 每行可解进 proto.AccountView 且与服务端元素逐字段相等（同形承诺
// F11）；表格列含名称/类型/机器/状态/窗口/最近重置/上次探测；probe 打新读数行，探测
// 失败结论退出码 1、成功 0（F12）；请求路径与方法。
// 边界：stub agentd（stubSquadAgentd 同款 harness）；真实 gateway 一端由 agentd 的
// AccountProbeForward 测试锁，两端拼起来穿过完整 JSON 边界。

import (
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func sampleAccounts(t *testing.T) proto.AccountsResp {
	t.Helper()
	probed := time.Now().Add(-3 * time.Minute).UTC().Truncate(time.Second)
	reset := time.Now().Add(2*time.Hour + 41*time.Minute).UTC().Truncate(time.Second)
	failedAt := time.Now().Add(-1 * time.Minute).UTC().Truncate(time.Second)
	return proto.AccountsResp{Accounts: []proto.AccountView{
		{Name: "og-linux", Kind: "opencode-go", Machine: "linux-01", HomeDir: "/root", Entry: "opencode-go",
			IntervalMinutes: 15, WarnPercent: 60, LowPercent: 80, Version: 2, State: "ok",
			Reading: &proto.AccountReadingView{ProbedAt: probed, Windows: []proto.AccountWindowView{
				{Key: "session", UsedPercent: 34, ResetsAt: &reset}, {Key: "weekly", UsedPercent: 58},
			}}, LastAttemptAt: &probed, Carriers: []string{}},
		{Name: "og-mini", Kind: "opencode-go", Machine: "mac-mini", HomeDir: "~", Entry: "opencode-go",
			Proxy: "socks5://127.0.0.1:7897", IntervalMinutes: 15, WarnPercent: 60, LowPercent: 80, Version: 1,
			State: "fail", LastAttemptAt: &failedAt,
			LastError: &proto.AccountProblemView{Category: "credential_missing", Message: "凭据文件不存在"},
			Carriers:  []string{}},
	}}
}

func TestAccountListJSONLinesMatchAPIViews(t *testing.T) {
	dir := t.TempDir()
	want := sampleAccounts(t)
	raw, _ := json.Marshal(want)
	var gotPath, gotMethod string
	stubSquadAgentd(t, dir, http.StatusOK, string(raw), func(r *http.Request, _ string) {
		gotPath, gotMethod = r.URL.Path, r.Method
	})
	out, errOut, err := runLedgerCLI(t, dir, "account", "list", "--json")
	if err != nil {
		t.Fatalf("account list --json: %v\nstderr=%s", err, errOut)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/accounts" {
		t.Fatalf("请求 = %s %s", gotMethod, gotPath)
	}
	lines := nonEmptyLines(out)
	if len(lines) != len(want.Accounts) {
		t.Fatalf("应一行一个账号，得 %d 行: %s", len(lines), out)
	}
	for i, ln := range lines {
		var v proto.AccountView
		if err := json.Unmarshal([]byte(ln), &v); err != nil {
			t.Fatalf("第 %d 行不是 AccountView: %v (%s)", i, err, ln)
		}
		if !reflect.DeepEqual(v, want.Accounts[i]) {
			t.Fatalf("第 %d 行与 API 元素不同形:\n got %+v\nwant %+v", i, v, want.Accounts[i])
		}
	}
}

func TestAccountListTableColumns(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(sampleAccounts(t))
	stubSquadAgentd(t, dir, http.StatusOK, string(raw), nil)
	out, errOut, err := runLedgerCLI(t, dir, "account", "list")
	if err != nil {
		t.Fatalf("account list: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{"名称", "类型", "机器", "状态", "窗口已用", "最近重置", "上次探测",
		"og-linux", "正常", "5h 34%", "周 58%", "2h4", "og-mini", "探测失败", "凭据文件不存在"} {
		if !strings.Contains(out, want) {
			t.Fatalf("表格缺 %q:\n%s", want, out)
		}
	}
}

func TestAccountProbeExitCodes(t *testing.T) {
	views := sampleAccounts(t).Accounts
	cases := []struct {
		name    string
		view    proto.AccountView
		wantErr bool
	}{
		{name: "成功", view: views[0], wantErr: false},
		{name: "探测失败结论", view: views[1], wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			raw, _ := json.Marshal(tc.view)
			var gotPath, gotMethod string
			stubSquadAgentd(t, dir, http.StatusOK, string(raw), func(r *http.Request, _ string) {
				gotPath, gotMethod = r.URL.Path, r.Method
			})
			out, errOut, err := runLedgerCLI(t, dir, "account", "probe", tc.view.Name)
			if gotMethod != http.MethodPost || gotPath != "/api/accounts/"+tc.view.Name+"/probe" {
				t.Fatalf("请求 = %s %s", gotMethod, gotPath)
			}
			if !strings.Contains(out, tc.view.Name) {
				t.Fatalf("stdout 应打新读数行: %s", out)
			}
			if (err != nil) != tc.wantErr || ExitCode(err) != map[bool]int{false: 0, true: 1}[tc.wantErr] {
				t.Fatalf("退出码不符: err=%v code=%d stderr=%s", err, ExitCode(err), errOut)
			}
		})
	}
	dir := t.TempDir()
	stubSquadAgentd(t, dir, http.StatusBadGateway, `{"error":"转发到 c 失败: dial tcp"}`, nil)
	_, _, err := runLedgerCLI(t, dir, "account", "probe", "og-c")
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "转发到 c 失败") {
		t.Fatalf("服务端 502 应退出 1 并带原文: %v", err)
	}
}
