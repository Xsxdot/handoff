// peer supervisor：单台远端机器的同步生命周期管理。
//
// 职责：
//   - 按 fixed 顺序恢复：authenticate → negotiate → catch up → reconcile → connected
//   - 每台机器一个串行 worker（MachineID 隔离 cursor 与 backoff）
//   - 断线保留投影但 Machine=unavailable；重连从 cursor 补拉
//
// 边界：
//   - 一台坏机器不阻塞本机或其他远端（每机器独立 worker）
//   - 结构化日志覆盖 connect/auth/negotiate/catch-up/reconcile/connected/unavailable，
//     不打 token
package peer

import (
	"context"
	"fmt"
	"log/slog"
	"maps"

	"github.com/xushixin/handoff/internal/controlplane"
)

// SupervisorState 是单台远端机器的同步状态。
type SupervisorState string

const (
	SupervisorStateConnecting   SupervisorState = "connecting"
	SupervisorStateReconciling  SupervisorState = "reconciling"
	SupervisorStateConnected    SupervisorState = "connected"
	SupervisorStateUnavailable  SupervisorState = "unavailable"
	SupervisorStateIncompatible SupervisorState = "incompatible"
)

// ProjectorPort 是 supervisor 需要的投影能力（由 controlplane.Projector 实现）。
//
// 为什么用 controlplane 类型而非 peer 自建：控制面投影是领域语义，peer 只是
// 传输层；类型复用避免两套投影类型漂移。
type ProjectorPort interface {
	Apply(ctx context.Context, ev controlplane.MachineEvent) (controlplane.ControlEvent, bool, error)
}

// PeerClient 是 supervisor 需要的远端客户端能力。
type PeerClient interface {
	Hello(ctx context.Context) (Hello, error)
	MachineSnapshot(ctx context.Context) (MachineSnapshot, error)
	EventsAfter(ctx context.Context, machineID string, afterSeq int64, limit int) ([]MachineEvent, error)
	Close()
}

// SupervisorConfig 是 supervisor 的构造参数。
type SupervisorConfig struct {
	MachineID string
	Client    PeerClient
	Projector ProjectorPort
	OnState   func(SupervisorState) // 状态变化回调（机器表状态投影）
	// OnNegotiated 只接收本端白名单过滤后的 capability，不接收原始 hello map。
	OnNegotiated func(protocolVersion int, capabilities map[string]int)
	Log          *slog.Logger
}

// Supervisor 管理单台远端机器的同步生命周期。
type Supervisor struct {
	machineID    string
	client       PeerClient
	projector    ProjectorPort
	onState      func(SupervisorState)
	onNegotiated func(int, map[string]int)
	log          *slog.Logger
	// cursor 是已消费到的 machine_seq（按机器隔离）。
	cursor int64
}

// NewSupervisor 创建单机器 supervisor。
func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &Supervisor{
		machineID: cfg.MachineID, client: cfg.Client, projector: cfg.Projector,
		onState: cfg.OnState, onNegotiated: cfg.OnNegotiated, log: log,
	}
}

// Run 执行单次恢复尝试（authenticate → negotiate → catch up → reconcile）。
//
// 为什么是单次而非循环：supervisor 由上层调度（每机器独立 goroutine 或
// 重连触发）；本方法保证一次恢复的顺序性与状态回调。
func (s *Supervisor) Run(ctx context.Context) {
	s.setState(SupervisorStateConnecting)

	// 1. authenticate：Hello 即带 token 鉴权，失败映射错误。
	hello, err := s.client.Hello(ctx)
	if err != nil {
		s.log.Warn("peer 认证/连接失败", "machine_id", s.machineID, "cause", err)
		s.setState(SupervisorStateUnavailable)
		return
	}
	s.log.Info("peer hello 成功", "machine_id", s.machineID, "protocol_version", hello.ProtocolVersion)

	// 2. negotiate：capability 交集；缺核心项 → incompatible。
	negotiated, incompatible := Negotiate(hello.Capabilities)
	if incompatible {
		s.log.Warn("peer 协议不兼容（缺核心 capability）", "machine_id", s.machineID, "caps", hello.Capabilities)
		s.setState(SupervisorStateIncompatible)
		return
	}
	if s.onNegotiated != nil {
		s.onNegotiated(hello.ProtocolVersion, maps.Clone(negotiated))
	}
	s.setState(SupervisorStateReconciling)

	// 3. catch up：从 cursor 补拉 machine events 并投影。
	if err := s.catchUp(ctx); err != nil {
		s.log.Error("peer catch-up 失败", "machine_id", s.machineID, "cause", err)
		s.setState(SupervisorStateUnavailable)
		return
	}

	// 4. reconcile：全量快照校准（本计划先记录，投影差异由事件流承担）。
	if _, err := s.client.MachineSnapshot(ctx); err != nil {
		s.log.Warn("peer snapshot 失败", "machine_id", s.machineID, "cause", err)
	}

	// 5. connected：catch-up 完成、Reconcile 完成后才可写。
	s.log.Info("peer 已连接", "machine_id", s.machineID, "through_machine_seq", s.cursor)
	s.setState(SupervisorStateConnected)
}

// catchUp 从当前 cursor 分批补拉事件并投影。
func (s *Supervisor) catchUp(ctx context.Context) error {
	const batch = 200
	for {
		events, err := s.client.EventsAfter(ctx, s.machineID, s.cursor, batch)
		if err != nil {
			return err
		}
		for _, ev := range events {
			ce, applied, err := s.projector.Apply(ctx, toControlplaneEvent(ev))
			if err != nil {
				return fmt.Errorf("投影事件 %s/%d: %w", ev.MachineID, ev.MachineSeq, err)
			}
			if applied {
				s.log.Info("peer 事件已投影", "machine_id", s.machineID,
					"machine_seq", ev.MachineSeq, "control_revision", ce.ControlRevision, "kind", ev.Kind)
			}
			s.cursor = ev.MachineSeq
		}
		if len(events) < batch {
			return nil
		}
	}
}

// toControlplaneEvent 把 wire MachineEvent 转换为领域 MachineEvent。
func toControlplaneEvent(ev MachineEvent) controlplane.MachineEvent {
	return controlplane.MachineEvent{
		MachineID: ev.MachineID, MachineSeq: ev.MachineSeq, EventID: ev.EventID,
		Kind: controlplane.MachineEventKind(ev.Kind), ResourceID: ev.ResourceID,
		Payload: ev.Payload, CreatedAt: ev.CreatedAt,
	}
}

// setState 回调状态变化（nil 回调时跳过）。
func (s *Supervisor) setState(st SupervisorState) {
	if s.onState != nil {
		s.onState(st)
	}
}
