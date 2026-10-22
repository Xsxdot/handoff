// account_api_b453s2_test.go —— 账号 HTTP 面段 2 回归（B453：DELETE、代理打码、读数新键；B464 段 1
// 起代理在登录上、删除按「名下登录被载体引用」拒删）。
//
// 职责：穿真实 Handler/JSON 锁 DELETE（名下登录被引用 409 带 carriers、无引用删定义并连带读数、
// 版本冲突 409、不存在 404、缺 expect 400）；LoginView 里 proxy 密码段打码、GET 列表不含明文；
// reading 的 fingerprint/extra_usage/plan_type 投影（区分缺席）。探测到期判据 probeDue 的退避表
// 随探测器迁入 accountwork（TestProbeDueBackoff）。
// 边界：载体引用经 accounts.Service.SetReferrers 桩注入（生产由编制域 LoginReferrers 给出）；
// 登录代理的设置端点属 B464 段 2，这里直接改写登录行模拟「已设代理」。
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
	res := discoverAs(t, env, "a", acctHome(t))
	id, loginID := onlyAccount(t, res), res.Logins[0].ID
	refs := map[string][]string{loginID: {"c1"}}
	svc.SetReferrers(func() (map[string][]string, error) { return refs, nil })
	if err := svc.RecordProbe(id, accounts.ProbeOutcome{StartedAt: time.Now(), FinishedAt: time.Now(),
		Reading: &accounts.Reading{ProbedAt: time.Now(), Windows: []accounts.Window{{Key: "weekly", UsedPercent: 1}}}}); err != nil {
		t.Fatal(err)
	}
	code, body := schedReq(t, env, http.MethodDelete, "/api/accounts/"+id+"?expect=1", "")
	var refusal struct {
		Error    string   `json:"error"`
		Carriers []string `json:"carriers"`
	}
	_ = json.Unmarshal([]byte(body), &refusal)
	if code != http.StatusConflict || len(refusal.Carriers) != 1 || refusal.Carriers[0] != "c1" || !strings.Contains(refusal.Error, "c1") {
		t.Fatalf("名下登录被引用应 409 并列出载体: %d %s", code, body)
	}
	refs = nil
	if code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/"+id, ""); code != http.StatusBadRequest {
		t.Fatalf("缺 expect 应 400: %d %s", code, body)
	}
	if code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/"+id+"?expect=7", ""); code != http.StatusConflict {
		t.Fatalf("版本冲突应 409: %d %s", code, body)
	}
	if code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/"+id+"?expect=1", ""); code != http.StatusOK {
		t.Fatalf("无引用应删除: %d %s", code, body)
	}
	if _, err := env.srv.SchedulingRegistry().Get(accounts.KindReading, id); err != schedclient.ErrNotFound {
		t.Fatalf("读数行应连带删除: %v", err)
	}
	if code, body = schedReq(t, env, http.MethodDelete, "/api/accounts/"+id+"?expect=1", ""); code != http.StatusNotFound {
		t.Fatalf("已删再删应 404: %d %s", code, body)
	}
}

func TestAccountViewMasksLoginProxyAndProjectsReadingKeys(t *testing.T) {
	env, svc := newSingleAccountMachine(t)
	res := discoverAs(t, env, "a", acctHome(t))
	id, loginID := onlyAccount(t, res), res.Logins[0].ID
	const secret = "s3cr3t-proxy-pass"
	setLoginProxy(t, env.srv.SchedulingRegistry(), loginID, "socks5://u:"+secret+"@127.0.0.1:1080")
	bal := 12.5
	if err := svc.RecordProbe(id, accounts.ProbeOutcome{StartedAt: time.Now(), FinishedAt: time.Now(), Login: loginID,
		Reading: &accounts.Reading{ProbedAt: time.Now(), PlanType: "plus", Fingerprint: "sha256:3f9a1b2cc21e",
			ExtraUsage: &accounts.ExtraUsage{Unit: "credits", Balance: &bal},
			Windows:    []accounts.Window{{Key: "session", UsedPercent: 55}}}}); err != nil {
		t.Fatal(err)
	}
	code, out := schedReq(t, env, http.MethodGet, "/api/accounts", "")
	if code != http.StatusOK || strings.Contains(out, secret) {
		t.Fatalf("列表不得含代理密码: %d %s", code, out)
	}
	var resp proto.AccountsResp
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	v := resp.Accounts[0]
	r := v.Reading
	if r == nil || r.PlanType != "plus" || r.Fingerprint != "sha256:3f9a1b2cc21e" || r.ExtraUsage == nil ||
		r.ExtraUsage.Unit != "credits" || r.ExtraUsage.Balance == nil || *r.ExtraUsage.Balance != 12.5 ||
		r.ExtraUsage.Used != nil || len(v.Logins) != 1 || v.Logins[0].Proxy != "socks5://u:***@127.0.0.1:1080" {
		t.Fatalf("读数新键/登录代理投影不符: %s", out)
	}
	if strings.Contains(out, `"used":`) || strings.Contains(out, `"limit":`) || strings.Contains(out, `"expires_at"`) {
		t.Fatalf("缺席字段不应以零值出现: %s", out)
	}
	// 账本里仍是原值（读面打码，不改存储）。
	l, err := svc.Login(loginID)
	if err != nil || !strings.Contains(l.Proxy, secret) {
		t.Fatalf("登录行应保留原代理: %v", err)
	}
}
