// codex_test.go —— codex 额度适配回归（B453 段 2）。
//
// 职责：锁 ParseCodexUsage 的表（窗口按 limit_window_seconds 归类、reset_at/
// reset_after_seconds、allowed=false/limit_reached=true 判不可用、credits 进额外额度、
// 401/403/429/5xx、字段缺失、>100 钳制），与 Probe 的真实路径（读 tokens 条目、
// 请求头形状、JWT 过期判 credential_expired、仅 API key 登录判 credential_invalid、
// 指纹取 account_id）。
// 边界：成功样本 testdata/codex_usage_200.synthetic.json 按 b453-facts-providers §E
// 的 gist 样例合成，**未经真机**（U3，R2 替换）。
package quota

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func readTestdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCodexParseUsageTable(t *testing.T) {
	now := time.Unix(1778660000, 0).UTC()
	r1, r2 := time.Unix(1778670307, 0).UTC(), time.Unix(1779157165, 0).UTC()
	after := now.Add(100 * time.Second)
	cases := []struct {
		name    string
		status  int
		body    string
		windows []Window
		blocked Category
		failure Category
		plan    string
		extra   bool
	}{
		{name: "合成成功样本", status: 200, body: string(readTestdata(t, "codex_usage_200.synthetic.json")),
			windows: []Window{{Key: WindowSession, UsedPercent: 55, ResetsAt: &r1}, {Key: WindowWeekly, UsedPercent: 51, ResetsAt: &r2}},
			plan:    "plus", extra: true},
		{name: "Team 只有周窗口且放在 primary，reset_after_seconds 兜底", status: 200,
			body:    `{"plan_type":"team","rate_limit":{"allowed":true,"limit_reached":false,"primary_window":{"used_percent":130,"limit_window_seconds":604800,"reset_after_seconds":100},"secondary_window":null}}`,
			windows: []Window{{Key: WindowWeekly, UsedPercent: 100, ResetsAt: &after}}, plan: "team"},
		{name: "limit_reached 判不可用", status: 200,
			body:    `{"plan_type":"plus","rate_limit":{"allowed":true,"limit_reached":true,"primary_window":{"used_percent":100,"limit_window_seconds":18000,"reset_at":1778670307}}}`,
			windows: []Window{{Key: WindowSession, UsedPercent: 100, ResetsAt: &r1}}, blocked: CategoryLimitReached, plan: "plus"},
		{name: "allowed=false 判不可用", status: 200,
			body:    `{"rate_limit":{"allowed":false,"primary_window":{"used_percent":20,"limit_window_seconds":18000}}}`,
			windows: []Window{{Key: WindowSession, UsedPercent: 20}}, blocked: CategoryLimitReached},
		{name: "缺 rate_limit", status: 200, body: `{"plan_type":"plus"}`, failure: CategoryParse},
		{name: "两窗口都缺", status: 200, body: `{"rate_limit":{"allowed":true,"primary_window":null}}`, failure: CategoryParse},
		{name: "窗口缺 used_percent", status: 200, body: `{"rate_limit":{"primary_window":{"limit_window_seconds":18000}}}`, failure: CategoryParse},
		{name: "401", status: 401, body: `{"detail":"Unauthorized"}`, failure: CategoryUnauthorized},
		{name: "403", status: 403, body: `{}`, failure: CategoryUnauthorized},
		{name: "429", status: 429, body: ``, failure: CategoryRateLimited},
		{name: "500", status: 500, body: `oops`, failure: CategoryUpstream},
		{name: "非 JSON", status: 200, body: `<html>`, failure: CategoryParse},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := ParseCodexUsage(tc.status, []byte(tc.body), now)
			if tc.failure != "" {
				var f *Failure
				if !errors.As(err, &f) || f.Category != tc.failure || f.Message == "" {
					t.Fatalf("want %s, got %+v %v", tc.failure, snap, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertWindows(t, snap.Windows, tc.windows)
			if (tc.blocked == "") != (snap.Blocked == nil) || (snap.Blocked != nil && snap.Blocked.Category != tc.blocked) {
				t.Fatalf("blocked = %+v, want %q", snap.Blocked, tc.blocked)
			}
			if snap.PlanType != tc.plan {
				t.Fatalf("plan = %q, want %q", snap.PlanType, tc.plan)
			}
			if tc.extra && (snap.Extra == nil || snap.Extra.Balance == nil || *snap.Extra.Balance != 12.5 || snap.Extra.Unit != "credits") {
				t.Fatalf("额外额度 = %+v", snap.Extra)
			}
			if !tc.extra && snap.Extra != nil {
				t.Fatalf("不应有额外额度: %+v", snap.Extra)
			}
		})
	}
}

// fakeJWT 造一个只有 payload 有意义的 JWT（适配器只读 exp，不验签）。
func fakeJWT(exp time.Time, secret string) string {
	enc := base64.RawURLEncoding
	return enc.EncodeToString([]byte(`{"alg":"none"}`)) + "." +
		enc.EncodeToString([]byte(`{"exp":`+itoa(exp.Unix())+`,"s":"`+secret+`"}`)) + "." + secret
}

const codexRel = ".codex/auth.json"

func writeCodexAuth(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(home, codexRel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestCodexProbeHeadersFingerprintAndCredentialStates(t *testing.T) {
	var got http.Header
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, gotPath = r.Header.Clone(), r.URL.Path
		_, _ = w.Write(readTestdata(t, "codex_usage_200.synthetic.json"))
	}))
	defer srv.Close()
	token := fakeJWT(time.Now().Add(time.Hour), sentinelKey)
	home := writeCodexAuth(t, `{"OPENAI_API_KEY":null,"auth_mode":"chatgpt","last_refresh":"2026-10-22T00:00:00Z",`+
		`"tokens":{"access_token":"`+token+`","account_id":"acct-123","id_token":"x","refresh_token":"`+sentinelKey+`"}}`)
	a := &Codex{BaseURL: srv.URL, CredRelPath: codexRel}
	snap, err := a.Probe(context.Background(), Request{Kind: KindCodex, Home: home, Entry: "tokens"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if gotPath != "/backend-api/wham/usage" || got.Get("Authorization") != "Bearer "+token ||
		got.Get("ChatGPT-Account-Id") != "acct-123" || got.Get("User-Agent") != "codex-cli" ||
		got.Get("OpenAI-Beta") != "codex-1" || got.Get("originator") != "Codex Desktop" {
		t.Fatalf("请求形状不符: path=%s headers=%v", gotPath, redactHeader(got))
	}
	if snap.Fingerprint != Fingerprint("acct-123") || !strings.HasPrefix(snap.Fingerprint, "sha256:") || len(snap.Fingerprint) != len("sha256:")+12 {
		t.Fatalf("指纹应取 account_id 短哈希: %q", snap.Fingerprint)
	}

	cases := []struct {
		name string
		auth string
		want Category
	}{
		{name: "access_token 已过期", auth: `{"tokens":{"access_token":"` + fakeJWT(time.Now().Add(-time.Minute), sentinelKey) + `","account_id":"a"}}`, want: CategoryCredentialExpired},
		{name: "仅 API key 登录", auth: `{"OPENAI_API_KEY":"` + sentinelKey + `","tokens":null}`, want: CategoryCredentialInvalid},
		{name: "缺 tokens 条目", auth: `{"auth_mode":"chatgpt"}`, want: CategoryCredentialMissing},
		{name: "非 JSON", auth: `oops ` + sentinelKey, want: CategoryCredentialInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := a.Probe(context.Background(), Request{Kind: KindCodex, Home: writeCodexAuth(t, tc.auth), Entry: "tokens"})
			var f *Failure
			if !errors.As(err, &f) || f.Category != tc.want {
				t.Fatalf("want %s, got %v", tc.want, err)
			}
			if strings.Contains(err.Error(), sentinelKey) {
				t.Fatal("错误文本泄露凭据")
			}
			if tc.want == CategoryCredentialExpired && !strings.Contains(f.Message, "codex login") {
				t.Fatalf("过期文案应指路 codex login: %s", f.Message)
			}
		})
	}
}

func redactHeader(h http.Header) http.Header {
	c := h.Clone()
	c.Del("Authorization")
	return c
}
