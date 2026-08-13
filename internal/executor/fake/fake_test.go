// fake adapter 单元测试：Events 的通道契约（P1-11）。
//
// 职责：
//   - 未启动任务调 Events 返回已关闭通道——range 立即结束，不永久阻塞
//   - 已启动任务不受影响：Events 返回真实通道、事件可达、Stop 后关闭
package fake

import (
	"context"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// TestEventsClosedForUnstartedTask 覆盖 P1-11 的 fake 侧：未启动任务调 Events
// 返回**已关闭**通道——range 立即结束，而不是惰性新建的打开通道永久阻塞
// （旧实现对未知任务等价于 nil 通道）。
func TestEventsClosedForUnstartedTask(t *testing.T) {
	f := New(nil)
	select {
	case _, ok := <-f.Events("task-never-started"):
		if ok {
			t.Fatal("未启动任务的 Events 通道应已关闭（契约：通道关闭 = 执行终结）")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未启动任务的 Events 通道未关闭——range 会永久阻塞（P1-11）")
	}
}

// TestEventsLiveAfterStart 覆盖已启动任务不受回退影响：Start 后 Events 返回真实
// 通道、事件可达；Stop 后同一通道关闭（与 opencode adapter 语义一致）。
func TestEventsLiveAfterStart(t *testing.T) {
	f := New([]Step{{Finish: executor.Result{OK: true}}})
	if err := f.Start(context.Background(), executor.StartReq{Task: proto.Task{ID: "t-live"}}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ch := f.Events("t-live")
	select {
	case ev := <-ch:
		if ev.Type != "result" || ev.Result == nil || !ev.Result.OK {
			t.Fatalf("Start 后收到事件 = %+v, want result(OK)", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start 后 Events 通道未产出事件——真实通道被回退破坏")
	}
	if err := f.Stop("t-live"); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("Stop 后事件通道应已关闭")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 后事件通道未关闭")
	}
}
