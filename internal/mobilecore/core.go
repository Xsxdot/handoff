// Package mobilecore 是移动端 App 的连接核（B369）：relay 拨号 + 回环反代 +
// 会话存取的共享逻辑收口在这一个根模块包，经 gomobile 编为 Android AAR / iOS
// XCFramework（mobile/ 模块只做 gomobile 绑定壳，协议零重实现）。
//
// 职责：
//   - Pair：解析配对载体（proto.PairBundle），按机器登记造已选路的 agentd 客户端
//   - 回环反代：每台机器一个独立 loopback 端口，webview 固定加载该源
//   - 会话兑换：用 token 副本程序化走 ticket→cookie（回环门禁；反代不注入凭据）
//
// 边界：
//   - 不含 agentd：不拉起任务、不托管 PTY、不供页面、不落账本
//   - 不是第二台协调机：每个动作都是对 agentd 的 API 调用，由 agentd 落账
//   - 回环反代透传：不注入凭据，无 cookie 的同机其他 App 过不了 agentd 的闸
//   - 协议实现零重实现：拨号/选路复用 internal/relay 与 internal/client
package mobilecore

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/relay"
)

// DialFunc 是 Core 与选路实现之间的唯一接缝：按一份配对 bundle 与其中一台机器
// 登记，造一个已选路的 agentd 客户端与它的收尾函数。生产实现走 relay/client；
// 测试注入假工厂即可让整条链路直通。
type DialFunc func(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error)

// PairedMachine 是配对结果里一台机器的可操作摘要（gomobile 绑定面的返回形状）。
type PairedMachine struct {
	Name   string `json:"name"`
	Origin string `json:"origin"` // 该机的 loopback 源；离线机为空
	Online bool   `json:"online"` // 离线机标记、上线后补配（部分 bundle）
}

// PairResult 是一次配对的结果：全部机器登记（含离线），不整单失败。
type PairResult struct {
	Machines []PairedMachine `json:"machines"`
}

// DefaultDial 是生产 DialFunc：relay 形态走 relay.NewDialer + client.NewRelay，
// 直连形态走 client.New。relay credential 取机器覆盖，缺省沿用管道 credential。
func DefaultDial(ctx context.Context, b proto.PairBundle, m proto.PairMachine) (*client.Client, func(), error) {
	if m.Node != "" {
		if b.Relay == nil || b.Relay.URL == "" {
			return nil, nil, errors.New("relay 形态机器缺管道 relay 端点")
		}
		cred := m.Credential
		if cred == "" {
			cred = b.Relay.Credential
		}
		if err := relay.CheckTokenEntropy(m.Token); err != nil {
			return nil, nil, err
		}
		d := relay.NewDialer(b.Relay.URL, cred, m.Node, m.Token, "", slog.Default())
		return client.NewRelay(d, m.Token), func() { _ = d.Close() }, nil
	}
	if m.Addr == "" {
		return nil, nil, errors.New("机器登记既非 relay 也非直连形态")
	}
	return client.New(m.Addr, m.Token), func() {}, nil
}

// errCoreClosed 是核关闭后的统一拒绝错误：Close 幂等，关闭后 Pair/Origin/
// Activate/Retry/Session 一律拒收。
var errCoreClosed = errors.New("连接核已关闭")

// Core 是移动连接核。零值不可用，必须经 New 构造。
//
// 会话罐是**单槽**：任何时刻至多装一机的 cookie（cookie 不按端口隔离，
// RFC 6265），切机即清空并由 Activate 重兑换。
type Core struct {
	mu     sync.Mutex
	exchMu sync.Mutex // 串行化 Activate 的领票+兑换，避免并发切机交错覆盖
	dial   DialFunc
	log    *slog.Logger

	machines map[string]*machine
	closed   bool

	active          string // 当前会话罐所属机器名；空 = 无活动会话
	cookie          SessionCookie
	cookieExpiresAt time.Time
}

type machine struct {
	name    string
	reg     proto.PairMachine // 原始登记，供离线补配重试
	relay   *proto.PairRelay  // 该机所属 bundle 的管道端点，供补配重试
	online  bool
	origin  string
	cl      *client.Client
	cleanup func()
	srv     *http.Server
	ln      net.Listener
}

// New 构造连接核。dial 为 nil 时用 DefaultDial。
func New(dial DialFunc, log *slog.Logger) *Core {
	if dial == nil {
		dial = DefaultDial
	}
	if log == nil {
		log = slog.Default()
	}
	return &Core{dial: dial, log: log, machines: map[string]*machine{}}
}

// pairProbeTimeout 是单机可达性探测的时限。relay 形态的拨号是懒建的，造出
// Dialer 不等于隧道通——必须真发一次请求才能把「在线/离线」判成可证实的事实
// （spec：有机器离线时 = 部分 bundle，离线机标记、上线后补配）。
const pairProbeTimeout = 3 * time.Second

// Pair 解析一份配对 bundle 并登记其中的全部机器：可达的机器起 loopback 反代，
// 不可达的机器标 offline（部分 bundle，不整单失败）。重复扫码幂等：重配覆盖
// 旧客户端与反代，不泄漏旧端口与 goroutine。
func (c *Core) Pair(ctx context.Context, bundleJSON string) (PairResult, error) {
	b, err := proto.DecodePairBundle(bundleJSON)
	if err != nil {
		// 解码失败不登记任何机器（契约条 21）。
		return PairResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return PairResult{}, errCoreClosed
	}
	out := PairResult{}
	for _, m := range b.Machines {
		out.Machines = append(out.Machines, c.registerLocked(ctx, b, m))
	}
	return out, nil
}

// registerLocked 登记一台机器（调用方须持有 c.mu）：可达则拨号→探测→起回环
// 反代并标在线，不可达则**在册**标离线（部分 bundle，不整单失败；离线机保留
// 原始登记供 Retry 补配）。同名旧机器一律先停再换（重复扫码幂等、不泄漏资源）。
func (c *Core) registerLocked(ctx context.Context, b proto.PairBundle, m proto.PairMachine) PairedMachine {
	offline := &machine{name: m.Name, reg: m, relay: b.Relay, online: false}

	cl, cleanup, derr := c.dial(ctx, b, m)
	if derr != nil {
		c.log.Warn("配对机器登记失败，标离线待补配", "machine", m.Name, "cause", derr)
		c.replaceMachine(m.Name, offline)
		return PairedMachine{Name: m.Name, Online: false}
	}
	if !probeReachable(ctx, cl) {
		cleanup()
		c.log.Warn("配对机器不可达，标离线待补配", "machine", m.Name)
		c.replaceMachine(m.Name, offline)
		return PairedMachine{Name: m.Name, Online: false}
	}
	origin, ln, srv, lerr := c.startLoopback(m.Name, cl)
	if lerr != nil {
		cleanup()
		c.log.Warn("机器回环反代起不来，标离线待补配", "machine", m.Name, "cause", lerr)
		c.replaceMachine(m.Name, offline)
		return PairedMachine{Name: m.Name, Online: false}
	}
	c.replaceMachine(m.Name, &machine{
		name: m.Name, reg: m, relay: b.Relay, online: true,
		origin: origin, cl: cl, cleanup: cleanup, srv: srv, ln: ln,
	})
	c.log.Info("机器配对在线", "machine", m.Name, "origin", origin)
	return PairedMachine{Name: m.Name, Origin: origin, Online: true}
}

// Retry 对一台此前标记离线的机器重试配对（spec「离线机标记、上线后补配」）：
// 可达则补起回环反代并翻 online，仍不可达则保持 offline（返回 Online=false 且
// 不报错——补配是幂等重试，不是一次必须成功的操作）。
//
// 参数：
//   - machine: 已登记机器名（在线/离线均可；在线机重试等价重配）
//
// 返回：
//   - 该机的最新配对摘要
//   - 未登记机器、核已关闭时返回错误
func (c *Core) Retry(ctx context.Context, machine string) (PairedMachine, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return PairedMachine{}, errCoreClosed
	}
	old, ok := c.machines[machine]
	if !ok {
		return PairedMachine{}, fmt.Errorf("未配对的机器 %q", machine)
	}
	c.log.Info("离线补配重试开始", "machine", machine, "was_online", old.online)
	b := proto.PairBundle{Version: proto.PairVersion, Relay: old.relay}
	pm := c.registerLocked(ctx, b, old.reg)
	c.log.Info("离线补配重试结束", "machine", machine, "online", pm.Online)
	return pm, nil
}

// Activate 把某台机器的会话罐置为当前：切机即清掉旧机器的 cookie，程序化
// 兑换（或命中同机缓存复用）本机的 ticket→cookie，返回应注入 webview cookie
// jar 的 cookie。
//
// 参数：
//   - machine: 已配对且在线机器名
//
// 返回：
//   - 该机的 handoff_session cookie
//   - 机器未配对、核已关闭、兑换失败时返回错误（不返回空 cookie 冒充成功）
//
// 注意：
//   - 每机独立回环端口 + 核内单槽罐：任何时刻一罐只装一机的 cookie
//     （cookie 不按端口隔离，RFC 6265）
//   - 同机重复调用命中缓存，不重复领票；切到别的机器再切回会重新兑换
//   - 兑换在 c.exchMu 下串行（网络调用不持 c.mu），清罐在兑换前显式执行
func (c *Core) Activate(ctx context.Context, machine string) (SessionCookie, error) {
	c.exchMu.Lock()
	defer c.exchMu.Unlock()
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return SessionCookie{}, errCoreClosed
	}
	m, ok := c.machines[machine]
	if !ok {
		c.mu.Unlock()
		return SessionCookie{}, fmt.Errorf("未配对的机器 %q", machine)
	}
	if !m.online {
		c.mu.Unlock()
		return SessionCookie{}, fmt.Errorf("机器 %q 当前离线，无法建立会话", machine)
	}
	if c.active == machine && c.cookieValidLocked() {
		ck := c.cookie
		c.mu.Unlock()
		return ck, nil
	}
	// 清罐：任何时刻一罐只装一机的 cookie——切机先把旧机的 cookie 清掉。
	c.active = ""
	c.cookie = SessionCookie{}
	c.cookieExpiresAt = time.Time{}
	origin, cl := m.origin, m.cl
	c.mu.Unlock()

	ck, err := exchangeTicket(ctx, machine, origin, cl, c.log)
	if err != nil {
		return SessionCookie{}, err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return SessionCookie{}, errCoreClosed
	}
	c.active = machine
	c.cookie = ck
	c.cookieExpiresAt = expiryFrom(ck)
	c.mu.Unlock()
	c.log.Info("会话罐已切换", "machine", machine, "origin", origin, "cookie_name", ck.Name)
	return ck, nil
}

// Session 返回当前活动机器的会话 cookie（无活动会话时返回错误）。
func (c *Core) Session() (SessionCookie, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return SessionCookie{}, errCoreClosed
	}
	if !c.cookieValidLocked() {
		return SessionCookie{}, errors.New("当前没有活动会话")
	}
	return c.cookie, nil
}

// ActiveMachine 返回当前会话罐所属机器名；无活动会话时为空串。
func (c *Core) ActiveMachine() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.active
}

// cookieValidLocked 判定单槽罐里是否装着一个可用会话（调用方须持有 c.mu）。
func (c *Core) cookieValidLocked() bool {
	if c.active == "" || c.cookie.Name == "" || c.cookie.Value == "" {
		return false
	}
	if !c.cookieExpiresAt.IsZero() && !time.Now().Before(c.cookieExpiresAt) {
		return false
	}
	return true
}

// expiryFrom 把 cookie 的 MaxAge 翻成绝对过期时刻；<=0（会话 cookie）返回零值。
func expiryFrom(ck SessionCookie) time.Time {
	if ck.MaxAge <= 0 {
		return time.Time{}
	}
	return time.Now().Add(time.Duration(ck.MaxAge) * time.Second)
}

// probeReachable 发一次最小请求判该机器是否可达。任何 HTTP 响应（含 404/503）
// 都算可达——只把传输层失败判为离线；不解析响应体。
func probeReachable(ctx context.Context, cl *client.Client) bool {
	pctx, cancel := context.WithTimeout(ctx, pairProbeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(pctx, http.MethodGet, cl.BaseURL()+"/api/status", nil)
	if err != nil {
		return false
	}
	resp, err := cl.HTTPClient().Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// replaceMachine 关掉同名旧机器的资源再登记新机器（重复扫码幂等）。
func (c *Core) replaceMachine(name string, next *machine) {
	if old, ok := c.machines[name]; ok {
		stopMachine(old)
	}
	c.machines[name] = next
}

// Origin 返回某台机器 webview 应加载的 loopback 源。
//
// 边界：未配对或离线的机器都返回错误——离线机没有可加载的源，前端据
// PairResult.Online=false 显示「离线」而非加载空源（contract 缺陷族 7）。
func (c *Core) Origin(machine string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return "", errCoreClosed
	}
	m, ok := c.machines[machine]
	if !ok {
		return "", fmt.Errorf("未配对的机器 %q", machine)
	}
	if !m.online {
		return "", fmt.Errorf("机器 %q 当前离线", machine)
	}
	return m.origin, nil
}

// MachineNames 返回已登记机器名（含离线机，供设置页配对清单）。
func (c *Core) MachineNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.machines))
	for n := range c.machines {
		names = append(names, n)
	}
	return names
}

// Close 收掉全部 loopback 反代与客户端并清空会话罐（幂等）。
func (c *Core) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	for _, m := range c.machines {
		stopMachine(m)
	}
	c.machines = map[string]*machine{}
	c.active = ""
	c.cookie = SessionCookie{}
	c.cookieExpiresAt = time.Time{}
	return nil
}

func stopMachine(m *machine) {
	if m.srv != nil {
		_ = m.srv.Close()
	}
	if m.ln != nil {
		_ = m.ln.Close()
	}
	if m.cleanup != nil {
		m.cleanup()
	}
}

// startLoopback 为单台机器起一个独立 loopback 端口与透传反代。每机独立端口是
// 多机会话域语义的承载：cookie 不按端口隔离（RFC 6265），切机由上层清 webview
// 会话罐并重兑换，任何时刻一罐只装一机的 cookie。
func (c *Core) startLoopback(machineName string, cl *client.Client) (string, net.Listener, *http.Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, nil, fmt.Errorf("回环监听: %w", err)
	}
	srv := &http.Server{Handler: newReverseProxy(machineName, cl, c.log)}
	go func() { _ = srv.Serve(ln) }()
	return "http://" + ln.Addr().String(), ln, srv, nil
}
