// 本文件实现回环透传反代：webview 固定加载 http://127.0.0.1:<port>/，请求经
// 已选路的 agentd 客户端 Transport 原样投递到对端 agentd（relay 隧道或直连）。
//
// 边界（spec 回环门禁语义）：
//   - **反代不得注入凭据**。回环监听是设备全局、同机任意 App 可连，注入凭据会
//     把门禁让给反代；选定透传：cookie/Authorization 原样搬运，无 cookie 的同机
//     其他 App 过不了 agentd 的闸。
//   - 不做语义解释：方法/路径/查询/正文原样搬运，状态码与响应头原样回写。
package mobilecore

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"

	"github.com/Xsxdot/handoff/internal/client"
)

// newReverseProxy 造一台机器 webview 源的反代：目标恒为该机器的 client 基址
// （relay 形态为 http://localhost；直连形态为 http://<addr>）。
//
// 关键接线：Transport 取 client.HTTPClient().Transport，选路（relay 隧道 vs
// 直连）由此与桌面端同源；Host 改写成目标基址的 host——relay 投递的请求直达
// 对端 agentd.Handler，会先过 hostGuard（Host 白名单），loopback 三件套恒在白
// 名单内，任意占位名会被 403 拒。
func newReverseProxy(machineName string, cl *client.Client, log *slog.Logger) http.Handler {
	target, err := url.Parse(cl.BaseURL())
	if err != nil {
		// client.New 保证 baseURL 至少含 host；这里防御性兜底，不 panic。
		log.Error("机器基址不可解析，反代将恒拒请求", "machine", machineName, "base", cl.BaseURL(), "cause", err)
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "机器基址不可解析", http.StatusBadGateway)
		})
	}
	rp := &httputil.ReverseProxy{
		// 选路与桌面端同源：relay 走隧道，直连走 LAN。
		Transport: cl.HTTPClient().Transport,
		Director: func(r *http.Request) {
			r.URL.Scheme = target.Scheme
			r.URL.Host = target.Host
			// 回环源与目标源不同，Host 必须改写为对端白名单内的 loopback 名。
			r.Host = target.Host
			// 不注入任何凭据：cookie / Authorization 原样透传。
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Warn("回环反代投递失败", "machine", machineName, "path", r.URL.Path, "cause", err)
			http.Error(w, "agentd 不可达", http.StatusBadGateway)
		},
	}
	return rp
}
