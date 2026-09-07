package executor_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
)

func TestHarnessNameLiterals(t *testing.T) {
	if executor.HarnessOpenCode != "opencode" {
		t.Fatalf("HarnessOpenCode = %q", executor.HarnessOpenCode)
	}
	if executor.HarnessClaude != "claude" {
		t.Fatalf("HarnessClaude = %q", executor.HarnessClaude)
	}
	if executor.HarnessGrok != "grok" {
		t.Fatalf("HarnessGrok = %q", executor.HarnessGrok)
	}
	if executor.HarnessCodex != "codex" {
		t.Fatalf("HarnessCodex = %q", executor.HarnessCodex)
	}
	if executor.HarnessAGY != "agy" {
		t.Fatalf("HarnessAGY = %q", executor.HarnessAGY)
	}
}

func TestCapabilityNameLiterals(t *testing.T) {
	if executor.CapExecution != "execution" {
		t.Fatalf("CapExecution = %q", executor.CapExecution)
	}
	if executor.CapCoordination != "coordination" {
		t.Fatalf("CapCoordination = %q", executor.CapCoordination)
	}
	if executor.CapOneShot != "oneshot" {
		t.Fatalf("CapOneShot = %q", executor.CapOneShot)
	}
	if executor.CapProfile != "profile" {
		t.Fatalf("CapProfile = %q", executor.CapProfile)
	}
	if executor.CapSkills != "skills" {
		t.Fatalf("CapSkills = %q", executor.CapSkills)
	}
}

func TestUnsupportedErrorGolden(t *testing.T) {
	err := executor.UnsupportedError(executor.HarnessClaude, executor.CapCoordination)
	if !errors.Is(err, executor.ErrCapabilityUnsupported) {
		t.Fatalf("errors.Is = false: %v", err)
	}
	want := "harness 不支持该能力: claude 不支持 coordination（禁止静默兜底到另一家）"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

func TestBaselineReportMatrix(t *testing.T) {
	type expect struct {
		coordination  bool
		oneshotLimits []string
	}
	cases := map[string]expect{
		executor.HarnessOpenCode: {coordination: true},
		executor.HarnessClaude:   {},
		executor.HarnessGrok:     {},
		executor.HarnessAGY:      {},
		executor.HarnessCodex: {oneshotLimits: []string{
			executor.NativeLimitSandboxReadOnly,
			executor.NativeLimitInvokeExec,
			executor.NativeLimitEphemeral,
			executor.NativeLimitSkipGitRepoCheck,
			executor.NativeLimitColorNever,
			executor.NativeLimitIgnoreUserConfig,
		}},
	}
	for _, name := range executor.SupportedHarnesses() {
		rep, err := executor.BaselineReport(name)
		if err != nil {
			t.Fatalf("BaselineReport(%q): %v", name, err)
		}
		if rep.Harness != name {
			t.Fatalf("%s harness = %q", name, rep.Harness)
		}
		if len(rep.Caps) != 5 {
			t.Fatalf("%s caps len = %d, want 5", name, len(rep.Caps))
		}
		want := cases[name]
		must := []executor.CapabilityName{
			executor.CapExecution, executor.CapOneShot, executor.CapProfile, executor.CapSkills,
		}
		for _, cap := range must {
			if !executor.ReportHas(rep, cap) {
				t.Fatalf("%s missing %s", name, cap)
			}
		}
		if got := executor.ReportHas(rep, executor.CapCoordination); got != want.coordination {
			t.Fatalf("%s coordination = %v, want %v", name, got, want.coordination)
		}
		var oneshot executor.CapabilityDecl
		for _, c := range rep.Caps {
			if c.Name == executor.CapOneShot {
				oneshot = c
			}
		}
		if !reflect.DeepEqual(oneshot.NativeLimits, want.oneshotLimits) {
			t.Fatalf("%s oneshot limits = %#v, want %#v", name, oneshot.NativeLimits, want.oneshotLimits)
		}
	}
}

func TestBaselineReportUnknownHarness(t *testing.T) {
	_, err := executor.BaselineReport("gemini")
	if !errors.Is(err, executor.ErrUnknownHarness) {
		t.Fatalf("err = %v, want ErrUnknownHarness", err)
	}
}

func TestReportHasMissingCapIsUnsupported(t *testing.T) {
	rep := executor.CapabilityReport{Harness: "x"}
	if executor.ReportHas(rep, executor.CapExecution) {
		t.Fatal("empty report must not claim execution")
	}
}

func TestRegistryRequireCoordination(t *testing.T) {
	oc, err := executor.BaselineReport(executor.HarnessOpenCode)
	if err != nil {
		t.Fatal(err)
	}
	cl, err := executor.BaselineReport(executor.HarnessClaude)
	if err != nil {
		t.Fatal(err)
	}
	reg := executor.NewRegistry(
		executor.StaticProvider{HarnessName: executor.HarnessOpenCode, Rep: oc},
		executor.StaticProvider{HarnessName: executor.HarnessClaude, Rep: cl},
	)
	if err := reg.Require(executor.HarnessOpenCode, executor.CapCoordination); err != nil {
		t.Fatalf("opencode coordination: %v", err)
	}
	err = reg.Require(executor.HarnessClaude, executor.CapCoordination)
	if !errors.Is(err, executor.ErrCapabilityUnsupported) {
		t.Fatalf("claude coordination err = %v", err)
	}
	err = reg.Require("gemini", executor.CapExecution)
	if !errors.Is(err, executor.ErrUnknownHarness) {
		t.Fatalf("unknown harness err = %v", err)
	}
}

func TestCodexNativeLimitLiterals(t *testing.T) {
	if executor.NativeLimitSandboxReadOnly != "sandbox=read-only" {
		t.Fatalf("sandbox = %q", executor.NativeLimitSandboxReadOnly)
	}
	if executor.NativeLimitInvokeExec != "invoke=exec" {
		t.Fatalf("invoke = %q", executor.NativeLimitInvokeExec)
	}
}

var _ executor.Provider = executor.StaticProvider{}

func TestCLIOnPathIsNotFiveCapabilities(t *testing.T) {
	// 缝：RequireCapability / ReportHas。构造「名字叫 claude、PATH 可忽略、报告仍不支持 coordination」。
	rep, err := executor.BaselineReport(executor.HarnessClaude)
	if err != nil {
		t.Fatal(err)
	}
	if executor.ReportHas(rep, executor.CapCoordination) {
		t.Fatal("冻结 46：CLI 在 PATH 不得被解释成 coordination=true")
	}
	if err := executor.RequireCapability(rep, executor.CapCoordination); !errors.Is(err, executor.ErrCapabilityUnsupported) {
		t.Fatalf("err = %v", err)
	}
}

type profileProbe struct{}

func (profileProbe) Inspect(context.Context, executor.ProfileReq) (executor.ProfileReport, error) {
	return executor.ProfileReport{}, nil
}
func (profileProbe) Prepare(context.Context, executor.ProfileReq) (executor.ProfileReport, error) {
	return executor.ProfileReport{}, nil
}
func (profileProbe) Verify(context.Context, executor.ProfileReq) (executor.ProfileReport, error) {
	return executor.ProfileReport{}, nil
}

type profileBundle struct{ executor.StaticProvider }

func (profileBundle) Profile() executor.Profile { return profileProbe{} }

func TestProfileFromProviderUsesAdapterBundle(t *testing.T) {
	profile, ok := executor.ProfileFromProvider(profileBundle{StaticProvider: executor.StaticProvider{
		HarnessName: executor.HarnessOpenCode,
	}})
	if !ok || profile == nil {
		t.Fatalf("Adapter Bundle 的 Profile 必须可取得，ok=%v profile=%T", ok, profile)
	}

	production, ok := executor.ProfileFromProvider(opencode.New(nil))
	if !ok || production == nil {
		t.Fatalf("生产 Adapter Bundle 的 Profile 必须可取得，ok=%v profile=%T", ok, production)
	}
}
