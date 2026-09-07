package agentd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
)

func TestCoordinatorRunnerUnsupportedDoesNotCallOpenCode(t *testing.T) {
	var runs int
	h := hostapi.New()
	_ = h
	reg := executor.NewRegistry(executor.StaticProvider{
		HarnessName: executor.HarnessClaude,
		Rep:         mustReport(t, executor.HarnessClaude),
	})
	r := coordinatorRunner{
		coord: countingCoord{fn: func() { runs++ }},
		reg:   reg,
	}
	_, err := r.Launch(keysclient.SessionSpec{CLI: "claude", HomeDir: t.TempDir()}, "hi")
	if !errors.Is(err, executor.ErrCapabilityUnsupported) {
		t.Fatalf("err = %v", err)
	}
	if runs != 0 {
		t.Fatalf("禁止兜底 OpenCode，coord 调用次数=%d", runs)
	}
}

func TestCoordinatorRunnerUnsupportedGrokCodexAGY(t *testing.T) {
	for _, name := range []string{"grok", "codex", "agy"} {
		reg := executor.NewRegistry(executor.StaticProvider{
			HarnessName: name,
			Rep:         mustReport(t, name),
		})
		r := coordinatorRunner{coord: countingCoord{}, reg: reg}
		_, err := r.Launch(keysclient.SessionSpec{CLI: name, HomeDir: t.TempDir()}, "hi")
		if !errors.Is(err, executor.ErrCapabilityUnsupported) {
			t.Fatalf("%s err = %v", name, err)
		}
		if !strings.Contains(err.Error(), "禁止静默兜底到另一家") {
			t.Fatalf("%s 错误文本: %v", name, err)
		}
	}
}

func mustReport(t *testing.T, name string) executor.CapabilityReport {
	t.Helper()
	rep, err := executor.BaselineReport(name)
	if err != nil {
		t.Fatal(err)
	}
	return rep
}

type countingCoord struct{ fn func() }

func (c countingCoord) Launch(context.Context, executor.CoordSessionSpec, string) (executor.CoordTurnResult, error) {
	if c.fn != nil {
		c.fn()
	}
	return executor.CoordTurnResult{}, nil
}
func (c countingCoord) Resume(context.Context, executor.CoordSessionRef, string) (executor.CoordTurnResult, error) {
	return executor.CoordTurnResult{}, nil
}
func (c countingCoord) CancelTurn(context.Context, executor.CoordSessionRef) error { return nil }
