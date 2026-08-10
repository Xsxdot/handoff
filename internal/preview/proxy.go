// preview owner-loopback 反向代理。
//
// 职责：
//   - 把 /v1/preview-proxy/{nonce} 前缀剥离后的请求反向代理到本机 Preview 进程
//   - 仅拨号 127.0.0.1:port，绝不把调用方提供的 host 当作上游目标
//   - 剥离入站 Authorization/Cookie，避免任何 agentd 凭证进入上游 localhost 应用
//
// 边界：
//   - 不解析 nonce，不校验会话状态；会话解析由 wire 层经 LookupNonce 完成
//   - 无整体客户端超时，WebSocket 长连接可存活；上游响应由
//     ResponseHeaderTimeout + MaxResponseHeaderBytes 限制
//   - 日志只记录 machine/workspace/session/port 摘要，绝不记录 nonce 全文或凭证
package preview

import (
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"time"

	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// proxyLoopback 把请求代理到本机 Preview 端口。
//
// 入站请求的 URL.Path/RawQuery 保持不变：调用方已剥离 /v1/preview-proxy/{nonce}
// 前缀，本函数只重写 scheme/host 指向 loopback。
func proxyLoopback(w http.ResponseWriter, r *http.Request, session workspaceapi.PreviewSession, log *slog.Logger) {
	// 剥离入站凭证：Electron <webview> 无法携带 agentd 凭证，
	// 我们也不允许任何入站 Authorization/Cookie 泄漏到上游 localhost 应用。
	r.Header.Del("Authorization")
	r.Header.Del("Cookie")

	// 始终 loopback：owner-loopback 模式下 wire 契约只携带端口，
	// 因此调用方无法走私任何 host。
	target, _ := url.Parse("http://127.0.0.1:" + strconv.Itoa(session.Port))

	// 无整体客户端超时，WebSocket 长连接因此得以存活；
	// ResponseHeaderTimeout + MaxResponseHeaderBytes 约束上游响应头；
	// ForceAttemptHTTP2=false 保持 WebSocket hijack 走 HTTP/1.1。
	transport := &http.Transport{
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		IdleConnTimeout:        60 * time.Second,
		MaxIdleConns:           16,
		MaxIdleConnsPerHost:    4,
		ForceAttemptHTTP2:      false,
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
		},
		Transport:     transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			log.Error("Preview 上游不可用", "machine_id", session.MachineID,
				"workspace_id", session.WorkspaceID, "preview_session_id", session.PreviewSessionID,
				"port", session.Port, "cause", err)
			desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
				Code:        desktopapi.ProblemMachineOffline,
				Message:     "Preview 上游服务不可用",
				Retryable:   true,
				MachineID:   session.MachineID,
				WorkspaceID: session.WorkspaceID,
			})
		},
	}

	// 上游请求在 r.Context() 完成时被释放（ReverseProxy 会取消 backend round-trip）；
	// transport 的 IdleConnTimeout 约束池化连接的闲置寿命。
	proxy.ServeHTTP(w, r)
}
