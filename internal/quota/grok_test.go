// grok_test.go —— grok 额度适配回归（B453 段 2）。
//
// 职责：锁 ParseGrokBilling 的宽容解析表（creditUsagePercent→周、used/monthlyLimit→月、
// subscriptionTier→套餐、预付/按需进额外额度、未知字段忽略、401/403/429/5xx），与 Probe
// 的真实路径：条目按「键等于 issuer 或以 issuer:: 开头」选、多条取未过期且 expires_at
// 最晚；全部过期判 credential_expired（5 分钟偏斜，不自刷）；?format=credits 失败回退
// /billing；401 不回退；请求头形状；指纹取 user_id（缺则 email）。
// 边界：成功样本 testdata/grok_billing_{credits_,}200.real.json 是 R2（2026-10-22，gk-mbp）
// 真机取得的两个接口 200 正文：数值包在 {"val":N}、重置时刻在 config.currentPeriod.end /
// billingPeriodEnd、无 subscriptionTier、本账号 monthlyLimit=0（= 无月窗口）。有月窗口、
// 有额外额度的形态无真样本，用内联合成用例。auth.json 键形取自主会话只读键名事实。
package quota

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGrokParseBillingTable(t *testing.T) {
	weekEnd := mustTime(t, "2026-10-23T06:58:52.699759Z")
	monthEnd := mustTime(t, "2026-11-01T00:00:00Z")
	cases := []struct {
		name    string
		status  int
		body    string
		windows []Window
		failure Category
		plan    string
		extra   bool
	}{
		{name: "R2 真样本 credits：val 包装、currentPeriod.end、无套餐、额外额度全 0", status: 200,
			body:    string(readTestdata(t, "grok_billing_credits_200.real.json")),
			windows: []Window{{Key: WindowWeekly, UsedPercent: 64, ResetsAt: &weekEnd}}},
		{name: "R2 真样本 /billing：monthlyLimit=0 不出月窗口 → 无窗口", status: 200,
			body: string(readTestdata(t, "grok_billing_200.real.json")), failure: CategoryParse},
		{name: "合成：/billing 有月额度、回退 billingPeriodEnd", status: 200,
			body:    `{"config":{"monthlyLimit":{"val":200},"used":{"val":50},"billingPeriodEnd":"2026-11-01T00:00:00+00:00"}}`,
			windows: []Window{{Key: WindowMonthly, UsedPercent: 25, ResetsAt: &monthEnd}}},
		{name: "合成：有套餐与额外额度（val 包装）", status: 200,
			body:    `{"subscriptionTier":"SuperGrok","config":{"creditUsagePercent":42.5,"billingPeriodEnd":"2026-11-01T00:00:00+00:00","prepaidBalance":{"val":3.5},"onDemandUsed":{"val":1},"onDemandCap":{"val":10}}}`,
			windows: []Window{{Key: WindowWeekly, UsedPercent: 42.5, ResetsAt: &monthEnd}}, plan: "SuperGrok", extra: true},
		{name: "数字字符串", status: 200, body: `{"config":{"creditUsagePercent":"10"}}`,
			windows: []Window{{Key: WindowWeekly, UsedPercent: 10}}},
		{name: "没有任何窗口字段", status: 200, body: `{"subscriptionTier":"free"}`, failure: CategoryParse},
		{name: "401", status: 401, body: `{}`, failure: CategoryUnauthorized},
		{name: "403", status: 403, body: `{}`, failure: CategoryUnauthorized},
		{name: "429", status: 429, body: ``, failure: CategoryRateLimited},
		{name: "503", status: 503, body: `down`, failure: CategoryUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := ParseGrokBilling(tc.status, []byte(tc.body))
			if tc.failure != "" {
				var f *Failure
				if !errors.As(err, &f) || f.Category != tc.failure {
					t.Fatalf("want %s, got %+v %v", tc.failure, snap, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			assertWindows(t, snap.Windows, tc.windows)
			if snap.PlanType != tc.plan || (snap.Extra != nil) != tc.extra {
				t.Fatalf("plan=%q extra=%+v", snap.PlanType, snap.Extra)
			}
			if tc.extra && (snap.Extra.Balance == nil || *snap.Extra.Balance != 3.5 || *snap.Extra.Used != 1 || *snap.Extra.Limit != 10) {
				t.Fatalf("额外额度 = %+v", snap.Extra)
			}
		})
	}
}

const grokRel = ".grok/auth.json"

func writeGrokAuth(t *testing.T, content string) string {
	t.Helper()
	home := t.TempDir()
	p := filepath.Join(home, grokRel)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

func TestGrokProbeEntrySelectionFallbackAndExpiry(t *testing.T) {
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	later := time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	past := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	soon := time.Now().Add(2 * time.Minute).UTC().Format(time.RFC3339) // 5 分钟偏斜内 = 已过期
	var paths []string
	var gotAuth, gotTokenAuth, gotUser string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		gotAuth, gotTokenAuth, gotUser = r.Header.Get("Authorization"), r.Header.Get("X-XAI-Token-Auth"), r.Header.Get("x-userid")
		if r.URL.Query().Get("format") == "credits" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_, _ = w.Write([]byte(`{"config":{"monthlyLimit":{"val":100},"used":{"val":30}}}`))
	}))
	defer srv.Close()
	auth := `{"https://other.example::x":{"key":"wrong"},` +
		`"https://auth.x.ai::old":{"key":"k-old","user_id":"u-old","expires_at":"` + future + `"},` +
		`"https://auth.x.ai::new":{"key":"` + sentinelKey + `","user_id":"u-new","email":"a@b.c","expires_at":"` + later + `"},` +
		`"https://auth.x.ai::dead":{"key":"k-dead","user_id":"u-dead","expires_at":"` + past + `"}}`
	a := &Grok{BaseURL: srv.URL, CredRelPath: grokRel}
	snap, err := a.Probe(context.Background(), Request{Kind: KindGrok, Home: writeGrokAuth(t, auth), Entry: "https://auth.x.ai"})
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if len(paths) != 2 || paths[0] != "/v1/billing?format=credits" || paths[1] != "/v1/billing" {
		t.Fatalf("应先 credits 后回退 /billing: %v", paths)
	}
	if gotAuth != "Bearer "+sentinelKey || gotTokenAuth != "xai-grok-cli" || gotUser != "u-new" {
		t.Fatalf("应选未过期且最晚的条目并带头: tokenAuth=%q user=%q bearerOK=%v", gotTokenAuth, gotUser, gotAuth == "Bearer "+sentinelKey)
	}
	if snap.Fingerprint != Fingerprint("u-new") || len(snap.Windows) != 1 || snap.Windows[0].Key != WindowMonthly {
		t.Fatalf("指纹/窗口: %+v", snap)
	}

	// 裸 issuer 键同样认；全部过期（含 5 分钟偏斜内）判 credential_expired 且不调接口。
	before := len(paths)
	_, err = a.Probe(context.Background(), Request{Kind: KindGrok, Entry: "https://auth.x.ai",
		Home: writeGrokAuth(t, `{"https://auth.x.ai":{"key":"`+sentinelKey+`","expires_at":"`+soon+`"}}`)})
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryCredentialExpired || !strings.Contains(f.Message, "grok login") {
		t.Fatalf("偏斜内过期应判 credential_expired 并指路 grok login: %v", err)
	}
	if len(paths) != before {
		t.Fatal("凭据过期不得调接口")
	}
	_, err = a.Probe(context.Background(), Request{Kind: KindGrok, Entry: "https://auth.x.ai",
		Home: writeGrokAuth(t, `{"https://other.example":{"key":"x"}}`)})
	if !errors.As(err, &f) || f.Category != CategoryCredentialMissing {
		t.Fatalf("无匹配 issuer 应判 credential_missing: %v", err)
	}
}

func TestGrokProbeUnauthorizedDoesNotFallBack(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"bad ` + r.Header.Get("Authorization") + `"}`))
	}))
	defer srv.Close()
	a := &Grok{BaseURL: srv.URL, CredRelPath: grokRel}
	_, err := a.Probe(context.Background(), Request{Kind: KindGrok, Entry: "https://auth.x.ai",
		Home: writeGrokAuth(t, `{"https://auth.x.ai::u":{"key":"`+sentinelKey+`","user_id":"u"}}`)})
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryUnauthorized || calls.Load() != 1 {
		t.Fatalf("401 应直接判 unauthorized 不回退: calls=%d err=%v", calls.Load(), err)
	}
	if strings.Contains(err.Error(), sentinelKey) {
		t.Fatal("错误文本泄露凭据")
	}
}
