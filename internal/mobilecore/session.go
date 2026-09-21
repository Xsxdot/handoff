// 本文件实现移动连接核的会话兑换与单槽会话罐：Go 核用 token 副本程序化领
// 一次性 ticket，经本机回环反代请求 /console 拿 Set-Cookie，供绑定面注入
// webview cookie jar（spec 实现决定：回环门禁 + 多机会话域）。
//
// 边界：
//   - 不注入任何凭据给反代：ticket 在 URL 查询里，兑换请求本身不带
//     Authorization；cookie 只在本包内解析、由绑定面取走
//   - cookie 不落日志：日志只带 cookie 名与机器名（token / cookie 等同主令牌集）
//   - 不 import internal/agentd：cookie 名以本包常量镜像，由会话测试的
//     「源对齐断言」与 agentd 源逐字锁定
package mobilecore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
)

const (
	// sessionCookieName 镜像 agentd 的会话 cookie 名
	// （internal/agentd/auth.go#sessionCookieName）。移动核不得 import agentd；
	// 该字面值由 TestSessionCookieNameMatchesAgentdSource 与 agentd 源逐字锁定。
	sessionCookieName = "handoff_session"
	// mobileDeviceName 是移动核代领 ticket 时登记的设备名前缀（纯展示）。
	// 后接机器名，便于 agentd 会话表区分是哪台机器换出的会话。
	mobileDeviceName = "handoff-mobile"
	// exchangeTimeout 是单次 ticket→cookie 兑换的时限。
	exchangeTimeout = 5 * time.Second
)

// SessionCookie 是一台机器程序化兑换得到的浏览器会话 cookie（注入 webview 用）。
//
// 字段形状保持 gomobile 可绑（仅 string/bool/int；不用 time.Time）——绑定面把
// 它交给壳的平台 cookie API（WKHTTPCookieStore / CookieManager）。
type SessionCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Path     string `json:"path"`
	HttpOnly bool   `json:"http_only"`
	Secure   bool   `json:"secure"`
	SameSite string `json:"same_site"`
	MaxAge   int    `json:"max_age"`
}

// exchangeTicket 用 cl 的 token 副本领一张一次性 ticket，再经本机回环反代请求
// 兑换地址拿 Set-Cookie，返回会话 cookie。
//
// 参数：
//   - machine: 机器名（登记进设备名，便于对端会话表区分）
//   - origin: 该机的回环源（http://127.0.0.1:<port>），兑换请求必过反代
//   - cl: 该机的已选路客户端（领 ticket 走它的 token 与 Transport）
//   - log: 结构化日志
//
// 返回：
//   - 兑换到的 handoff_session cookie
//   - 领票失败、兑换地址不可解析、非 302、无 Set-Cookie、cookie 空值均返回错误
//
// 注意：
//   - **不跟随 302**：agentd 的 /console 是导航流，跟随会把请求带到 / 并把
//     cookie 换成对端跳转；用 http.ErrUseLastResponse 只取响应头
//   - 兑换请求不带 Authorization，反代也不注入——回环门禁的承重断言
func exchangeTicket(ctx context.Context, machine, origin string, cl *client.Client, log *slog.Logger) (SessionCookie, error) {
	log.Info("程序化兑换 ticket→cookie 开始", "machine", machine, "origin", origin)
	ticket, err := cl.IssueAuthTicket(ctx, mobileDeviceName+"-"+machine)
	if err != nil {
		log.Warn("代领 ticket 失败", "machine", machine, "cause", err)
		return SessionCookie{}, fmt.Errorf("代领 ticket: %w", err)
	}
	u, err := url.Parse(ticket.URL)
	if err != nil {
		return SessionCookie{}, fmt.Errorf("ticket 兑换地址不可解析: %w", err)
	}
	exURL := strings.TrimRight(origin, "/") + u.RequestURI()
	ectx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ectx, http.MethodGet, exURL, nil)
	if err != nil {
		return SessionCookie{}, fmt.Errorf("构造兑换请求: %w", err)
	}
	hc := &http.Client{
		Transport: &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := hc.Do(req)
	if err != nil {
		log.Warn("兑换请求失败", "machine", machine, "origin", origin, "cause", err)
		return SessionCookie{}, fmt.Errorf("请求兑换地址: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		log.Warn("兑换响应非 302", "machine", machine, "status", resp.StatusCode)
		return SessionCookie{}, fmt.Errorf("兑换 ticket 期望 302，得到 %d", resp.StatusCode)
	}
	for _, ck := range resp.Cookies() {
		if ck.Name != sessionCookieName {
			continue
		}
		if ck.Value == "" {
			// 名字在、值为空：既不是「字段缺失」也不是可用会话，必须报错。
			log.Warn("兑换响应 cookie 值为空", "machine", machine, "cookie_name", ck.Name)
			return SessionCookie{}, errors.New("兑换响应的 " + sessionCookieName + " cookie 值为空")
		}
		out := toSessionCookie(ck)
		log.Info("程序化兑换 ticket→cookie 成功",
			"machine", machine, "origin", origin, "cookie_name", out.Name, "secure", out.Secure)
		return out, nil
	}
	log.Warn("兑换响应没有会话 cookie", "machine", machine, "cookie_name", sessionCookieName)
	return SessionCookie{}, errors.New("兑换响应没有 " + sessionCookieName + " cookie")
}

// toSessionCookie 把标准库 cookie 投影成本包 DTO（跨绑定面的手工投影点）。
//
// 注意：只搬运壳注入 webview 所需的字段；不复制 Raw/RawExpires（webview 由
// 壳按 Name/Value/Path/域自行构造 cookie），也不改 MaxAge 语义（<=0 即会话 cookie）。
func toSessionCookie(ck *http.Cookie) SessionCookie {
	return SessionCookie{
		Name:     ck.Name,
		Value:    ck.Value,
		Path:     ck.Path,
		HttpOnly: ck.HttpOnly,
		Secure:   ck.Secure,
		SameSite: sameSiteName(ck.SameSite),
		MaxAge:   ck.MaxAge,
	}
}

// sameSiteName 把标准库 SameSite 枚举投影成绑定面可携带的稳定字面值。
func sameSiteName(m http.SameSite) string {
	switch m {
	case http.SameSiteDefaultMode:
		return "Default"
	case http.SameSiteLaxMode:
		return "Lax"
	case http.SameSiteStrictMode:
		return "Strict"
	case http.SameSiteNoneMode:
		return "None"
	}
	return ""
}
