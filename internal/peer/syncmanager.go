// peer syncmanager：多台远端机器的同步调度。
//
// 职责：
//   - 为每台配置的远端机器启动一个独立 worker（串行，机器 ID 隔离）
//   - 断线 backoff 重连；一台坏机器不阻塞其他
//   - 机器状态变化回调投影到控制面 Machine 表
//
// 边界：
//   - 不持有真实 token：token 由凭证解析函数按 Machine.SecretRef 提供
//   - 非 loopback 明文 HTTP 的 fail-closed 门控由调用方（cmd）执行
package peer

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// SyncManagerConfig 是同步管理器的构造参数。
type SyncManagerConfig struct {
	// Machines 描述要同步的远端机器（含 endpoint 与 secret_ref）。
	Machines []SyncMachine
	// CredentialResolver 按 secret_ref 返回 token。
	CredentialResolver func(secretRef string) string
	// Projector 是控制面投影端口（controlplane.Projector）。
	Projector ProjectorPort
	// OnMachineState 回调机器状态变化（machine_id, state）。
	OnMachineState func(machineID string, state SupervisorState)
	// Interval 是重连间隔；0=默认 30s。
	Interval time.Duration
	Log      *slog.Logger
}

// SyncMachine 描述一台待同步的远端机器。
type SyncMachine struct {
	MachineID string
	Endpoint  string
	SecretRef string
}

// SyncManager 管理多台远端机器的同步生命周期。
type SyncManager struct {
	cfg      SyncManagerConfig
	log      *slog.Logger
	interval time.Duration
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	mu       sync.Mutex
	running  bool
}

// NewSyncManager 创建同步管理器。
func NewSyncManager(cfg SyncManagerConfig) *SyncManager {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	interval := cfg.Interval
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &SyncManager{cfg: cfg, log: log, interval: interval}
}

// Start 为每台机器启动独立同步 worker。
func (m *SyncManager) Start(ctx context.Context) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	for _, mach := range m.cfg.Machines {
		m.wg.Add(1)
		go m.runMachine(ctx, mach)
	}
	m.log.Info("peer 同步管理器已启动", "machine_count", len(m.cfg.Machines))
}

// Stop 停止全部 worker。
func (m *SyncManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.running {
		return
	}
	m.running = false
	if m.cancel != nil {
		m.cancel()
	}
	m.wg.Wait()
	m.log.Info("peer 同步管理器已停止")
}

// runMachine 是单台机器的同步循环（含 backoff 重连）。
func (m *SyncManager) runMachine(ctx context.Context, mach SyncMachine) {
	defer m.wg.Done()
	token := ""
	if m.cfg.CredentialResolver != nil {
		token = m.cfg.CredentialResolver(mach.SecretRef)
	}
	client := NewClient(ClientConfig{Endpoint: mach.Endpoint, Token: token})
	defer client.Close()

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		supervisor := NewSupervisor(SupervisorConfig{
			MachineID: mach.MachineID,
			Client:    client,
			Projector: m.cfg.Projector,
			OnState: func(st SupervisorState) {
				if m.cfg.OnMachineState != nil {
					m.cfg.OnMachineState(mach.MachineID, st)
				}
			},
			Log: m.log,
		})
		supervisor.Run(ctx)
		// backoff 后重连；ctx 取消立即退出
		select {
		case <-ctx.Done():
			return
		case <-time.After(m.interval):
		}
	}
}
