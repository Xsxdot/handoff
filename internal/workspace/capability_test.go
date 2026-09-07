package workspace_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/workspace"
)

type fakeCapability struct{}

func (fakeCapability) EnsureRepoUsable(context.Context, string) error { return nil }
func (fakeCapability) ResolveBaseline(context.Context, string, string) (workspace.Baseline, error) {
	return workspace.Baseline{}, nil
}
func (fakeCapability) Prepare(context.Context, workspace.PrepareReq) (workspace.Prepared, error) {
	return workspace.Prepared{}, nil
}
func (fakeCapability) CreateManual(context.Context, string, string, workspace.ManualReq) (workspace.ManualTree, error) {
	return workspace.ManualTree{}, nil
}
func (fakeCapability) DiffRange(context.Context, string, string, string) (string, error) {
	return "", nil
}
func (fakeCapability) ReadFile(context.Context, string, string) (workspace.FileContent, error) {
	return workspace.FileContent{}, nil
}
func (fakeCapability) RecycleManaged(context.Context, string, string) error { return nil }

var _ workspace.Capability = fakeCapability{}

func TestCapabilityHasNoPush(t *testing.T) {
	var c workspace.Capability
	iface := reflect.TypeOf(&c).Elem()
	for i := 0; i < iface.NumMethod(); i++ {
		if iface.Method(i).Name == "Push" {
			t.Fatal("Capability 不得包含 Push")
		}
	}
}

func TestPrepareReqHasNoCardIDs(t *testing.T) {
	if workspace.HasLedgerAttachField(workspace.PrepareReq{}) {
		t.Fatal("PrepareReq 不得包含 CardIDs/CardResults")
	}
	if workspace.HasLedgerAttachField(workspace.ManualReq{}) {
		t.Fatal("ManualReq 不得包含 CardIDs/CardResults")
	}
	if workspace.HasLedgerAttachField(workspace.ManualTree{}) {
		t.Fatal("ManualTree 不得包含 CardIDs/CardResults")
	}
}

func TestManualModeLiterals(t *testing.T) {
	if workspace.ManualNewBranch != "new_branch" {
		t.Fatalf("ManualNewBranch = %q, want new_branch", workspace.ManualNewBranch)
	}
	if workspace.ManualExistingBranch != "existing_branch" {
		t.Fatalf("ManualExistingBranch = %q, want existing_branch", workspace.ManualExistingBranch)
	}
}

func TestTriggerAndDecisionLiterals(t *testing.T) {
	if workspace.TriggerCompensate != "compensate" {
		t.Fatalf("TriggerCompensate = %q", workspace.TriggerCompensate)
	}
	if workspace.TriggerStop != "stop" {
		t.Fatalf("TriggerStop = %q", workspace.TriggerStop)
	}
	if workspace.TriggerDone != "done" {
		t.Fatalf("TriggerDone = %q", workspace.TriggerDone)
	}
	if workspace.TriggerExplicit != "explicit" {
		t.Fatalf("TriggerExplicit = %q", workspace.TriggerExplicit)
	}
	if workspace.RetainKeep != "keep" {
		t.Fatalf("RetainKeep = %q", workspace.RetainKeep)
	}
	if workspace.RetainRecycle != "recycle" {
		t.Fatalf("RetainRecycle = %q", workspace.RetainRecycle)
	}
}
