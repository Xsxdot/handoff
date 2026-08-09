// Package machineauthority 定义本机资源权威与所属机器 agentd 的边界。
//
// 职责：
//   - Authority：本机资源边界的唯一入口（InspectPath/Clone/ReconcileLocation/Snapshot）
//   - Inventory：用 Git 2.25 基线命令发现 worktree/branch/HEAD
//   - Reconcile：把真实仓库状态与 store 投影对齐，变化经 durable outbox 上报
//   - GitWatcher：文件系统变化只是「尽快扫描」的提示，不直接当事实
//
// 边界：
//   - 本计划先实现 Inspect/Clone/Inventory；文件内容、PTY、Preview 方法留到
//     计划 02，禁止用 Electron Node fs 作为临时替代
//   - 错误日志带 machine/location/path 摘要，不打完整 remote URL 凭据
package machineauthority

import (
	"context"

	"github.com/xushixin/handoff/internal/controlplane"
)

// PathInspection 描述对某个路径的检查结果（InspectPath/Clone 的返回）。
//
// 类型定义在 controlplane（领域层 MachineCommander 端口引用它），
// 此处为 machineauthority 侧的类型别名，保持 API 面一致。
type PathInspection = controlplane.PathInspection

// CloneCommand 是一次 git clone 的具名命令（避免 machine/Git/path 位置错乱）。
type CloneCommand struct {
	GitURL    string
	ClonePath string
}

// ReconcileResult 是一次 Reconcile 的扫描结果与变化统计。
type ReconcileResult struct {
	Workspaces        []controlplane.Workspace
	GitRefs           []controlplane.GitRef
	Upserted          int
	Removed           int
	LocationWorkspace string
}

// MachineSnapshot 是所属机器资源的全量快照（peer hello/snapshot 用）。
type MachineSnapshot struct {
	Workspaces        []controlplane.Workspace
	GitRefs           []controlplane.GitRef
	TaskSummaries     []controlplane.TaskSummary
	ThroughMachineSeq int64
}

// Authority 明确本机资源边界。
//
// 本计划先实现 Inspect/Clone/Inventory；文件内容、PTY、Preview 方法留到计划 02，
// 禁止用 Electron Node fs 作为临时替代。
type Authority interface {
	InspectPath(ctx context.Context, path string) (PathInspection, error)
	Clone(ctx context.Context, cmd CloneCommand) (PathInspection, error)
	ReconcileLocation(ctx context.Context, loc controlplane.ProjectLocation) (ReconcileResult, error)
	Snapshot(ctx context.Context) (MachineSnapshot, error)
}
