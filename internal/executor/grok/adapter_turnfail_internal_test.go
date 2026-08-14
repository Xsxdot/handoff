// adapter_turnfail_internal_test.go —— 回合失败与执行终结必须走两个出口的回归测试。
//
// 职责：
//   - 断言回合失败（emitTurnFailed）只投 result{OK:false}、**不关事件通道**，
//     续接回合的事件仍能送达
//   - 断言执行终结（emitFatal）投 result{OK:false} 并**关闭事件通道**
//   - 断言 fatal 的关闭语义保持幂等（先到者生效）
//   - 断言 turn → fatal 的先后顺序不破坏 fatal 关通道的一次性
//
// 边界：
//   - 不起进程、不连网络，只验两个出口对事件通道的语义
package grok

import (
	"context"
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

// TestTurnFailureKeepsEventChannelOpen 回合失败 ≠ executor 完了，事件通道必须
// 保持打开，续接回合的事件才能送出去。
func TestTurnFailureKeepsEventChannelOpen(t *testing.T) {
	a, r := NewAdapterWithRunForTest("t1")

	a.emitTurnFailed(r, "回合非正常收尾 stopReason=cancelled")

	ev := <-r.evCh
	if ev.Result == nil || ev.Result.OK {
		t.Fatalf("应投出 result{OK:false}，实际 %+v", ev)
	}
	r.emitMu.Lock()
	closed := r.evClosed
	r.emitMu.Unlock()
	if closed {
		t.Fatal("回合失败不该关闭事件通道")
	}
	// 通道仍可用：续接回合的事件必须能送出去
	if !a.emit(r, executor.AdapterEvent{Type: "progress", Text: "续接回合的第一条"}) {
		t.Fatal("回合失败后 emit 应仍然成功——这正是 B92 被吞掉的那条路")
	}
	if next := <-r.evCh; next.Text != "续接回合的第一条" {
		t.Fatalf("续接事件没送达，实际 %+v", next)
	}
}

// TestFatalFailureClosesEventChannel 连接断了、进程死了，这条运行态真的不可用
// 了，必须关通道让 manager 的 mediate 循环退出走对账。
func TestFatalFailureClosesEventChannel(t *testing.T) {
	a, r := NewAdapterWithRunForTest("t1")

	a.emitFatal(r, "ACP 连接断开: 测试")

	ev := <-r.evCh
	if ev.Result == nil || ev.Result.OK {
		t.Fatalf("应投出 result{OK:false}，实际 %+v", ev)
	}
	if _, ok := <-r.evCh; ok {
		t.Fatal("fatal 之后事件通道应已关闭")
	}
}

// TestFatalIsIdempotent 断开处置、看门狗判死两条 fatal 路径可能同时到达，
// closeEvents 的幂等保证只有先到者生效。
func TestFatalIsIdempotent(t *testing.T) {
	a, r := NewAdapterWithRunForTest("t1")
	a.emitFatal(r, "第一次")
	a.emitFatal(r, "第二次") // 不许 panic（send on closed channel）
	if ev := <-r.evCh; ev.Result == nil || ev.Result.FailReason != "第一次" {
		t.Fatalf("只有先到者生效，实际 %+v", ev)
	}
	if _, ok := <-r.evCh; ok {
		t.Fatal("第二次不该再投出事件")
	}
}

// TestTurnFailureThenFatalStillCloses 回合失败后 serve 才真的死掉，是完全可能的
// 顺序。此时 fatal 仍须关通道。
func TestTurnFailureThenFatalStillCloses(t *testing.T) {
	a, r := NewAdapterWithRunForTest("t1")
	a.emitTurnFailed(r, "回合失败")
	<-r.evCh
	a.emitFatal(r, "serve 死了")
	<-r.evCh
	if _, ok := <-r.evCh; ok {
		t.Fatal("fatal 之后通道应关闭")
	}
}

func TestSendRefusesOnClosedChannel(t *testing.T) {
	// why：这是本次修复的安全网。即便将来又冒出某条我们没想到的关通道路径，
	// 这道守卫也会把「静默吞掉一整个回合」变成「continue 当场报错、manager 走
	// 四级恢复阶梯」。B92 花了 2 小时 + 一次人工排查才被发现，代价全部来自静默
	a, r := NewAdapterWithRunForTest("t1")
	a.emitFatal(r, "连接断了")

	err := a.Send(context.Background(), r.taskID, "接着干")

	if err == nil {
		t.Fatal("通道已关闭时 Send 必须报错，不能静默开新回合")
	}
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("必须是 ErrTaskNotRunning（manager 的四级恢复阶梯以它为触发条件），实际 %v", err)
	}
}
