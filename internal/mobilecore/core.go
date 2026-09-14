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

// Core 是移动连接核。零值不可用，必须经 New 构造。
type Core struct {
	mu       sync.Mutex
	dial     DialFunc
	log      *slog.Logger
	machines map[string]*machine
	closed   bool
}

type machine struct {
	name    string
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
// 不可达的机器标 offline（部分 bundle，不整单失败）。重复扫码幂等：重配即覆盖
// 旧客户端并重开反代。
func (c *Core) Pair(ctx context.Context, bundleJSON string) (PairResult, error) {
	b, err := proto.DecodePairBundle(bundleJSON)
	if err != nil {
		return PairResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return PairResult{}, errors.New("连接核已关闭")
	}
	out := PairResult{}
	for _, m := range b.Machines {
		cl, cleanup, derr := c.dial(ctx, b, m)
		if derr != nil {
			c.log.Warn("配对机器登记失败，标离线待补配", "machine", m.Name, "cause", derr)
			out.Machines = append(out.Machines, PairedMachine{Name: m.Name, Online: false})
			continue
		}
		if !probeReachable(ctx, cl) {
			cleanup()
			c.log.Warn("配对机器不可达，标离线待补配", "machine", m.Name)
			out.Machines = append(out.Machines, PairedMachine{Name: m.Name, Online: false})
			continue
		}
		origin, ln, srv, lerr := c.startLoopback(m.Name, cl)
		if lerr != nil {
			cleanup()
			c.log.Warn("机器回环反代起不来，标离线待补配", "machine", m.Name, "cause", lerr)
			out.Machines = append(out.Machines, PairedMachine{Name: m.Name, Online: false})
			continue
		}
		c.replaceMachine(m.Name, &machine{name: m.Name, origin: origin, cl: cl, cleanup: cleanup, srv: srv, ln: ln})
		out.Machines = append(out.Machines, PairedMachine{Name: m.Name, Origin: origin, Online: true})
	}
	return out, nil
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
func (c *Core) Origin(machine string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	m, ok := c.machines[machine]
	if !ok {
		return "", fmt.Errorf("未配对的机器 %q", machine)
	}
	return m.origin, nil
}

// MachineNames 返回已登记机器名（供设置页配对清单）。
func (c *Core) MachineNames() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	names := make([]string, 0, len(c.machines))
	for n := range c.machines {
		names = append(names, n)
	}
	return names
}

// Close 收掉全部 loopback 反代与客户端（幂等）。
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
