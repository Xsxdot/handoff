// 本文件实现 agentd 常驻侧的 target 客户端复用池。
//
// 职责：
//   - 按 target 名缓存客户端与其 relay 隧道，一台机器一条隧道、全子系统共用
//   - 配置变更时失效重建，target 删除时关掉并移出
//   - Names 提供「当前有哪些机器」的唯一判据（活快照）
//
// 边界：
//   - 不探活：拿到 client 之后怎么用、算不算可达，由调用方决定
//   - 不预热：预热在 warm.go，两者刻意分开（隧道通没通 ≠ 对端活没活）
//   - 调用方**不**负责关闭 For 返回的 client：隧道归池，进程退出时统一 Close
package targetclient

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/relay"
)

// entry 是一台机器的缓存条目。
//
// 存 target 原值是为了做失效判定：config.Target 全是 string 字段（可比较），
// 整体 != 比较能覆盖每一个字段——逐字段比会在 relay 将来加字段时漏掉新字段，
// 而漏掉的表现是「改了配置不生效」，属于最难查的一类。
type entry struct {
	target config.Target
	client *client.Client
	// dialer 只在 relay 形态非 nil；预热要拿它主动建隧道。
	dialer  *relay.Dialer
	cleanup func()
}

// Pool 是按 target 名缓存的客户端池。
//
// 并发安全：全部字段访问都在 mu 保护下；conf 由调用方保证并发安全
// （agentd 侧传的是 Server.conf，读的是 atomic 快照）。
type Pool struct {
	conf func() *config.Config
	log  *slog.Logger

	mu      sync.Mutex
	entries map[string]*entry
	closed  bool
	// 预热参数与缝。warmTick/warmBackoff* 生产用包级默认，测试注入毫秒级值；
	// ensure 生产为 nil（走 realEnsure），测试替换以避开真 relay 服务端。
	warmTick           time.Duration
	warmBackoffInitial time.Duration
	warmBackoffMax     time.Duration
	ensure             func(ctx context.Context, name string) error
}

// NewPool 构造复用池。
//
// 参数：
//   - conf: 取当前配置快照的函数；**每次 For/Names 都会现调**，池因此跟随活配置
//   - log: 日志器；nil 时用 slog.Default()
func NewPool(conf func() *config.Config, log *slog.Logger) *Pool {
	if log == nil {
		log = slog.Default()
	}
	return &Pool{
		conf: conf, log: log, entries: make(map[string]*entry),
		warmTick:           warmTick,
		warmBackoffInitial: warmBackoffInitial,
		warmBackoffMax:     warmBackoffMax,
	}
}

// Names 返回当前配置里全部 target 名，已排序。
//
// 排序是为了让 UI 列表与日志顺序稳定：每次刷新都跳序会让人以为数据在变。
func (p *Pool) Names() []string {
	targets := p.conf().Targets
	names := make([]string, 0, len(targets))
	for name := range targets {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// For 取一台机器的客户端，必要时构造或重建。
//
// 参数：
//   - name: target 名，必须已在配置里登记
//
// 返回：
//   - client: **调用方不负责关闭**——隧道归池所有，进程退出时由 Close 统一关
//   - err: 机器未登记、池已关闭、或 New 的选路错误（ErrNoEndpoint / token 熵不足）
//
// 注意：不发任何网络请求。relay 隧道由 Dialer 惰性建立或由 Warm 预热。
func (p *Pool) For(name string) (*client.Client, error) {
	t, ok := p.conf().Targets[name]
	if !ok {
		// 配置里没有了：连带把可能残留的缓存条目关掉，否则一条隧道会一直挂着
		p.drop(name)
		p.log.Warn("请求未登记机器的客户端", "target", name)
		return nil, fmt.Errorf("target %s 未在配置中登记", name)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, fmt.Errorf("target 客户端池已关闭")
	}
	if e, ok := p.entries[name]; ok {
		if e.target == t {
			return e.client, nil
		}
		// 配置变了：旧隧道用的是旧 token/节点，必须关掉重建
		p.log.Info("target 配置变更，重建客户端", "target", name)
		e.cleanup()
		delete(p.entries, name)
	}

	c, dialer, cleanup, err := newWithDialer(name, t, p.log)
	if err != nil {
		return nil, err
	}
	p.entries[name] = &entry{target: t, client: c, dialer: dialer, cleanup: cleanup}
	p.log.Info("target 客户端已建立并入池", "target", name, "relay", t.IsRelay(), "pool_size", len(p.entries))
	return c, nil
}

// drop 关掉并移出一条缓存条目；不存在时无副作用。
func (p *Pool) drop(name string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	e, ok := p.entries[name]
	if !ok {
		return
	}
	e.cleanup()
	delete(p.entries, name)
	p.log.Info("target 已从池中移出", "target", name, "pool_size", len(p.entries))
}

// size 返回池内条目数，仅供测试断言。
func (p *Pool) size() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.entries)
}

// Close 关掉池内全部客户端与隧道，之后 For 一律报错。
//
// 注意：relay.Dialer.Close 是终态（closed 标志阻止重连），所以池关了就不会
// 再复活——这符合进程退出语义，不要在运行期调它来「清一下缓存」。
func (p *Pool) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	for name, e := range p.entries {
		e.cleanup()
		p.log.Debug("关闭 target 客户端", "target", name)
	}
	n := len(p.entries)
	p.entries = make(map[string]*entry)
	p.log.Info("target 客户端池已关闭", "closed", n)
	return nil
}
