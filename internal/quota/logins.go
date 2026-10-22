// logins.go —— 「这个 HOME 里有哪些已登录身份」的离线取材（B464 S1，plan §1 quota 行）。
//
// 职责：发现器逐 HOME 调 Registry.Logins，各适配器只读凭据文件/库、认出已登录身份，交出
// 来源名、身份短指纹（Fingerprint）、可读 label 与到期时刻——原始身份材料（key、账号 id、
// org id）只在本包内存里参与哈希，从不出包（spec「密钥不离机」）。
//
// 边界：不出网、不刷新、不改写任何文件；「没登录」（凭据缺失）不是错误，只有形状不对、
// 读不了等「看不清」才报问题（发现器据此不对该 HOME 下「已失效」结论）。claude/agy/
// codex/grok/第三方 key 的取材在 B464 段 2（依赖段 0 真机结论），段 1 只有 opencode-go。
package quota

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// LoginFinding 是一个 HOME 里离线认出的一份登录。Fingerprint = `sha256:<hex12>`。
type LoginFinding struct {
	Kind        string
	Provider    string // 仅第三方 key（段 2）
	Source      string // 凭据来源名：auth.json / opencode.db …
	Fingerprint string
	Label       string // 可读身份（邮箱）；段 1 不取
	ExpiresAt   *time.Time
}

// LoginReader 是能离线列出 HOME 下已登录身份的适配。返回的问题一律是 *Failure，文案只带
// 路径与来源名。home 是调用方已展开的绝对路径。
type LoginReader interface {
	Logins(home string) ([]LoginFinding, []error)
}

// Logins 按 kind 字典序问每个实现了 LoginReader 的适配器，汇总该 HOME 的登录与问题。
func (r *Registry) Logins(home string) ([]LoginFinding, []error) {
	kinds := make([]string, 0, len(r.adapters))
	for k := range r.adapters {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var found []LoginFinding
	var problems []error
	for _, k := range kinds {
		lr, ok := r.adapters[k].(LoginReader)
		if !ok {
			continue
		}
		f, p := lr.Logins(home)
		found = append(found, f...)
		problems = append(problems, p...)
	}
	log().Debug("离线取材完成", "home", home, "logins", len(found), "problems", len(problems))
	return found, problems
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

// Logins 实现 LoginReader（B464 段 1）：auth.json 的 opencode-go API key 条目与 opencode.db 的
// 当前登录账号各是一份登录，身份材料不同（key 本身 / 「账号 id/org id」），天然不互认
// （spec S1）。两处都只读；没登录不是问题。第三方 provider key 条目在段 2（plan Q12）。
func (a *OpencodeGo) Logins(home string) ([]LoginFinding, []error) {
	if a.CredRelPath == "" {
		return nil, nil // 本平台无已知落点（unsupported_platform）：静默跳过
	}
	var out []LoginFinding
	var problems []error
	authPath := filepath.Join(home, a.CredRelPath)
	if key, err := a.readKey(home, opencodeGoDefaultEntry); err == nil {
		out = append(out, LoginFinding{Kind: KindOpencodeGo, Source: sourceAuthJSON, Fingerprint: Fingerprint(key)})
	} else if p, bad := classifyAbsent(err, authPath); bad {
		log().Warn("opencode-go auth.json 看不清，本轮不认该来源", "home", home, "cause", p)
		problems = append(problems, p)
	}
	dbPath := filepath.Join(home, a.dbRelPath())
	if cred, err := a.loginCred(home); err == nil {
		out = append(out, LoginFinding{Kind: KindOpencodeGo, Source: sourceLoginDB,
			Fingerprint: Fingerprint(cred.identity), ExpiresAt: cred.login.expiry})
	} else if p, bad := classifyAbsent(err, dbPath); bad {
		log().Warn("opencode-go opencode.db 看不清，本轮不认该来源", "home", home, "cause", p)
		problems = append(problems, p)
	}
	return out, problems
}
