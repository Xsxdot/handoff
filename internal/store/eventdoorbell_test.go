package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestEventDoorbell(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "doorbell.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	defer st.Close()

	taskAWake, cancelA := st.SubscribeEventDoorbell("task-a")
	defer cancelA()
	_, cancelB := st.SubscribeEventDoorbell("task-b")
	defer cancelB()
	taskBWake, cancelB2 := st.SubscribeEventDoorbell("task-b")
	defer cancelB2()

	if _, err := st.AppendEvent("task-b", proto.EventTypeProgress, map[string]string{"text": "other"}); err != nil {
		t.Fatalf("追加 task-b progress: %v", err)
	}
	select {
	case <-taskAWake:
		t.Fatal("task-a 不应收到 task-b 的门铃")
	case <-time.After(100 * time.Millisecond):
	}
	select {
	case <-taskBWake:
	case <-time.After(time.Second):
		t.Fatal("task-b 应在一秒内收到自己的门铃")
	}

	if _, err := st.AppendEvent("task-a", proto.EventTypeProgress, map[string]string{"text": "progress"}); err != nil {
		t.Fatalf("追加 task-a progress: %v", err)
	}
	if _, err := st.AppendEvent("task-a", proto.EventTypePermissionRequest,
		map[string]string{"permission_id": "perm-1"}); err != nil {
		t.Fatalf("追加 task-a permission_request: %v", err)
	}
	select {
	case <-taskAWake:
	case <-time.After(time.Second):
		t.Fatal("task-a 应收到事件门铃")
	}
	select {
	case <-taskAWake:
		t.Fatal("未消费第一条通知前第二次追加不应生成第二个通知")
	case <-time.After(100 * time.Millisecond):
	}

	events, err := st.EventsFromAsc("task-a", 0, 10)
	if err != nil {
		t.Fatalf("EventsFromAsc: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("task-a 真实事件数 = %d, want 2", len(events))
	}
	if events[0].Seq >= events[1].Seq {
		t.Fatalf("task-a 事件未按 seq 升序返回: %d, %d", events[0].Seq, events[1].Seq)
	}
	if events[0].Type != proto.EventTypeProgress || events[1].Type != proto.EventTypePermissionRequest {
		t.Fatalf("task-a 事件类型 = %q, %q", events[0].Type, events[1].Type)
	}

	// cancel 必须幂等，且取消后不应再收到新的通知。
	cancelA()
	cancelA()
	if _, err := st.AppendEvent("task-a", proto.EventTypeProgress, map[string]string{"text": "after-cancel"}); err != nil {
		t.Fatalf("取消后追加 task-a progress: %v", err)
	}
	select {
	case <-taskAWake:
		t.Fatal("取消后不应收到 task-a 门铃")
	case <-time.After(100 * time.Millisecond):
	}
}
