package executor_test

import (
	"context"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

type fakeProfile struct{}

func (fakeProfile) Inspect(context.Context, executor.ProfileReq) (executor.ProfileReport, error) {
	return executor.ProfileReport{}, nil
}
func (fakeProfile) Prepare(context.Context, executor.ProfileReq) (executor.ProfileReport, error) {
	return executor.ProfileReport{}, nil
}
func (fakeProfile) Verify(context.Context, executor.ProfileReq) (executor.ProfileReport, error) {
	return executor.ProfileReport{}, nil
}

var _ executor.Profile = fakeProfile{}

func TestCredentialPolicyLiterals(t *testing.T) {
	if executor.CredentialStandalone != "standalone" {
		t.Fatalf("standalone = %q", executor.CredentialStandalone)
	}
	if executor.CredentialMainHomeSync != "main_home_sync" {
		t.Fatalf("main_home_sync = %q", executor.CredentialMainHomeSync)
	}
}

func TestZeroProfileReportIsNotSuccess(t *testing.T) {
	var r executor.ProfileReport
	if r.Prepared || r.Verified || r.EngineOK {
		t.Fatalf("zero report must not look successful: %+v", r)
	}
}
