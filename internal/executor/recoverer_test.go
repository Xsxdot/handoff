package executor_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

type fakeRecoverer struct{}

func (fakeRecoverer) Resume(executor.ResumeReq) (executor.ResumeOutcome, error) {
	return executor.ResumeOutcome{}, nil
}

var _ executor.Recoverer = fakeRecoverer{}

func TestRecovererIsNotAdapterMethodSet(t *testing.T) {
	var ad executor.Adapter
	_, ok := any(ad).(executor.Recoverer)
	if ok {
		t.Fatal("nil Adapter must not satisfy Recoverer")
	}
}
