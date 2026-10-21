// account_api_b453s2_test.go —— 账号 HTTP 面段 2 回归（B453：DELETE、代理打码、读数新键、
// 失败退避节拍）。
//
// 职责：穿真实 Handler/JSON 锁 DELETE（被引用 409 带 carriers、无引用删定义并连带读数、
// 版本冲突 409、不存在 404、缺 expect 400）；AccountView 与 GET 列表里 proxy 密码段打码、
// PUT 回填打码值保留原值；reading 的 fingerprint/extra_usage/plan_type 投影（区分缺席）；
// 以及探测到期判据 probeDue 的退避表（30s×2ⁿ 封顶为账号间隔）。
// 边界：载体引用经 accounts.Service.SetReferrers 桩注入（段 3 接编制域）。
package agentd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

func newSingleAccountMachine(t *testing.T) (*schedEnv, *accounts.Service) {
	t.Helper()
	st, err := ledger.Open(t.TempDir() + "/shared.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	env := newAccountMachine(t, "a", st, nil, "")
	return env, env.srv.accounts.(*accounts.Service)
}

func TestAccountDeleteThroughHandler(t *testing.T) {
	env, svc := newSingleAccountMachine(t)
	refs := []string{"c1"}
	svc.SetReferrers(func(string) ([]string, error) { return refs, nil })
	if code, body := schedReq(t, env, http.MethodPut, "/api/accounts/og?expect=0",
		`{"kind":"opencode-go","machine":"a","home_dir":"/root"}`); code != http.StatusOK {
		t.Fatalf("登记: %d %s", code, body)
	}
	if err := svc.RecordProbe("og", accounts.ProbeOutcome{StartedAt: time.Now(), FinishedAt: time.Now(),
		Reading: &accounts.Reading{ProbedAt: time.Now(), Windows: []accounts.Window{{Key: "weekly", UsedPercent: 1}}}}); err != nil {
		t.Fatal(err)
	}
	code, body := schedReq(t, env, http.MethodDelete, "/api/accounts/og?expect=1", "")
	var refusal struct {
		Error    string   `json:"error"`
		Carriers []string `json:"carriers"`
	}
	_ = json.Unmarshal([]byte(body), &refusal)
	if code != http.StatusConflict || len(refusal.Carriers) != 1 || refusal.Carriers[0] != "c1" || !strings.Contains(refusal.Error, "c1") {
		t.Fatalf("被引用应 409 并列出载体: %d %s", code, body)
	}
	refs = nil
	if code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/og", ""); code != http.StatusBadRequest {
		t.Fatalf("缺 expect 应 400: %d %s", code, body)
	}
	if code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/og?expect=7", ""); code != http.StatusConflict {
		t.Fatalf("版本冲突应 409: %d %s", code, body)
	}
	if code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/og?expect=1", ""); code != http.StatusOK {
		t.Fatalf("无引用应删除: %d %s", code, body)
	}
	if _, err := env.srv.SchedulingRegistry().Get(accounts.KindReading, "og"); err != schedclient.ErrNotFound {
		t.Fatalf("读数行应连带删除: %v", err)
	}
	if code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/og?expect=1", ""); code != http.StatusNotFound {
		t.Fatalf("已删再删应 404: %d %s", code, body)
	}
}

func TestAccountViewMasksProxyAndProjectsReadingKeys(t *testing.T) {
	env, svc := newSingleAccountMachine(t)
	const secret = "s3cr3t-proxy-pass"
	code, body := schedReq(t, env, http.MethodPut, "/api/accounts/cx?expect=0",
		`{"kind":"codex","machine":"a","home_dir":"~","proxy":"socks5://u:`+secret+`@127.0.0.1:1080"}`)
	if code != http.StatusOK || strings.Contains(body, secret) || !strings.Contains(body, `"proxy":"socks5://u:***@127.0.0.1:1080"`) {
		t.Fatalf("PUT 回执 proxy 应打码: %d %s", code, body)
	}
	// 控制台回填打码值改间隔：原代理保留（账本里仍是明文原值）。
	if code, body = schedReq(t, env, http.MethodPut, "/api/accounts/cx?expect=1",
		`{"kind":"codex","machine":"a","home_dir":"~","proxy":"socks5://u:***@127.0.0.1:1080","interval_minutes":30}`); code != http.StatusOK {
		t.Fatalf("回填打码值: %d %s", code, body)
	}
	v, err := svc.Get("cx")
	if err != nil || !strings.Contains(v.Account.Proxy, secret) || v.Account.IntervalMinutes != 30 {
		t.Fatalf("账本应保留原代理: %+v %v", v.Account, err)
	}
	bal := 12.5
	if err := svc.RecordProbe("cx", accounts.ProbeOutcome{StartedAt: time.Now(), FinishedAt: time.Now(),
		Reading: &accounts.Reading{ProbedAt: time.Now(), PlanType: "plus", Fingerprint: "sha256:3f9a1b2cc21e",
			ExtraUsage: &accounts.ExtraUsage{Unit: "credits", Balance: &bal},
			Windows:    []accounts.Window{{Key: "session", UsedPercent: 55}}}}); err != nil {
		t.Fatal(err)
	}
	code, body = schedReq(t, env, http.MethodGet, "/api/accounts", "")
	if code != http.StatusOK || strings.Contains(body, secret) {
		t.Fatalf("列表不得含代理密码: %d %s", code, body)
	}
	var resp proto.AccountsResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	r := resp.Accounts[0].Reading
	if r == nil || r.PlanType != "plus" || r.Fingerprint != "sha256:3f9a1b2cc21e" || r.ExtraUsage == nil ||
		r.ExtraUsage.Unit != "credits" || r.ExtraUsage.Balance == nil || *r.ExtraUsage.Balance != 12.5 ||
		r.ExtraUsage.Used != nil || resp.Accounts[0].Proxy != "socks5://u:***@127.0.0.1:1080" {
		t.Fatalf("读数新键投影不符: %s", body)
	}
	if strings.Contains(body, `"used":`) || strings.Contains(body, `"limit":`) {
		t.Fatalf("缺席字段不应以零值出现: %s", body)
	}
}

func TestAccountProbeDueBackoff(t *testing.T) {
	now := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	ago := func(d time.Duration) *time.Time { t := now.Add(-d); return &t }
	fail := &accounts.Problem{Category: "network", Message: "x"}
	cases := []struct {
		name     string
		interval int
		rec      accounts.ReadingRecord
		want     bool
	}{
		{"从未尝试", 15, accounts.ReadingRecord{}, true},
		{"成功后未满间隔", 15, accounts.ReadingRecord{LastAttemptAt: ago(14 * time.Minute)}, false},
		{"成功后满间隔", 15, accounts.ReadingRecord{LastAttemptAt: ago(15 * time.Minute)}, true},
		{"失败 1 次 30s 内", 15, accounts.ReadingRecord{LastAttemptAt: ago(29 * time.Second), LastError: fail, FailureStreak: 1}, false},
		{"失败 1 次满 30s", 15, accounts.ReadingRecord{LastAttemptAt: ago(30 * time.Second), LastError: fail, FailureStreak: 1}, true},
		{"失败 3 次满 2m", 15, accounts.ReadingRecord{LastAttemptAt: ago(2 * time.Minute), LastError: fail, FailureStreak: 3}, true},
		{"失败 3 次未满 2m", 15, accounts.ReadingRecord{LastAttemptAt: ago(119 * time.Second), LastError: fail, FailureStreak: 3}, false},
		{"失败多次封顶为间隔（5 分钟）", 5, accounts.ReadingRecord{LastAttemptAt: ago(5 * time.Minute), LastError: fail, FailureStreak: 20}, true},
		{"失败多次封顶前不到", 5, accounts.ReadingRecord{LastAttemptAt: ago(4 * time.Minute), LastError: fail, FailureStreak: 20}, false},
	}
	for _, tc := range cases {
		v := accounts.View{Account: accounts.Account{IntervalMinutes: tc.interval}, Record: tc.rec}
		if got := probeDue(v, now); got != tc.want {
			t.Fatalf("%s: probeDue = %v, want %v", tc.name, got, tc.want)
		}
	}
}
