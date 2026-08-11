// 本文件实现 Host 白名单中间件：在鉴权**之前**拒绝 Host 头不在白名单内的请求。
//
// 职责：
//   - 取 r.Host 的 host 部分（忽略端口）与白名单比对，不匹配即 403
//   - 白名单 = 回环三件套 + cfg.Listen 的 host（通配地址除外）+ cfg.Web.AllowedHosts
//   - 拒绝时打 Warn 并记 Host 与来源地址——这是 DNS rebinding 攻击的唯一信号
//
// 边界：
//   - 不做任何身份判断（那是 auth 中间件的事）。本层先于鉴权执行正是为了不让
//     攻击者从「凭据对不对」的状态码差异里读出信息
//   - **挡不住本机恶意进程**：进程可以伪造任意 Host 头，那一层由凭据兜住。
//     两层各司其职，不要指望其中任何一层单独成立
package agentd

import (
	"net"
	"net/http"
	"sort"
	"strings"
)

// loopbackHosts 是恒定在白名单内的回环名称。
var loopbackHosts = []string{"127.0.0.1", "localhost", "::1"}

// hostGuard 是 Host 白名单中间件，必须包在 auth 之外（先于鉴权执行）。
//
// 参数：
//   - next: 被包住的处理器（通常是 auth 包好的整棵路由）
//
// 返回：
//   - 带白名单校验的处理器
//
// 注意：
//   - 白名单在构造时算一次：cfg 构造后只读，无需每请求重算
//   - 它同时把 coder/websocket 默认 Origin 校验的洞补上了：rebinding 的
//     `Host: evil.com` 在这一层就被挡下，根本到不了 websocket.Accept
func (s *Server) hostGuard(next http.Handler) http.Handler {
	allowed := s.allowedHosts()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowed[strings.ToLower(hostOnly(r.Host))]; !ok {
			s.log.Warn("Host 不在白名单，拒绝请求（DNS rebinding 的唯一信号）",
				"host", r.Host, "remote_addr", r.RemoteAddr,
				"method", r.Method, "path", r.URL.Path)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host 不被允许"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowedHosts 计算白名单集合（小写归一）。
func (s *Server) allowedHosts() map[string]struct{} {
	out := make(map[string]struct{}, len(loopbackHosts)+len(s.cfg.Web.AllowedHosts)+1)
	for _, h := range loopbackHosts {
		out[h] = struct{}{}
	}
	// cfg.Listen 的 host：agentd 监听在 192.168.x.x 时，用该地址访问是正当的。
	// 通配地址除外——0.0.0.0 / :: 不是可用于访问的 Host，放进白名单没有意义，
	// 还会让「监听全网卡」意外变成「接受一个叫 0.0.0.0 的域名」。
	if h := strings.ToLower(hostOnly(s.cfg.Listen)); h != "" && h != "0.0.0.0" && h != "::" {
		out[h] = struct{}{}
	}
	for _, h := range s.cfg.Web.AllowedHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out[h] = struct{}{}
		}
	}
	return out
}

// hostOnly 取 host:port 中的 host 部分，并去掉 IPv6 字面量的方括号。
//
// 为什么不直接拿 r.Host 比对：端口不是安全边界。同一个 agentd 会被
// 127.0.0.1:7777 与 httptest 的随机端口访问，把端口算进白名单会让全部现有
// 测试与任意换端口的部署一起失效。
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(hostport, "[]")
}

// sortedKeys 取集合的有序键，让启动日志里的白名单顺序稳定可比。
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
