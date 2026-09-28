// diag_test.go 锁关联标识的 context 往返与日志展开行为：无关联时 Attrs 为 nil
// （空 id 不得冒充有关联），有关联时 operation_id/refresh_id 按序展开。
package diag

import (
	"context"
	"strings"
	"testing"
)

func TestWithFromRoundTrip(t *testing.T) {
	ctx := With(context.Background(), Info{OperationID: "op-abc", RefreshID: "rf-123"})
	got := From(ctx)
	if got.OperationID != "op-abc" || got.RefreshID != "rf-123" {
		t.Fatalf("context 往返失真: %+v", got)
	}
}

func TestFromWithoutInfoReturnsZero(t *testing.T) {
	if got := From(context.Background()); got != (Info{}) {
		t.Fatalf("无关联信息应返回零值: %+v", got)
	}
	if got := From(With(context.Background(), Info{})); got != (Info{}) {
		t.Fatalf("空 Info 应返回零值: %+v", got)
	}
}

func TestAttrsOmitsEmptyAndExpandsPresent(t *testing.T) {
	if attrs := Attrs(context.Background()); attrs != nil {
		t.Fatalf("无关联时 Attrs 应为 nil: %v", attrs)
	}
	attrs := Attrs(With(context.Background(), Info{OperationID: "op-x"}))
	if len(attrs) != 2 || attrs[0] != "operation_id" || attrs[1] != "op-x" {
		t.Fatalf("仅 operation_id 时应展开一对键值: %v", attrs)
	}
	attrs = Attrs(With(context.Background(), Info{OperationID: "op-x", RefreshID: "rf-y"}))
	if len(attrs) != 4 || attrs[2] != "refresh_id" || attrs[3] != "rf-y" {
		t.Fatalf("双 id 应按序展开两对键值: %v", attrs)
	}
}

func TestNewIDsHaveDistinctPrefixesAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		op := NewOperationID()
		rf := NewRefreshID()
		if !strings.HasPrefix(op, "op-") || !strings.HasPrefix(rf, "rf-") {
			t.Fatalf("前缀缺失: op=%q rf=%q", op, rf)
		}
		if seen[op] || seen[rf] {
			t.Fatalf("id 重复: op=%q rf=%q", op, rf)
		}
		seen[op], seen[rf] = true, true
	}
}
