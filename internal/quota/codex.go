// codex.go —— codex（ChatGPT 账号登录）额度适配（B453 段 2）。
//
// 职责：只读 `<HOME>/.codex/auth.json › tokens`（access_token、account_id），以 Bearer
// 调 ChatGPT 内部接口 `GET <base>/backend-api/wham/usage`，把 primary/secondary 窗口按
// limit_window_seconds 归 session/weekly/monthly；allowed=false 或 limit_reached=true 判
// 「明示不可用」（plan Q4 归无额度）；credits 进额外额度；plan_type 作套餐。
// 接口与字段来自 b453-facts-providers §E / facts-orca §1（逆向资料，**未经真机**，U3）。
//
// 边界：只读凭据，绝不刷新 token——access_token 过期（JWT exp 已过）直接判
// credential_expired，等 codex CLI 自己刷新；只有 OPENAI_API_KEY 的登录没有订阅额度。
package quota

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"
)

const (
	codexDefaultBase  = "https://chatgpt.com"
	codexUsagePath    = "/backend-api/wham/usage"
	codexDefaultEntry = "tokens"
	codexLoginHint    = "等待 codex 刷新（在该机执行 codex login）"
)

// Codex 是 codex 适配器。零值字段取缺省。
type Codex struct {
	BaseURL     string        // 缺省 https://chatgpt.com
	CredRelPath string        // 凭据文件相对 HOME（.codex/auth.json）；空 = 本平台无已知落点
	Timeout     time.Duration // 缺省 20s
}

// Probe 实现 Prober。指纹取 tokens.account_id（不随 token 刷新而变）。
func (a *Codex) Probe(ctx context.Context, req Request) (Snapshot, error) {
	entry := req.Entry
	if entry == "" {
		entry = codexDefaultEntry
	}
	token, accountID, secrets, err := a.readTokens(req.Home, entry)
	if err != nil {
		log().Warn("codex 凭据读取失败", "home", req.Home, "entry", entry, "cause", err)
		return Snapshot{}, err
	}
	headers := map[string]string{
		"Authorization": "Bearer " + token, "Accept": "application/json",
		"User-Agent": "codex-cli", "OpenAI-Beta": "codex-1", "originator": "Codex Desktop",
	}
	if accountID != "" {
		headers["ChatGPT-Account-Id"] = accountID
	}
	base := a.BaseURL
	if base == "" {
		base = codexDefaultBase
	}
	status, body, err := getJSON(ctx, KindCodex, strings.TrimRight(base, "/")+codexUsagePath, headers,
		req.Proxy, a.Timeout, secrets...)
	if err != nil {
		return Snapshot{}, err
	}
	snap, err := ParseCodexUsage(status, body, time.Now())
	if err != nil {
		log().Warn("codex 额度接口结果为失败", "status", status, "cause", err)
		return Snapshot{}, err
	}
	snap.Fingerprint = Fingerprint(accountID)
	return snap, nil
}

// codexTokens 是 auth.json › tokens 条目里本适配用到的字段。
type codexTokens struct {
	AccessToken  string `json:"access_token"`
	AccountID    string `json:"account_id"`
	IDToken      string `json:"id_token"`
	RefreshToken string `json:"refresh_token"`
}

// loadTokens 只读解析条目，不判过期（指纹取材与探测共用）。
func (a *Codex) loadTokens(home, entry string) (codexTokens, error) {
	top, path, err := readCredJSON(home, a.CredRelPath, "在该机执行 codex login")
	if err != nil {
		return codexTokens{}, err
	}
	raw, ok := top[entry]
	if !ok || string(raw) == "null" {
		if key, has := top["OPENAI_API_KEY"]; has && string(key) != "null" && string(key) != `""` {
			return codexTokens{}, fail(CategoryCredentialInvalid,
				"凭据文件 %s 只有 OPENAI_API_KEY（按量 API key 登录），没有 ChatGPT 订阅额度可查", path)
		}
		return codexTokens{}, fail(CategoryCredentialMissing, "凭据文件 %s 里没有条目 %q（在该机执行 codex login）", path, entry)
	}
	var t codexTokens
	if err := json.Unmarshal(raw, &t); err != nil || strings.TrimSpace(t.AccessToken) == "" {
		return codexTokens{}, fail(CategoryCredentialInvalid, "凭据条目 %q 缺少 access_token", entry)
	}
	return t, nil
}

// readTokens 取 access_token 与 account_id；返回的 secrets 是要从正文/错误里抹掉的全部值。
func (a *Codex) readTokens(home, entry string) (token, accountID string, secrets []string, err error) {
	t, err := a.loadTokens(home, entry)
	if err != nil {
		return "", "", nil, err
	}
	secrets = []string{t.AccessToken, t.IDToken, t.RefreshToken}
	if exp, ok := jwtExpiry(t.AccessToken); ok && !exp.After(time.Now()) {
		return "", "", nil, fail(CategoryCredentialExpired, "codex 凭据已过期（%s 到期），%s",
			exp.Format(time.RFC3339), codexLoginHint)
	}
	return t.AccessToken, t.AccountID, secrets, nil
}

// Identity 实现 Identifier：tokens.account_id（token 过期也照取——指纹问的是「哪个账号」）。
func (a *Codex) Identity(home, entry string) (string, error) {
	if entry == "" {
		entry = codexDefaultEntry
	}
	t, err := a.loadTokens(home, entry)
	if err != nil {
		return "", err
	}
	return t.AccountID, nil
}

// jwtExpiry 读 JWT payload 的 exp（不验签——只用来判断是否值得发请求）。
func jwtExpiry(token string) (time.Time, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return time.Time{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return time.Time{}, false
	}
	var claims struct {
		Exp float64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp <= 0 {
		return time.Time{}, false
	}
	return time.Unix(int64(claims.Exp), 0).UTC(), true
}

// ParseCodexUsage 把 wham/usage 的状态码与响应体解析成读数（外部缝上的解析入口）。
// now 用于 reset_after_seconds（reset_at 缺席时的兜底）。
func ParseCodexUsage(status int, body []byte, now time.Time) (Snapshot, error) {
	if err := statusFailure(KindCodex, status, body, "codex 凭据已失效或过期，"+codexLoginHint); err != nil {
		return Snapshot{}, err
	}
	type window struct {
		UsedPercent        *float64       `json:"used_percent"`
		LimitWindowSeconds float64        `json:"limit_window_seconds"`
		ResetAfterSeconds  *float64       `json:"reset_after_seconds"`
		ResetAt            flexibleMoment `json:"reset_at"`
	}
	var doc struct {
		PlanType  string `json:"plan_type"`
		RateLimit *struct {
			Allowed      *bool   `json:"allowed"`
			LimitReached *bool   `json:"limit_reached"`
			Primary      *window `json:"primary_window"`
			Secondary    *window `json:"secondary_window"`
		} `json:"rate_limit"`
		Credits *struct {
			HasCredits bool `json:"has_credits"`
			Unlimited  bool `json:"unlimited"`
			Balance    any  `json:"balance"`
		} `json:"credits"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Snapshot{}, fail(CategoryParse, "响应不是合法 JSON：%s", snippet(body))
	}
	if doc.RateLimit == nil {
		return Snapshot{}, fail(CategoryParse, "响应缺少 rate_limit：%s", snippet(body))
	}
	out := Snapshot{PlanType: doc.PlanType}
	for i, w := range []*window{doc.RateLimit.Primary, doc.RateLimit.Secondary} {
		if w == nil {
			continue
		}
		if w.UsedPercent == nil {
			return Snapshot{}, fail(CategoryParse, "rate_limit 窗口缺少 used_percent：%s", snippet(body))
		}
		reset := w.ResetAt.t
		if reset == nil && w.ResetAfterSeconds != nil {
			t := now.Add(time.Duration(*w.ResetAfterSeconds * float64(time.Second))).UTC()
			reset = &t
		}
		out.Windows = append(out.Windows, Window{Key: codexWindowKey(w.LimitWindowSeconds, i),
			UsedPercent: clampPercent(*w.UsedPercent), ResetsAt: reset})
	}
	if len(out.Windows) == 0 {
		return Snapshot{}, fail(CategoryParse, "rate_limit 里没有任何窗口：%s", snippet(body))
	}
	rl := doc.RateLimit
	if (rl.Allowed != nil && !*rl.Allowed) || (rl.LimitReached != nil && *rl.LimitReached) {
		out.Blocked = fail(CategoryLimitReached, "codex 额度已用尽（allowed=false 或 limit_reached=true）")
	}
	if c := doc.Credits; c != nil {
		bal, hasBal := num(c.Balance)
		if c.HasCredits || c.Unlimited || (hasBal && bal > 0) {
			out.Extra = &Extra{Unit: "credits", Unlimited: c.Unlimited}
			if hasBal {
				out.Extra.Balance = &bal
			}
		}
	}
	return out, nil
}

// codexWindowKey 按窗口长度归类（不要假定 primary=5h：Team 计划可能只有周窗口且放在
// primary）。长度缺席时按位置兜底：primary → session、secondary → weekly。
func codexWindowKey(seconds float64, index int) string {
	switch {
	case seconds <= 0 && index == 0:
		return WindowSession
	case seconds <= 0:
		return WindowWeekly
	case seconds <= 24*3600:
		return WindowSession
	case seconds <= 10*24*3600:
		return WindowWeekly
	default:
		return WindowMonthly
	}
}
