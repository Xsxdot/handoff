// Package ledgermirror 是 agentd 的账本镜像子系统：把挂账 task 的事件
// 从各执行机镜像进账本单流。
//
// 本包不依赖 internal/agentd，也不依赖 internal/targetclient：机器清单与
// 客户端经 Machines 接口注入（消费者侧接口），生产实现是 agentd 的
// target 客户端池，测试注入内存实现，全程不碰网络。
//
// 边界：本包不解析机器地址、不选传输形态（直连 / relay）、不管隧道生命周期
// —— 那三件事全归池。包内出现 config.Target 的 Addr/Token 字段读取即是回退。
package ledgermirror

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// Source 一条 per-task 事件订阅：从 fromSeq（排他）起回放 + 跟流，
// 阻塞直到 ctx 取消或连接终结。
//
// 参数：
//   - c: 该机器当前的客户端，由 Machines.For 现取；**每次重订阅都重新取**，
//     机器改了地址或令牌时池会重建客户端，旧实例不再被使用（B163 ②）
//   - fromSeq: 排他水位，调用方按本机已落账的最大 source_seq 传
//
// 注意：实现方不得关闭 c —— 隧道归池，进程退出时统一关。
type Source func(ctx context.Context, c *client.Client, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error

// Options 子系统参数。零值取生产默认。
type Options struct {
	Holder   string
	Tick     time.Duration
	LeaseTTL time.Duration
	Source   Source
}

// Machines 是账本镜像对「有哪些机器、怎么连它」的唯一依赖。
//
// 为什么是消费者侧接口而不是直接 import targetclient：本包只需要「清单 +
// 取客户端」两件事，声明在这里既让测试能注入内存实现，也不给本包引入
// relay 传输栈的依赖。生产实现是 *targetclient.Pool，方法集逐字相同。
//
// 注意：For 不发任何网络请求，可以每轮对账现调；它的错误是**配置性**的
// （机器未登记 / 无端点 / relay token 熵不足），不是网络抖动——因此
// For 失败按「这台机器当前不可用」处理，而不是重试。
type Machines interface {
	// Names 返回当前登记的机器名，已排序。判据取自活配置快照。
	Names() []string
	// For 取某台机器的客户端；**调用方不负责关闭它**。
	For(name string) (*client.Client, error)
}

// subscription 是一条在飞订阅的登记：取消函数 + 它当时用的客户端实例。
//
// 为什么要记住客户端实例：它就是「这台机器的配置变没变」的判据。池在
// target 值不变时返回同一实例，变了则关掉旧隧道重建
// （internal/targetclient/pool.go 的 e.target == t 判等）。于是
// 实例不等 ⇒ 配置已变 ⇒ 必须退订重订；否则在飞订阅会拿旧 addr/token
// 无限重连下去（B163 ②，改地址后永不生效）。
type subscription struct {
	cancel context.CancelFunc
	client *client.Client
}

// DefaultSource 生产事件源：client.StreamEventsOnce 的薄包装。
//
// 为什么带 MarkForwarded：这一跳是 agentd→agentd，标记让对端不再向外扇出
// （一跳封顶，A→B→A 不成环），与任务镜像的镜像订阅同款
// （internal/agentd/mirror.go 的 c.MarkForwarded().StreamEventsOnce）。
func DefaultSource(ctx context.Context, c *client.Client, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error {
	return c.MarkForwarded().StreamEventsOnce(ctx, taskID, fromSeq, onEvent)
}

var mirrorSkip = map[proto.EventType]bool{
	proto.EventTypeProgress:         true,
	proto.EventTypeApproverDecision: true,
	proto.EventTypeApproverDisabled: true,
}

var errMirrorArchived = errors.New("镜像 task 已归档")

// Mirror 镜像子系统实例。
type Mirror struct {
	st       *ledger.Store
	machines Machines
	opt      Options
	log      *slog.Logger

	holding    atomic.Bool
	mu         sync.Mutex
	subs       map[string]*subscription
	conn       map[string]bool
	ended      map[string]bool
	wg         sync.WaitGroup
	wgMu       sync.Mutex
	runMu      sync.Mutex
	runStop    context.CancelFunc
	runDone    chan struct{}
	runStarted atomic.Bool
	stopAsked  atomic.Bool
}

// New 构造。
//
// 参数：
//   - st: 账本库，镜像事件的落点
//   - machines: 活的机器清单与客户端来源；生产传 agentd 的 target 客户端池，
//     **必须与任务镜像共用同一个池实例**——两个池等于两套 relay 隧道，
//     relay 侧会看到重复的节点连接
//   - opt: 参数，零值取生产默认
func New(st *ledger.Store, machines Machines, opt Options) *Mirror {
	if opt.Tick == 0 {
		opt.Tick = 10 * time.Second
	}
	if opt.LeaseTTL == 0 {
		opt.LeaseTTL = 30 * time.Second
	}
	if opt.Source == nil {
		opt.Source = DefaultSource
	}
	if opt.Holder == "" {
		opt.Holder = "unknown"
	}
	return &Mirror{st: st, machines: machines, opt: opt,
		log:  slog.Default().With("subsystem", "ledgermirror"),
		subs: map[string]*subscription{}, conn: map[string]bool{}, ended: map[string]bool{}}
}

func (m *Mirror) setConn(key string, ok bool) {
	m.mu.Lock()
	m.conn[key] = ok
	m.mu.Unlock()
}

// Holding 当前是否持有镜像 lease（测试与状态面用）。
func (m *Mirror) Holding() bool { return m.holding.Load() }

// Run 主循环：每 Tick 取/续 lease；持有则对账订阅集，失去则停掉全部
// 订阅（续约失败立即停写，绝不双写）。阻塞直到 ctx 取消。
func (m *Mirror) Run(ctx context.Context) {
	runCtx, runStop := context.WithCancel(ctx)
	runDone := make(chan struct{})
	m.runMu.Lock()
	m.runStop = runStop
	m.runDone = runDone
	m.runMu.Unlock()
	m.runStarted.Store(true)
	defer close(runDone)
	defer runStop()
	if m.stopAsked.Load() || runCtx.Err() != nil {
		return
	}
	m.log.Info("账本镜像子系统启动", "holder", m.opt.Holder, "tick", m.opt.Tick, "lease_ttl", m.opt.LeaseTTL)
	tick := time.NewTicker(m.opt.Tick)
	defer tick.Stop()
	for {
		if runCtx.Err() != nil {
			return
		}
		got, err := m.st.AcquireMirrorLease(m.opt.Holder, m.opt.LeaseTTL)
		if err != nil {
			m.log.Warn("lease 操作失败", "err", err)
		}
		if got != m.holding.Load() {
			m.log.Info("镜像 lease 状态变化", "holding", got)
		}
		m.holding.Store(got)
		if got {
			m.reconcile(runCtx)
		} else {
			m.stopAllSubs("失去 lease")
		}
		select {
		case <-runCtx.Done():
			m.holding.Store(false)
			m.log.Info("账本镜像子系统退出", "cause", runCtx.Err())
			m.stopAllSubs("关停")
			return
		case <-tick.C:
		}
	}
}

// Stop 等全部订阅 goroutine 退出。必须在账本库 Close 之前调用。
func (m *Mirror) Stop() {
	m.stopAsked.Store(true)
	m.holding.Store(false)
	m.runMu.Lock()
	runStop, runDone := m.runStop, m.runDone
	m.runMu.Unlock()
	if m.runStarted.Load() && runStop != nil {
		runStop()
	}
	m.stopAllSubs("Stop")
	m.wgMu.Lock()
	m.wg.Wait()
	m.wgMu.Unlock()
	if m.runStarted.Load() && runDone != nil {
		<-runDone
	}
}

// reconcile 对账挂账表与当前登记机器，建立和收掉订阅，并刷新健康行。
//
// 三条判据都取自活配置（经 Machines）：机器在不在、客户端是不是同一个实例、
// 该机器本轮取不取得到客户端。任何一条不成立都收掉对应订阅——镜像宁可少订，
// 也不能拿旧凭据连下去（那正是 B163 ② 的形态：改了地址却永不生效）。
func (m *Mirror) reconcile(ctx context.Context) {
	names := m.machines.Names()
	registered := make(map[string]bool, len(names))
	for _, n := range names {
		registered[n] = true
	}
	links, err := m.st.AllTaskLinks()
	if err != nil {
		m.log.Warn("读挂账表失败", "err", err)
		return
	}
	live, err := m.st.LiveMirrorTargets()
	if err != nil {
		m.log.Warn("读在飞镜像 target 失败", "err", err)
		return
	}
	want := map[string]ledger.TaskLink{}
	for _, link := range links {
		if !registered[link.Target] {
			continue
		}
		want[link.Target+"/"+link.TaskID] = link
	}

	// 现取客户端：只对本轮真有挂账的机器取一次（For 不发网络请求）。
	// 取不到就当这台机器本轮不可用——For 的错误是配置性的，重试没有意义。
	clients := map[string]*client.Client{}
	for _, link := range want {
		if _, ok := clients[link.Target]; ok {
			continue
		}
		c, err := m.machines.For(link.Target)
		if err != nil {
			m.log.Warn("取机器客户端失败，本轮跳过该机器", "target", link.Target, "err", err)
			continue
		}
		clients[link.Target] = c
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for key, sub := range m.subs {
		link, ok := want[key]
		switch {
		case !ok:
			m.dropSubLocked(key, sub, "机器或挂账已不在")
			// 挂账/机器都没了，终态记忆一并忘掉：将来它再回来时按新的一轮处理
			delete(m.ended, key)
		case clients[link.Target] == nil:
			m.dropSubLocked(key, sub, "本轮取不到该机器的客户端")
		case clients[link.Target] != sub.client:
			m.dropSubLocked(key, sub, "机器配置已变更，退订重订")
		}
	}
	for key, link := range want {
		if ctx.Err() != nil {
			return
		}
		if m.ended[key] {
			continue
		}
		if _, ok := m.subs[key]; ok {
			continue
		}
		c := clients[link.Target]
		if c == nil {
			continue
		}
		subCtx, cancel := context.WithCancel(ctx)
		m.subs[key] = &subscription{cancel: cancel, client: c}
		m.wgMu.Lock()
		m.wg.Add(1)
		m.wgMu.Unlock()
		go m.subscribe(subCtx, link, c)
		m.log.Info("起订", "sub", key, "target", link.Target)
	}

	// 活连接按「还没归档的订阅」计。已 archived 的挂账行仍在 card_tasks 里，
	// 但订阅已退——再拿它们挡空 touch，心跳会停在最后一条镜像上，看板把
	// 「没东西可镜像」画成断链（mac-02 本机全归档后亮「事件流滞后」就是这个）。
	alive := map[string]bool{}
	for key := range want {
		if m.ended[key] {
			continue
		}
		separator := strings.IndexByte(key, '/')
		if separator < 0 {
			continue
		}
		target := key[:separator]
		if m.conn[key] {
			alive[target] = true
		}
	}
	for _, name := range names {
		// 仍有非终态挂账、但当前一条活连接都没有 → 真断链，不刷新心跳。
		if live[name] && !alive[name] {
			continue
		}
		if err := m.st.TouchMirrorHealth(name, 0); err != nil {
			m.log.Warn("touch 健康失败", "target", name, "err", err)
		}
	}
	// 配置里已经没有、只剩 cursor 的名字：全归档就空 touch（旧看板只看
	// updated_at）；仍有在飞挂账则不碰——订不到，应当亮灯。
	rows, err := m.st.MirrorHealth()
	if err != nil {
		m.log.Warn("读镜像健康失败", "err", err)
		return
	}
	for _, row := range rows {
		if registered[row.Target] {
			continue
		}
		if live[row.Target] {
			continue
		}
		if err := m.st.TouchMirrorHealth(row.Target, 0); err != nil {
			m.log.Warn("touch 健康失败", "target", row.Target, "err", err)
		}
	}
}

// dropSubLocked 退订一条并清掉连接状态。**调用方必须持有 m.mu**。
//
// 注意：不动 m.ended —— 那是「这条挂账已终态」的记忆，与「这条订阅为什么被收掉」
// 是两件事；误删会让已归档的 task 在下一轮被重新订阅。
func (m *Mirror) dropSubLocked(key string, sub *subscription, reason string) {
	sub.cancel()
	delete(m.subs, key)
	delete(m.conn, key)
	m.log.Info("退订", "sub", key, "reason", reason)
}

// subscribe 单 task 常驻订阅：watermark 起拉、断线退避重连、事件过滤后幂等落账。
//
// 参数 c 是起订那一刻的客户端实例；它对应的机器配置若在运行期被改，reconcile
// 会取消本 ctx 并用新实例重起一条（见 subscription 的注释），**本函数不自行换客户端**。
func (m *Mirror) subscribe(ctx context.Context, link ledger.TaskLink, c *client.Client) {
	defer m.wg.Done()
	key := link.Target + "/" + link.TaskID
	defer m.setConn(key, false)
	backoff := 300 * time.Millisecond
	const maxBackoff = 10 * time.Second
	for ctx.Err() == nil {
		wm, err := m.st.MirrorWatermark(link.Target, link.TaskID)
		if err != nil {
			m.log.Warn("读 watermark 失败，退避重试", "target", link.Target, "task", link.TaskID,
				"backoff", backoff, "err", err)
			m.setConn(key, false)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}
		m.setConn(key, true)
		err = m.opt.Source(ctx, c, link.TaskID, wm, func(e proto.Event) error {
			if mirrorSkip[e.Type] {
				return nil
			}
			wrote, err := m.st.AppendMirroredEvent(link.CardID, ledger.MirroredEvent{
				Target: link.Target, Task: link.TaskID, SourceSeq: e.Seq,
				Type: string(e.Type), Payload: e.Payload, CreatedAt: e.CreatedAt,
			})
			if err != nil {
				return err
			}
			if wrote {
				m.log.Debug("镜像事件", "target", link.Target, "task", link.TaskID,
					"seq", e.Seq, "type", e.Type)
				if err := m.st.TouchMirrorHealth(link.Target, e.Seq); err != nil {
					m.log.Warn("touch 健康失败", "target", link.Target, "err", err)
				}
			}
			if e.Type == proto.EventTypeArchived {
				return errMirrorArchived
			}
			return nil
		})
		m.setConn(key, false)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errMirrorArchived) || err == nil {
			m.mu.Lock()
			m.ended[key] = true
			m.mu.Unlock()
			m.log.Info("订阅正常终结（task 归档）", "target", link.Target, "task", link.TaskID)
			return
		}
		m.log.Warn("订阅断开，退避重连", "target", link.Target, "task", link.TaskID,
			"backoff", backoff, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (m *Mirror) stopAllSubs(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.subs) > 0 {
		m.log.Info("停全部订阅", "n", len(m.subs), "reason", reason)
	}
	for key, sub := range m.subs {
		sub.cancel()
		delete(m.subs, key)
		delete(m.conn, key)
	}
}
