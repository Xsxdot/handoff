// opencodego.go —— opencode-go 额度适配（B453 段 1 首故事；轮 5 增登录账号来源）。
//
// 职责：按条目选凭据来源并取三窗口读数——
//   - API key 来源：读账号 HOME 下 opencode 的 auth.json 指定条目（type=api），以 Bearer 调
//     `GET <base>/zen/go/v1/usage`，把 rolling/weekly/monthly 解析成 session/weekly/monthly。
//     R1（2026-10-22）真机证实接口与鉴权形态，取得 401 `api_key_expired` 错误体；成功响应
//     的字段形状仍未经真机（percent 按 0–100、resetsAt 兼容秒/毫秒/RFC3339，来自 orca 转述）。
//   - 登录账号来源（spec r1.3 S2，用户 2026-10-22 批准读 opencode.db）：只读打开 opencode 的
//     opencode.db，取当前登录账号的 access_token 与 org，以 Bearer + x-org-id 调
//     `GET <base>/console/api/go/status`，解析 access.meters 三窗口（opencode_login.go）。
//
// 来源选择（plan §8 r3 F9）：条目空 = 自动（auth.json 有 API key 条目 `opencode-go` 用它，
// 否则用登录账号）；`opencode.db` = 只用登录账号；其他值 = 只用 auth.json 同名条目。
//
// 边界：凭据文件与库一律只读打开，绝不改写；凭据值（key、token、org）不进日志、错误文案、
// 读数与 API——出网时作为 secrets 交 getJSON 抹除。
package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

const (
	opencodeGoDefaultBase = "https://opencode.ai"
	opencodeGoUsagePath   = "/zen/go/v1/usage"
	// opencodeGoStatusPath 是登录账号来源的 console 状态端点（spec r1.3 实测 200）。
	opencodeGoStatusPath   = "/console/api/go/status"
	opencodeGoDefaultEntry = "opencode-go"
	// opencodeGoLoginEntry 是「只用登录账号」的条目取值（plan §8 r3 F9）。
	opencodeGoLoginEntry = "opencode.db"
	// 日志里的来源名（不含任何凭据值）。
	sourceAuthJSON = "auth.json"
	sourceLoginDB  = "opencode.db"
)

// opencodeCred 是选定来源后的凭据：API key，或登录账号（token + org + 身份）。
type opencodeCred struct {
	source   string // sourceAuthJSON | sourceLoginDB
	key      string // API key（auth.json 来源）
	login    loginAccount
	identity string // 指纹取材：API key 本身，或「账号 id/org id」
}

// OpencodeGo 是 opencode-go 适配器。零值字段取缺省。
type OpencodeGo struct {
	BaseURL     string        // 缺省 https://opencode.ai（测试注入 httptest 地址）
	CredRelPath string        // 凭据文件相对 HOME 的路径；空 = 本平台无已知落点
	Timeout     time.Duration // 缺省 20s
}

// Probe 实现 Prober：选来源 → 读凭据 → 调对应接口 → 解析。指纹取不随 token 刷新而变的身份
// （API key 来源取 key，登录账号来源取「账号 id/org id」），只存短哈希。
//
// 返回：成功读数（可能 Blocked=无订阅）或 *Failure。失败原因保证不含凭据值。
func (a *OpencodeGo) Probe(ctx context.Context, req Request) (Snapshot, error) {
	cred, err := a.resolve(req.Home, req.Entry)
	if err != nil {
		log().Warn("opencode-go 凭据读取失败", "home", req.Home, "entry", req.Entry, "cause", err)
		return Snapshot{}, err
	}
	log().Info("opencode-go 凭据来源", "home", req.Home, "entry", req.Entry, "source", cred.source)
	base := a.BaseURL
	if base == "" {
		base = opencodeGoDefaultBase
	}
	base = strings.TrimRight(base, "/")
	var snap Snapshot
	if cred.source == sourceLoginDB {
		if cred.login.expired(time.Now()) {
			f := fail(CategoryCredentialExpired, "opencode 登录已过期，在该机运行 opencode 重新登录")
			log().Warn("opencode-go 登录账号已过期，不出网", "home", req.Home, "cause", f)
			return Snapshot{}, f
		}
		status, body, err := getJSON(ctx, KindOpencodeGo, base+opencodeGoStatusPath,
			map[string]string{"Authorization": "Bearer " + cred.login.token, "x-org-id": cred.login.org,
				"Accept": "application/json"},
			req.Proxy, a.Timeout, cred.login.token, cred.login.org)
		if err != nil {
			return Snapshot{}, err
		}
		if snap, err = ParseOpencodeGoConsoleStatus(status, body); err != nil {
			log().Warn("opencode-go console 状态结果为失败", "status", status, "cause", err)
			return Snapshot{}, err
		}
	} else {
		status, body, err := getJSON(ctx, KindOpencodeGo, base+opencodeGoUsagePath,
			map[string]string{"Authorization": "Bearer " + cred.key, "Accept": "application/json"},
			req.Proxy, a.Timeout, cred.key)
		if err != nil {
			return Snapshot{}, err
		}
		if snap, err = ParseOpencodeGoUsage(status, body); err != nil {
			log().Warn("opencode-go 额度接口结果为失败", "status", status, "cause", err)
			return Snapshot{}, err
		}
	}
	snap.Fingerprint = Fingerprint(cred.identity)
	return snap, nil
}

// Identity 实现 Identifier：与 Probe 同一来源选择——API key 本身，或登录账号的「账号 id/org id」
// （只经 Fingerprint 出包，不外露）。登录过期不影响身份：同一账号，只是探测会失败。
func (a *OpencodeGo) Identity(home, entry string) (string, error) {
	cred, err := a.resolve(home, entry)
	if err != nil {
		return "", err
	}
	return cred.identity, nil
}

// resolve 按条目选凭据来源（plan §8 r3 F9）并读出凭据。自动模式下 auth.json 不成立
// （文件缺、条目缺或不是 API key）才回落登录账号；两处都没有时合成一句点名两处的
// credential_missing。本平台无已知落点（unsupported_platform）不回落。
func (a *OpencodeGo) resolve(home, entry string) (opencodeCred, error) {
	switch entry {
	case opencodeGoLoginEntry:
		return a.loginCred(home)
	case "":
		key, authErr := a.readKey(home, opencodeGoDefaultEntry)
		if authErr == nil {
			return opencodeCred{source: sourceAuthJSON, key: key, identity: key}, nil
		}
		var af *Failure
		if errors.As(authErr, &af) && af.Category == CategoryUnsupportedPlatform {
			return opencodeCred{}, authErr
		}
		cred, loginErr := a.loginCred(home)
		if loginErr == nil {
			log().Info("opencode-go auth.json 无可用 API key 条目，改用登录账号", "home", home, "cause", authErr)
			return cred, nil
		}
		var lf *Failure
		if errors.As(loginErr, &lf) && lf.Category == CategoryCredentialMissing {
			return opencodeCred{}, a.bothMissing(home, af)
		}
		return opencodeCred{}, loginErr
	default:
		key, err := a.readKey(home, entry)
		if err != nil {
			return opencodeCred{}, err
		}
		return opencodeCred{source: sourceAuthJSON, key: key, identity: key}, nil
	}
}

// bothMissing 是自动模式两处都没有凭据的失败：点名两处路径与各自的处置。auth.json 条目在
// 但不是 API key 时保留那句具体原因。
func (a *OpencodeGo) bothMissing(home string, authErr *Failure) error {
	authPath := filepath.Join(home, a.CredRelPath)
	authPart := fmt.Sprintf("%s 无 opencode-go 条目", authPath)
	if authErr != nil && authErr.Category == CategoryCredentialInvalid {
		authPart = authErr.Message
	}
	return fail(CategoryCredentialMissing,
		"%s，且 %s 无登录账号（在该机执行 opencode auth login 登记 opencode-go API key，或运行 opencode 登录账号）",
		authPart, filepath.Join(home, a.dbRelPath()))
}

// loginCred 读登录账号并组装凭据；org 为空 = 凭据形态不成立（console 状态端点必须带组织）。
func (a *OpencodeGo) loginCred(home string) (opencodeCred, error) {
	acc, err := readLoginAccount(home, a.dbRelPath())
	if err != nil {
		return opencodeCred{}, err
	}
	if acc.org == "" {
		return opencodeCred{}, fail(CategoryCredentialInvalid,
			"%s 的当前登录账号没有组织（account_state.active_org_id 为空），在该机运行 opencode 选择组织后重试",
			filepath.Join(home, a.dbRelPath()))
	}
	return opencodeCred{source: sourceLoginDB, login: acc, identity: acc.id + "/" + acc.org}, nil
}

// dbRelPath 是 opencode.db 相对 HOME 的路径：与 auth.json 同在 opencode 的 data 目录
// （凭据相对路径仍只由 toolchain 那张表维护）；本平台无已知落点时为空。
func (a *OpencodeGo) dbRelPath() string {
	if a.CredRelPath == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(a.CredRelPath), opencodeGoLoginEntry)
}

// readKey 只读打开 auth.json 取条目的 API key。错误文案只带路径与条目名。
func (a *OpencodeGo) readKey(home, entry string) (string, error) {
	entries, path, err := readCredJSON(home, a.CredRelPath, "在该机执行 opencode auth login 登记 opencode-go API key")
	if err != nil {
		return "", err
	}
	rawEntry, ok := entries[entry]
	if !ok {
		return "", fail(CategoryCredentialMissing, "凭据文件 %s 里没有条目 %q", path, entry)
	}
	var item struct {
		Type string `json:"type"`
		Key  string `json:"key"`
	}
	if err := json.Unmarshal(rawEntry, &item); err != nil {
		return "", fail(CategoryCredentialInvalid, "凭据条目 %q 形状无法解析", entry)
	}
	if item.Type != "api" || strings.TrimSpace(item.Key) == "" {
		return "", fail(CategoryCredentialInvalid, "凭据条目 %q 不是 API key（type=%q）；opencode-go 探测只支持 API key 条目", entry, item.Type)
	}
	return item.Key, nil
}

// ParseOpencodeGoUsage 把 usage 接口的状态码与响应体解析成读数（外部缝上的解析入口）。
//
// 判定：正文含 EntitlementError 或 403 → 成功读数且 Blocked=无订阅；错误码
// api_key_expired（R1 真样本）→ credential_expired；其余 401 → unauthorized；
// 429 → rate_limited（限速不是额度耗尽，plan Q10）；其余非 2xx → upstream；2xx 需有
// usage 且至少一个带 percent 的窗口，否则 parse。
func ParseOpencodeGoUsage(status int, body []byte) (Snapshot, error) {
	if bytes.Contains(body, []byte("EntitlementError")) || status == http.StatusForbidden {
		return Snapshot{Blocked: fail(CategoryNoSubscription, "opencode-go 无订阅或未开通（HTTP %d）", status)}, nil
	}
	if status == http.StatusUnauthorized && bytes.Contains(body, []byte("api_key_expired")) {
		return Snapshot{}, fail(CategoryCredentialExpired,
			"opencode-go API key 已过期（在 OpenCode Console 新建 API key，写入该机 auth.json 的 opencode-go 条目）")
	}
	if err := statusFailure(KindOpencodeGo, status, body, "opencode-go API key 无效或已吊销"); err != nil {
		return Snapshot{}, err
	}
	var doc struct {
		Usage map[string]json.RawMessage `json:"usage"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Snapshot{}, fail(CategoryParse, "响应不是合法 JSON：%s", snippet(body))
	}
	if doc.Usage == nil {
		return Snapshot{}, fail(CategoryParse, "响应缺少 usage 字段：%s", snippet(body))
	}
	var out Snapshot
	for _, pair := range []struct{ field, key string }{
		{"rolling", WindowSession}, {"weekly", WindowWeekly}, {"monthly", WindowMonthly},
	} {
		raw, ok := doc.Usage[pair.field]
		if !ok || string(raw) == "null" {
			continue
		}
		var w struct {
			Percent  *float64       `json:"percent"`
			ResetsAt flexibleMoment `json:"resetsAt"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			return Snapshot{}, fail(CategoryParse, "usage.%s 形状无法解析：%v", pair.field, err)
		}
		if w.Percent == nil {
			return Snapshot{}, fail(CategoryParse, "usage.%s 缺少 percent", pair.field)
		}
		out.Windows = append(out.Windows, Window{
			Key: pair.key, UsedPercent: clampPercent(*w.Percent), ResetsAt: w.ResetsAt.t,
		})
	}
	if len(out.Windows) == 0 {
		return Snapshot{}, fail(CategoryParse, "usage 里没有任何已知窗口（rolling/weekly/monthly）：%s", snippet(body))
	}
	return out, nil
}
