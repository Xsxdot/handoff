package store

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

func TestEventHookFiresAfterInsert(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	defer st.Close()

	var got []proto.Event
	st.SetEventHook(func(e proto.Event) { got = append(got, e) })

	evt, err := st.AppendEvent("task-1", proto.EventTypeProgress, map[string]string{"text": "跑起来了"})
	if err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("钩子应被调用 1 次，实得 %d", len(got))
	}
	if got[0].Seq != evt.Seq || got[0].Type != proto.EventTypeProgress {
		t.Errorf("钩子收到的事件应与返回值一致：%+v vs %+v", got[0], evt)
	}
}

// 钩子 panic 不能把一次事件落库拖垮：事件已经进库了，
// 让一个可见性副作用去回滚它是本末倒置。
func TestEventHookPanicDoesNotBreakAppend(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	defer st.Close()

	st.SetEventHook(func(proto.Event) { panic("钩子炸了") })

	evt, err := st.AppendEvent("task-1", proto.EventTypeProgress, map[string]string{"text": "x"})
	if err != nil {
		t.Fatalf("钩子 panic 不该让 AppendEvent 失败：%v", err)
	}
	if evt.Seq == 0 {
		t.Error("事件应当已落库并带上 seq")
	}
}

// 未注册钩子时一切照旧。
func TestAppendEventWithoutHook(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	defer st.Close()
	if _, err := st.AppendEvent("task-1", proto.EventTypeProgress, map[string]string{}); err != nil {
		t.Fatalf("无钩子时 AppendEvent 应正常：%v", err)
	}
}

// TestEventHookCarriesPermissionAutoAllow 验证新审计类型沿既有 hook 观察链透传，
// 不需要为它新增 Store 接口或特殊分支。
func TestEventHookCarriesPermissionAutoAllow(t *testing.T) {
	st, err := Open(filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatalf("打开库: %v", err)
	}
	defer st.Close()

	var got proto.Event
	st.SetEventHook(func(e proto.Event) { got = e })
	if _, err := st.AppendEvent("task-1", proto.EventTypePermissionAutoAllow,
		map[string]string{"permission_id": "perm-1", "rule": "safe-command"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	if got.Type != proto.EventTypePermissionAutoAllow {
		t.Fatalf("hook event type = %q, want %q", got.Type, proto.EventTypePermissionAutoAllow)
	}
	var payload map[string]string
	if err := json.Unmarshal(got.Payload, &payload); err != nil {
		t.Fatalf("decode hook payload: %v", err)
	}
	if payload["permission_id"] != "perm-1" || payload["rule"] != "safe-command" {
		t.Fatalf("hook payload = %#v, want permission_id/rule", payload)
	}
}
