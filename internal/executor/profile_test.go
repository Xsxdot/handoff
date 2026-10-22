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

func TestZeroProfileReportIsNotSuccess(t *testing.T) {
	var r executor.ProfileReport
	if r.Prepared || r.Verified || r.EngineOK {
		t.Fatalf("zero report must not look successful: %+v", r)
	}
}
