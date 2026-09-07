package workspace_test

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/workspace"
)

func TestID8AndTaskBranchGoldenVectors(t *testing.T) {
	tests := []struct {
		name   string
		taskID string
		id8    string
		branch string
	}{
		{name: "uuid prefix", taskID: "abcdefgh-rest", id8: "abcdefgh", branch: "handoff/abcdefgh"},
		{name: "exactly 8", taskID: "abcdefgh", id8: "abcdefgh", branch: "handoff/abcdefgh"},
		{name: "shorter than 8", taskID: "short", id8: "short", branch: "handoff/short"},
		{name: "full uuid", taskID: "137a7dc9-df89-4c1c-891e-ebe106c68b37", id8: "137a7dc9", branch: "handoff/137a7dc9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := workspace.ID8(tt.taskID); got != tt.id8 {
				t.Fatalf("ID8(%q) = %q, want %q", tt.taskID, got, tt.id8)
			}
			if got := workspace.TaskBranch(tt.taskID); got != tt.branch {
				t.Fatalf("TaskBranch(%q) = %q, want %q", tt.taskID, got, tt.branch)
			}
		})
	}
}

func TestTaskBranchPrefixLiteral(t *testing.T) {
	if workspace.TaskBranchPrefix != "handoff/" {
		t.Fatalf("TaskBranchPrefix = %q, want handoff/", workspace.TaskBranchPrefix)
	}
}

func TestIsCommitSHAGoldenVectors(t *testing.T) {
	const good = "482aab1f9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b6"
	if !workspace.IsCommitSHA(good) {
		t.Fatalf("40 位小写 hex 应通过: %q", good)
	}
	bads := []string{
		"",
		"482aab1",
		"482AAB1F9E12A3B4C5D6E7F8A9B0C1D2E3F4A5B6",
		"482aab1f9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b6g",
		"482aab1f9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b",
		"origin/main",
	}
	for _, s := range bads {
		if workspace.IsCommitSHA(s) {
			t.Fatalf("IsCommitSHA(%q) 应为 false", s)
		}
	}
}

func TestNewResultRefRejectsBadCommit(t *testing.T) {
	if _, err := workspace.NewResultRef("/repo", "handoff/ab", "not-a-sha", "/wt"); err == nil {
		t.Fatal("非法 commit 必须拒绝")
	}
	ref, err := workspace.NewResultRef("/repo", "handoff/ab", "482aab1f9e12a3b4c5d6e7f8a9b0c1d2e3f4a5b6", "/wt")
	if err != nil {
		t.Fatal(err)
	}
	if ref.Repo != "/repo" || ref.Branch != "handoff/ab" || ref.Path != "/wt" {
		t.Fatalf("ResultRef 字段丢失: %+v", ref)
	}
	empty, err := workspace.NewResultRef("/repo", "feat/x", "", "")
	if err != nil {
		t.Fatalf("空 commit 表示未知，应允许: %v", err)
	}
	if empty.Commit != "" {
		t.Fatalf("空 commit 应保持空串: %+v", empty)
	}
}
