package executor_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/keysclient"
)

type fakeCoordinator struct{}

func (fakeCoordinator) Launch(context.Context, executor.CoordSessionSpec, string) (executor.CoordTurnResult, error) {
	return executor.CoordTurnResult{}, nil
}
func (fakeCoordinator) Resume(context.Context, executor.CoordSessionRef, string) (executor.CoordTurnResult, error) {
	return executor.CoordTurnResult{}, nil
}
func (fakeCoordinator) CancelTurn(context.Context, executor.CoordSessionRef) error { return nil }

type fakeAttacher struct{}

func (fakeAttacher) AttachInfo(context.Context, executor.CoordSessionRef) (executor.CoordAttachInfo, error) {
	return executor.CoordAttachInfo{}, nil
}

var (
	_ executor.Coordinator   = fakeCoordinator{}
	_ executor.CoordAttacher = fakeAttacher{}
)

func TestRequireLaunchPrompt(t *testing.T) {
	if err := executor.RequireLaunchPrompt("hello"); err != nil {
		t.Fatalf("non-empty: %v", err)
	}
	err := executor.RequireLaunchPrompt("")
	if !errors.Is(err, executor.ErrEmptyLaunchPrompt) {
		t.Fatalf("empty prompt err = %v", err)
	}
	err = executor.RequireLaunchPrompt("  \n")
	if !errors.Is(err, executor.ErrEmptyLaunchPrompt) {
		t.Fatalf("blank prompt err = %v", err)
	}
}

func TestRequireResumeRef(t *testing.T) {
	if err := executor.RequireResumeRef(executor.CoordSessionRef{SessionID: "ses_1"}); err != nil {
		t.Fatalf("with id: %v", err)
	}
	err := executor.RequireResumeRef(executor.CoordSessionRef{})
	if !errors.Is(err, executor.ErrEmptyResumeSession) {
		t.Fatalf("empty session err = %v", err)
	}
}

func TestCoordTypesMirrorKeysclientFields(t *testing.T) {
	assertSameFields(t, reflect.TypeOf(executor.CoordSessionSpec{}), reflect.TypeOf(keysclient.SessionSpec{}))
	assertSameFields(t, reflect.TypeOf(executor.CoordSessionRef{}), reflect.TypeOf(keysclient.SessionRef{}))
	assertSameFields(t, reflect.TypeOf(executor.CoordTurnResult{}), reflect.TypeOf(keysclient.TurnResult{}))
	assertSameFields(t, reflect.TypeOf(executor.CoordAttachInfo{}), reflect.TypeOf(keysclient.AttachInfo{}))
}

func assertSameFields(t *testing.T, got, want reflect.Type) {
	t.Helper()
	if got.NumField() != want.NumField() {
		t.Fatalf("%s fields = %d, %s fields = %d", got.Name(), got.NumField(), want.Name(), want.NumField())
	}
	for i := 0; i < want.NumField(); i++ {
		gf, wf := got.Field(i), want.Field(i)
		if gf.Name != wf.Name || gf.Type != wf.Type || string(gf.Tag) != string(wf.Tag) {
			t.Fatalf("%s field %d = %s %s %q, want %s %s %q",
				got.Name(), i, gf.Name, gf.Type, gf.Tag, wf.Name, wf.Type, wf.Tag)
		}
	}
}
