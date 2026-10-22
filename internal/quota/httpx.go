// httpx.go —— 三家 HTTP 额度适配共用的出网与凭据读取（B453）。
//
// 职责：一次带代理与超时的 GET（响应体读入前就抹掉全部凭据值）、只读打开凭据 JSON
// 文件、宽容数值解析、正文片段截断。
// 边界：不解释业务字段；凭据值只经参数 secrets 传入用于抹除，不落日志。
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
	// defaultHTTPTimeout 取 orca 同类适配 10–30s 区间（plan §6 授权）。
	defaultHTTPTimeout = 20 * time.Second
	// respBodyLimit 限制读入的响应体；额度接口正文是几百字节到几 KB。
	respBodyLimit = 64 << 10
	// snippetLimit 是写进失败原因的正文片段上限（已抹凭据）。
	snippetLimit = 200
)

// getJSON 发一次 GET，返回状态码与已抹凭据的正文。传输层失败归类为 network/proxy/timeout。
// secrets 是要从正文与错误文本里抹掉的全部凭据值（正文可能回显请求头）。
func getJSON(ctx context.Context, kind, url string, headers map[string]string, proxy string,
	timeout time.Duration, secrets ...string) (int, []byte, error) {
	transport, err := proxycfg.Transport(proxy)
	if err != nil {
		return 0, nil, fail(CategoryProxy, "代理配置无效：%v", err)
	}
	// 每次探测独立 Transport（账号代理各不相同）；用完即关空闲连接，不在进程里攒连接池。
	defer transport.CloseIdleConnections()
	if timeout <= 0 {
		timeout = defaultHTTPTimeout
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, nil, fail(CategoryNetwork, "构造请求失败：%v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	started := time.Now()
	log().Info("额度接口调用开始", "kind", kind, "url", url, "proxy", proxycfg.Mask(proxy), "timeout", timeout)
	resp, err := (&http.Client{Transport: transport, Timeout: timeout}).Do(req)
	if err != nil {
		f := classifyTransportErr(err, proxy)
		f.Message = redactAll(f.Message, secrets)
		log().Warn("额度接口调用失败", "kind", kind, "url", url, "category", f.Category,
			"elapsed", time.Since(started), "cause", f.Message)
		return 0, nil, f
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, respBodyLimit))
	if err != nil {
		f := fail(CategoryNetwork, "读取响应失败：%s", redactAll(err.Error(), secrets))
		log().Warn("额度接口读响应失败", "kind", kind, "url", url, "status", resp.StatusCode, "cause", f.Message)
		return 0, nil, f
	}
	for _, s := range secrets {
		if s != "" {
			body = bytes.ReplaceAll(body, []byte(s), []byte("***"))
		}
	}
	log().Info("额度接口调用返回", "kind", kind, "url", url, "status", resp.StatusCode,
		"bytes", len(body), "elapsed", time.Since(started))
	return resp.StatusCode, body, nil
}

// readCredJSON 只读打开 HOME 下的凭据 JSON（顶层对象）。错误文案只带路径，不回显内容
// （json 错误可能带出文件片段，即凭据本身）。
func readCredJSON(home, rel, loginHint string) (map[string]json.RawMessage, string, error) {
	if rel == "" {
		return nil, "", fail(CategoryUnsupportedPlatform, "本平台没有已知的凭据文件落点")
	}
	if home == "" || !filepath.IsAbs(home) {
		return nil, "", fail(CategoryCredentialMissing, "凭据 HOME %q 不是绝对路径", home)
	}
	path := filepath.Join(home, rel)
	raw, err := os.ReadFile(path) // 只读打开，不改 mtime
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, path, fail(CategoryCredentialMissing, "凭据文件 %s 不存在（%s）", path, loginHint)
		}
		return nil, path, fail(CategoryCredentialMissing, "读取凭据文件 %s 失败：%v", path, err)
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, path, fail(CategoryCredentialInvalid, "凭据文件 %s 不是合法 JSON 对象", path)
	}
	return top, path, nil
}

// statusFailure 把通用的非 2xx 状态归类：401/403 → unauthorized（文案由调用方给）、
// 429 → rate_limited（限速不是额度耗尽，plan Q10）、其余 → upstream。2xx 返回 nil。
func statusFailure(kind string, status int, body []byte, unauthorizedMsg string) error {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fail(CategoryUnauthorized, "%s（HTTP %d）：%s", unauthorizedMsg, status, snippet(body))
	case status == http.StatusTooManyRequests:
		return fail(CategoryRateLimited, "%s 额度接口限速（HTTP 429），稍后重试", kind)
	case status < 200 || status >= 300:
		return fail(CategoryUpstream, "%s 额度接口返回 HTTP %d：%s", kind, status, snippet(body))
	}
	return nil
}

// classifyTransportErr 把 http.Client.Do 的错误归类。代理握手失败单列，便于人判断
// 是代理挂了还是目标不可达。
func classifyTransportErr(err error, proxy string) *Failure {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return fail(CategoryTimeout, "额度接口超时：%v", err)
	}
	if strings.Contains(err.Error(), "proxyconnect") || strings.Contains(strings.ToLower(err.Error()), "socks") {
		return fail(CategoryProxy, "经代理 %s 出网失败：%v", proxycfg.Mask(proxy), err)
	}
	return fail(CategoryNetwork, "额度接口不可达：%v", err)
}

// num 宽容地取数值：JSON 数字、数字字符串，或 grok 式 {"val": N} 包装。
func num(v any) (float64, bool) {
	switch x := v.(type) {
	case map[string]any:
		if inner, ok := x["val"]; ok {
			return num(inner)
		}
		return 0, false
	case float64:
		return x, true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(x), 64)
		return f, err == nil
	}
	return 0, false
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

func redactAll(s string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			s = strings.ReplaceAll(s, secret, "***")
		}
	}
	return s
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

// flexibleMoment 解析时刻：数字（< 1e12 视为秒，否则毫秒）、数字字符串、RFC3339。
type flexibleMoment struct{ t *time.Time }

func (m *flexibleMoment) UnmarshalJSON(data []byte) error {
	t, err := parseMoment(data)
	m.t = t
	return err
}

func parseMoment(data []byte) (*time.Time, error) {
	s := strings.TrimSpace(string(data))
	if s == "null" || s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, `"`) {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return nil, err
		}
		str = strings.TrimSpace(str)
		if str == "" {
			return nil, nil
		}
		if n, err := strconv.ParseFloat(str, 64); err == nil {
			t := epochToTime(n)
			return &t, nil
		}
		t, err := time.Parse(time.RFC3339, str)
		if err != nil {
			return nil, fmt.Errorf("时刻 %q 不是 RFC3339 或时间戳", str)
		}
		t = t.UTC()
		return &t, nil
	}
	n, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, fmt.Errorf("时刻 %s 不是数字", s)
	}
	t := epochToTime(n)
	return &t, nil
}

func epochToTime(n float64) time.Time {
	if n >= 1e12 {
		return time.UnixMilli(int64(n)).UTC()
	}
	return time.Unix(int64(n), 0).UTC()
}
