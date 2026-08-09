package opencode

import "testing"

// swapTmuxKill 替换包级 tmux kill 执行点，返回恢复函数（与包内既有测试的
// 全局替换手法一致）。
func swapTmuxKill(fn func(session string) error) func() {
	old := tmuxKill
	tmuxKill = fn
	return func() { tmuxKill = old }
}

// TestReapFallsBackToDeterministicName serve.json 缺失时按确定性命名回收——
// B20 现场正是「运行态丢了但会话名恒为 handoff-<id8>」，兜底完全可用。
func TestReapFallsBackToDeterministicName(t *testing.T) {
	var killed string
	restore := swapTmuxKill(func(session string) error { killed = session; return nil })
	defer restore()

	a := New(quietLogger())
	if err := a.Reap("abcdef12-3456", t.TempDir()); err != nil { // 空 taskDir，无 serve.json
		t.Fatal(err)
	}
	if killed != "handoff-abcdef12" {
		t.Fatalf("应按确定性命名回收，实际杀了 %q", killed)
	}
}
