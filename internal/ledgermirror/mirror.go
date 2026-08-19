// Package ledgermirror 是 agentd 的账本镜像子系统：把挂账 task 的事件
// 从各执行机镜像进账本单流。
//
// 本包不依赖 internal/agentd；事件源经 Source 注入，生产实现为
// client.StreamEventsOnce 包装，测试不碰网络。
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
	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// Source 一条 per-task 事件订阅：从 fromSeq（排他）起回放 + 跟流，
// 阻塞直到 ctx 取消或连接终结。
type Source func(ctx context.Context, addr, token, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error

// Options 子系统参数。零值取生产默认。
type Options struct {
	Holder   string
	Tick     time.Duration
	LeaseTTL time.Duration
	Source   Source
}

// DefaultSource 生产事件源：client.StreamEventsOnce 的薄包装。
func DefaultSource(ctx context.Context, addr, token, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error {
	return client.New(addr, token).StreamEventsOnce(ctx, taskID, fromSeq, onEvent)
}

var mirrorSkip = map[proto.EventType]bool{
	proto.EventTypeProgress:         true,
	proto.EventTypeApproverDecision: true,
	proto.EventTypeApproverDisabled: true,
}

var errMirrorArchived = errors.New("镜像 task 已归档")

// Mirror 镜像子系统实例。
type Mirror struct {
	st      *ledger.Store
	targets func() map[string]config.Target
	opt     Options
	log     *slog.Logger

	holding    atomic.Bool
	mu         sync.Mutex
	subs       map[string]context.CancelFunc
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

// New 构造。targets 用函数注入（config 可被 /api/machines 热改）。
func New(st *ledger.Store, targets func() map[string]config.Target, opt Options) *Mirror {
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
	return &Mirror{st: st, targets: targets, opt: opt,
		log:  slog.Default().With("subsystem", "ledgermirror"),
		subs: map[string]context.CancelFunc{}, conn: map[string]bool{}, ended: map[string]bool{}}
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

// reconcile 对账 card_tasks 与已登记 target，建立和收掉订阅，并刷新健康行。
func (m *Mirror) reconcile(ctx context.Context) {
	targets := m.targets()
	links, err := m.st.AllTaskLinks()
	if err != nil {
		m.log.Warn("读挂账表失败", "err", err)
		return
	}
	want := map[string]ledger.TaskLink{}
	for _, link := range links {
		if _, ok := targets[link.Target]; !ok {
			continue
		}
		want[link.Target+"/"+link.TaskID] = link
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for key, cancel := range m.subs {
		if _, ok := want[key]; !ok {
			cancel()
			delete(m.subs, key)
			delete(m.conn, key)
			delete(m.ended, key)
			m.log.Info("退订", "sub", key)
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
		subCtx, cancel := context.WithCancel(ctx)
		m.subs[key] = cancel
		target := targets[link.Target]
		m.wgMu.Lock()
		m.wg.Add(1)
		m.wgMu.Unlock()
		go m.subscribe(subCtx, link, target)
		m.log.Info("起订", "sub", key)
	}

	hasSub := map[string]bool{}
	alive := map[string]bool{}
	for key := range want {
		separator := strings.IndexByte(key, '/')
		if separator < 0 {
			continue
		}
		target := key[:separator]
		hasSub[target] = true
		if m.conn[key] {
			alive[target] = true
		}
	}
	for name := range targets {
		if hasSub[name] && !alive[name] {
			continue
		}
		if err := m.st.TouchMirrorHealth(name, 0); err != nil {
			m.log.Warn("touch 健康失败", "target", name, "err", err)
		}
	}
}

// subscribe 单 task 常驻订阅：watermark 起拉、断线退避重连、事件过滤后幂等落账。
func (m *Mirror) subscribe(ctx context.Context, link ledger.TaskLink, target config.Target) {
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
		err = m.opt.Source(ctx, target.Addr, target.Token, link.TaskID, wm, func(e proto.Event) error {
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
	for key, cancel := range m.subs {
		cancel()
		delete(m.subs, key)
		delete(m.conn, key)
	}
}
