package executor_test

import (
	"context"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

type fakeOneShot struct{}

func (fakeOneShot) Invoke(context.Context, executor.OneShotReq) (executor.OneShotReply, error) {
	return executor.OneShotReply{}, nil
}

var _ executor.OneShot = fakeOneShot{}

func TestOneShotLiterals(t *testing.T) {
	if executor.EffortLow != "low" {
		t.Fatalf("EffortLow = %q", executor.EffortLow)
	}
	if executor.OneShotOK != "ok" {
		t.Fatalf("OneShotOK = %q", executor.OneShotOK)
	}
	if executor.OneShotFailed != "failed" {
		t.Fatalf("OneShotFailed = %q", executor.OneShotFailed)
	}
	if executor.OneShotCanceled != "canceled" {
		t.Fatalf("OneShotCanceled = %q", executor.OneShotCanceled)
	}
}

func TestGrokBaselineOneShotHasNoDefaultEffort(t *testing.T) {
	rep, err := executor.BaselineReport(executor.HarnessGrok)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Caps {
		if c.Name != executor.CapOneShot {
			continue
		}
		for _, lim := range c.NativeLimits {
			if lim == "effort=low" || lim == executor.EffortLow {
				t.Fatalf("grok oneshot native limits must not encode default effort: %#v", c.NativeLimits)
			}
		}
	}
}

func TestOneShotArgsStillEncodesGrokLowUntilMigrated(t *testing.T) {
	// 现状债务：OneShotArgs 仍把 low effort 写进 grok 默认。本卡冻结「策略在调用方」
	// 之后，实现节点必须让这条路径退役；本测试锁住迁移前的旧函数未被本节点改掉。
	got, err := executor.OneShotArgs(executor.HarnessGrok, "m", "p")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"grok", "--effort", "low", "-m", "m", "-p", "p"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}
