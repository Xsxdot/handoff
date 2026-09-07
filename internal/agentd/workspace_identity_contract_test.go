package agentd

import (
	"context"
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/workspace"
)

func TestID8MatchesWorkspaceCanonical(t *testing.T) {
	samples := []string{
		"abcdefgh-rest",
		"abcdefgh",
		"short",
		"137a7dc9-df89-4c1c-891e-ebe106c68b37",
	}
	for _, s := range samples {
		if got, want := id8(s), workspace.ID8(s); got != want {
			t.Fatalf("id8(%q) = %q, workspace.ID8 = %q", s, got, want)
		}
		if got, want := taskBranch(s), workspace.TaskBranch(s); got != want {
			t.Fatalf("taskBranch(%q) = %q, workspace.TaskBranch = %q", s, got, want)
		}
	}
}


func TestResolveBaselineRejectsUppercaseSHA(t *testing.T) {
	repo := initTestRepo(t)
	_, err := ResolveBaseline(context.Background(), repo, "482AAB1F9E12A3B4C5D6E7F8A9B0C1D2E3F4A5B6")
	if !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("大写 hex 必须失败且错误链含 ErrBadWorkspaceReq，实得 %v", err)
	}
}

