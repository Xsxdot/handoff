// Package controlplane 定义 handoff 桌面控制面的领域模型与领域端口。
//
// 职责：
//   - 定义 Machine/Project/ProjectLocation/Workspace/GitRef/TaskSummary/
//     Operation/MachineEvent/ControlEvent 等稳定领域类型与枚举
//   - 提供 Repository 端口，领域层不依赖 database/sql 与具体持久化实现
//   - 校验 Project Location 约束（1–2 个、本机至多一个、远端至多一个）
//
// 边界：
//   - 纯领域层：无 I/O、无 HTTP、无 DB 访问；持久化由 store 包实现 Repository
//   - 不承载任何 secret/token 值（只保存 SecretRef 引用，见 Machine）
//   - 资源一律使用稳定 ID 作主键；路径只是资源描述与执行快照，不是跨层主键
package controlplane

import (
	"encoding/json"
	"fmt"
	"time"
)

// MachineKind 表示机器类型。
type MachineKind string

const (
	MachineKindLocal  MachineKind = "local"
	MachineKindRemote MachineKind = "remote"
)

// MachineStatus 表示机器连接状态（spec §12.1 五值）。
//
// 状态语义：
//   - connecting：建立连接中，保留树结构但不开放资源操作
//   - reconciling：已认证，正在 capability、事件补拉和资源校准；写操作仍关闭
//   - connected：cursor 已追平、Reconcile 完成、核心 capability 可用
//   - unavailable：网络、认证或进程不可达，资源现场不可用
//   - incompatible：已连接但缺少核心 capability，需升级 agentd
type MachineStatus string

const (
	MachineStatusConnecting   MachineStatus = "connecting"
	MachineStatusReconciling  MachineStatus = "reconciling"
	MachineStatusConnected    MachineStatus = "connected"
	MachineStatusUnavailable  MachineStatus = "unavailable"
	MachineStatusIncompatible MachineStatus = "incompatible"
)

// LocationRole 表示项目 Location 在本机/远端的角色。
type LocationRole string

const (
	LocationRoleLocal  LocationRole = "local"
	LocationRoleRemote LocationRole = "remote"
)

// Compatible 断言 role 与 machine kind 是否匹配：local 只对应本机、
// remote 只对应远端。
func (r LocationRole) Compatible(k MachineKind) bool {
	switch r {
	case LocationRoleLocal:
		return k == MachineKindLocal
	case LocationRoleRemote:
		return k == MachineKindRemote
	}
	return false
}

// LocationSource 表示 Location 的创建来源。
type LocationSource string

const (
	LocationSourceExistingPath LocationSource = "existing_path"
	LocationSourceGitClone     LocationSource = "git_clone"
)

// WorkspaceKind 表示工作区类型。
type WorkspaceKind string

const (
	WorkspaceKindMain     WorkspaceKind = "main"
	WorkspaceKindWorktree WorkspaceKind = "worktree"
	WorkspaceKindDetached WorkspaceKind = "detached"
)

// Availability 表示工作区资源可用性。
type Availability string

const (
	AvailabilityAvailable   Availability = "available"
	AvailabilityUnavailable Availability = "unavailable"
)

// OperationKind 表示长操作的类别。
type OperationKind string

const (
	OperationKindCreateProject  OperationKind = "create_project"
	OperationKindClone          OperationKind = "clone"
	OperationKindRegisterPath   OperationKind = "register_path"
	OperationKindCreateWorktree OperationKind = "create_worktree"
)

// OperationState 表示长操作的生命周期状态。
type OperationState string

const (
	OperationStatePending   OperationState = "pending"
	OperationStateRunning   OperationState = "running"
	OperationStatePartial   OperationState = "partial"
	OperationStateSucceeded OperationState = "succeeded"
	OperationStateFailed    OperationState = "failed"
)

// MachineEventKind 表示所属机器 outbox 事件类型。
type MachineEventKind string

const (
	MachineEventWorkspaceUpsert MachineEventKind = "workspace.upsert"
	MachineEventWorkspaceRemove MachineEventKind = "workspace.remove"
	MachineEventGitRefUpsert    MachineEventKind = "git_ref.upsert"
	MachineEventGitRefRemove    MachineEventKind = "git_ref.remove"
	MachineEventTaskUpsert      MachineEventKind = "task.upsert"
	MachineEventTaskRemove      MachineEventKind = "task.remove"
	MachineEventOperationUpsert MachineEventKind = "operation.upsert"
)

// ControlEventKind 表示控制面投影事件类型（全局单调 revision）。
type ControlEventKind string

const (
	ControlEventKindMachineUpsert     ControlEventKind = "machine.upsert"
	ControlEventKindProjectUpsert     ControlEventKind = "project.upsert"
	ControlEventKindLocationUpsert    ControlEventKind = "location.upsert"
	ControlEventKindWorkspaceUpsert   ControlEventKind = "workspace.upsert"
	ControlEventKindWorkspaceRemove   ControlEventKind = "workspace.remove"
	ControlEventKindGitRefUpsert      ControlEventKind = "git_ref.upsert"
	ControlEventKindGitRefRemove      ControlEventKind = "git_ref.remove"
	ControlEventKindTaskSummaryUpsert ControlEventKind = "task_summary.upsert"
	ControlEventKindTaskSummaryRemove ControlEventKind = "task_summary.remove"
	ControlEventKindOperationUpsert   ControlEventKind = "operation.upsert"
)

// TaskSummaryState 表示任务摘要的展示状态（与 proto.TaskState 兼容的超集，
// 含 stalled 以便桌面直接展示）。
type TaskSummaryState string

const (
	TaskSummaryStatePending       TaskSummaryState = "pending"
	TaskSummaryStateRunning       TaskSummaryState = "running"
	TaskSummaryStateWaitingAnswer TaskSummaryState = "waiting_answer"
	TaskSummaryStateWaitingReview TaskSummaryState = "waiting_review"
	TaskSummaryStateCompleted     TaskSummaryState = "completed"
	TaskSummaryStateFailed        TaskSummaryState = "failed"
	TaskSummaryStateStalled       TaskSummaryState = "stalled"
)

// Machine 表示一台已注册的开发机。
//
// 为什么 SecretRef 存引用而非 secret 值：桌面与 peer 同步都只携带公开投影，
// token/secret 只由运行时 credential resolver 从配置按 SecretRef 读取，
// 杜绝 secret 落库或进入 renderer。
type Machine struct {
	ID              string
	DisplayName     string
	Kind            MachineKind
	Endpoint        string
	SecretRef       string
	ProtocolVersion int
	Capabilities    map[string]int
	Status          MachineStatus
	LastSeenAt      *time.Time
}

// Project 表示一个用户项目；至少含一个 ProjectLocation。
type Project struct {
	ID          string
	Name        string
	GitIdentity string // 规范化 remote identity；未知时为空
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProjectLocation 表示项目在本机或一台远端开发机上的目录。
//
// 约束（见 ValidateProjectLocations）：每项目本机 0..1、远端 0..1，合计 >=1。
type ProjectLocation struct {
	ID              string
	ProjectID       string
	MachineID       string
	MachineKind     MachineKind // role 与 machine kind 一致性校验用（冗余派生）
	Role            LocationRole
	MainWorkspaceID string
	Source          LocationSource
	GitURL          string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Workspace 表示机器上的一个工作区（main/worktree/detached）。
//
// LocationID 为 nil 表示 detached：项目外路径 dispatch 时创建，添加项目后
// 由控制面按 machine_id + repo_identity/git_common_dir + canonical_path 归并，
// 只更新 location_id/kind，不重写 Workspace ID。
type Workspace struct {
	ID            string
	MachineID     string
	LocationID    *string
	Kind          WorkspaceKind
	Path          string
	CanonicalPath string
	RepoIdentity  string
	GitCommonDir  string
	Branch        string
	HeadOID       string
	Availability  Availability
	LastScannedAt time.Time
}

// GitRef 表示一个 ProjectLocation 下的分支引用。
//
// 分支清单属于 ProjectLocation：本机 clone 与远端 clone 的 refs 可能不同。
type GitRef struct {
	LocationID             string
	Name                   string
	HeadOID                string
	CheckedOutWorkspaceIDs []string
}

// TaskSummary 表示跨机器任务摘要（桌面左栏计数的投影）。
//
// 只承载展示摘要，不含 plan/事件全文；完整任务详情走所属机器 agentd。
type TaskSummary struct {
	TaskID      string
	MachineID   string
	WorkspaceID string
	Name        string
	Executor    string
	State       TaskSummaryState
	Attention   int // 待用户处理数（挂起工单数等）
	UpdatedAt   time.Time
}

// Operation 表示一个 durable 长操作（项目创建/clone/register_path/create_worktree）。
//
// OperationID 即 command_id，同时作为幂等键；HTTP/WS 断开不取消后台 operation。
type Operation struct {
	OperationID string
	Kind        OperationKind
	State       OperationState
	ProjectID   string
	Targets     []OperationTargetResult
	Progress    string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// OperationTargetResult 表示 Operation 单个目标的执行结果。
type OperationTargetResult struct {
	TargetID  string
	MachineID string
	State     OperationState
	Result    *OperationResult
	Error     *OperationError
}

// OperationResult 表示目标成功的权威结果。
type OperationResult struct {
	WorkspaceID string
	LocationID  string
	Path        string
}

// OperationError 表示目标失败的原因（不泄露内部错误细节）。
type OperationError struct {
	Code    string
	Message string
}

// MachineEvent 是所属机器 agentd 的 durable outbox 事件。
//
// machine_seq 每机器单调递增；本机控制面按 (machine_id, machine_seq) 幂等
// 投影为 control_revision。payload 是完整可幂等 upsert 的公开投影，不含
// 本地 secret 或文件内容。
type MachineEvent struct {
	MachineID  string
	MachineSeq int64
	EventID    string
	Kind       MachineEventKind
	ResourceID string
	Payload    json.RawMessage
	CreatedAt  time.Time
}

// ControlEvent 是控制面投影事件，每条获得全局单调 control_revision。
type ControlEvent struct {
	ControlRevision int64
	Kind            ControlEventKind
	ResourceID      string
	Payload         json.RawMessage
	CreatedAt       time.Time
}

// Snapshot 是控制面全量投影快照（bootstrap 数据源）。
type Snapshot struct {
	ControlRevision     int64
	Machines            []Machine
	Projects            []Project
	Locations           []ProjectLocation
	Workspaces          []Workspace
	GitRefs             []GitRef
	ActiveTaskSummaries []TaskSummary
	Operations          []Operation
}

// ValidateProjectLocations 校验项目 Location 约束：
//
//   - 合计必须为 1–2 个
//   - 本机 role 至多一个
//   - 远端 role 至多一个
//   - role 与 machine kind 必须一致
//
// 为什么在本层而非 handler：这是项目域的权威业务规则，必须由领域层校验，
// handler/assembler 只做字段转换，不承载业务规则。
func ValidateProjectLocations(locs []ProjectLocation) error {
	if len(locs) == 0 {
		return fmt.Errorf("项目必须至少有一个 Location")
	}
	if len(locs) > 2 {
		return fmt.Errorf("项目 Location 最多两个（本机至多一个、远端至多一个），当前 %d 个", len(locs))
	}
	var localCount, remoteCount int
	for _, l := range locs {
		if !l.Role.Compatible(l.MachineKind) {
			return fmt.Errorf("Location role %q 与 machine kind %q 不一致", l.Role, l.MachineKind)
		}
		switch l.Role {
		case LocationRoleLocal:
			localCount++
		case LocationRoleRemote:
			remoteCount++
		default:
			return fmt.Errorf("未知 Location role %q", l.Role)
		}
	}
	if localCount > 1 {
		return fmt.Errorf("项目本机 Location 至多一个，当前 %d 个", localCount)
	}
	if remoteCount > 1 {
		return fmt.Errorf("项目远端 Location 至多一个，当前 %d 个", remoteCount)
	}
	return nil
}
