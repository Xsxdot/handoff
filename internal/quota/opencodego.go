// opencodego.go —— opencode-go 额度适配（B453 段 1 首故事）。
//
// 职责：读账号 HOME 下 opencode 的 auth.json 指定条目（API key），以 Bearer 调
// `GET <base>/zen/go/v1/usage`，把 rolling/weekly/monthly 解析成 session/weekly/monthly
// 三窗口。接口与字段按 b453-facts-orca.md §1 opencode 节转述，**未经真机证实**（U1）：
// percent 按 0–100 理解、resetsAt 兼容秒/毫秒/RFC3339，真机 R1 取到真样本后校正。
//
// 边界：只认 `auth.json › <条目>` 且条目 type=api 的形态；opencode 账号登录（OAuth，
// 落 opencode.db）不在本适配范围（plan 拍板 Q2）。凭据文件只读打开，绝不改写。
package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Xsxdot/handoff/internal/proxycfg"
)

const (
	opencodeGoDefaultBase  = "https://opencode.ai"
	opencodeGoUsagePath    = "/zen/go/v1/usage"
	opencodeGoDefaultEntry = "opencode-go"
	// opencodeGoTimeout 取 orca 同类适配的 15–30s 区间中值（plan §6 授权 10–30s）。
	opencodeGoTimeout = 20 * time.Second
	// respBodyLimit 限制读入的响应体；额度接口正文是几百字节量级。
	respBodyLimit = 64 << 10
	// snippetLimit 是写进失败原因的正文片段上限（已抹掉凭据值）。
	snippetLimit = 200
)

// OpencodeGo 是 opencode-go 适配器。零值字段取缺省。
type OpencodeGo struct {
	BaseURL     string        // 缺省 https://opencode.ai（测试注入 httptest 地址）
	CredRelPath string        // 凭据文件相对 HOME 的路径；空 = 本平台无已知落点
	Timeout     time.Duration // 缺省 20s
}

// Probe 实现 Prober：读凭据 → 调用量接口 → 解析。
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
	transport, terr := proxycfg.Transport(req.Proxy)
	if terr != nil {
		f := fail(CategoryProxy, "代理配置无效：%v", terr)
		log().Warn("opencode-go 代理配置无效", "entry", entry, "cause", f)
		return Snapshot{}, f
	}
	timeout := a.Timeout
	if timeout <= 0 {
		timeout = opencodeGoTimeout
	}
	base := a.BaseURL
	if base == "" {
		base = opencodeGoDefaultBase
	}
	url := strings.TrimRight(base, "/") + opencodeGoUsagePath
	httpReq, rerr := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if rerr != nil {
		return Snapshot{}, fail(CategoryNetwork, "构造请求失败：%v", rerr)
	}
	httpReq.Header.Set("Authorization", "Bearer "+key)
	httpReq.Header.Set("Accept", "application/json")
	client := &http.Client{Transport: transport, Timeout: timeout}
	started := time.Now()
	log().Info("opencode-go 额度接口调用开始", "url", url, "entry", entry,
		"proxy", proxycfg.Redact(req.Proxy), "timeout", timeout)
	resp, derr := client.Do(httpReq)
	if derr != nil {
		f := classifyTransportErr(derr, req.Proxy)
		f.Message = redact(f.Message, key)
		log().Warn("opencode-go 额度接口调用失败", "url", url, "category", f.Category,
			"elapsed", time.Since(started), "cause", f.Message)
		return Snapshot{}, f
	}
	defer resp.Body.Close()
	body, berr := io.ReadAll(io.LimitReader(resp.Body, respBodyLimit))
	if berr != nil {
		f := fail(CategoryNetwork, "读取响应失败：%s", redact(berr.Error(), key))
		log().Warn("opencode-go 额度接口读响应失败", "url", url, "status", resp.StatusCode, "cause", f.Message)
		return Snapshot{}, f
	}
	// 正文可能回显请求头（错误页常见）：解析与失败文本只见抹掉凭据后的正文。
	body = bytes.ReplaceAll(body, []byte(key), []byte("***"))
	snap, perr := ParseOpencodeGoUsage(resp.StatusCode, body)
	if perr != nil {
		log().Warn("opencode-go 额度接口结果为失败", "url", url, "status", resp.StatusCode,
			"elapsed", time.Since(started), "cause", perr)
		return Snapshot{}, perr
	}
	log().Info("opencode-go 额度接口调用完成", "url", url, "status", resp.StatusCode,
		"windows", len(snap.Windows), "blocked", snap.Blocked != nil, "elapsed", time.Since(started))
	return snap, nil
}

// readKey 只读打开 auth.json 取条目的 API key。错误文案只带路径与条目名。
func (a *OpencodeGo) readKey(home, entry string) (string, error) {
	if a.CredRelPath == "" {
		return "", fail(CategoryUnsupportedPlatform, "本平台没有已知的 opencode 凭据文件落点")
	}
	if home == "" || !filepath.IsAbs(home) {
		return "", fail(CategoryCredentialMissing, "凭据 HOME %q 不是绝对路径", home)
	}
	path := filepath.Join(home, a.CredRelPath)
	raw, err := os.ReadFile(path) // os.ReadFile 以只读打开，不改 mtime
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fail(CategoryCredentialMissing, "凭据文件 %s 不存在（在该机执行 opencode auth login 登记 opencode-go API key）", path)
		}
		return "", fail(CategoryCredentialMissing, "读取凭据文件 %s 失败：%v", path, err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		// 不回显 err：json 错误可能带出文件片段（即凭据本身）。
		return "", fail(CategoryCredentialInvalid, "凭据文件 %s 不是合法 JSON 对象", path)
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
// 判定：正文含 EntitlementError 或 403 → 成功读数且 Blocked=无订阅；401 → unauthorized；
// 429 → rate_limited（限速不是额度耗尽，plan Q10）；其余非 2xx → upstream；2xx 需有
// usage 且至少一个带 percent 的窗口，否则 parse。
func ParseOpencodeGoUsage(status int, body []byte) (Snapshot, error) {
	if bytes.Contains(body, []byte("EntitlementError")) || status == http.StatusForbidden {
		return Snapshot{Blocked: fail(CategoryNoSubscription, "opencode-go 无订阅或未开通（HTTP %d）", status)}, nil
	}
	switch {
	case status == http.StatusUnauthorized:
		return Snapshot{}, fail(CategoryUnauthorized, "opencode-go API key 无效或已吊销（HTTP 401）：%s", snippet(body))
	case status == http.StatusTooManyRequests:
		return Snapshot{}, fail(CategoryRateLimited, "opencode-go 额度接口限速（HTTP 429），稍后重试")
	case status < 200 || status >= 300:
		return Snapshot{}, fail(CategoryUpstream, "opencode-go 额度接口返回 HTTP %d：%s", status, snippet(body))
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

// flexibleMoment 解析重置时刻：数字（< 1e12 视为秒，否则毫秒）、数字字符串、RFC3339。
type flexibleMoment struct{ t *time.Time }

func (m *flexibleMoment) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if s == "null" || s == "" {
		return nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		str = strings.TrimSpace(str)
		if str == "" {
			return nil
		}
		if n, err := strconv.ParseFloat(str, 64); err == nil {
			t := epochToTime(n)
			m.t = &t
			return nil
		}
		t, err := time.Parse(time.RFC3339, str)
		if err != nil {
			return fmt.Errorf("重置时刻 %q 不是 RFC3339 或时间戳", str)
		}
		t = t.UTC()
		m.t = &t
		return nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return fmt.Errorf("重置时刻 %s 不是数字", s)
	}
	t := epochToTime(n)
	m.t = &t
	return nil
}

func epochToTime(n float64) time.Time {
	if n >= 1e12 {
		return time.UnixMilli(int64(n)).UTC()
	}
	return time.Unix(int64(n), 0).UTC()
}

func clampPercent(p float64) float64 {
	switch {
	case p < 0:
		return 0
	case p > 100:
		return 100
	default:
		return p
	}
}

// classifyTransportErr 把 http.Client.Do 的错误归类。代理握手失败单列，便于人判断
// 是代理挂了还是目标不可达。
func classifyTransportErr(err error, proxy string) *Failure {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fail(CategoryTimeout, "额度接口超时：%v", err)
	}
	if strings.Contains(err.Error(), "proxyconnect") || strings.Contains(strings.ToLower(err.Error()), "socks") {
		return fail(CategoryProxy, "经代理 %s 出网失败：%v", proxycfg.Redact(proxy), err)
	}
	return fail(CategoryNetwork, "额度接口不可达：%v", err)
}

// snippet 返回可进失败原因的正文片段（调用方已抹凭据）：截断到 snippetLimit 字节。
func snippet(body []byte) string {
	s := strings.TrimSpace(string(body))
	if len(s) > snippetLimit {
		s = s[:snippetLimit]
		for !utf8.ValidString(s) && len(s) > 0 {
			s = s[:len(s)-1]
		}
		s += "…"
	}
	return s
}

func redact(s, secret string) string {
	if secret == "" {
		return s
	}
	return strings.ReplaceAll(s, secret, "***")
}
