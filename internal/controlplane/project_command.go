// controlplane 项目创建命令：具名 command 与 MachineCommander 端口。
//
// 职责：
//   - 定义 CreateProjectCommand / CreateLocationCommand / InspectPathCommand /
//     CloneLocationCommand
//   - 定义 MachineCommander 端口（InspectPath/Clone），供 ProjectService 调用
//
// 边界：
//   - 只定义命令与端口，不含业务校验（由 ProjectService 执行）
//   - OperationID + TargetID 组成单个目录副作用的幂等键
package controlplane

import "context"

// CreateProjectCommand 是项目创建的完整命令。
//
// OperationID 同时作为幂等键：HTTP 断开不取消后台 operation，重试同 ID
// 只补失败目标，不重复执行已成功的目录副作用。
type CreateProjectCommand struct {
	OperationID string
	Name        string
	Locations   []CreateLocationCommand
}

// CreateLocationCommand 描述一个 Location 的创建意图。
//
// Source 决定执行路径：
//   - existing_path：InspectPath 检查既有目录
//   - git_clone：Clone 到 ClonePath（空=默认 ~/.handoff/<repo-name>）
type CreateLocationCommand struct {
	TargetID  string
	MachineID string
	Role      LocationRole
	Source    LocationSource
	Path      string // existing_path：目标目录
	GitURL    string // git_clone：源 URL
	ClonePath string // git_clone：目标目录（空=自动）
}

// InspectPathCommand 是检查既有目录的具名命令。
type InspectPathCommand struct {
	OperationID string
	TargetID    string
	MachineID   string
	Path        string
}

// CloneLocationCommand 是 git clone 的具名命令。
type CloneLocationCommand struct {
	OperationID string
	TargetID    string
	MachineID   string
	GitURL      string
	ClonePath   string
}

// MachineCommander 是目标机器 agentd 的资源命令端口。
//
// 为什么用具名 command：避免 machine/Git/path 位置错乱，也让调用链可追踪
// （每条命令都带 operation/target id）。
type MachineCommander interface {
	InspectPath(ctx context.Context, cmd InspectPathCommand) (PathInspection, error)
	Clone(ctx context.Context, cmd CloneLocationCommand) (PathInspection, error)
}
