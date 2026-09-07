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

