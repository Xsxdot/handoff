package grok_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

// TestReapFallsBackToDeterministicName serve.json 缺失时按确定性命名回收——
// B20 现场正是「运行态丢了但会话名恒为 handoff-<id8>」，兜底完全可用。
func TestReapFallsBackToDeterministicName(t *testing.T) {
	var killed string
	restore := grok.SwapTmuxKillForTest(func(session string) error { killed = session; return nil })
	defer restore()

	a := grok.New(nil)
	if err := a.Reap("abcdef12-3456", t.TempDir()); err != nil { // 空 taskDir，无 serve.json
		t.Fatal(err)
	}
	if killed != "handoff-abcdef12" {
		t.Fatalf("应按确定性命名回收，实际杀了 %q", killed)
	}
}
