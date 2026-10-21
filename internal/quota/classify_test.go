package quota

// classify_test.go —— 回合失败三类分类的初版表（B453 段 3，plan Q10/Q19/V11 前半）。
//
// 样本来源：本表全部是**合成样本**（依据 orca 字段说明、OpenAI/xAI 公开错误码与检测链既有
// 字样），段 4 由 R6a 真样本替换/补充（plan U7）。承重断言是「限速不得判额度」与「不确定
// 判其他」，签名细节以真样本为准。

import (
	"strings"
	"testing"
	"time"
)

func TestClassifyTurnFailureThreeWay(t *testing.T) {
	cases := []struct {
		name, cli, kind, text string
		want                  TurnFailureClass
	}{
		{"codex 额度耗尽（usage_limit_reached）", "codex", "codex", `{"error":{"type":"usage_limit_reached","message":"You've hit your usage limit."}}`, TurnQuota},
		{"OpenAI insufficient_quota 走 429 也是额度", "codex", "", `429 Too Many Requests: {"error":{"code":"insufficient_quota"}}`, TurnQuota},
		{"opencode-go 无订阅（EntitlementError）", "opencode", "opencode-go", `403 Forbidden: EntitlementError: no active subscription`, TurnQuota},
		{"opencode 外壳 ENTITLEMENT_ERROR", "opencode", "", `ENTITLEMENT_ERROR: model requires subscription`, TurnQuota},
		{"grok credits 耗尽", "grok", "grok", `billing: out of credits`, TurnQuota},
		{"通用 quota exceeded", "opencode", "", `Error: quota exceeded for this account`, TurnQuota},
		{"429 限速", "codex", "codex", `HTTP 429 Too Many Requests`, TurnRateLimited},
		{"rate limit 字样", "opencode", "", `Rate limit reached for requests, retry after 20s`, TurnRateLimited},
		{"rate_limit_exceeded 码", "grok", "", `{"code":"rate_limit_exceeded"}`, TurnRateLimited},
		{"空文本判其他", "codex", "", "", TurnOther},
		{"截断文本判其他", "codex", "", `stream closed unexpectedly: unexpected EOF`, TurnOther},
		{"单独 quota 字样不确定 → 其他", "opencode", "", `checking quota...`, TurnOther},
		{"登录类不归本分类", "codex", "", `401 unauthorized: please login`, TurnOther},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyTurnFailure(tc.cli, tc.kind, tc.text)
			if got.Class != tc.want {
				t.Fatalf("ClassifyTurnFailure(%q,%q,%q) = %q，want %q", tc.cli, tc.kind, tc.text, got.Class, tc.want)
			}
			if got.Class != TurnOther && strings.TrimSpace(got.Signature) == "" {
				t.Fatalf("命中类别必须带签名（日志/标记里说清依据），得 %+v", got)
			}
		})
	}
}

// TestClassifyTurnFailureKindNarrowsSignatures 锁「账号 kind 缩小签名集合」：codex 账号不认
// opencode-go 的 EntitlementError 签名（另一家的错误不是这个账号额度耗尽）。
func TestClassifyTurnFailureKindNarrowsSignatures(t *testing.T) {
	text := `EntitlementError: no active subscription`
	if got := ClassifyTurnFailure("opencode", "opencode-go", text); got.Class != TurnQuota {
		t.Fatalf("opencode-go 账号应判额度，得 %+v", got)
	}
	if got := ClassifyTurnFailure("opencode", "codex", text); got.Class != TurnOther {
		t.Fatalf("codex 账号遇 opencode-go 签名应判其他，得 %+v", got)
	}
}

func TestCredentialFingerprintOfflineMatchesProbe(t *testing.T) {
	reg := NewRegistry(map[string]Prober{
		KindOpencodeGo: &OpencodeGo{CredRelPath: testCredRelPath},
		KindCodex:      &Codex{CredRelPath: codexRel},
		KindGrok:       &Grok{CredRelPath: grokRel},
		KindAgy:        &Agy{},
	})
	ogHome := writeOpencodeAuth(t, `{"opencode-go":{"type":"api","key":"`+sentinelKey+`"}}`)
	got, err := reg.CredentialFingerprint(KindOpencodeGo, ogHome, "")
	if err != nil || got != Fingerprint(sentinelKey) {
		t.Fatalf("opencode-go 离线指纹 = %q/%v，want %q", got, err, Fingerprint(sentinelKey))
	}
	// codex：access_token 已过期也照样取 account_id——指纹语义是「哪个账号」不是「token 还活着没」。
	cxHome := writeCodexAuth(t, `{"tokens":{"access_token":"`+fakeJWTForFP()+`","account_id":"acct-123"}}`)
	got, err = reg.CredentialFingerprint(KindCodex, cxHome, "")
	if err != nil || got != Fingerprint("acct-123") {
		t.Fatalf("codex 离线指纹 = %q/%v，want %q", got, err, Fingerprint("acct-123"))
	}
	gkHome := writeGrokAuth(t, `{"https://auth.x.ai::u1":{"key":"`+sentinelKey+`","user_id":"user-9","expires_at":1}}`)
	got, err = reg.CredentialFingerprint(KindGrok, gkHome, "")
	if err != nil || got != Fingerprint("user-9") {
		t.Fatalf("grok 离线指纹（已过期条目也取身份）= %q/%v，want %q", got, err, Fingerprint("user-9"))
	}
	got, err = reg.CredentialFingerprint(KindAgy, t.TempDir(), "")
	if err != nil || got != "" {
		t.Fatalf("agy 无离线身份来源，应空串不比对，得 %q/%v", got, err)
	}
	if _, err := reg.CredentialFingerprint(KindCodex, t.TempDir(), ""); err == nil {
		t.Fatal("凭据文件缺失应报错（调用方据此不比对）")
	} else if strings.Contains(err.Error(), sentinelKey) {
		t.Fatalf("错误文本泄密：%v", err)
	}
	if _, err := reg.CredentialFingerprint("claude", t.TempDir(), ""); err == nil {
		t.Fatal("未注册 kind 应报错")
	}
}

func fakeJWTForFP() string { return fakeJWT(timeLongAgo, sentinelKey) }

var timeLongAgo = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
