// 本文件实现 Host 白名单中间件：在鉴权**之前**拒绝 Host 头不在白名单内的请求。
//
// 职责：
//   - 取 r.Host 的 host 部分（忽略端口）与白名单比对，不匹配即 403
//   - 白名单 = 回环三件套 + cfg.Listen 的 host（通配地址除外）+ cfg.Web.AllowedHosts
//     + **监听通配地址时，本机网卡的非回环 IP**（B104）
//   - 拒绝时打 Warn 并记 Host 与来源地址——这是 DNS rebinding 攻击的唯一信号
//
// 边界：
//   - 不做任何身份判断（那是 auth 中间件的事）。本层先于鉴权执行正是为了不让
//     攻击者从「凭据对不对」的状态码差异里读出信息
//   - **挡不住本机恶意进程**：进程可以伪造任意 Host 头，那一层由凭据兜住。
//     两层各司其职，不要指望其中任何一层单独成立
package agentd

import (
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// loopbackHosts 是恒定在白名单内的回环名称。
var loopbackHosts = []string{"127.0.0.1", "localhost", "::1"}

// localIPsFn 是本机网卡地址枚举的测试缝。生产恒为 localIPs。
var localIPsFn = localIPs

// nicRefreshGap 是「白名单未命中时重新枚举网卡」的最小间隔。
//
// 为什么需要这个间隔：网卡地址会在运行中变（VPN 上下线、DHCP 续租、Tailscale
// 刚接入）。构造时算一次的白名单会让「agentd 起来之后才拿到的地址」永远进不了
// 名单——那正是 B104 要修的那类「必须重启才好」的故障。但也不能每请求都枚举，
// 所以：只在**未命中**且 Host 是 IP 字面量时重算，且两次重算至少隔这么久。
//
// 是变量而非常量：测试要把它调到 0，否则一条用例里连着两次探测就被间隔挡住。
var nicRefreshGap = 5 * time.Second

// localIPs 枚举本机所有网卡上的非回环单播地址。
//
// 返回：地址的字符串形式（IPv6 不带方括号也不带 zone）；枚举失败时返回 nil
//
// 为什么不过滤链路本地地址：它们同样是「这台机器的地址」，用它访问本机是正当的。
// 名单里多几条本机自己的地址不扩大攻击面——见 allowedHosts 的安全论证。
func localIPs() []string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP == nil || ipnet.IP.IsLoopback() {
			continue
		}
		out = append(out, strings.ToLower(ipnet.IP.String()))
	}
	return out
}

// isWildcardListen 判断监听地址是不是「全网卡」。
//
// 参数：listen 为 cfg.Listen 原文（可能是 ":7777" / "0.0.0.0:7777" / "0.0.0:7777"）
//
// 注意：空 host 也算通配——`:7777` 与 `0.0.0.0:7777` 语义相同。
// `0.0.0` 这种少一段的写法在生产配置里真实出现过（mac-02 的 `listen: 0.0.0:7777`），
// 它被 net 库当成合法 host 解析，所以必须一并认出来，否则这条修复对它无效。
func isWildcardListen(listen string) bool {
	switch strings.ToLower(hostOnly(listen)) {
	case "", "0.0.0.0", "0.0.0", "::", "[::]":
		return true
	}
	return false
}

// hostAllowlist 是 Host 白名单的运行时载体：静态集合 + 通配监听下的网卡地址补充。
//
// 职责：回答「这个 Host 允许吗」，并在监听通配地址时按需刷新本机网卡地址。
//
// 边界：不认识 HTTP，也不决定拒绝之后做什么（打日志、写 403 都是中间件的事）。
type hostAllowlist struct {
	mu       sync.RWMutex
	set      map[string]struct{}
	wildcard bool      // 监听通配地址：本机网卡 IP 也是正当 Host
	nextScan time.Time // 下一次允许重新枚举网卡的时刻
	log      *slog.Logger
}

// allow 判断 host（已去端口、未归一大小写）是否在白名单内。
//
// 参数：host 为 hostOnly 的结果
//
// 返回：允许则 true
//
// 注意：只有「未命中 + 监听通配 + host 是 IP 字面量」三条同时成立时才会重新枚举
// 网卡。**域名一律不触发枚举**——域名不可能等于本机网卡 IP，为它付 syscall 代价
// 毫无收益，而且那会让攻击者用随机域名刷出无限次 InterfaceAddrs 调用。
func (a *hostAllowlist) allow(host string) bool {
	h := strings.ToLower(host)
	a.mu.RLock()
	_, ok := a.set[h]
	a.mu.RUnlock()
	if ok {
		return true
	}
	if !a.wildcard || net.ParseIP(h) == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.set[h]; ok { // 等锁期间已被别的请求补进来
		return true
	}
	now := time.Now()
	if now.Before(a.nextScan) {
		return false
	}
	a.nextScan = now.Add(nicRefreshGap)
	added := 0
	for _, ip := range localIPsFn() {
		if _, dup := a.set[ip]; !dup {
			a.set[ip] = struct{}{}
			added++
		}
	}
	if added > 0 {
		// 这条要能看见：它意味着「本机地址在运行中变了」，是排查跨机访问
		// 时最需要的一行——否则表现只是一个说不清原因的 403
		a.log.Info("监听通配地址，重新枚举本机网卡后补入 Host 白名单",
			"added", added, "probe_host", h)
	}
	_, ok = a.set[h]
	return ok
}

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
	al := s.hostAllowlist()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h := hostOnly(r.Host); !al.allow(h) {
			s.log.Warn("Host 不在白名单，拒绝请求（DNS rebinding 的唯一信号）",
				"host", r.Host, "remote_addr", r.RemoteAddr,
				"method", r.Method, "path", r.URL.Path)
			// 错误文案给「下一步做什么」而不只是「不行」：跨机访问被挡是这条
			// 判据最常见的**正当**失败，而从控制台上它只显示成「已断开」。
			// 不回显白名单内容——那对排障没有增量，对探测者反而是情报
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "Host 不被允许：" + h +
					"（若这是跨机访问，请在该机 ~/.handoff/config.yaml 的 web.allowed_hosts 里加入这个地址后重启 agentd）",
			})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// hostAllowlist 构造白名单载体。
//
// 返回：静态集合已算好；监听通配地址时 wildcard 为 true，本机网卡地址已并入，
// 并允许运行期按需重新枚举（见 hostAllowlist.allow）
func (s *Server) hostAllowlist() *hostAllowlist {
	return &hostAllowlist{
		set:      s.allowedHosts(),
		wildcard: isWildcardListen(s.cfg.Listen),
		log:      s.log,
	}
}

// allowedHosts 计算白名单集合（小写归一）。
//
// 安全论证（为什么把本机网卡 IP 放进来不削弱 DNS rebinding 防线）：rebinding
// 的机制是让**攻击者的域名**解析到目标地址，浏览器随后发出的请求里 Host 头是
// **那个域名**（`evil.com`），不是 IP——这正是本中间件挡得住它的原因。要让浏览器
// 发出 `Host: 100.73.238.21`，页面必须直接请求 `http://100.73.238.21:port/`，
// 那是一个普通的跨源请求：同源策略不让攻击者读响应，而写操作仍要过后面的鉴权层。
// 换句话说，本机自己的 IP 出现在名单里，既不给攻击者读通道，也不给它免鉴权的
// 写通道。**能任意伪造 Host 的非浏览器客户端本来就不受 rebinding 约束**，那一层
// 从来是由凭据兜底的（见本文件头「边界」第二条）。
func (s *Server) allowedHosts() map[string]struct{} {
	out := make(map[string]struct{}, len(loopbackHosts)+len(s.cfg.Web.AllowedHosts)+1)
	for _, h := range loopbackHosts {
		out[h] = struct{}{}
	}
	// cfg.Listen 的 host：agentd 监听在 192.168.x.x 时，用该地址访问是正当的。
	// 通配地址除外——0.0.0.0 / :: 不是可用于访问的 Host，放进白名单没有意义，
	// 还会让「监听全网卡」意外变成「接受一个叫 0.0.0.0 的域名」。
	if h := strings.ToLower(hostOnly(s.cfg.Listen)); h != "" && !isWildcardListen(s.cfg.Listen) {
		out[h] = struct{}{}
	}
	// 监听通配地址时补上本机网卡的非回环 IP（B104）：「监听全网卡 + 被远程协调者
	// 用 IP 访问」是 handoff 最标准的远程执行机形态，而它以前每台都要手工补一行
	// 配置才能被控制台看见，失败信号还只是一个 403——从控制台上只显示「已断开」。
	if isWildcardListen(s.cfg.Listen) {
		for _, ip := range localIPsFn() {
			out[ip] = struct{}{}
		}
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
