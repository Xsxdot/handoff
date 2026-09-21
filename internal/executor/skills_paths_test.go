package executor_test

import (
	"reflect"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/agy"
	"github.com/Xsxdot/handoff/internal/executor/claudecode"
	"github.com/Xsxdot/handoff/internal/executor/codex"
	"github.com/Xsxdot/handoff/internal/executor/grok"
	"github.com/Xsxdot/handoff/internal/executor/opencode"
)

func TestNativeSkillRelDirs(t *testing.T) {
	got := []string{
		claudecode.SkillsRelDir,
		codex.SkillsRelDir,
		opencode.SkillsRelDir,
		grok.SkillsRelDir,
		agy.SkillsRelDir,
	}
	want := []string{
		".claude/skills",
		".codex/skills",
		".config/opencode/skills",
		".grok/skills",
		".gemini/antigravity-cli/skills",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
}

func TestAdaptersImplementSkills(t *testing.T) {
	var _ executor.Skills = claudecode.New(nil)
	var _ executor.Skills = codex.New(nil)
	var _ executor.Skills = opencode.New(nil)
	var _ executor.Skills = grok.New(nil)
	var _ executor.Skills = agy.New(nil)
}
