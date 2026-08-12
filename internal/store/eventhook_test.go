package store

import (
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/proto"
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
