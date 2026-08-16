package codex

import (
	"context"
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestTurnFailureKeepsEventChannelOpen(t *testing.T) {
	// why：回合失败 ≠ codex app-server 完了。以前 emitFailed 一律 closeEvents，
	// 于是 Send→startTurn 在同一个 runstate 上开新回合，新回合的一切事件在 emit
	// 里被 evClosed 短路静默丢弃，任务卡 running 到 2h 看门狗。这是 grok 上实测
	// 到的 B92，codex 结构相同
	a, r := NewAdapterWithRunForTest("T-B99-1")

	a.emitTurnFailed(r, "回合失败: 测试")

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
	if !a.emit(r, executor.AdapterEvent{Type: "progress", Text: "续接回合的第一条"}) {
		t.Fatal("回合失败后 emit 应仍然成功——这正是被吞掉的那条路")
	}
	if next := <-r.evCh; next.Text != "续接回合的第一条" {
		t.Fatalf("续接事件没送达，实际 %+v", next)
	}
}

func TestFatalFailureClosesEventChannel(t *testing.T) {
	a, r := NewAdapterWithRunForTest("T-B99-2")
	a.emitFatal(r, "codex 连接断开: 测试")
	if ev := <-r.evCh; ev.Result == nil || ev.Result.OK {
		t.Fatalf("应投出 result{OK:false}，实际 %+v", ev)
	}
	if _, ok := <-r.evCh; ok {
		t.Fatal("fatal 之后事件通道应已关闭")
	}
}

func TestFatalIsIdempotent(t *testing.T) {
	// why：断开处置与进程判死两条 fatal 路径可能同时到达。一次性语义原本由
	// closeEvents 承担，拆分后必须确认它仍在 fatal 这一侧完整保留
	a, r := NewAdapterWithRunForTest("T-B99-3")
	a.emitFatal(r, "第一次")
	a.emitFatal(r, "第二次") // 不许 panic（send on closed channel）
	if ev := <-r.evCh; ev.Result == nil || ev.Result.FailReason != "第一次" {
		t.Fatalf("只有先到者生效，实际 %+v", ev)
	}
	if _, ok := <-r.evCh; ok {
		t.Fatal("第二次不该再投出事件")
	}
}

func TestAuthRefreshIsFatal(t *testing.T) {
	// why：登录态失效判 turn 的话，continue 会开一个立刻又失败的新回合，变成
	// 人肉重试循环——而该处既有注释自己写着「登录态失效重试一万次也不会好」。
	// 判 fatal 让运行态作废、错误明确交回给人去 codex login
	a, r := NewAdapterWithRunForTest("T-B99-4")
	a.emitFatal(r, "codex 登录态失效，请在 executor 机重新 `codex login`")
	<-r.evCh
	if _, ok := <-r.evCh; ok {
		t.Fatal("登录态失效属 fatal，通道应关闭")
	}
}

func TestSendRefusesOnClosedChannel(t *testing.T) {
	// why：这是本次修复的安全网。即便将来又冒出某条没想到的关通道路径，这道
	// 守卫也把「静默吞掉一整个回合」变成「continue 当场报错、manager 走四级
	// 恢复阶梯」。B92 在 grok 上花了 2 小时 + 一次人工排查才被发现，代价全部
	// 来自静默
	a, r := NewAdapterWithRunForTest("T-B99-5")
	a.emitFatal(r, "连接断了")

	err := a.Send(context.Background(), r.taskID, "接着干")

	if err == nil {
		t.Fatal("通道已关闭时 Send 必须报错，不能静默开新回合")
	}
	if !errors.Is(err, executor.ErrTaskNotRunning) {
		t.Fatalf("必须是 ErrTaskNotRunning（manager 的四级恢复阶梯以它为触发条件），实际 %v", err)
	}
}
