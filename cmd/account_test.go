package cmd

// account_test.go —— handoff account list/probe 的命令级回归（B453 段 1，spec F11/F12）。
//
// 职责：锁 list --json 每行可解进 proto.AccountView 且与服务端元素逐字段相等（同形承诺
// F11 / B464 C1，含 logins）；表格列含名称/类型/状态/窗口/最近重置/上次探测，账号行下逐条登录
// （B464：机器:HOME、来源、状态、代理、载体）；probe 打新读数行，探测
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
	changed := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	return proto.AccountsResp{Accounts: []proto.AccountView{
		{ID: "opencode-go:3f9a1b2cc21e", Name: "og-linux", Kind: "opencode-go", Fingerprint: "sha256:3f9a1b2cc21e",
			IntervalMinutes: 15, WarnPercent: 60, LowPercent: 80, Version: 2, State: "ok",
			Reading: &proto.AccountReadingView{ProbedAt: probed, Windows: []proto.AccountWindowView{
				{Key: "session", UsedPercent: 34, ResetsAt: &reset}, {Key: "weekly", UsedPercent: 58},
				{Key: "monthly", UsedPercent: 13.310402631759644}, {Key: "weekly", Group: "Claude and GPT models", UsedPercent: 58.5},
			}}, LastAttemptAt: &probed, ProbeLogin: "login-aa00000000000001",
			Logins: []proto.LoginView{
				{ID: "login-aa00000000000001", Machine: "linux-01", Home: "~/.handoff/home/mimo", Source: "opencode.db",
					Fingerprint: "sha256:3f9a1b2cc21e", State: "ok", ChangedAt: changed, Carriers: []string{"mimo", "mimo-2"}},
				{ID: "login-aa00000000000002", Machine: "mac-mini", Home: "~", Source: "opencode.db",
					Fingerprint: "sha256:3f9a1b2cc21e", State: "gone", ChangedAt: changed, Carriers: []string{}},
			},
			Carriers: []string{"mimo", "mimo-2"}},
		{ID: "opencode-go:77d0c1e2a3b4", Name: "og-mini", Kind: "opencode-go", Fingerprint: "sha256:77d0c1e2a3b4",
			IntervalMinutes: 15, WarnPercent: 60, LowPercent: 80, Version: 1,
			State: "fail", LastAttemptAt: &failedAt, ProbeLogin: "login-bb00000000000001",
			LastError: &proto.AccountProblemView{Category: "credential_missing", Message: "凭据文件不存在"},
			Logins: []proto.LoginView{{ID: "login-bb00000000000001", Machine: "mac-mini", Home: "~", Source: "auth.json",
				Fingerprint: "sha256:77d0c1e2a3b4", State: "ok", ChangedAt: changed, Proxy: "socks5://u:***@127.0.0.1:7897",
				Carriers: []string{}}},
			Carriers: []string{}},
		// B464 段 2：不支持额度探测（na）的 claude 账号，阈值与间隔缺席；一条已过期登录。
		{ID: "claude:c1c1c1c1c1c1", Name: "cl@example.com", Kind: "claude", Label: "cl@example.com",
			Fingerprint: "sha256:c1c1c1c1c1c1", Version: 1, State: "na",
			Logins: []proto.LoginView{{ID: "login-cc00000000000001", Machine: "mac-mini", Home: "~", Source: "claude-auth",
				Fingerprint: "sha256:c1c1c1c1c1c1", State: "expired", ChangedAt: changed, Carriers: []string{"cl"}}},
			Carriers: []string{"cl"}},
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
	// B464：账号行下逐条登录行（机器:HOME、来源、登录状态、代理、所用载体；可探登录标 *）。
	for _, want := range []string{"名称", "类型", "状态", "窗口已用", "最近重置", "上次探测",
		"og-linux", "正常", "5h 34%", "周 58%", "月 13%", "Claude and GPT models 周 59%", "2h4", "og-mini", "探测失败", "凭据文件不存在",
		"└*登录", "linux-01:~/.handoff/home/mimo", "opencode.db", "载体:mimo,mimo-2", "└ 登录", "mac-mini:~", "已失效",
		"代理:socks5://u:***@127.0.0.1:7897", "代理:-", "载体:-",
		"cl@example.com", "claude", "不支持额度探测", "claude-auth", "已过期", "载体:cl"} {
		if !strings.Contains(out, want) {
			t.Fatalf("表格缺 %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "13.3") || strings.Contains(out, "58.5") {
		t.Fatalf("表格百分比应取整显示:\n%s", out)
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
