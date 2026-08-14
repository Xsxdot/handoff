package codex

import (
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
