// Package ledgermirror 是 agentd 的账本镜像子系统：把挂账 task 的事件
// 从各执行机镜像进账本单流。
//
// 本包不依赖 internal/agentd，也不依赖 internal/targetclient：机器清单与
// 客户端经 Machines 接口注入（消费者侧接口），生产实现是 agentd 的
// target 客户端池经 gateway 侧适配器（agentd.LedgerMirrorMachines）收窄后的
// 形态，测试注入内存实现，全程不碰网络。
//
// 边界：本包不解析机器地址、不选传输形态（直连 / relay）、不管隧道生命周期
// —— 那三件事全归池。包内出现 config.Target 的 Addr/Token 字段读取即是回退。
package ledgermirror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
//
// B233.20：c 的类型是事件流订阅缝（client.EventStreamClient），不再引用聚合
// 客户端；标记语义已收进缝方法本身（ForwardedStreamEventsOnce）。
type Source func(ctx context.Context, c client.EventStreamClient, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error

// Options 子系统参数。零值取生产默认。
type Options struct {
	Holder string
	// SelfName 是本机自名（B439）。判等 target == SelfName → 本机源（LocalSource）。
	// 空 = 无自名：回退 IsSelfTarget 的地址判据（S5，行为与今天一致）。
	SelfName    string
	Tick        time.Duration
	LeaseTTL    time.Duration
	Source      Source
	LocalSource Source
	// LocalTasks is the host's public task inventory. It is read independently
	// of the remote pool because the local machine is deliberately absent there.
	LocalTasks func(context.Context) ([]proto.TaskView, error)
	// LocalDiscovery returns bounded creation-sequence pages from the local task store.
	LocalDiscovery func(context.Context, int64, int) (*proto.TaskDiscoveryPage, error)
	// LocalTaskResponsibility reads one local task and its pending tickets.
	LocalTaskResponsibility func(context.Context, string) (TaskResponsibilityStatus, error)
	// IsSelfTarget 是无自名时的回退判据（地址判）。有 SelfName 时不再参与
	// 名字面判等。
	IsSelfTarget func(target string) bool
	// TicketNotice records the separately addressed route for permission/question
	// events before the source cursor is committed. Replays are expected and must be idempotent.
	TicketNotice func(target, taskID, cardID, originSession string, event proto.Event) error
	// RefreshTicketNotice re-resolves active notices from current task/card/resident facts.
	RefreshTicketNotice func(target, taskID, cardID, originSession string, current ledger.TaskTicketNoticeState) error
}

// LocalEventStore is the persisted-event boundary used by the local source.
//
// The local source replays task events from the exclusive store and waits on its
// task-scoped doorbell; it deliberately does not use a Hub or a network client.
type LocalEventStore interface {
	EventsFromAsc(taskID string, fromSeq int64, limit int) ([]proto.Event, error)
	SubscribeEventDoorbell(taskID string) (<-chan struct{}, func())
}

// Machines 是账本镜像对「有哪些机器、怎么连它」的唯一依赖。
//
// 为什么是消费者侧接口而不是直接 import targetclient：本包只需要「清单 +
// 取客户端」两件事，声明在这里既让测试能注入内存实现，也不给本包引入
// relay 传输栈的依赖。生产实现是 gateway 侧适配器（agentd.LedgerMirrorMachines）
// 包着 *targetclient.Pool：For 透传池的规范实例（B233.20 起返回值收窄为
// client.EventStreamClient）。
//
// 注意：For 不发任何网络请求，可以每轮对账现调；它的错误是**配置性**的
// （机器未登记 / 无端点 / relay token 熵不足），不是网络抖动——因此
// For 失败按「这台机器当前不可用」处理，而不是重试。
type Machines interface {
	// Names 返回当前登记的机器名，已排序。判据取自活配置快照。
	Names() []string
	// For 取某台机器的客户端；**调用方不负责关闭它**。
	//
	// 返回值必须是池的规范实例（同一 target 配置不变时返回同一实例）——
	// 对账按实例判等识别「机器配置已变更」（B163 ②），在适配层另造副本
	// 会让订阅每轮退订重订。
	For(name string) (client.EventStreamClient, error)
}

// TaskInventory is an optional read side implemented by production machine
// adapters. It discovers explicitly session-originated tasks that have no card link.
type TaskInventory interface {
	Tasks(ctx context.Context, name string) ([]proto.TaskView, error)
}

// TaskDiscovery is the bounded source-side incremental inventory used in production.
type TaskDiscovery interface {
	DiscoverTasks(ctx context.Context, name string, afterSeq int64, limit int) (*proto.TaskDiscoveryPage, error)
}

// TaskResponsibilityStatus is the authoritative one-task check used for terminal
// candidates. PendingTickets is included because terminal tasks may still own
// unanswered work that must remain routable.
type TaskResponsibilityStatus struct {
	State          proto.TaskState
	PendingTickets int
}

// TaskResponsibilityReader is an optional exact-source read used only for a
// terminal candidate or a normally closed source stream.
type TaskResponsibilityReader interface {
	TaskResponsibility(ctx context.Context, name, taskID string) (TaskResponsibilityStatus, error)
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
	client client.EventStreamClient
	link   ledger.TaskLink
}

// DefaultSource 生产事件源：client.ForwardedStreamEventsOnce 的薄包装。
//
// 为什么带一跳封顶：这一跳是 agentd→agentd，标记让对端不再向外扇出
// （一跳封顶，A→B→A 不成环），与任务镜像的镜像订阅同款
// （internal/workspace 的 RemoteTaskSource 适配器也包 MarkForwarded）。
func DefaultSource(ctx context.Context, c client.EventStreamClient, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error {
	return c.ForwardedStreamEventsOnce(ctx, taskID, fromSeq, onEvent)
}

const localSourceBatchSize = 100

// NewLocalSource creates a source that reads persisted events from the local store.
//
// 参数：
//   - events: 本机事件库；必须同时提供按 seq 升序的排他回放和 task 门铃
//   - log: 结构化日志器；nil 时使用默认日志器
//
// 返回：
//   - 本机事件源。调用时 c 必须为 nil；fromSeq 是排他的，事件按 seq 升序回放，
//     函数阻塞至 ctx 取消或事件回调失败
//
// 注意：本机路径不读取 c、不订阅 Hub；门铃只提示有新事件，真实事件每次都从
// store 读取，以避免 Hub 的有限缓冲造成事件缺口。
func NewLocalSource(events LocalEventStore, logger *slog.Logger) Source {
	if logger == nil {
		logger = slog.Default()
	}
	return func(ctx context.Context, _ client.EventStreamClient, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error {
		if events == nil {
			err := errors.New("本机事件源缺少 store")
			logger.Warn("本机账本源无法启动", "task", taskID, "from_seq", fromSeq, "cause", err)
			return err
		}
		if onEvent == nil {
			err := errors.New("本机事件源缺少事件回调")
			logger.Warn("本机账本源无法启动", "task", taskID, "from_seq", fromSeq, "cause", err)
			return err
		}
		wake, cancel := events.SubscribeEventDoorbell(taskID)
		defer cancel()
		cursor := fromSeq
		logger.Info("本机账本源启动", "task", taskID, "from_seq", fromSeq)
		for {
			if err := ctx.Err(); err != nil {
				logger.Info("本机账本源退出", "task", taskID, "from_seq", cursor, "cause", err)
				return err
			}
			batch, err := events.EventsFromAsc(taskID, cursor, localSourceBatchSize)
			if err != nil {
				logger.Warn("本机账本源回放失败", "task", taskID, "from_seq", cursor, "cause", err)
				return err
			}
			if len(batch) > 0 {
				logger.Debug("本机账本源回放事件", "task", taskID, "from_seq", cursor, "count", len(batch))
				for _, event := range batch {
					if err := ctx.Err(); err != nil {
						logger.Info("本机账本源退出", "task", taskID, "from_seq", cursor, "cause", err)
						return err
					}
					if err := onEvent(event); err != nil {
						logger.Warn("本机账本源交付失败", "task", taskID,
							"seq", event.Seq, "type", event.Type, "cause", err)
						return err
					}
					cursor = event.Seq
					logger.Debug("本机账本源事件已交付", "task", taskID,
						"seq", event.Seq, "type", event.Type)
				}
				continue
			}
			select {
			case <-ctx.Done():
				logger.Info("本机账本源退出", "task", taskID, "from_seq", cursor, "cause", ctx.Err())
				return ctx.Err()
			case <-wake:
				logger.Debug("本机账本源收到门铃", "task", taskID, "from_seq", cursor)
			}
		}
	}
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

	holding        atomic.Bool
	mu             sync.Mutex
	subs           map[string]*subscription
	conn           map[string]bool
	ended          map[string]bool
	archived       map[string]bool
	inventoryMu    sync.Mutex
	taskInventory  map[string][]proto.TaskView
	localInventory []proto.TaskView
	discoveryMu    sync.Mutex
	discovered     map[string]map[string]proto.TaskDiscoveryItem
	discoverySeq   map[string]int64
	discoveryReady map[string]bool
	wg             sync.WaitGroup
	wgMu           sync.Mutex
	runMu          sync.Mutex
	runStop        context.CancelFunc
	runDone        chan struct{}
	runStarted     atomic.Bool
	stopAsked      atomic.Bool
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
		subs: map[string]*subscription{}, conn: map[string]bool{}, ended: map[string]bool{}, archived: map[string]bool{},
		taskInventory: map[string][]proto.TaskView{}, discovered: map[string]map[string]proto.TaskDiscoveryItem{},
		discoverySeq: map[string]int64{}, discoveryReady: map[string]bool{}}
}

func (m *Mirror) setConn(key string, ok bool) {
	m.mu.Lock()
	m.conn[key] = ok
	m.mu.Unlock()
}

// isLocalTarget 三分支（B439 §4.6）：
//   - ""（存量无归属）→ 本机源（S7 兼容分支；WU1 保行为，WU3 补 WARN/健康面）
//   - == 自名 → 本机源
//   - 其余 → 走池（远端）
//
// 无自名时回退地址判据（IsSelfTarget），保持今天行为（S5）。
func (m *Mirror) isLocalTarget(target string) bool {
	if target == "" {
		return true
	}
	if localName := m.localTargetName(); localName != "" && target == localName {
		return true
	}
	if m.opt.SelfName != "" {
		return target == m.opt.SelfName
	}
	return m.opt.IsSelfTarget != nil && m.opt.IsSelfTarget(target)
}

func (m *Mirror) localTargetName() string {
	if m.opt.SelfName != "" {
		return m.opt.SelfName
	}
	if m.opt.Holder != "" && m.opt.Holder != "unknown" {
		return m.opt.Holder
	}
	return ""
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
func (m *Mirror) addInventoryTask(want map[string]ledger.TaskLink, linked map[string]bool,
	originSessions map[string]string, target string, task proto.TaskView) {
	key := target + "/" + task.ID
	originSessions[key] = task.OriginSession
	if linked[key] {
		link := want[key]
		link.OriginSession = task.OriginSession
		want[key] = link
		return
	}
	if task.OriginSession == "" && task.State != proto.TaskStateWaitingAnswer {
		return
	}
	want[key] = ledger.TaskLink{Target: target, TaskID: task.ID, Purpose: "origin_session", OriginSession: task.OriginSession}
	m.log.Debug("发现未挂卡会话来源任务，纳入事件恢复", "target", target, "task", task.ID, "state", task.State)
}

type discoveryCursor struct {
	Seq int64 `json:"seq"`
}

const taskDiscoveryCursorKind = "b469_task_discovery_cursor"
const taskDiscoveryPageSize = 100

func normalizeDiscoveryMachine(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

func (m *Mirror) advanceDiscoveryCursor(id string, seq int64) (int64, error) {
	body, _ := json.Marshal(discoveryCursor{Seq: seq})
	for attempt := 0; attempt < 3; attempt++ {
		entry, err := m.st.RegistryGet(taskDiscoveryCursorKind, id)
		version := 0
		currentSeq := int64(0)
		if err == nil {
			version = entry.Version
			var saved discoveryCursor
			if err := json.Unmarshal(entry.Body, &saved); err != nil || saved.Seq < 0 {
				return 0, fmt.Errorf("decode task discovery cursor %s version %d", id, entry.Version)
			}
			currentSeq = saved.Seq
		} else if !errors.Is(err, ledger.ErrNotFound) {
			return 0, fmt.Errorf("read task discovery cursor %s: %w", id, err)
		}
		if currentSeq >= seq {
			return currentSeq, nil
		}
		if _, err := m.st.RegistryPut(taskDiscoveryCursorKind, id, version, body, m.opt.Holder); err == nil {
			m.log.Info("任务发现水位已推进", "source", id, "source_seq", seq, "attempt", attempt+1)
			return seq, nil
		} else if !errors.Is(err, ledger.ErrCASConflict) {
			return 0, fmt.Errorf("advance task discovery cursor %s to %d: %w", id, seq, err)
		}
		m.log.Debug("任务发现水位 CAS 冲突，重读当前版本", "source", id, "source_seq", seq, "attempt", attempt+1)
	}
	return 0, fmt.Errorf("advance task discovery cursor %s to %d after CAS retries: %w", id, seq, ledger.ErrCASConflict)
}

// discoverSourcePage rebuilds the in-memory subscription responsibility from seq 0
// after process restart, then reads only pages beyond the last committed source seq.
// A page is added to the responsibility cache before its registry cursor is advanced.
func (m *Mirror) discoverSourcePage(ctx context.Context, name string, fetch func(context.Context, int64, int) (*proto.TaskDiscoveryPage, error)) error {
	if name == "" || fetch == nil {
		return errors.New("task discovery source unavailable")
	}
	id := normalizeDiscoveryMachine(name)
	m.discoveryMu.Lock()
	if m.discovered[id] == nil {
		m.discovered[id] = map[string]proto.TaskDiscoveryItem{}
	}
	ready := m.discoveryReady[id]
	after := m.discoverySeq[id]
	m.discoveryMu.Unlock()
	if !ready {
		after = 0
	}
	persistedSeq := int64(0)
	entry, getErr := m.st.RegistryGet(taskDiscoveryCursorKind, id)
	if getErr == nil {
		var saved discoveryCursor
		if err := json.Unmarshal(entry.Body, &saved); err != nil || saved.Seq < 0 {
			return fmt.Errorf("decode task discovery cursor %s version %d", id, entry.Version)
		}
		persistedSeq = saved.Seq
	} else if !errors.Is(getErr, ledger.ErrNotFound) {
		return fmt.Errorf("read task discovery cursor %s: %w", id, getErr)
	}

	for {
		m.log.Debug("请求任务增量发现页", "source", id, "after_seq", after, "limit", taskDiscoveryPageSize)
		callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		page, err := fetch(callCtx, after, taskDiscoveryPageSize)
		cancel()
		if err != nil {
			return fmt.Errorf("discover tasks from %s after %d: %w", name, after, err)
		}
		if page == nil || page.Items == nil || page.NextSeq < after {
			return fmt.Errorf("invalid task discovery page from %s after %d", name, after)
		}
		last := after
		for _, item := range page.Items {
			if item.Seq <= last || item.TaskID == "" {
				return fmt.Errorf("invalid task discovery item from %s at seq %d", name, item.Seq)
			}
			last = item.Seq
		}
		if len(page.Items) == 0 && page.NextSeq != after {
			return fmt.Errorf("empty task discovery page advanced from %d to %d", after, page.NextSeq)
		}
		if page.NextSeq != last {
			return fmt.Errorf("task discovery next_seq %d does not match last item %d", page.NextSeq, last)
		}
		m.discoveryMu.Lock()
		for _, item := range page.Items {
			// Terminal tasks with no source-side pending ticket have no ongoing
			// subscription responsibility. The source cursor still advances below.
			if item.State.IsTerminal() && !item.HasPendingTickets {
				delete(m.discovered[id], item.TaskID)
				continue
			}
			m.discovered[id][item.TaskID] = item
		}
		m.discoveryMu.Unlock()
		m.log.Debug("任务发现页读取完成", "source", id, "after_seq", after,
			"next_seq", page.NextSeq, "items", len(page.Items), "has_more", page.HasMore)
		if page.NextSeq > persistedSeq {
			var putErr error
			persistedSeq, putErr = m.advanceDiscoveryCursor(id, page.NextSeq)
			if putErr != nil {
				return putErr
			}
		}
		after = page.NextSeq
		if !page.HasMore {
			break
		}
		if len(page.Items) == 0 {
			return fmt.Errorf("task discovery source %s claims more with an empty page", name)
		}
	}
	m.discoveryMu.Lock()
	if after < persistedSeq {
		after = persistedSeq
	}
	m.discoverySeq[id] = after
	m.discoveryReady[id] = true
	m.discoveryMu.Unlock()
	return nil
}

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
	nonTerminal, err := m.st.NonTerminalLinks()
	if err != nil {
		m.log.Warn("读非终态挂账集合失败", "err", err)
		return
	}
	want := map[string]ledger.TaskLink{}
	linked := make(map[string]bool, len(links))
	for _, link := range links {
		if link.Target == "" {
			// B439 S7：存量无归属 '' 挂账不迁移、不静默。仍按「持有者本机」尽力
			// 订阅（持有者恰是派发机时仍可用），但每轮对每条**非终态**的 '' 挂账
			// 打一次 WARN——对账周期天然限频，同一条不刷屏；终态 '' 静默（没有
			// 可镜像的东西）。这是旧缺陷族「不报错、无 WARN、健康行正常」的
			// 显性化出口。
			if nonTerminal[link.Target+"/"+link.TaskID] {
				m.log.Warn("历史无归属挂账，仅在持有者本机时可镜像",
					"card", link.CardID, "task", link.TaskID, "node", link.Node, "attempt", link.Attempt)
			}
			want[link.Target+"/"+link.TaskID] = link
			continue
		}
		if !registered[link.Target] && !m.isLocalTarget(link.Target) {
			// 挂账目标机不在本机机器清单里：取不到客户端，订不到。
			//
			// 在飞/终态分档（S7，与同函数健康行 live/终态分档对齐）：只有该
			// target 仍有非终态挂账（live[link.Target]）才打 WARN——退役机或
			// 旧别名 target 的全部历史挂账（含已归档）若每 tick 每条重复 WARN，
			// 会淹没真断链信号。终态挂账静默跳订；在飞挂账不静默：打 WARN
			// （带 target/task/card）让断链可见，健康行留在未刷新状态（断链
			// 亮灯）。这正是 B439 要杀的静默失效族（S1 关键失败）。
			if !live[link.Target] {
				continue
			}
			m.log.Warn("挂账目标机不在本机机器清单，无法订阅（断链）",
				"card", link.CardID, "target", link.Target, "task", link.TaskID,
				"node", link.Node, "attempt", link.Attempt)
			continue
		}
		want[link.Target+"/"+link.TaskID] = link
		linked[link.Target+"/"+link.TaskID] = true
	}
	// Task discovery is an independent recovery path; it never fabricates card links.
	originSessions := map[string]string{}
	if discovery, ok := m.machines.(TaskDiscovery); ok {
		for _, name := range names {
			name := name
			if err := m.discoverSourcePage(ctx, name, func(callCtx context.Context, after int64, limit int) (*proto.TaskDiscoveryPage, error) {
				return discovery.DiscoverTasks(callCtx, name, after, limit)
			}); err != nil {
				m.log.Warn("读取任务增量发现失败，保留最后可用订阅责任", "target", name, "cause", err)
			}
		}
	} else if inventory, ok := m.machines.(TaskInventory); ok {
		// Compatibility for non-production test/legacy machine adapters. Production uses
		// the source creation cursor and never falls back after a discovery 404.
		type inventoryResult struct {
			name  string
			tasks []proto.TaskView
			err   error
		}
		results := make(chan inventoryResult, len(names))
		for _, name := range names {
			name := name
			go func() {
				callCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
				defer cancel()
				tasks, err := inventory.Tasks(callCtx, name)
				results <- inventoryResult{name: name, tasks: tasks, err: err}
			}()
		}
		for remaining := len(names); remaining > 0; remaining-- {
			select {
			case result := <-results:
				if result.err != nil {
					m.log.Warn("读取任务清单失败", "target", result.name, "cause", result.err)
					continue
				}
				m.inventoryMu.Lock()
				m.taskInventory[result.name] = append([]proto.TaskView(nil), result.tasks...)
				m.inventoryMu.Unlock()
			case <-ctx.Done():
				remaining = 1
			}
		}
		m.inventoryMu.Lock()
		for _, name := range names {
			for _, task := range m.taskInventory[name] {
				m.addInventoryTask(want, linked, originSessions, name, task)
			}
		}
		m.inventoryMu.Unlock()
	}
	if m.opt.LocalDiscovery != nil {
		localTarget := m.localTargetName()
		if err := m.discoverSourcePage(ctx, localTarget, m.opt.LocalDiscovery); err != nil {
			m.log.Warn("读取本机任务增量发现失败，保留最后可用订阅责任", "target", localTarget, "cause", err)
		}
	} else if m.opt.LocalTasks != nil {
		localCtx, cancelLocal := context.WithTimeout(ctx, 5*time.Second)
		tasks, err := m.opt.LocalTasks(localCtx)
		cancelLocal()
		if err != nil {
			m.log.Warn("读取本机来源任务清单失败，保留上次成功清单", "target", m.opt.SelfName, "cause", err)
		} else {
			m.inventoryMu.Lock()
			m.localInventory = append([]proto.TaskView(nil), tasks...)
			m.inventoryMu.Unlock()
		}
		m.inventoryMu.Lock()
		localTarget := m.localTargetName()
		for _, task := range m.localInventory {
			m.addInventoryTask(want, linked, originSessions, localTarget, task)
		}
		m.inventoryMu.Unlock()
	}
	addDiscovered := func(target string, item proto.TaskDiscoveryItem) {
		key := target + "/" + item.TaskID
		if linked[key] {
			originSessions[key] = item.OriginSession
			link := want[key]
			link.OriginSession = item.OriginSession
			want[key] = link
			return
		}
		originSessions[key] = item.OriginSession
		want[key] = ledger.TaskLink{Target: target, TaskID: item.TaskID, Purpose: "task_discovery", OriginSession: item.OriginSession}
	}
	m.discoveryMu.Lock()
	for _, name := range names {
		for _, item := range m.discovered[normalizeDiscoveryMachine(name)] {
			addDiscovered(name, item)
		}
	}
	if local := m.localTargetName(); local != "" {
		for _, item := range m.discovered[normalizeDiscoveryMachine(local)] {
			addDiscovered(local, item)
		}
	}
	m.discoveryMu.Unlock()
	if m.opt.RefreshTicketNotice != nil {
		type sourceTask struct{ machine, task string }
		sources := make([]sourceTask, 0, len(originSessions))
		for key := range originSessions {
			parts := strings.SplitN(key, "/", 2)
			if len(parts) == 2 {
				sources = append(sources, sourceTask{machine: parts[0], task: parts[1]})
			}
		}
		for _, source := range sources {
			notices, err := m.st.ListActiveTaskTicketNoticesForTask(source.machine, source.task)
			if err != nil {
				m.log.Warn("按源任务读取活动工单通知失败，保留现有路由", "target", source.machine, "task", source.task, "cause", err)
				continue
			}
			m.discoveryMu.Lock()
			item, discovered := m.discovered[normalizeDiscoveryMachine(source.machine)][source.task]
			m.discoveryMu.Unlock()
			m.mu.Lock()
			archived := m.archived[source.machine+"/"+source.task]
			m.mu.Unlock()
			if discovered && (item.State.IsTerminal() || archived) {
				status, statusErr := m.readTaskResponsibility(ctx, source.machine, source.task)
				if statusErr != nil {
					m.log.Warn("核验终态任务责任失败，保留订阅", "target", source.machine, "task", source.task, "cause", statusErr)
				} else if (status.State.IsTerminal() || archived) && status.PendingTickets == 0 && len(notices) == 0 {
					m.discoveryMu.Lock()
					delete(m.discovered[normalizeDiscoveryMachine(source.machine)], source.task)
					m.discoveryMu.Unlock()
					key := source.machine + "/" + source.task
					delete(originSessions, key)
					if !linked[key] {
						delete(want, key)
						m.mu.Lock()
						delete(m.ended, key)
						delete(m.archived, key)
						m.mu.Unlock()
					}
					m.log.Info("终态任务已无待答票，释放发现责任", "target", source.machine, "task", source.task)
				} else if status.State != "" && status.State != item.State {
					m.discoveryMu.Lock()
					item.State = status.State
					item.HasPendingTickets = status.PendingTickets > 0
					m.discovered[normalizeDiscoveryMachine(source.machine)][source.task] = item
					m.discoveryMu.Unlock()
				}
			}
			for _, notice := range notices {
				key := notice.SourceMachine + "/" + notice.TaskID
				originSession := originSessions[key]
				cardID, cardErr := m.st.CardOfTask(notice.SourceMachine, notice.TaskID)
				if cardErr != nil && !errors.Is(cardErr, ledger.ErrNotFound) {
					m.log.Warn("反查活动工单通知卡失败，保留原路由", "target", notice.SourceMachine,
						"task", notice.TaskID, "ticket", notice.TicketID, "cause", cardErr)
					continue
				}
				if errors.Is(cardErr, ledger.ErrNotFound) {
					cardID = ""
				}
				if err := m.opt.RefreshTicketNotice(notice.SourceMachine, notice.TaskID, cardID, originSession, notice); err != nil {
					m.log.Warn("刷新活动工单通知归属失败", "target", notice.SourceMachine,
						"task", notice.TaskID, "ticket", notice.TicketID,
						"ownership_version", notice.OwnershipVersion, "cause", err)
				}
			}
		}
	}

	// 现取客户端：只对本轮真有挂账的机器取一次（For 不发网络请求）。
	// 取不到就当这台机器本轮不可用——For 的错误是配置性的，重试没有意义。
	clients := map[string]client.EventStreamClient{}
	for _, link := range want {
		if m.isLocalTarget(link.Target) {
			continue
		}
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
		case m.isLocalTarget(link.Target) && sub.client != nil:
			m.dropSubLocked(key, sub, "本机目标形态变更，退订重订")
		case !m.isLocalTarget(link.Target) && clients[link.Target] == nil:
			m.dropSubLocked(key, sub, "本轮取不到该机器的客户端")
		case !m.isLocalTarget(link.Target) && clients[link.Target] != sub.client:
			m.dropSubLocked(key, sub, "机器配置已变更，退订重订")
		case sub.link.CardID != link.CardID || sub.link.Node != link.Node || sub.link.Attempt != link.Attempt:
			// Node/Attempt 是订阅写入 task_mirrored envelope 的身份投影；重试
			// 只改这两个字段也必须换订阅，否则迟到事件会继续冒充旧尝试。
			m.dropSubLocked(key, sub, "工作流尝试身份已变更，退订重订")
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
		if !m.isLocalTarget(link.Target) && c == nil {
			continue
		}
		subCtx, cancel := context.WithCancel(ctx)
		m.subs[key] = &subscription{cancel: cancel, client: c, link: link}
		m.wgMu.Lock()
		m.wg.Add(1)
		m.wgMu.Unlock()
		go m.subscribe(subCtx, link, c)
		m.log.Info("起订", "sub", key, "target", link.Target, "task", link.TaskID,
			"node", link.Node, "attempt", link.Attempt)
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
		if m.isLocalTarget(name) {
			continue
		}
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
		if m.isLocalTarget(row.Target) {
			continue
		}
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
func (m *Mirror) readTaskResponsibility(ctx context.Context, target, taskID string) (TaskResponsibilityStatus, error) {
	callCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if m.isLocalTarget(target) {
		if m.opt.LocalTaskResponsibility == nil {
			return TaskResponsibilityStatus{}, errors.New("本机任务责任读取未配置")
		}
		return m.opt.LocalTaskResponsibility(callCtx, taskID)
	}
	reader, ok := m.machines.(TaskResponsibilityReader)
	if !ok {
		return TaskResponsibilityStatus{}, errors.New("来源机器不支持单任务责任读取")
	}
	return reader.TaskResponsibility(callCtx, target, taskID)
}

func (m *Mirror) updateDiscoveredResponsibility(target, taskID string, status TaskResponsibilityStatus) {
	id := normalizeDiscoveryMachine(target)
	m.discoveryMu.Lock()
	defer m.discoveryMu.Unlock()
	item, ok := m.discovered[id][taskID]
	if !ok {
		return
	}
	item.State = status.State
	item.HasPendingTickets = status.PendingTickets > 0
	m.discovered[id][taskID] = item
}

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
func (m *Mirror) subscribe(ctx context.Context, link ledger.TaskLink, c client.EventStreamClient) {
	defer m.wg.Done()
	key := link.Target + "/" + link.TaskID
	defer m.setConn(key, false)
	backoff := 300 * time.Millisecond
	const maxBackoff = 10 * time.Second
	for ctx.Err() == nil {
		wm, err := m.st.MirrorWatermark(link.Target, link.TaskID)
		if err != nil {
			m.log.Warn("读 watermark 失败，退避重试", "card", link.CardID, "target", link.Target,
				"task", link.TaskID, "node", link.Node, "attempt", link.Attempt,
				"backoff", backoff, "cause", err)
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
		var source Source
		if m.isLocalTarget(link.Target) {
			source = m.opt.LocalSource
			if source == nil {
				err = errors.New("本机账本源未配置")
				m.log.Warn("本机订阅无法建立", "card", link.CardID, "target", link.Target,
					"task", link.TaskID, "node", link.Node, "attempt", link.Attempt,
					"from_seq", wm, "cause", err)
			} else {
				m.log.Info("建立本机账本订阅", "card", link.CardID, "target", link.Target,
					"task", link.TaskID, "node", link.Node, "attempt", link.Attempt, "from_seq", wm)
			}
		} else {
			if c == nil {
				err = errors.New("远端客户端缺失")
				m.log.Warn("远端订阅无法建立", "card", link.CardID, "target", link.Target,
					"task", link.TaskID, "node", link.Node, "attempt", link.Attempt,
					"from_seq", wm, "cause", err)
			} else {
				source = m.opt.Source
				m.log.Info("建立远端账本订阅", "card", link.CardID, "target", link.Target,
					"task", link.TaskID, "node", link.Node, "attempt", link.Attempt, "from_seq", wm)
			}
		}
		terminalSignal := false
		if source != nil {
			err = source(ctx, c, link.TaskID, wm, func(e proto.Event) error {
				if e.Type == proto.EventTypeFailed {
					terminalSignal = true
				}
				if mirrorSkip[e.Type] {
					m.log.Debug("账本镜像过滤事件", "target", link.Target, "task", link.TaskID,
						"node", link.Node, "attempt", link.Attempt, "seq", e.Seq, "type", e.Type)
					return nil
				}
				// Record routing before the source cursor advances. If this fails,
				// source replay is safe because notice writes are idempotent.
				if m.opt.TicketNotice != nil && (e.Type == proto.EventTypeQuestion || e.Type == proto.EventTypePermissionRequest ||
					e.Type == proto.EventTypeTicketAnswered || e.Type == proto.EventTypeTicketsVoided) {
					if err := m.opt.TicketNotice(link.Target, link.TaskID, link.CardID, link.OriginSession, e); err != nil {
						m.log.Warn("工单通知路由记录失败", "target", link.Target, "task", link.TaskID,
							"source_seq", e.Seq, "type", e.Type, "cause", err)
						return err
					}
				}
				wrote, err := m.st.AppendMirroredEvent(link.CardID, ledger.MirroredEvent{
					Target: link.Target, Task: link.TaskID, Node: link.Node, Attempt: link.Attempt, SourceSeq: e.Seq,
					Type: string(e.Type), Payload: e.Payload, CreatedAt: e.CreatedAt,
				})
				if err != nil {
					m.log.Warn("账本镜像落库失败", "target", link.Target, "task", link.TaskID,
						"node", link.Node, "attempt", link.Attempt, "seq", e.Seq, "type", e.Type, "card", link.CardID, "cause", err)
					return err
				}
				if wrote {
					m.log.Debug("账本镜像事件已落账", "target", link.Target, "task", link.TaskID,
						"node", link.Node, "attempt", link.Attempt, "seq", e.Seq, "type", e.Type, "card", link.CardID)
					if !m.isLocalTarget(link.Target) {
						if err := m.st.TouchMirrorHealth(link.Target, e.Seq); err != nil {
							m.log.Warn("touch 健康失败", "card", link.CardID, "target", link.Target,
								"task", link.TaskID, "node", link.Node, "attempt", link.Attempt,
								"seq", e.Seq, "cause", err)
						}
					}
				}
				if e.Type == proto.EventTypeArchived {
					return errMirrorArchived
				}
				return nil
			})
		}
		m.setConn(key, false)
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errMirrorArchived) {
			status, statusErr := m.readTaskResponsibility(ctx, link.Target, link.TaskID)
			m.mu.Lock()
			m.archived[key] = true
			m.ended[key] = true
			m.mu.Unlock()
			if statusErr == nil {
				m.updateDiscoveredResponsibility(link.Target, link.TaskID, status)
				m.log.Info("订阅正常终结（收到 task archived 事件）", "target", link.Target,
					"task", link.TaskID, "pending_tickets", status.PendingTickets)
			} else {
				m.log.Warn("归档事件已到但无法核验待答票，保留通知责任", "target", link.Target,
					"task", link.TaskID, "cause", statusErr)
			}
			return
		}
		// A WebSocket normal close is only a transport signal. The source may
		// still be live (or retain unanswered tickets), so verify its authority
		// before ending the responsibility just as for EOF/terminal events.
		if err == nil || terminalSignal || errors.Is(err, client.ErrStreamNormalClosure) {
			status, statusErr := m.readTaskResponsibility(ctx, link.Target, link.TaskID)
			if statusErr == nil && status.State.IsTerminal() {
				m.updateDiscoveredResponsibility(link.Target, link.TaskID, status)
				if status.PendingTickets == 0 {
					m.mu.Lock()
					m.ended[key] = true
					m.mu.Unlock()
					m.log.Info("源任务权威状态已终态且无待答票，停止事件订阅", "target", link.Target, "task", link.TaskID,
						"state", status.State)
					return
				}
				m.log.Warn("源任务已终态但仍有待答票，保留订阅", "target", link.Target,
					"task", link.TaskID, "state", status.State, "pending_tickets", status.PendingTickets)
			}
			if statusErr != nil {
				m.log.Warn("事件源结束但权威任务状态读取失败，保留订阅并重连", "target", link.Target,
					"task", link.TaskID, "cause", statusErr)
			} else if !status.State.IsTerminal() {
				m.log.Warn("事件源结束但任务仍非终态，保留订阅并重连", "target", link.Target,
					"task", link.TaskID, "state", status.State)
			}
			err = errors.New("event source closed before authoritative task termination")
		}
		m.log.Warn("订阅断开，退避重连", "card", link.CardID, "target", link.Target,
			"task", link.TaskID, "node", link.Node, "attempt", link.Attempt,
			"backoff", backoff, "cause", err)
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
