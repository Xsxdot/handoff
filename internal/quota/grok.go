// grok.go —— grok（xAI Grok CLI 登录）额度适配（B453 段 2）。
//
// 职责：只读 `<HOME>/.grok/auth.json` 里首选 issuer 的条目（键等于 issuer 或以
// `issuer::` 开头——真机键形为 `https://auth.x.ai::<uuid>`；多条时取未过期且 expires_at
// 最晚者），以 Bearer + `X-XAI-Token-Auth: xai-grok-cli` 调
// `GET <base>/v1/billing?format=credits`，失败（非鉴权类）回退 `/v1/billing`。
// creditUsagePercent → 周（重置 = config.currentPeriod.end，回退 billingPeriodEnd）、
// used/monthlyLimit → 月（monthlyLimit=0 = 无月窗口；重置 = billingPeriodEnd）、
// subscriptionTier → 套餐（缺席则不报）、prepaidBalance/onDemandUsed/onDemandCap → 额外额度。
// R2 真样本证实数值包在 {"val":N}、字段在 config 下；仍在顶层与 config/billing 里宽容查找。
// 与 orca 同：credits 成功即用，不再为月窗口追调 /billing（失败才回退）。
//
// 边界：只读凭据，不自刷新——过期（含 5 分钟偏斜）直接判 credential_expired，等 grok CLI 刷新。
package quota

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	grokDefaultBase   = "https://cli-chat-proxy.grok.com"
	grokDefaultIssuer = "https://auth.x.ai"
	grokExpirySkew    = 5 * time.Minute
	grokLoginHint     = "等待 grok 刷新（在该机执行 grok login）"
)

// Grok 是 grok 适配器。零值字段取缺省。
type Grok struct {
	BaseURL     string        // 缺省 https://cli-chat-proxy.grok.com
	CredRelPath string        // 凭据文件相对 HOME（.grok/auth.json）；空 = 本平台无已知落点
	Timeout     time.Duration // 缺省 20s
}

type grokEntry struct {
	Key       string         `json:"key"`
	UserID    string         `json:"user_id"`
	Email     string         `json:"email"`
	ExpiresAt flexibleMoment `json:"expires_at"`
}

// Probe 实现 Prober。指纹取条目 user_id（缺则 email）。
func (a *Grok) Probe(ctx context.Context, req Request) (Snapshot, error) {
	issuer := req.Entry
	if issuer == "" {
		issuer = grokDefaultIssuer
	}
	e, err := a.pickEntry(req.Home, issuer)
	if err != nil {
		log().Warn("grok 凭据读取失败", "home", req.Home, "issuer", issuer, "cause", err)
		return Snapshot{}, err
	}
	headers := map[string]string{
		"Authorization": "Bearer " + e.Key, "X-XAI-Token-Auth": "xai-grok-cli", "Accept": "application/json",
	}
	if e.UserID != "" {
		headers["x-userid"] = e.UserID
	}
	base := a.BaseURL
	if base == "" {
		base = grokDefaultBase
	}
	base = strings.TrimRight(base, "/")
	var snap Snapshot
	for i, path := range []string{"/v1/billing?format=credits", "/v1/billing"} {
		status, body, gerr := getJSON(ctx, KindGrok, base+path, headers, req.Proxy, a.Timeout, e.Key)
		if gerr != nil {
			return Snapshot{}, gerr // 传输层失败：换路径也一样，不回退
		}
		snap, err = ParseGrokBilling(status, body)
		if err == nil {
			break
		}
		if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests || i == 1 {
			log().Warn("grok 额度接口结果为失败", "path", path, "status", status, "cause", err)
			return Snapshot{}, err
		}
		log().Info("grok credits 接口失败，回退 /v1/billing", "status", status, "cause", err)
	}
	identity := e.UserID
	if identity == "" {
		identity = e.Email
	}
	snap.Fingerprint = Fingerprint(identity)
	return snap, nil
}

// pickEntry 选首选 issuer 的条目：键等于 issuer 或以 `issuer::` 开头；多条取未过期且
// expires_at 最晚（无 expires_at 视为不过期、排在有时刻者之后）。全部过期 → credential_expired。
func (a *Grok) pickEntry(home, issuer string) (grokEntry, error) {
	top, path, err := readCredJSON(home, a.CredRelPath, "在该机执行 grok login")
	if err != nil {
		return grokEntry{}, err
	}
	var best *grokEntry
	matched, expired := 0, 0
	deadline := time.Now().Add(grokExpirySkew)
	for k, raw := range top {
		if k != issuer && !strings.HasPrefix(k, issuer+"::") {
			continue
		}
		matched++
		var e grokEntry
		if json.Unmarshal(raw, &e) != nil || strings.TrimSpace(e.Key) == "" {
			continue
		}
		if e.ExpiresAt.t != nil && !e.ExpiresAt.t.After(deadline) {
			expired++
			continue
		}
		if best == nil || laterExpiry(e, *best) {
			c := e
			best = &c
		}
	}
	switch {
	case best != nil:
		return *best, nil
	case expired > 0:
		return grokEntry{}, fail(CategoryCredentialExpired, "grok 凭据已过期，%s", grokLoginHint)
	case matched > 0:
		return grokEntry{}, fail(CategoryCredentialInvalid, "凭据文件 %s 里 issuer %q 的条目缺少 key", path, issuer)
	default:
		return grokEntry{}, fail(CategoryCredentialMissing, "凭据文件 %s 里没有 issuer %q 的条目（在该机执行 grok login）", path, issuer)
	}
}

// Identity 实现 Identifier：首选 issuer 条目的 user_id（缺则 email）。不判过期——指纹问的是
// 「哪个账号」；多条时与 pickEntry 同序取 expires_at 最晚者。
func (a *Grok) Identity(home, issuer string) (string, error) {
	if issuer == "" {
		issuer = grokDefaultIssuer
	}
	top, path, err := readCredJSON(home, a.CredRelPath, "在该机执行 grok login")
	if err != nil {
		return "", err
	}
	var best *grokEntry
	for k, raw := range top {
		if k != issuer && !strings.HasPrefix(k, issuer+"::") {
			continue
		}
		var e grokEntry
		if json.Unmarshal(raw, &e) != nil {
			continue
		}
		if best == nil || laterExpiry(e, *best) {
			c := e
			best = &c
		}
	}
	if best == nil {
		return "", fail(CategoryCredentialMissing, "凭据文件 %s 里没有 issuer %q 的条目（在该机执行 grok login）", path, issuer)
	}
	if best.UserID != "" {
		return best.UserID, nil
	}
	return best.Email, nil
}

func laterExpiry(a, b grokEntry) bool {
	switch {
	case a.ExpiresAt.t == nil:
		return false
	case b.ExpiresAt.t == nil:
		return true
	default:
		return a.ExpiresAt.t.After(*b.ExpiresAt.t)
	}
}

// ParseGrokBilling 把 billing 接口的状态码与响应体解析成读数（外部缝上的解析入口）。
// 字段在顶层或 config/billing 子对象里查找；未知字段忽略。没有任何窗口字段 → parse。
func ParseGrokBilling(status int, body []byte) (Snapshot, error) {
	if err := statusFailure(KindGrok, status, body, "grok 凭据已失效或过期，"+grokLoginHint); err != nil {
		return Snapshot{}, err
	}
	var root map[string]any
	if err := json.Unmarshal(body, &root); err != nil {
		return Snapshot{}, fail(CategoryParse, "响应不是合法 JSON：%s", snippet(body))
	}
	find := func(key string) (any, bool) {
		for _, scope := range []map[string]any{root, asMap(root["config"]), asMap(root["billing"])} {
			if v, ok := scope[key]; ok && v != nil {
				return v, true
			}
		}
		return nil, false
	}
	// 重置时刻：周窗口取 currentPeriod.end（R2 真样本），回退 billingPeriodEnd；月窗口取 billingPeriodEnd。
	periodEnd := momentOf(find("billingPeriodEnd"))
	weekReset := periodEnd
	if cp, ok := find("currentPeriod"); ok {
		if t := momentOf(asMap(cp)["end"], true); t != nil {
			weekReset = t
		}
	}
	var out Snapshot
	if v, ok := find("creditUsagePercent"); ok {
		if p, ok := num(v); ok {
			out.Windows = append(out.Windows, Window{Key: WindowWeekly, UsedPercent: clampPercent(p), ResetsAt: weekReset})
		}
	}
	limitV, hasLimit := find("monthlyLimit")
	usedV, hasUsed := find("used")
	if hasLimit && hasUsed {
		limit, ok1 := num(limitV)
		used, ok2 := num(usedV)
		// monthlyLimit=0 表示没有月额度（R2 真样本），不出月窗口。
		if ok1 && ok2 && limit > 0 {
			out.Windows = append(out.Windows, Window{Key: WindowMonthly, UsedPercent: clampPercent(used / limit * 100), ResetsAt: periodEnd})
		}
	}
	if len(out.Windows) == 0 {
		return Snapshot{}, fail(CategoryParse, "响应里没有可用窗口（无 creditUsagePercent，且 monthlyLimit 缺席或为 0）：%s", snippet(body))
	}
	if v, ok := find("subscriptionTier"); ok {
		if s, ok := v.(string); ok {
			out.PlanType = s
		}
	}
	var extra Extra
	has := false
	for key, dst := range map[string]**float64{"prepaidBalance": &extra.Balance, "onDemandUsed": &extra.Used, "onDemandCap": &extra.Limit} {
		if v, ok := find(key); ok {
			if f, ok := num(v); ok {
				val := f
				*dst = &val
				has = has || f > 0
			}
		}
	}
	if has {
		out.Extra = &extra
	}
	return out, nil
}

// momentOf 把宽容取到的字段值解析成时刻（RFC3339/时间戳）；取不到或解析失败为 nil。
func momentOf(v any, ok bool) *time.Time {
	if !ok || v == nil {
		return nil
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	t, err := parseMoment(raw)
	if err != nil {
		return nil
	}
	return t
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}
