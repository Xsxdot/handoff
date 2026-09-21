package executor

import "context"

// SnapshotApplier 是可选能力：Continue 开始时把新快照编进原生配置。
// 未实现则 OpenCode 路径必须报能力不足，不得假装同等保证。
type SnapshotApplier interface {
	ApplySnapshot(ctx context.Context, taskID string, snap PolicySnapshot) error
}
