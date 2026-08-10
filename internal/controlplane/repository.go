// controlplane 领域端口定义：Repository 与 ConfiguredMachine。
//
// 职责：
//   - 声明领域层所需的持久化与投影端口，领域层不依赖 database/sql
//   - ConfiguredMachine 描述配置的远端机器（secret 值不进入领域层）
//
// 边界：
//   - 只有接口，无实现；实现由 store 包与 machineauthority 提供
//   - 返回的 bool 表示重复 machine event 被幂等忽略，实现不得依赖错误文本判断重复
package controlplane

import (
	"context"
	"errors"
)

// ErrNotFound 表示按 id 查询的资源不存在。
var ErrNotFound = errors.New("资源不存在")

// ConfiguredMachine 描述一条配置的远端机器（来自 config.Targets）。
//
// SecretRef 是运行时 credential resolver 读取 token 的键（config.targets.<name>），
// 不携带 secret 值本身——防止 token 进入领域层或落库。
type ConfiguredMachine struct {
	ConfigKey   string // config.Targets 的 map key，稳定身份
	DisplayName string
	Kind        MachineKind
	Endpoint    string
	SecretRef   string
}

// Repository 是控制面领域层所需的持久化端口。
//
// 为什么领域层不直接依赖 store.Store：DDD 依赖方向要求领域层只声明它需要的
// 能力，持久化细节由 store 包注入，便于替换与测试。
type Repository interface {
	// EnsureLocalMachine 确保存在稳定的本机 Machine 并返回它（创建或复用）。
	EnsureLocalMachine(ctx context.Context, displayName string) (Machine, error)

	// SyncConfiguredMachines 把配置的远端机器投影为 Machine 行；
	// 删除的 target 保留 last-known Machine 但标 unavailable。
	SyncConfiguredMachines(ctx context.Context, configured []ConfiguredMachine) ([]Machine, error)

	// Snapshot 返回控制面全量投影快照（bootstrap 数据源）。
	Snapshot(ctx context.Context) (Snapshot, error)

	// UpsertWorkspaceWithMachineEvent 在同一事务内 upsert Workspace 并追加
	// machine event，保证资源更新与 outbox 同生同灭。
	UpsertWorkspaceWithMachineEvent(ctx context.Context, ws Workspace, kind MachineEventKind) (MachineEvent, error)

	// RemoveWorkspaceWithMachineEvent 在同一事务内移除 Workspace 并追加事件。
	RemoveWorkspaceWithMachineEvent(ctx context.Context, machineID, workspaceID string) (MachineEvent, error)

	// UpsertGitRefsWithMachineEvents 在同一事务内 upsert GitRef 集合并追加事件。
	UpsertGitRefsWithMachineEvents(ctx context.Context, locationID string, refs []GitRef) ([]MachineEvent, error)

	// AppendTaskSummaryEvent 追加任务摘要事件（Task 创建/更新时调用）。
	AppendTaskSummaryEvent(ctx context.Context, summary TaskSummary) (MachineEvent, error)

	// ApplyMachineEvent 在一个事务内：幂等记录 machine event → 更新投影 →
	// 追加 ControlEvent → 更新 last_machine_seq。
	//
	// 返回的 bool 表示该 machine event 重复被幂等忽略（未分配新 revision）。
	ApplyMachineEvent(ctx context.Context, ev MachineEvent) (ControlEvent, bool, error)

	// MachineEventsAfter 返回机器在 afterSeq 之后的事件，按 machine_seq 升序，最多 limit 条。
	MachineEventsAfter(ctx context.Context, machineID string, afterSeq int64, limit int) ([]MachineEvent, error)

	// ControlEventsAfter 返回 revision 之后的 control events，升序，最多 limit 条。
	ControlEventsAfter(ctx context.Context, afterRevision int64, limit int) ([]ControlEvent, error)

	// MigrateLegacyTasks 把全部 machine_id 为空的旧任务绑定 local Machine 与
	// detached Workspace，并在同一事务 upsert task_summaries；返回迁移的任务数。
	// 这是 BootstrapService 的第三步，必须原子完成（两 ID 同写同灭）。
	MigrateLegacyTasks(ctx context.Context, localMachineID string) (int, error)

	// CreateProject 在同一事务内创建 Project、ProjectLocation 与 main Workspace，
	// 并追加 control event；返回产生的 ControlEvent。
	CreateProject(ctx context.Context, p Project, locations []ProjectLocation, workspaces []Workspace,
		finalOperation *Operation) ([]ControlEvent, error)

	// CreateOperation 持久化一个 pending Operation（operation_id 幂等），并返回
	// operation.upsert；重复 ID 未写入时返回零值事件。
	CreateOperation(ctx context.Context, op Operation) (ControlEvent, error)

	// UpdateOperation 更新 Operation 状态/目标结果并返回 operation.upsert。
	UpdateOperation(ctx context.Context, op Operation) (ControlEvent, error)

	// GetOperation 按 operation_id 读取 Operation；不存在返回 ErrNotFound。
	GetOperation(ctx context.Context, operationID string) (Operation, error)

	// ListOperations 返回全部 Operation（bootstrap 快照用）。
	ListOperations(ctx context.Context) ([]Operation, error)

	// GetWorkspace 按 id 读取 Workspace。
	GetWorkspace(ctx context.Context, id string) (Workspace, error)

	// GetProject 按 id 读取 Project。
	GetProject(ctx context.Context, id string) (Project, error)

	// GetMachine 按 id 读取 Machine。
	GetMachine(ctx context.Context, id string) (Machine, error)

	// ResolveWorkspaceForPath 解析一个目录对应的工作区（命中已有或创建 detached）。
	ResolveWorkspaceForPath(ctx context.Context, machineID, canonical, displayPath string) (Workspace, error)

	// AdoptWorkspace 把 detached Workspace 归并为主目录（只更新 location_id/kind，
	// 保留 Workspace ID 与全部 Task 引用）。
	AdoptWorkspace(ctx context.Context, wsID, locationID string) error
}
