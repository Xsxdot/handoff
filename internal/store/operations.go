// store operations.go：durable Operation 的持久化。
//
// 职责：
//   - CreateOperation/UpdateOperation/GetOperation/ListOperations：
//     durable 长操作的生命周期存储（operation_id 即幂等键）
//
// 边界：
//   - 业务编排（逐目标执行、partial/failed 判定）由 ProjectService 承担，
//     本层只做行存储
//   - targets 以 JSON 数组存列，回读时解析为 OperationTargetResult 列表
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/xushixin/handoff/internal/controlplane"
)

// CreateOperation 持久化一个 Operation（同 ID 重复调用幂等保留首个 pending）。
func (s *Store) CreateOperation(ctx context.Context, op controlplane.Operation) error {
	targets, err := json.Marshal(op.Targets)
	if err != nil {
		return fmt.Errorf("序列化 operation targets: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO operations (operation_id, kind, state, project_id, targets, progress, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(operation_id) DO NOTHING`,
		op.OperationID, string(op.Kind), string(op.State), op.ProjectID,
		string(targets), op.Progress, fmtTime(op.CreatedAt), fmtTime(op.UpdatedAt))
	if err != nil {
		return fmt.Errorf("写入 operation %s: %w", op.OperationID, err)
	}
	return nil
}

// UpdateOperation 更新 Operation 状态/目标结果。
func (s *Store) UpdateOperation(ctx context.Context, op controlplane.Operation) error {
	targets, err := json.Marshal(op.Targets)
	if err != nil {
		return fmt.Errorf("序列化 operation targets: %w", err)
	}
	_, err = s.db.ExecContext(ctx, `
UPDATE operations SET kind = ?, state = ?, project_id = ?, targets = ?, progress = ?, updated_at = ?
WHERE operation_id = ?`,
		string(op.Kind), string(op.State), op.ProjectID, string(targets),
		op.Progress, fmtTime(op.UpdatedAt), op.OperationID)
	if err != nil {
		return fmt.Errorf("更新 operation %s: %w", op.OperationID, err)
	}
	return nil
}

// GetOperation 按 operation_id 读取；不存在返回 ErrNotFound。
func (s *Store) GetOperation(ctx context.Context, operationID string) (controlplane.Operation, error) {
	var (
		op        controlplane.Operation
		kind      string
		state     string
		targets   string
		createdAt string
		updatedAt string
	)
	err := s.db.QueryRowContext(ctx, `
SELECT operation_id, kind, state, project_id, targets, progress, created_at, updated_at
FROM operations WHERE operation_id = ?`, operationID).
		Scan(&op.OperationID, &kind, &state, &op.ProjectID, &targets, &op.Progress, &createdAt, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return controlplane.Operation{}, ErrNotFound
	}
	if err != nil {
		return controlplane.Operation{}, fmt.Errorf("读取 operation %s: %w", operationID, err)
	}
	op.Kind = controlplane.OperationKind(kind)
	op.State = controlplane.OperationState(state)
	if err := json.Unmarshal([]byte(targets), &op.Targets); err != nil {
		return controlplane.Operation{}, fmt.Errorf("解析 operation targets: %w", err)
	}
	op.CreatedAt = parseTime(createdAt)
	op.UpdatedAt = parseTime(updatedAt)
	return op, nil
}

// ListOperations 返回全部 Operation。
func (s *Store) ListOperations(ctx context.Context) ([]controlplane.Operation, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT operation_id, kind, state, project_id, targets, progress, created_at, updated_at
FROM operations ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("查询 operations: %w", err)
	}
	defer rows.Close()
	var out []controlplane.Operation
	for rows.Next() {
		var (
			op        controlplane.Operation
			kind      string
			state     string
			targets   string
			createdAt string
			updatedAt string
		)
		if err := rows.Scan(&op.OperationID, &kind, &state, &op.ProjectID, &targets, &op.Progress,
			&createdAt, &updatedAt); err != nil {
			return nil, fmt.Errorf("读取 operation 行: %w", err)
		}
		op.Kind = controlplane.OperationKind(kind)
		op.State = controlplane.OperationState(state)
		if err := json.Unmarshal([]byte(targets), &op.Targets); err != nil {
			return nil, fmt.Errorf("解析 operation targets: %w", err)
		}
		op.CreatedAt = parseTime(createdAt)
		op.UpdatedAt = parseTime(updatedAt)
		out = append(out, op)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历 operations: %w", err)
	}
	return out, nil
}
