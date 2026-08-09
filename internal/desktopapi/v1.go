// Package desktopapi 定义桌面 v1 协议线的 wire DTO、错误线格式与无状态转换器。
//
// 职责：
//   - BootstrapResponse / ControlEventEnvelope / OperationDTO 等 wire 类型
//   - Problem 错误线格式与 WriteProblem 响应写回
//   - CatalogAssembler：领域模型 ↔ wire DTO 的纯转换（无业务校验）
//
// 边界：
//   - 不直接 JSON marshal 领域对象：所有跨进程传输都必须经 wire DTO
//   - 不含业务规则：Location 数量、role/Machine 匹配等由 ProjectService 校验
//   - 不含 DB/I/O；handler 层只做 decode → assembler → service → assembler → encode
//
// 为什么独立 wire 层：领域对象与桌面协议可能各自演进，直接序列化领域结构会让
// 契约漂移直接污染领域层；wire DTO + golden JSON 让 Go/TS 两端的契约都有锁定。
package desktopapi

import (
	"encoding/json"
	"time"
)

// MachineDTO 是桌面可见的机器行（不含 SecretRef/Endpoint 之外的安全字段）。
type MachineDTO struct {
	ID              string         `json:"id"`
	DisplayName     string         `json:"display_name"`
	Kind            string         `json:"kind"`
	Endpoint        string         `json:"endpoint"`
	ProtocolVersion int            `json:"protocol_version"`
	Capabilities    map[string]int `json:"capabilities"`
	Status          string         `json:"status"`
	LastSeenAt      *time.Time     `json:"last_seen_at"`
}

// ProjectDTO 是桌面可见的项目行。
type ProjectDTO struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	GitIdentity string    `json:"git_identity,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// ProjectLocationDTO 是桌面可见的 Location 行。
type ProjectLocationDTO struct {
	ID              string    `json:"id"`
	ProjectID       string    `json:"project_id"`
	MachineID       string    `json:"machine_id"`
	Role            string    `json:"role"`
	MainWorkspaceID string    `json:"main_workspace_id"`
	Source          string    `json:"source"`
	GitURL          string    `json:"git_url,omitempty"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// WorkspaceDTO 是桌面可见的工作区行。
type WorkspaceDTO struct {
	ID            string    `json:"id"`
	MachineID     string    `json:"machine_id"`
	LocationID    *string   `json:"location_id"`
	Kind          string    `json:"kind"`
	Path          string    `json:"path"`
	CanonicalPath string    `json:"canonical_path"`
	RepoIdentity  string    `json:"repo_identity,omitempty"`
	GitCommonDir  string    `json:"git_common_dir,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	HeadOID       string    `json:"head_oid,omitempty"`
	Availability  string    `json:"availability"`
	LastScannedAt time.Time `json:"last_scanned_at"`
}

// GitRefDTO 是桌面可见的分支引用行。
type GitRefDTO struct {
	LocationID             string   `json:"location_id"`
	Name                   string   `json:"name"`
	HeadOID                string   `json:"head_oid"`
	CheckedOutWorkspaceIDs []string `json:"checked_out_workspace_ids"`
}

// TaskSummaryDTO 是桌面可见的任务摘要行。
type TaskSummaryDTO struct {
	TaskID      string    `json:"task_id"`
	MachineID   string    `json:"machine_id"`
	WorkspaceID string    `json:"workspace_id"`
	Name        string    `json:"name"`
	Executor    string    `json:"executor"`
	State       string    `json:"state"`
	Attention   int       `json:"attention"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// OperationTargetDTO 是 Operation 单目标结果。
type OperationTargetDTO struct {
	TargetID  string `json:"target_id"`
	MachineID string `json:"machine_id"`
	State     string `json:"state"`
	// Result/Error 二选一，由 state 决定语义；Error 不泄露内部细节。
	Result *OperationResultDTO `json:"result,omitempty"`
	Error  *OperationErrorDTO  `json:"error,omitempty"`
}

// OperationResultDTO 是目标成功的权威结果。
type OperationResultDTO struct {
	WorkspaceID string `json:"workspace_id"`
	LocationID  string `json:"location_id"`
	Path        string `json:"path"`
}

// OperationErrorDTO 是目标失败原因。
type OperationErrorDTO struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// OperationDTO 是桌面可见的长操作行。
type OperationDTO struct {
	OperationID string               `json:"operation_id"`
	Kind        string               `json:"kind"`
	State       string               `json:"state"`
	ProjectID   string               `json:"project_id,omitempty"`
	Targets     []OperationTargetDTO `json:"targets"`
	Progress    string               `json:"progress,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   time.Time            `json:"updated_at"`
}

// BootstrapResponse 是 GET /v1/bootstrap 的响应体。
//
// 所有集合字段必须是非 nil 数组（空集合编码为 [] 而非 null）。
// ControlRevision 是快照对应的 revision，客户端随后以 after=该值订阅控制流。
type BootstrapResponse struct {
	Machines            []MachineDTO         `json:"machines"`
	Projects            []ProjectDTO         `json:"projects"`
	Locations           []ProjectLocationDTO `json:"locations"`
	Workspaces          []WorkspaceDTO       `json:"workspaces"`
	GitRefs             []GitRefDTO          `json:"git_refs"`
	ActiveTaskSummaries []TaskSummaryDTO     `json:"active_task_summaries"`
	Operations          []OperationDTO       `json:"operations"`
	ControlRevision     int64                `json:"control_revision"`
}

// ControlEventEnvelope 是控制流上的一条增量事件。
type ControlEventEnvelope struct {
	Revision   int64           `json:"revision"`
	Kind       string          `json:"kind"`
	ResourceID string          `json:"resource_id"`
	Payload    json.RawMessage `json:"payload"`
	CreatedAt  time.Time       `json:"created_at"`
}

// CreateProjectRequest 是 POST /v1/projects/operations 的请求体。
type CreateProjectRequest struct {
	OperationID string                     `json:"operation_id"`
	Name        string                     `json:"name"`
	Locations   []CreateProjectLocationReq `json:"locations"`
}

// CreateProjectLocationReq 是 CreateProjectRequest 的一个 Location。
type CreateProjectLocationReq struct {
	MachineID string `json:"machine_id"`
	Role      string `json:"role"`
	Source    string `json:"source"`
	Path      string `json:"path,omitempty"`
	GitURL    string `json:"git_url,omitempty"`
	ClonePath string `json:"clone_path,omitempty"`
}
