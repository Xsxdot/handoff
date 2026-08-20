// 本文件实现远端任务的事件镜像：让浏览器永远只连本机一条 WS，也能看到
// 远端任务的实时事件。
//
// 职责：
//   - discovery loop：每 mirrorDiscoveryTick 对每台 target 拉一次任务列表，
//     给活跃任务开上游订阅，给终态任务收掉订阅
//   - 上游订阅：从本机水位续拉，事件写进 mirror_events 并 Publish 进本机 Hub
//   - 快照刷新：收到事件即防抖刷新该 target 的任务快照（事件即门铃）
//
// 边界：
//   - 不改派发链路：dispatch --target 仍是 CLI 直拨远端，本机 agentd 不知情；
//     镜像因此挂在**发现**上而不是派发上
//   - 不推导状态：任务状态一律来自远端的 GET /api/tasks，本机不拿事件重算
//     状态机（那是重新实现一遍状态机，两份必然漂移）
//   - 不改 CLI wait：--target 直拨照旧。镜像跑稳后再谈让 wait 走本机
//   - 副本不是真相：整表删掉可从 from_seq=0 重建
package agentd

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/targetclient"
)

// mirrorDiscoveryTick 是发现轮询间隔（§6.1）。慢对账靠它补漏「不伴随事件的
// 跃迁」与断线空窗。
const mirrorDiscoveryTick = 30 * time.Second

// 上游断线重连退避（§6.1）：300ms 起、×2、上限 10s。
// 刻意快于审核者 CLI 的 1s→60s：镜像断线期间本机看板会显示陈旧数据，
// 而 CLI 断线只是一个人在等。
const (
	mirrorBackoffInitial = 300 * time.Millisecond
	mirrorBackoffMax     = 10 * time.Second
)

// snapshotDebounce 是「事件即门铃」的防抖窗口：突发事件合并成一次列表拉取。
const snapshotDebounce = 500 * time.Millisecond

// mirrorDiscoverBudget 是单轮发现对全部 target 的总预算（与机器探活同量级）。
const mirrorDiscoverBudget = 3 * time.Second

// errMirrorDone 是镜像订阅的内部哨兵：事件流收到终态事件，循环该收手了。
//
// 为什么需要一个哨兵而不是让 StreamEventsOnce 自然返回：终态事件到达后远端
// 可能仍挂着连接（等任务归档），而 mirror_events 不会再增长；显式返回它让
// 订阅循环立刻退出，不必等远端掐线。
var errMirrorDone = errors.New("镜像任务已终结")

// Mirror 是事件镜像的管理器：发现远端活跃任务、持有上游订阅、维护快照。
//
// 并发安全：subs/ring 两个 map 的全部访问都在 mu 保护下；wg 跟踪全部
// 订阅与门铃 goroutine，Stop 等它们收干净才返回。
type Mirror struct {
	// pool 同时提供两样东西：客户端与「有哪些机器」的判据。
	//
	// 为什么不再持 *config.Config：那是 NewMirror 时的静态快照，控制台运行期
	// 新增的机器要重启 agentd 才会被镜像——而「加完看不见」很容易被误当成
	// 对端故障去查。
	pool *targetclient.Pool
	st   *store.Store
	hub  *Hub
	log  *slog.Logger

	mu    sync.Mutex
	subs  map[string]context.CancelFunc // task_id → 取消订阅
	ring  map[string]chan struct{}      // target → 防抖门铃（缓冲 1）
	loops map[string]struct{}           // 已启动的 target 快照循环
	wg    sync.WaitGroup
}

// NewMirror 创建镜像管理器。
//
// 参数：
//   - pool: target 客户端池（同时提供客户端与活的 target 清单）
//   - st: 本机存储（mirror_events / mirror_tasks 的落点）
//   - hub: 本机实时路由（镜像事件经它 Publish，让 /ws/events 订阅者立刻收到）
//   - log: 本镜像的日志入口
func NewMirror(pool *targetclient.Pool, st *store.Store, hub *Hub, log *slog.Logger) *Mirror {
	return &Mirror{
		pool:  pool,
		st:    st,
		hub:   hub,
		log:   log,
		subs:  map[string]context.CancelFunc{},
		ring:  map[string]chan struct{}{},
		loops: map[string]struct{}{},
	}
}

// machineNames 返回当前要镜像的机器名，已排序。判据只有一处：池。
func (m *Mirror) machineNames() []string { return m.pool.Names() }

// Run 启动发现循环：先立刻跑一轮，然后按 mirrorDiscoveryTick 周期跑，
// ctx 取消时收掉全部订阅与门铃 goroutine。
//
// 注意：Run 内调 Stop 收尾，测试可直接调 Stop 而不用等 ctx 取消。
func (m *Mirror) Run(ctx context.Context) {
	m.log.Info("镜像启动", "tick", mirrorDiscoveryTick.String(), "targets", len(m.machineNames()))
	m.ensureSnapshotLoops(ctx)
	m.discoverOnce(ctx)
	ticker := time.NewTicker(mirrorDiscoveryTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.log.Info("镜像退出", "cause", ctx.Err())
			m.Stop()
			return
		case <-ticker.C:
			m.ensureSnapshotLoops(ctx)
			m.discoverOnce(ctx)
		}
	}
}

// ensureSnapshotLoops 为当前配置里尚未见过的机器启动快照门铃循环。
//
// 运行期新增 target 也必须有消费者：否则事件虽会落库，ringBell 却没有循环来
// 防抖刷新任务快照，新增机器的状态会停在首次发现的旧值。
func (m *Mirror) ensureSnapshotLoops(ctx context.Context) {
	for _, name := range m.machineNames() {
		m.mu.Lock()
		if _, ok := m.loops[name]; ok {
			m.mu.Unlock()
			continue
		}
		m.loops[name] = struct{}{}
		m.mu.Unlock()
		m.wg.Add(1)
		go m.runSnapshotLoop(ctx, name)
	}
}

// Stop 收掉全部订阅与门铃 goroutine，等它们全部退出才返回。
//
// 为什么必须 wg.Wait：镜像的 goroutine 会写本机库与 hub，直接返回等于把
// 「还在写库」的进程留给调用方去关库——竞态（同前 watchdog 的停机顺序纪律）。
func (m *Mirror) Stop() {
	m.mu.Lock()
	for _, cancel := range m.subs {
		cancel()
	}
	m.subs = map[string]context.CancelFunc{}
	m.mu.Unlock()
	m.wg.Wait()
}

// discoverOnce 跑一轮发现：对每台 target 拉任务列表，快照进 mirror_tasks，
// 活跃任务开订阅、终态任务收订阅。单台失败不影响其余。
func (m *Mirror) discoverOnce(ctx context.Context) {
	names := m.machineNames()

	fanCtx, cancel := context.WithTimeout(ctx, mirrorDiscoverBudget)
	defer cancel()

	type result struct {
		name  string
		views []proto.TaskView
		err   error
	}
	results := make([]result, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			c, err := m.pool.For(name)
			if err != nil {
				results[i] = result{name: name, err: err}
				return
			}
			views, err := c.MarkForwarded().ListTasks(fanCtx)
			results[i] = result{name: name, views: views, err: err}
		}(i, name)
	}
	wg.Wait()

	subscribed, dropped, unreachable := 0, 0, 0
	for _, r := range results {
		if r.err != nil {
			m.log.Warn("镜像发现失败", "machine", r.name, "cause", r.err)
			unreachable++
			continue
		}
		now := time.Now().UTC()
		for _, tv := range r.views {
			if err := m.st.UpsertMirrorTask(r.name, tv.Task, now); err != nil {
				m.log.Warn("镜像快照落库失败", "task", tv.Task.ID, "machine", r.name, "cause", err)
				continue
			}
			if tv.Task.State.IsTerminal() {
				// 终态：快照保留（供审阅历史），订阅收掉
				if m.cancelSub(tv.Task.ID) {
					dropped++
				}
				continue
			}
			if !m.isSubscribed(tv.Task.ID) {
				subCtx, subCancel := context.WithCancel(ctx)
				m.registerSub(tv.Task.ID, subCancel)
				m.wg.Add(1)
				go m.subscribe(subCtx, r.name, tv.Task.ID)
				subscribed++
			}
		}
	}
	m.log.Info("镜像发现完成", "machines", len(results),
		"subscribed", subscribed, "dropped", dropped, "unreachable", unreachable)
}

// subscribe 是一条任务的常驻上游订阅：从本机水位续拉事件，断线退避重连，
// 直到任务终态、上下文取消或 Stop。
func (m *Mirror) subscribe(ctx context.Context, machine string, taskID string) {
	defer m.wg.Done()
	defer m.unregisterSub(taskID)
	backoff := mirrorBackoffInitial
	attempt := 0
	for {
		// 每轮现读水位：重连自然续拉，不用记住上次到哪
		fromSeq, err := m.st.MirrorWatermark(taskID)
		if err != nil {
			m.log.Warn("镜像订阅：读水位失败", "task", taskID, "machine", machine, "cause", err)
			return
		}
		m.log.Info("镜像订阅建立", "task", taskID, "machine", machine, "from_seq", fromSeq)
		c, err := m.pool.For(machine)
		if err != nil {
			m.log.Warn("镜像订阅：取客户端失败", "task", taskID, "machine", machine, "cause", err)
			return
		}
		err = c.MarkForwarded().StreamEventsOnce(ctx, taskID, fromSeq,
			func(ev proto.Event) error { return m.onEvent(ctx, machine, taskID, ev) })

		switch {
		case ctx.Err() != nil:
			m.log.Info("镜像订阅收掉", "task", taskID, "machine", machine, "reason", "stopped")
			return
		case errors.Is(err, errMirrorDone):
			// 终态事件已在 onEvent 里落库并 Publish，这里只收手
			m.log.Info("镜像订阅收掉", "task", taskID, "machine", machine, "reason", "terminal")
			return
		case err == nil:
			m.log.Warn("镜像订阅意外结束", "task", taskID, "machine", machine, "cause", "stream returned nil")
		default:
			m.log.Warn("镜像订阅断线，等待后重连", "task", taskID, "machine", machine,
				"attempt", attempt, "backoff_ms", backoff.Milliseconds(), "cause", err)
		}

		select {
		case <-ctx.Done():
			m.log.Info("镜像订阅收掉", "task", taskID, "machine", machine, "reason", "stopped")
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > mirrorBackoffMax {
			backoff = mirrorBackoffMax
		}
		attempt++
	}
}

// onEvent 处理上游事件的每一帧：落库、去重、广播、敲门铃、识别终态。
//
// 返回 errMirrorDone 表示任务已终态；返回其他错误会中止本次连接（重连时凭
// 未推进的水位把失败事件重新拉回来）。
func (m *Mirror) onEvent(ctx context.Context, machine string, taskID string, ev proto.Event) error {
	inserted, err := m.st.AppendMirrorEvent(taskID, ev)
	if err != nil {
		m.log.Error("镜像事件落库失败", "task", taskID, "machine", machine, "seq", ev.Seq, "cause", err)
		// 落库失败必须中止连接：水位未推进，重连会从旧水位重新拉，事件不丢
		return err
	}
	if !inserted {
		// 重复（重连补拉重复到达）：不 Publish——否则重连会给前端重复推同一条
		m.log.Debug("镜像事件重复，跳过广播", "task", taskID, "seq", ev.Seq)
		return nil
	}
	m.log.Debug("镜像事件入库并广播", "task", taskID, "machine", machine, "seq", ev.Seq, "type", ev.Type)
	m.hub.Publish(ev)
	m.ringBell(machine)
	if ev.Type == proto.EventTypeCompleted || ev.Type == proto.EventTypeFailed {
		return errMirrorDone
	}
	return nil
}

// refreshSnapshot 拉一次该 target 的任务列表并全部 upsert（事件即门铃的落点）。
func (m *Mirror) refreshSnapshot(ctx context.Context, machine string) {
	c, err := m.pool.For(machine)
	if err != nil {
		m.log.Warn("镜像快照刷新：取客户端失败", "machine", machine, "cause", err)
		return
	}
	tasks, err := c.MarkForwarded().ListTasks(ctx)
	if err != nil {
		m.log.Warn("镜像快照刷新失败", "machine", machine, "cause", err)
		return
	}
	now := time.Now().UTC()
	for _, tv := range tasks {
		if err := m.st.UpsertMirrorTask(machine, tv.Task, now); err != nil {
			m.log.Warn("镜像快照落库失败", "task", tv.Task.ID, "machine", machine, "cause", err)
		}
	}
	m.log.Debug("镜像快照刷新完成", "machine", machine, "tasks", len(tasks))
}

// runSnapshotLoop 消费某 target 的门铃：响铃后等一个防抖窗口，窗口内再次
// 响铃就重置计时，等事件潮过去刷一次快照。
func (m *Mirror) runSnapshotLoop(ctx context.Context, machine string) {
	defer m.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-m.ringOf(machine):
		}
		timer := time.NewTimer(snapshotDebounce)
		drained := false
		for !drained {
			select {
			case <-m.ringOf(machine):
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(snapshotDebounce)
			case <-timer.C:
				drained = true
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
		m.refreshSnapshot(ctx, machine)
	}
}

// ringOf 取某 target 的门铃通道，没有就建一个（缓冲 1：一个待处理门铃足够，
// 防抖窗口会合并后续响铃）。
func (m *Mirror) ringOf(machine string) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch := m.ring[machine]; ch != nil {
		return ch
	}
	ch := make(chan struct{}, 1)
	m.ring[machine] = ch
	return ch
}

// ringBell 敲响某 target 的门铃（非阻塞：已有待处理门铃时放弃，防抖会合并）。
func (m *Mirror) ringBell(machine string) {
	ch := m.ringOf(machine)
	select {
	case ch <- struct{}{}:
	default:
	}
}

// registerSub 登记一条订阅（task_id → 取消函数）。
func (m *Mirror) registerSub(taskID string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[taskID] = cancel
}

// unregisterSub 摘除一条订阅登记（subscribe 退出时调用）。
func (m *Mirror) unregisterSub(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.subs, taskID)
}

// cancelSub 取消一条订阅；返回 false 表示它本来就不在订阅表里。
func (m *Mirror) cancelSub(taskID string) bool {
	m.mu.Lock()
	cancel, ok := m.subs[taskID]
	if ok {
		delete(m.subs, taskID)
	}
	m.mu.Unlock()
	if ok {
		cancel()
	}
	return ok
}

// isSubscribed 报告该任务当前是否持有订阅。
func (m *Mirror) isSubscribed(taskID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.subs[taskID]
	return ok
}
