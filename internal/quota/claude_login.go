// claude_login.go —— claude 登录的离线取材（B464 段 2，plan Q6 分支 b、R0-1）。
//
// 职责：判断一个 HOME 里 claude 是否已登录、是哪个账号——
//   - 身份：`<HOME>/.claude.json › oauthAccount.accountUuid`，label 取 `emailAddress`；
//   - 登录判据：在该 HOME 下跑 `claude auth status --json` 读 `loggedIn`（R0-1：oauthAccount
//     存在 ≠ 已登录，那可能是旧缓存）。只有 loggedIn=true 时身份才可信。
//   - 凭据变更时刻：linux 取 `.claude/.credentials.json` 的 mtime，没有（macOS 钥匙串）则取
//     `.claude.json` 的 mtime（重新登录会改写 oauthAccount）。claude 自刷新，Refreshable 恒真。
//
// 边界：handoff 不读钥匙串、不读凭据文件内容（只 stat）；子进程独立进程组、有时限，环境里去掉
// CLAUDE_CONFIG_DIR 与 ANTHROPIC_API_KEY/ANTHROPIC_AUTH_TOKEN（只按该 HOME 的登录判定）。
// 结果按「两个文件的 mtime/大小」缓存——发现节拍 60s，文件不变不重跑 node 子进程；缓存最多
// 保留 claudeCacheTTL。darwin 上「只认主 HOME」的 kind 规则在账号域（accounts.LoginMainHomeOnly），
// 由发现器在调用前过滤，本文件不判平台。
package quota

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	claudeConfigRel     = ".claude.json"
	claudeCredRel       = ".claude/.credentials.json"
	claudeStatusTimeout = 15 * time.Second
	claudeCacheTTL      = 10 * time.Minute
)

// Claude 是 claude 的离线取材（只实现 LoginReader，不支持额度探测，plan Q11）。零值字段取缺省。
type Claude struct {
	Binary  string        // 缺省 "claude"（经 exec.LookPath 找）
	Timeout time.Duration // `claude auth status` 时限，缺省 15s

	mu    sync.Mutex
	cache map[string]claudeCached
}

type claudeCached struct {
	key      string
	at       time.Time
	findings []LoginFinding
}

type claudeOAuthAccount struct {
	AccountUUID  string `json:"accountUuid"`
	EmailAddress string `json:"emailAddress"`
}

// Logins 实现 LoginReader。没有 .claude.json 或其中没有 oauthAccount → 未登录（不起子进程）；
// claude 未安装 → 未登录（Debug）；子进程超时/输出不是 JSON、已登录却缺 accountUuid → 问题。
func (c *Claude) Logins(home string) ([]LoginFinding, []error) {
	if !procGroupSupported {
		return nil, nil
	}
	if home == "" || !filepath.IsAbs(home) {
		return nil, nil
	}
	cfgPath := filepath.Join(home, claudeConfigRel)
	key, ok := claudeFileKey(home)
	if !ok {
		return nil, nil // 没有 .claude.json：这个 HOME 从没用过 claude
	}
	if hit, ok := c.cached(home, key); ok {
		return hit, nil
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		p := fail(CategoryCredentialInvalid, "读取 %s 失败：%v", cfgPath, err)
		log().Warn("claude .claude.json 读不了，本轮不认", "home", home, "cause", p)
		return nil, []error{p}
	}
	var cfg struct {
		OAuthAccount *claudeOAuthAccount `json:"oauthAccount"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		p := fail(CategoryCredentialInvalid, "%s 不是合法 JSON 对象", cfgPath)
		log().Warn("claude .claude.json 形状不对，本轮不认", "home", home, "cause", p)
		return nil, []error{p}
	}
	if cfg.OAuthAccount == nil {
		c.store(home, key, nil)
		return nil, nil
	}
	loggedIn, err := c.status(home)
	if err != nil {
		log().Warn("claude 登录判据取不到，本轮不认", "home", home, "cause", err)
		return nil, []error{err}
	}
	if !loggedIn {
		log().Debug("claude 未登录（auth status loggedIn=false）", "home", home)
		c.store(home, key, nil)
		return nil, nil
	}
	uuid := strings.TrimSpace(cfg.OAuthAccount.AccountUUID)
	if uuid == "" {
		p := fail(CategoryCredentialInvalid, "claude 已登录，但 %s 的 oauthAccount 缺少 accountUuid，认不出账号", cfgPath)
		log().Warn("claude .claude.json 形状不对，本轮不认", "home", home, "cause", p)
		return nil, []error{p}
	}
	changed := fileChangedAt(filepath.Join(home, claudeCredRel))
	if changed == nil {
		changed = fileChangedAt(cfgPath)
	}
	found := []LoginFinding{{Kind: KindClaude, Source: sourceClaudeAuth, Fingerprint: Fingerprint(uuid),
		Label: strings.TrimSpace(cfg.OAuthAccount.EmailAddress), Refreshable: true, ChangedAt: changed}}
	c.store(home, key, found)
	return found, nil
}

// status 在该 HOME 下跑 `claude auth status --json` 读 loggedIn。未安装 → false, nil。
// 非零退出但 stdout 是带 loggedIn 的 JSON 照常采信（未登录时 CLI 可能以非零退出）。
func (c *Claude) status(home string) (bool, error) {
	name := c.Binary
	if name == "" {
		name = "claude"
	}
	bin, err := exec.LookPath(name)
	if err != nil {
		log().Debug("找不到 claude 可执行文件，按未登录处理", "bin", name)
		return false, nil
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = claudeStatusTimeout
	}
	out, _, runErr := runGroupLabeled(context.Background(), timeout, "claude", bin, claudeEnv(home), "auth", "status", "--json")
	var st struct {
		LoggedIn *bool `json:"loggedIn"`
	}
	if jerr := json.Unmarshal(out, &st); jerr == nil && st.LoggedIn != nil {
		return *st.LoggedIn, nil
	}
	// 失败文案不回显 CLI 的 stdout/stderr：那是第三方程序的输出，不保证不含凭据（V4）。
	var f *Failure
	switch {
	case errors.As(runErr, &f) && f.Category == CategoryTimeout:
		return false, runErr
	case runErr != nil:
		return false, fail(CategoryUpstream, "在 %s 下执行 claude auth status --json 失败（非零退出且输出不是带 loggedIn 的 JSON，%d 字节）", home, len(out))
	}
	return false, fail(CategoryParse, "在 %s 下 claude auth status --json 的输出不是带 loggedIn 的 JSON（%d 字节）", home, len(out))
}

// claudeEnv 以登录 HOME 运行，去掉会让判定偏离该 HOME 的变量。
func claudeEnv(home string) []string {
	drop := map[string]bool{"HOME": true, "CLAUDE_CONFIG_DIR": true, "ANTHROPIC_API_KEY": true, "ANTHROPIC_AUTH_TOKEN": true}
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); !drop[k] {
			env = append(env, kv)
		}
	}
	return append(env, "HOME="+home)
}

// claudeFileKey 是缓存键：.claude.json 与 .claude/.credentials.json 的 mtime 与大小。
// .claude.json 不存在返回 false。
func claudeFileKey(home string) (string, bool) {
	cfg, err := os.Stat(filepath.Join(home, claudeConfigRel))
	if err != nil {
		return "", false
	}
	key := fmt.Sprintf("%d/%d", cfg.ModTime().UnixNano(), cfg.Size())
	if cred, err := os.Stat(filepath.Join(home, claudeCredRel)); err == nil {
		key += fmt.Sprintf("|%d/%d", cred.ModTime().UnixNano(), cred.Size())
	}
	return key, true
}

func (c *Claude) cached(home, key string) ([]LoginFinding, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[home]
	if !ok || e.key != key || time.Since(e.at) > claudeCacheTTL {
		return nil, false
	}
	return append([]LoginFinding(nil), e.findings...), true
}

func (c *Claude) store(home, key string, findings []LoginFinding) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cache == nil {
		c.cache = map[string]claudeCached{}
	}
	c.cache[home] = claudeCached{key: key, at: time.Now(), findings: append([]LoginFinding(nil), findings...)}
}
