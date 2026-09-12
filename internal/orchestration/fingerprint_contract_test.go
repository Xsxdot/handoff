package orchestration

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

// TestPermFingerprintForMatchesExecutorCanonical 锁住 manager 私有副本与
// executor.PermFingerprint 金样本同值。本卡 Ticket 0 不改生产接线，实现节点
// 必须把 permFingerprintFor 收成对 canonical 的包装。
func TestPermFingerprintForMatchesExecutorCanonical(t *testing.T) {
	events := []executor.AdapterEvent{
		{
			Text: "bash: echo hello",
			Perm: &executor.PermRequest{Tool: executor.PermToolBash, Command: "echo hello"},
		},
		{
			Text: "edit: files",
			Perm: &executor.PermRequest{Tool: executor.PermToolEdit, Paths: []string{"b.txt", "a.txt"}},
		},
		{Text: "Bash: rm -rf /"},
		{},
	}
	for _, ev := range events {
		got := permFingerprintFor(ev)
		want := executor.PermFingerprint(ev)
		if got != want {
			t.Fatalf("permFingerprintFor drifted: got %q want %q text=%q", got, want, ev.Text)
		}
	}
}
