// 本文件实现独立于任务 Mirror 的 preview owner 聚合与实时投影。
//
// 职责：
//   - 先向每台 owner 拉列表，再订阅 preview WS；断线重连重复该顺序
//   - 给远端 session/event 盖 machine 章，维护可重建的内存镜像
//   - 把部分机器失败保留为 MachineStatus，而不是伪装成空的正常列表
//
// 边界：
//   - 不使用任务事件 cursor、mirror_events 或 coordinator Store
//   - 不写远端 owner truth；关闭/打开仍由明确的 client 路由负责
package agentd

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/targetclient"
)

const previewMirrorBackoffMax = 10 * time.Second

// PreviewMirror is a bounded supervisor for owner snapshots and preview WS streams.
type PreviewMirror struct {
	pool         *targetclient.Pool
	owner        *PreviewOwner
	hub          *PreviewHub
	isSelfTarget func(string) bool
	log          *slog.Logger

	mu        sync.RWMutex
	sessions  map[string]proto.PreviewSession
	machines  map[string]proto.MachineStatus
	cancels   map[string]context.CancelFunc
	started   bool
	stopped   bool
	runCancel context.CancelFunc
	stopOnce  sync.Once
	stopDone  chan struct{}
	wg        sync.WaitGroup
}

// NewPreviewMirror constructs a projection with no persistent coordinator state.
func NewPreviewMirror(pool *targetclient.Pool, owner *PreviewOwner, hub *PreviewHub,
	isSelfTarget func(string) bool, log *slog.Logger) *PreviewMirror {
	if log == nil {
		log = slog.Default()
	}
	if hub == nil && owner != nil {
		hub = owner.hub
	}
	if hub == nil {
		hub = NewPreviewHub(log)
	}
	return &PreviewMirror{
		pool: pool, owner: owner, hub: hub, isSelfTarget: isSelfTarget, log: log,
		sessions: make(map[string]proto.PreviewSession), machines: make(map[string]proto.MachineStatus),
		cancels: make(map[string]context.CancelFunc), stopDone: make(chan struct{}),
	}
}

// Run performs an initial list-before-WS projection and supervises target streams.
func (m *PreviewMirror) Run(ctx context.Context) {
	m.mu.Lock()
	if m.started || m.stopped {
		m.mu.Unlock()
		return
	}
	m.started = true
	runCtx, runCancel := context.WithCancel(ctx)
	m.runCancel = runCancel
	m.mu.Unlock()
	m.refreshAll(runCtx)
	m.ensureLoops(runCtx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-runCtx.Done():
			m.Stop()
			return
		case <-m.stopDone:
			return
		case <-ticker.C:
			m.refreshAll(runCtx)
			m.ensureLoops(runCtx)
		}
	}
}

// Stop cancels every remote supervisor and waits for all network goroutines.
func (m *PreviewMirror) Stop() {
	m.stopOnce.Do(func() {
		m.mu.Lock()
		m.stopped = true
		if m.runCancel != nil {
			m.runCancel()
		}
		for name, cancel := range m.cancels {
			cancel()
			delete(m.cancels, name)
		}
		m.mu.Unlock()
		m.wg.Wait()
		close(m.stopDone)
		m.log.Info("preview mirror 已停止", "operation", "preview_mirror_stop")
	})
}

// ListAll refreshes list-before-WS data and returns the current owner projection.
// Individual target failures are encoded in Machines and do not erase successful data.
func (m *PreviewMirror) ListAll(ctx context.Context) (*proto.PreviewListResp, error) {
	m.refreshAll(ctx)
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessions := make([]proto.PreviewSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		sessions = append(sessions, session)
	}
	sort.Slice(sessions, func(i, j int) bool {
		if sessions[i].Machine != sessions[j].Machine {
			return sessions[i].Machine < sessions[j].Machine
		}
		return sessions[i].ID < sessions[j].ID
	})
	machines := make([]proto.MachineStatus, 0, len(m.machines))
	for _, status := range m.machines {
		machines = append(machines, status)
	}
	sort.Slice(machines, func(i, j int) bool { return machines[i].Name < machines[j].Name })
	return &proto.PreviewListResp{Sessions: sessions, Machines: machines}, nil
}

// Resolve finds one session by the composite machine/id identity.
func (m *PreviewMirror) Resolve(id, machine string) (proto.PreviewSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[previewSessionKey(machine, id)]
	return session, ok
}

func previewSessionKey(machine, id string) string { return machine + "\x1f" + id }

func (m *PreviewMirror) refreshAll(ctx context.Context) {
	m.mu.RLock()
	stopped := m.stopped
	m.mu.RUnlock()
	if stopped {
		return
	}
	now := time.Now().UTC()
	nextSessions := make(map[string]proto.PreviewSession)
	nextMachines := make(map[string]proto.MachineStatus)
	listedMachines := make(map[string]bool)
	if m.owner != nil {
		resp, err := m.owner.List(ctx)
		status := proto.MachineStatus{Name: "", FetchedAt: now, Ok: err == nil}
		if err != nil {
			status.Error = err.Error()
			m.log.Warn("preview 本机列表失败", "machine", "", "cause", err)
		} else {
			listedMachines[""] = true
			for _, session := range resp.Sessions {
				session.Machine = ""
				nextSessions[previewSessionKey("", session.ID)] = session
			}
		}
		nextMachines[""] = status
	}
	if m.pool != nil {
		for _, name := range m.pool.Names() {
			if m.isSelfTarget != nil && m.isSelfTarget(name) {
				continue
			}
			status := proto.MachineStatus{Name: name, FetchedAt: now}
			c, err := m.pool.For(name)
			if err == nil {
				resp, callErr := c.ListPreviews(ctx)
				err = callErr
				if err == nil {
					status.Ok = true
					listedMachines[name] = true
					for _, session := range resp.Sessions {
						session.Machine = name
						nextSessions[previewSessionKey(name, session.ID)] = session
					}
				}
			}
			if err != nil {
				status.Error = err.Error()
				m.log.Warn("preview 远端列表失败", "machine", name, "cause", err)
			}
			nextMachines[name] = status
		}
	}
	m.mu.Lock()
	for key, session := range m.sessions {
		if !listedMachines[session.Machine] {
			nextSessions[key] = session
		}
	}
	closed := previewClosedEvents(m.sessions, nextSessions)
	m.sessions, m.machines = nextSessions, nextMachines
	m.mu.Unlock()
	m.publishClosedEvents(closed, "preview mirror 列表收敛关闭事件")
	m.log.Info("preview mirror 列表刷新成功", "sessions", len(nextSessions), "machines", len(nextMachines))
}

func (m *PreviewMirror) ensureLoops(parent context.Context) {
	if m.pool == nil {
		return
	}
	m.mu.RLock()
	stopped := m.stopped
	m.mu.RUnlock()
	if stopped {
		return
	}
	for _, name := range m.pool.Names() {
		if m.isSelfTarget != nil && m.isSelfTarget(name) {
			continue
		}
		m.mu.Lock()
		if m.stopped {
			m.mu.Unlock()
			return
		}
		if _, exists := m.cancels[name]; exists {
			m.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithCancel(parent)
		m.cancels[name] = cancel
		m.wg.Add(1)
		m.mu.Unlock()
		go m.runTarget(ctx, name)
	}
}

func (m *PreviewMirror) runTarget(ctx context.Context, name string) {
	defer m.wg.Done()
	backoff := 300 * time.Millisecond
	for {
		if ctx.Err() != nil {
			return
		}
		c, err := m.pool.For(name)
		if err == nil {
			// This list is intentionally immediately before every fresh WS. It closes the
			// disconnect gap without borrowing task cursor semantics.
			err = m.refreshTarget(ctx, name, c)
			if err == nil {
				err = c.StreamPreviewEventsOnce(ctx, func(event proto.PreviewEvent) error {
					m.applyRemoteEvent(name, event)
					return nil
				})
			}
		}
		if err != nil && ctx.Err() == nil {
			m.log.Warn("preview mirror 远端流失败", "machine", name, "cause", err)
		}
		if !waitPreviewBackoff(ctx, backoff) {
			return
		}
		if backoff < previewMirrorBackoffMax {
			backoff *= 2
			if backoff > previewMirrorBackoffMax {
				backoff = previewMirrorBackoffMax
			}
		}
	}
}

func (m *PreviewMirror) refreshTarget(ctx context.Context, name string, c interface {
	ListPreviews(context.Context) (*proto.PreviewListResp, error)
}) error {
	resp, err := c.ListPreviews(ctx)
	if err != nil {
		m.setMachineFailure(name, err)
		return err
	}
	listed := make(map[string]struct{}, len(resp.Sessions))
	for _, session := range resp.Sessions {
		listed[session.ID] = struct{}{}
	}
	m.mu.Lock()
	closed := make([]proto.PreviewEvent, 0)
	for key, session := range m.sessions {
		if session.Machine == name {
			if _, ok := listed[session.ID]; !ok {
				closed = append(closed, proto.PreviewEvent{Type: proto.PreviewEventClosed, Session: session, Machine: name})
			}
			delete(m.sessions, key)
		}
	}
	for _, session := range resp.Sessions {
		session.Machine = name
		m.sessions[previewSessionKey(name, session.ID)] = session
	}
	m.machines[name] = proto.MachineStatus{Name: name, Ok: true, FetchedAt: time.Now().UTC()}
	m.mu.Unlock()
	m.publishClosedEvents(closed, "preview mirror 目标列表收敛关闭事件")
	return nil
}

func (m *PreviewMirror) applyRemoteEvent(name string, event proto.PreviewEvent) {
	if event.Type != proto.PreviewEventCreated && event.Type != proto.PreviewEventClosed {
		m.log.Warn("preview mirror 忽略未知事件", "machine", name, "event", event.Type, "session", event.Session.ID)
		return
	}
	event.Machine = name
	event.Session.Machine = name
	key := previewSessionKey(name, event.Session.ID)
	m.mu.Lock()
	if event.Type == proto.PreviewEventClosed {
		delete(m.sessions, key)
	} else {
		m.sessions[key] = event.Session
	}
	m.mu.Unlock()
	m.hub.Publish(event)
	m.log.Info("preview mirror 事件投影成功", "machine", name, "event", event.Type, "session", event.Session.ID)
}

func (m *PreviewMirror) setMachineFailure(name string, err error) {
	m.mu.Lock()
	m.machines[name] = proto.MachineStatus{Name: name, FetchedAt: time.Now().UTC(), Error: err.Error()}
	m.mu.Unlock()
}

func previewClosedEvents(before, after map[string]proto.PreviewSession) []proto.PreviewEvent {
	closed := make([]proto.PreviewEvent, 0)
	for key, session := range before {
		if _, ok := after[key]; ok {
			continue
		}
		closed = append(closed, proto.PreviewEvent{Type: proto.PreviewEventClosed, Session: session, Machine: session.Machine})
	}
	return closed
}

func (m *PreviewMirror) publishClosedEvents(events []proto.PreviewEvent, reason string) {
	for _, event := range events {
		m.hub.Publish(event)
		m.log.Info(reason, "operation", "preview_closed", "machine", event.Machine, "session", event.Session.ID)
	}
}

func waitPreviewBackoff(ctx context.Context, delay time.Duration) bool {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
