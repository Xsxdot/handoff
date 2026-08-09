// controlplane BootstrapService：agentd 启动时的控制面初始化。
//
// 职责：
//   - 确保稳定本机 Machine 身份
//   - 把 config.Targets 投影为配置远端 Machine（secret_ref 引用，不落 token）
//   - 迁移旧任务（machine_id/workspace_id 为空）绑定本机与 detached Workspace
//   - 投影本机 machine events 到控制日志
//
// 边界：
//   - 初始化顺序固定，任一步失败即返回错误，拒绝以未就绪状态提供 desktop /v1 写服务
//   - token/secret 不进日志、不进领域对象
package controlplane

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/xushixin/handoff/internal/config"
)

// BootstrapService 负责 agentd 启动时的控制面初始化。
type BootstrapService struct {
	repo Repository
	log  *slog.Logger
}

// NewBootstrapService 创建控制面初始化服务。
//
// 参数：
//   - repo: 控制面持久化端口（实现方通常是 *store.Store）
//   - log: 本服务日志入口
func NewBootstrapService(repo Repository, log *slog.Logger) *BootstrapService {
	return &BootstrapService{repo: repo, log: log}
}

// Initialize 按固定顺序初始化控制面并返回本机 Machine。
//
// 顺序（不可交换）：
//  1. ensure local Machine
//  2. sync configured remote metadata
//  3. migrate legacy tasks
//  4. project local machine events into control log
//
// 为什么顺序固定：machine_id 必须先存在（配置机器与旧任务都引用本机身份）；
// 旧任务迁移要在任何 peer 同步之前完成，保证桌面 bootstrap 看到完整投影；
// 事件投影在最后，让以上副作用都落库后再产出 control events。
func (s *BootstrapService) Initialize(ctx context.Context, cfg *config.Config) (Machine, error) {
	local, err := s.repo.EnsureLocalMachine(ctx, "本机")
	if err != nil {
		return Machine{}, fmt.Errorf("确保本机身份: %w", err)
	}
	s.log.Info("控制面初始化：本机身份就绪", "machine_id", local.ID)

	configured := s.configuredFromConfig(cfg)
	if _, err := s.repo.SyncConfiguredMachines(ctx, configured); err != nil {
		return Machine{}, fmt.Errorf("同步配置机器: %w", err)
	}
	s.log.Info("控制面初始化：配置机器已同步", "machine_id", local.ID, "target_count", len(configured))

	migrated, err := s.repo.MigrateLegacyTasks(ctx, local.ID)
	if err != nil {
		return Machine{}, fmt.Errorf("迁移旧任务: %w", err)
	}
	s.log.Info("控制面初始化：旧任务迁移完成", "machine_id", local.ID, "migrated_tasks", migrated)

	if err := s.projectLocalEvents(ctx, local.ID); err != nil {
		return Machine{}, fmt.Errorf("投影本机事件: %w", err)
	}
	return local, nil
}

// configuredFromConfig 把 config.Targets 投影为 ConfiguredMachine 列表。
//
// 为什么 secret_ref 固定为 config.targets.<name>.token：credential resolver
// 按此键从配置读 token，领域层与 DB 只存引用，token 永不落库/进日志。
func (s *BootstrapService) configuredFromConfig(cfg *config.Config) []ConfiguredMachine {
	var out []ConfiguredMachine
	for name, t := range cfg.Targets {
		if t.Addr == "" {
			continue
		}
		displayName := t.DisplayName
		if displayName == "" {
			displayName = name
		}
		out = append(out, ConfiguredMachine{
			ConfigKey:   name,
			DisplayName: displayName,
			Kind:        MachineKindRemote,
			Endpoint:    t.Addr,
			SecretRef:   "config.targets." + name + ".token",
		})
	}
	return out
}

// projectLocalEvents 把本机 durable outbox 中未投影的事件补进控制日志。
//
// 为什么本机也走 ApplyMachineEvent：桌面 handler 不得为 local 分支直接查原始表，
// 本机资源事件与远端 peer 事件共用同一投影入口（spec §8.3）。
func (s *BootstrapService) projectLocalEvents(ctx context.Context, machineID string) error {
	const batch = 200
	after := int64(0)
	for {
		events, err := s.repo.MachineEventsAfter(ctx, machineID, after, batch)
		if err != nil {
			return err
		}
		for _, ev := range events {
			ce, applied, err := s.repo.ApplyMachineEvent(ctx, ev)
			if err != nil {
				return fmt.Errorf("投影本机事件 %s/%d: %w", ev.MachineID, ev.MachineSeq, err)
			}
			if applied {
				s.log.Info("本机事件已投影", "machine_id", machineID,
					"machine_seq", ev.MachineSeq, "control_revision", ce.ControlRevision, "kind", ce.Kind)
			}
			after = ev.MachineSeq
		}
		if len(events) < batch {
			return nil
		}
	}
}
