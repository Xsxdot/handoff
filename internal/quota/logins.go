// logins.go —— 「这个 HOME 里有哪些已登录身份」的离线取材（B464 S1，plan §1 quota 行、V2）。
//
// 职责：发现器逐 HOME 调 Registry.Logins，各适配器只读凭据文件/库、认出已登录身份，交出
// 来源名、身份短指纹（Fingerprint）、可读 label、到期时刻、能否自刷新与凭据变更时刻——原始
// 身份材料（key、账号 id、user_id、sub、accountUuid）只在本包内存里参与哈希，从不出包
// （spec「密钥不离机」）。各 kind 的身份材料（plan L2，段 0 R0 结论）：
//   - opencode-go：auth.json `opencode-go` 条目的 key / opencode.db 当前账号「账号 id/org id」；
//   - apikey：auth.json 里 type=api 且名 ≠ opencode-go 的条目 key（provider = 条目名，Q12）；
//   - codex：auth.json › tokens.account_id（label = id_token 的 email claim）；只有
//     OPENAI_API_KEY 不认作登录（Q19）；
//   - grok：auth.json 里键等于 `https://auth.x.ai` 或以 `https://auth.x.ai::` 开头的条目
//     （R0-6），user_id，缺则 email；
//   - agy：`.gemini/antigravity-cli/antigravity-oauth-token` 的 id_token `sub`（label = email，
//     Q7 分支 a）；
//   - claude：见 claude_login.go（`claude auth status --json` 的 loggedIn + `.claude.json ›
//     oauthAccount.accountUuid`）。
//
// 边界：不出网、不刷新、不改写任何文件；「没登录」（凭据缺失）不是错误，只有形状不对、
// 读不了等「看不清」才报问题（发现器据此不对该 HOME 下「已失效」结论）。
package quota

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// 各 kind 的凭据来源名（登录行 source，登录槽位的一部分——改名 = 换槽位，旧登录标已失效）。
const (
	sourceCodexAuth  = "auth.json"
	sourceGrokAuth   = "auth.json"
	sourceAgyToken   = "antigravity-oauth-token"
	sourceClaudeAuth = "claude-auth"
)

// agyTokenRelPath 是 agy 登录 token 相对 HOME 的落点（R0-3 真机读数）。
const agyTokenRelPath = ".gemini/antigravity-cli/antigravity-oauth-token"

// LoginFinding 是一个 HOME 里离线认出的一份登录。Fingerprint = `sha256:<hex12>`。
//
// Refreshable = 凭据里有 refresh token（CLI 会自刷新，离线到期已过不等于过期，plan Q5）；
// ChangedAt = 凭据最近变更的时刻（文件 mtime，或库里账号行的更新时刻）：晚于登录的失效证据
// 即视为「已重新登录/已刷新」，登录回正常。取不到为 nil。
type LoginFinding struct {
	Kind        string
	Provider    string // 仅第三方 key（apikey）
	Source      string // 凭据来源名：auth.json / opencode.db / antigravity-oauth-token / claude-auth
	Fingerprint string
	Label       string // 可读身份（邮箱）；取不到为空
	ExpiresAt   *time.Time
	Refreshable bool
	ChangedAt   *time.Time
}

// LoginReader 是能离线列出 HOME 下已登录身份的适配。返回的问题一律是 *Failure，文案只带
// 路径与来源名。home 是调用方已展开的绝对路径。
type LoginReader interface {
	Logins(home string) ([]LoginFinding, []error)
}

// RegisterLoginReader 登记一个只有离线取材、不支持额度探测的 kind（claude）。只在组装期、
// 注册表投入使用前调用（之后注册表只读，并发安全）。
func (r *Registry) RegisterLoginReader(kind string, lr LoginReader) {
	if r.readers == nil {
		r.readers = map[string]LoginReader{}
	}
	r.readers[kind] = lr
}

// Logins 按 kind 字典序问每个实现了 LoginReader 的适配器（含 RegisterLoginReader 登记的），
// 汇总该 HOME 的登录与问题。keep 非空时只问 keep(kind) 为真的 kind——被过滤的 kind 不读
// 文件、不起子进程（发现器按 kind 规则在 darwin 非主 HOME 跳过 claude，plan Q6 分支 b）。
func (r *Registry) Logins(home string, keep ...func(kind string) bool) ([]LoginFinding, []error) {
	readers := map[string]LoginReader{}
	for k, a := range r.adapters {
		if lr, ok := a.(LoginReader); ok {
			readers[k] = lr
		}
	}
	for k, lr := range r.readers {
		readers[k] = lr
	}
	kinds := make([]string, 0, len(readers))
	for k := range readers {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var found []LoginFinding
	var problems []error
	for _, k := range kinds {
		if !keepKind(keep, k) {
			log().Debug("离线取材：按 kind 规则跳过", "home", home, "kind", k)
			continue
		}
		f, p := readers[k].Logins(home)
		found = append(found, f...)
		problems = append(problems, p...)
	}
	log().Debug("离线取材完成", "home", home, "logins", len(found), "problems", len(problems))
	return found, problems
}

func keepKind(keep []func(string) bool, kind string) bool {
	for _, k := range keep {
		if k != nil && !k(kind) {
			return false
		}
	}
	return true
}

// fileChangedAt 返回文件 mtime（UTC）；取不到为 nil。
func fileChangedAt(path string) *time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	t := info.ModTime().UTC()
	return &t
}

// jwtClaims 解 JWT payload（不验签——只取 email/sub 等身份 claim）。形状不对返回 false。
func jwtClaims(token string) (map[string]any, bool) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil, false
	}
	var claims map[string]any
	if json.Unmarshal(payload, &claims) != nil {
		return nil, false
	}
	return claims, true
}

func claimString(claims map[string]any, key string) string {
	s, _ := claims[key].(string)
	return strings.TrimSpace(s)
}

// classifyAbsent 把一次来源读取的失败分成三类：没登录（凭据缺失，返回 nil, false）、本平台无
// 已知落点（跳过，nil, false）、看不清（返回问题, true）。问题文案保证点名路径。
func classifyAbsent(err error, path string) (error, bool) {
	var f *Failure
	if !errors.As(err, &f) {
		return fail(CategoryCredentialInvalid, "%s：%v", path, err), true
	}
	switch f.Category {
	case CategoryCredentialMissing, CategoryUnsupportedPlatform:
		return nil, false
	}
	if path != "" && !strings.Contains(f.Message, path) {
		return fail(f.Category, "%s：%s", path, f.Message), true
	}
	return f, true
}

// Logins 实现 LoginReader：auth.json 的 opencode-go API key 条目与 opencode.db 的当前登录账号
// 各是一份 opencode-go 登录，身份材料不同（key 本身 / 「账号 id/org id」），天然不互认
// （spec S1）；auth.json 里其余 type=api 的条目各是一份第三方 key 登录（kind apikey，provider =
// 条目名，plan Q12），type=oauth 的第三方条目不认（记 Debug）。都只读；没登录不是问题。
func (a *OpencodeGo) Logins(home string) ([]LoginFinding, []error) {
	if a.CredRelPath == "" {
		return nil, nil // 本平台无已知落点（unsupported_platform）：静默跳过
	}
	var out []LoginFinding
	var problems []error
	authPath := filepath.Join(home, a.CredRelPath)
	authChanged := fileChangedAt(authPath)
	if key, err := a.readKey(home, opencodeGoDefaultEntry); err == nil {
		out = append(out, LoginFinding{Kind: KindOpencodeGo, Source: sourceAuthJSON, Fingerprint: Fingerprint(key),
			ChangedAt: authChanged})
	} else if p, bad := classifyAbsent(err, authPath); bad {
		log().Warn("opencode-go auth.json 看不清，本轮不认该来源", "home", home, "cause", p)
		problems = append(problems, p)
	}
	out = append(out, a.thirdPartyKeys(home, authChanged)...)
	dbPath := filepath.Join(home, a.dbRelPath())
	if cred, err := a.loginCred(home); err == nil {
		refreshable, updated := loginRefreshInfo(home, a.dbRelPath(), cred.login.id)
		out = append(out, LoginFinding{Kind: KindOpencodeGo, Source: sourceLoginDB,
			Fingerprint: Fingerprint(cred.identity), ExpiresAt: cred.login.expiry, Refreshable: refreshable, ChangedAt: updated})
	} else if p, bad := classifyAbsent(err, dbPath); bad {
		log().Warn("opencode-go opencode.db 看不清，本轮不认该来源", "home", home, "cause", p)
		problems = append(problems, p)
	}
	return out, problems
}

// thirdPartyKeys 认 auth.json 里 type=api、名 ≠ opencode-go、key 非空的条目（plan Q12）。文件
// 读不了/不是 JSON 已由 opencode-go 条目那一路报过问题，这里静默。
func (a *OpencodeGo) thirdPartyKeys(home string, changed *time.Time) []LoginFinding {
	entries, _, err := readCredJSON(home, a.CredRelPath, "")
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []LoginFinding
	for _, name := range names {
		if name == opencodeGoDefaultEntry {
			continue
		}
		var item struct {
			Type string `json:"type"`
			Key  string `json:"key"`
		}
		if json.Unmarshal(entries[name], &item) != nil {
			log().Debug("opencode auth.json 条目形状无法解析，不认作第三方 key", "home", home, "entry", name)
			continue
		}
		switch {
		case item.Type != "api":
			log().Debug("opencode auth.json 非 API key 条目不认作登录（Q12）", "home", home, "entry", name, "type", item.Type)
			continue
		case strings.TrimSpace(item.Key) == "":
			log().Debug("opencode auth.json API key 条目 key 为空，不认", "home", home, "entry", name)
			continue
		}
		out = append(out, LoginFinding{Kind: KindAPIKey, Provider: name, Source: sourceAuthJSON,
			Fingerprint: Fingerprint(item.Key), ChangedAt: changed})
	}
	return out
}

// Logins 实现 LoginReader：auth.json › tokens 有 account_id 即一份 codex 登录。label 取
// id_token 的 email claim，到期取 access_token 的 exp，有 refresh_token = 可自刷新。只有
// OPENAI_API_KEY（无订阅身份）不认作登录，记 Info（plan Q19）。
func (a *Codex) Logins(home string) ([]LoginFinding, []error) {
	top, path, err := readCredJSON(home, a.CredRelPath, "在该机执行 codex login")
	if err != nil {
		if p, bad := classifyAbsent(err, path); bad {
			log().Warn("codex auth.json 看不清，本轮不认", "home", home, "cause", p)
			return nil, []error{p}
		}
		return nil, nil
	}
	raw, ok := top[codexDefaultEntry]
	if !ok || string(raw) == "null" {
		if key, has := top["OPENAI_API_KEY"]; has && string(key) != "null" && string(key) != `""` {
			log().Info("codex HOME 只有 OPENAI_API_KEY（按量 key，无订阅身份），不认作 codex 登录", "home", home)
		}
		return nil, nil
	}
	var t codexTokens
	if err := json.Unmarshal(raw, &t); err != nil || strings.TrimSpace(t.AccountID) == "" {
		p := fail(CategoryCredentialInvalid, "凭据文件 %s 的 tokens 条目缺少 account_id，认不出账号", path)
		log().Warn("codex auth.json 形状不对，本轮不认", "home", home, "cause", p)
		return nil, []error{p}
	}
	f := LoginFinding{Kind: KindCodex, Source: sourceCodexAuth, Fingerprint: Fingerprint(t.AccountID),
		Refreshable: strings.TrimSpace(t.RefreshToken) != "", ChangedAt: fileChangedAt(path)}
	if claims, ok := jwtClaims(t.IDToken); ok {
		f.Label = claimString(claims, "email")
	}
	if exp, ok := jwtExpiry(t.AccessToken); ok {
		e := exp.UTC()
		f.ExpiresAt = &e
	}
	return []LoginFinding{f}, nil
}

// Logins 实现 LoginReader：首选 issuer 的条目（键等于 issuer 或以 `issuer::` 开头，R0-6 真机
// 键形为 `https://auth.x.ai::<uuid>`）里 expires_at 最晚的一条是一份 grok 登录（已过期也照认
// 身份——过期与否归登录状态，plan Q5）。身份 user_id，缺则 email；label email。
func (a *Grok) Logins(home string) ([]LoginFinding, []error) {
	top, path, err := readCredJSON(home, a.CredRelPath, "在该机执行 grok login")
	if err != nil {
		if p, bad := classifyAbsent(err, path); bad {
			log().Warn("grok auth.json 看不清，本轮不认", "home", home, "cause", p)
			return nil, []error{p}
		}
		return nil, nil
	}
	var best *grokEntry
	for k, raw := range top {
		if k != grokDefaultIssuer && !strings.HasPrefix(k, grokDefaultIssuer+"::") {
			continue
		}
		var e grokEntry
		if json.Unmarshal(raw, &e) != nil {
			log().Debug("grok 条目形状无法解析，跳过", "home", home)
			continue
		}
		if best == nil || laterExpiry(e, *best) {
			c := e
			best = &c
		}
	}
	if best == nil {
		return nil, nil
	}
	identity := strings.TrimSpace(best.UserID)
	if identity == "" {
		identity = strings.TrimSpace(best.Email)
	}
	if identity == "" {
		p := fail(CategoryCredentialInvalid, "凭据文件 %s 里 issuer %q 的条目既无 user_id 也无 email，认不出账号", path, grokDefaultIssuer)
		log().Warn("grok auth.json 形状不对，本轮不认", "home", home, "cause", p)
		return nil, []error{p}
	}
	return []LoginFinding{{Kind: KindGrok, Source: sourceGrokAuth, Fingerprint: Fingerprint(identity),
		Label: strings.TrimSpace(best.Email), ExpiresAt: best.ExpiresAt.t,
		Refreshable: strings.TrimSpace(best.RefreshToken) != "", ChangedAt: fileChangedAt(path)}}, nil
}

// agyTokenFile 是 antigravity-oauth-token 里本适配用到的字段（R0-3 键形）。
type agyTokenFile struct {
	IDToken string `json:"id_token"`
	Token   struct {
		Expiry       flexibleMoment `json:"expiry"`
		RefreshToken string         `json:"refresh_token"`
	} `json:"token"`
}

// Logins 实现 LoginReader（plan Q7 分支 a）：只读 token 文件，id_token 的 sub 是身份、email 是
// label，token.expiry 是到期，有 refresh_token = 可自刷新。不跑 `agy -p /usage`。
func (a *Agy) Logins(home string) ([]LoginFinding, []error) {
	rel := a.TokenRelPath
	if rel == "" {
		rel = agyTokenRelPath
	}
	top, path, err := readCredJSON(home, rel, "在该机以该 HOME 交互运行 agy 登录")
	if err != nil {
		if p, bad := classifyAbsent(err, path); bad {
			log().Warn("agy token 文件看不清，本轮不认", "home", home, "cause", p)
			return nil, []error{p}
		}
		return nil, nil
	}
	raw, _ := json.Marshal(top)
	var tf agyTokenFile
	if err := json.Unmarshal(raw, &tf); err != nil {
		p := fail(CategoryCredentialInvalid, "agy token 文件 %s 形状无法解析", path)
		log().Warn("agy token 文件形状不对，本轮不认", "home", home, "cause", p)
		return nil, []error{p}
	}
	claims, ok := jwtClaims(tf.IDToken)
	sub := claimString(claims, "sub")
	if !ok || sub == "" {
		p := fail(CategoryCredentialInvalid, "agy token 文件 %s 的 id_token 缺失或没有 sub，认不出账号", path)
		log().Warn("agy token 文件形状不对，本轮不认", "home", home, "cause", p)
		return nil, []error{p}
	}
	return []LoginFinding{{Kind: KindAgy, Source: sourceAgyToken, Fingerprint: Fingerprint(sub),
		Label: claimString(claims, "email"), ExpiresAt: tf.Token.Expiry.t,
		Refreshable: strings.TrimSpace(tf.Token.RefreshToken) != "", ChangedAt: fileChangedAt(path)}}, nil
}
