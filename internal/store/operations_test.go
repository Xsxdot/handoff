// store operations.go 测试：durable Operation 生命周期存储。
//
// 职责：
//   - CreateOperation 幂等（同 ID 保留首个 pending）
//   - UpdateOperation 更新状态与 targets
//   - GetOperation/ListOperations 回读
//
// 边界：
//   - 使用真实 SQLite 文件（t.TempDir）
//   - 业务编排（partial 判定）由 ProjectService 测试覆盖
package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
)

// TestOperationLifecycle 验证 create → update → get 全流程。
func TestOperationLifecycle(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	op := controlplane.Operation{
		OperationID: "op1", Kind: controlplane.OperationKindCreateProject,
		State:     controlplane.OperationStatePending,
		CreatedAt: now(), UpdatedAt: now(),
	}
	if err := s.CreateOperation(ctx, op); err != nil {
		t.Fatalf("CreateOperation: %v", err)
	}
	// 同 ID 幂等：重复创建保留首个
	if err := s.CreateOperation(ctx, op); err != nil {
		t.Fatalf("重复 CreateOperation: %v", err)
	}

	// 更新为 succeeded + targets
	op.State = controlplane.OperationStateSucceeded
	op.ProjectID = "p1"
	op.Targets = []controlplane.OperationTargetResult{{
		TargetID: "tg1", MachineID: "m1", State: controlplane.OperationStateSucceeded,
		Result: &controlplane.OperationResult{WorkspaceID: "ws1", LocationID: "loc1", Path: "/r"},
	}}
	if err := s.UpdateOperation(ctx, op); err != nil {
		t.Fatalf("UpdateOperation: %v", err)
	}

	got, err := s.GetOperation(ctx, "op1")
	if err != nil {
		t.Fatalf("GetOperation: %v", err)
	}
	if got.State != controlplane.OperationStateSucceeded || got.ProjectID != "p1" {
		t.Fatalf("got = %+v", got)
	}
	if len(got.Targets) != 1 || got.Targets[0].Result.WorkspaceID != "ws1" {
		t.Fatalf("targets 回读 = %+v", got.Targets)
	}

	if _, err := s.GetOperation(ctx, "nope"); err != store.ErrNotFound {
		t.Fatalf("GetOperation(nope) err = %v, want ErrNotFound", err)
	}
}

// TestListOperations 验证全部 operation 回读。
func TestListOperations(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	for _, id := range []string{"op1", "op2"} {
		if err := s.CreateOperation(ctx, controlplane.Operation{
			OperationID: id, Kind: controlplane.OperationKindCreateProject,
			State: controlplane.OperationStatePending, CreatedAt: now(), UpdatedAt: now(),
		}); err != nil {
			t.Fatalf("CreateOperation(%s): %v", id, err)
		}
	}
	ops, err := s.ListOperations(ctx)
	if err != nil {
		t.Fatalf("ListOperations: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("ops = %d, want 2", len(ops))
	}
}
