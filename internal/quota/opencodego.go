// opencodego.go —— opencode-go 额度适配（B453 段 1 首故事）。
//
// 职责：读账号 HOME 下 opencode 的 auth.json 指定条目（API key），以 Bearer 调
// `GET <base>/zen/go/v1/usage`，把 rolling/weekly/monthly 解析成 session/weekly/monthly
// 三窗口。R1（2026-10-22）真机证实接口存在、Bearer API key 鉴权形态成立，并取得 401
// `api_key_expired` 错误体；**成功响应的字段形状仍未经真机**（percent 按 0–100 理解、
// resetsAt 兼容秒/毫秒/RFC3339，来自 orca 转述）。
//
// 边界：只认 `auth.json › <条目>` 且条目 type=api 的形态；opencode 账号登录（OAuth，
// 落 opencode.db）不在本适配范围（plan 拍板 Q2）。凭据文件只读打开，绝不改写。
package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

const (
	opencodeGoDefaultBase  = "https://opencode.ai"
	opencodeGoUsagePath    = "/zen/go/v1/usage"
	opencodeGoDefaultEntry = "opencode-go"
)

// OpencodeGo 是 opencode-go 适配器。零值字段取缺省。
type OpencodeGo struct {
	BaseURL     string        // 缺省 https://opencode.ai（测试注入 httptest 地址）
	CredRelPath string        // 凭据文件相对 HOME 的路径；空 = 本平台无已知落点
	Timeout     time.Duration // 缺省 20s
}

// Probe 实现 Prober：读凭据 → 调用量接口 → 解析。指纹取 API key 的短哈希（只存前缀）。
//
// 返回：成功读数（可能 Blocked=无订阅）或 *Failure。失败原因保证不含凭据值。
func (a *OpencodeGo) Probe(ctx context.Context, req Request) (Snapshot, error) {
	entry := req.Entry
	if entry == "" {
		entry = opencodeGoDefaultEntry
	}
	key, err := a.readKey(req.Home, entry)
	if err != nil {
		log().Warn("opencode-go 凭据读取失败", "home", req.Home, "entry", entry, "cause", err)
		return Snapshot{}, err
	}
	base := a.BaseURL
	if base == "" {
		base = opencodeGoDefaultBase
	}
	status, body, err := getJSON(ctx, KindOpencodeGo, strings.TrimRight(base, "/")+opencodeGoUsagePath,
		map[string]string{"Authorization": "Bearer " + key, "Accept": "application/json"},
		req.Proxy, a.Timeout, key)
	if err != nil {
		return Snapshot{}, err
	}
	snap, err := ParseOpencodeGoUsage(status, body)
	if err != nil {
		log().Warn("opencode-go 额度接口结果为失败", "status", status, "cause", err)
		return Snapshot{}, err
	}
	snap.Fingerprint = Fingerprint(key)
	return snap, nil
}

// Identity 实现 Identifier：条目的 API key 本身（只经 Fingerprint 出包，不外露）。
func (a *OpencodeGo) Identity(home, entry string) (string, error) {
	if entry == "" {
		entry = opencodeGoDefaultEntry
	}
	return a.readKey(home, entry)
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
