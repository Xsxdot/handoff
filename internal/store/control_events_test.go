// store control_events 测试：全局投影事件的读取与游标。
//
// 职责：
//   - ControlEventsAfter 按 revision 升序拉取
//   - CurrentCursor 返回机器消费进度
//   - control_revision 全局单调
//
// 边界：
//   - 使用真实 SQLite 文件（t.TempDir）
//   - 控制事件写入在 ApplyMachineEvent 内，这里只测读取侧
package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/store"
)

// TestControlEventsAfterAscending 验证按 revision 升序拉取。
func TestControlEventsAfterAscending(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	// 连续投影三条 workspace 事件
	for i := 1; i <= 3; i++ {
		ev := controlplane.MachineEvent{
			MachineID: "m1", EventID: "evt-" + itoa2(int64(i)),
			Kind:       controlplane.MachineEventWorkspaceUpsert,
			ResourceID: "ws" + itoa2(int64(i)),
			Payload: marshalWorkspace(controlplane.Workspace{ID: "ws" + itoa2(int64(i)),
				MachineID: "m1", Kind: controlplane.WorkspaceKindMain,
				Path: "/r", CanonicalPath: "/r"}),
		}
		if _, applied, err := s.ApplyMachineEvent(ctx, ev); err != nil || !applied {
			t.Fatalf("Apply %d: applied=%v err=%v", i, applied, err)
		}
	}
	events, err := s.ControlEventsAfter(ctx, 0, 10)
	if err != nil {
		t.Fatalf("ControlEventsAfter: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("events = %d, want 3", len(events))
	}
	for i := 1; i < len(events); i++ {
		if events[i].ControlRevision <= events[i-1].ControlRevision {
			t.Fatalf("revision 应升序: %d -> %d", events[i-1].ControlRevision, events[i].ControlRevision)
		}
	}
	// after=2 只返回 3
	after, err := s.ControlEventsAfter(ctx, 2, 10)
	if err != nil || len(after) != 1 || after[0].ControlRevision != 3 {
		t.Fatalf("after=2 应返回 [3]: %+v err=%v", after, err)
	}
}

// TestCurrentCursorAdvances 验证 cursor 随投影前进。
func TestCurrentCursorAdvances(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	if cur, err := s.CurrentCursor(ctx, "m1"); err != nil || cur != 0 {
		t.Fatalf("初始 cursor = %d err=%v, want 0", cur, err)
	}
	for i := int64(1); i <= 2; i++ {
		ev := controlplane.MachineEvent{
			MachineID: "m1", EventID: "evt-" + itoa2(i),
			Kind: controlplane.MachineEventWorkspaceUpsert, ResourceID: "ws1",
			Payload: marshalWorkspace(controlplane.Workspace{ID: "ws1", MachineID: "m1",
				Kind: controlplane.WorkspaceKindMain, Path: "/r", CanonicalPath: "/r"}),
		}
		if _, applied, err := s.ApplyMachineEvent(ctx, ev); err != nil || !applied {
			t.Fatalf("Apply %d: applied=%v err=%v", i, applied, err)
		}
	}
	if cur, err := s.CurrentCursor(ctx, "m1"); err != nil || cur != 2 {
		t.Fatalf("cursor = %d err=%v, want 2", cur, err)
	}
}

// itoa2 是整数转字符串的测试助手。
func itoa2(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
