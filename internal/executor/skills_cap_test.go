package executor_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/skill"
)

type fakeSkills struct{}

func (fakeSkills) Inspect(context.Context, string) ([]executor.SkillSite, error) {
	return nil, nil
}
func (fakeSkills) Install(context.Context, executor.SkillInstallReq) ([]executor.SkillSite, error) {
	return nil, nil
}
func (fakeSkills) Remove(context.Context, executor.SkillRemoveReq) error { return nil }

var _ executor.Skills = fakeSkills{}

func TestSkillStateLiteralsMatchInstallPackage(t *testing.T) {
	if executor.SkillInstalled != skill.StateInstalled {
		t.Fatalf("installed: %q vs %q", executor.SkillInstalled, skill.StateInstalled)
	}
	if executor.SkillSkipped != skill.StateSkipped {
		t.Fatalf("skipped: %q vs %q", executor.SkillSkipped, skill.StateSkipped)
	}
	if executor.SkillInSync != skill.StateInSync {
		t.Fatalf("in_sync: %q vs %q", executor.SkillInSync, skill.StateInSync)
	}
	if executor.SkillStale != skill.StateStale {
		t.Fatalf("stale: %q vs %q", executor.SkillStale, skill.StateStale)
	}
	if executor.SkillMissing != skill.StateMissing {
		t.Fatalf("missing: %q vs %q", executor.SkillMissing, skill.StateMissing)
	}
	if executor.SkillDefaultName != "handoff" {
		t.Fatalf("default name = %q", executor.SkillDefaultName)
	}
}

func TestGuardRemoveRequiresAuthorization(t *testing.T) {
	err := executor.GuardRemove(executor.SkillRemoveReq{Home: "/tmp", Name: "handoff"})
	if !errors.Is(err, executor.ErrSkillRemoveUnauthorized) {
		t.Fatalf("unauthorized err = %v", err)
	}
	if err := executor.GuardRemove(executor.SkillRemoveReq{Authorized: true}); err != nil {
		t.Fatalf("authorized: %v", err)
	}
}

func TestSkillDirNameDefault(t *testing.T) {
	if got := executor.SkillDirName(""); got != executor.SkillDefaultName {
		t.Fatalf("empty = %q", got)
	}
	if got := executor.SkillDirName("custom"); got != "custom" {
		t.Fatalf("custom = %q", got)
	}
}
