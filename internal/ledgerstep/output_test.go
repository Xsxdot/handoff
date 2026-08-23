// 本文件覆盖节点产出路径模板和 git diff 路径投影的纯函数边界。
// 测试不访问网络或文件系统，只验证稳定的输入到输出映射。
package ledgerstep

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestRenderOutputPath(t *testing.T) {
	now := time.Date(2026, 8, 23, 14, 5, 6, 0, time.FixedZone("CST", 8*60*60))
	got := RenderOutputPath(
		"docs/{{DATE}}/{{CARD_LOWER}}-{{CARD}}-{{NODE}}.md",
		ledger.Card{ID: "B201"},
		ledger.NodeDef{Name: "plan"},
		now,
	)
	want := "docs/2026-08-23/b201-B201-plan.md"
	if got != want {
		t.Fatalf("RenderOutputPath = %q, want %q", got, want)
	}
}

func TestRenderOutputPathLeavesUnknownPlaceholderLiteral(t *testing.T) {
	got := RenderOutputPath("docs/{{UNKNOWN}}.md", ledger.Card{ID: "B201"}, ledger.NodeDef{Name: "plan"}, time.Time{})
	if got != "docs/{{UNKNOWN}}.md" {
		t.Fatalf("unknown placeholder changed: %q", got)
	}
}

func TestChangedPaths(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/docs/old.md b/docs/new.md",
		"similarity index 90%",
		"rename from docs/old.md",
		"rename to docs/new.md",
		"diff --git a/docs/modified.md b/docs/modified.md",
		"diff --git a/docs/added.md b/docs/added.md",
		"new file mode 100644",
		"diff --git a/docs/deleted.md b/docs/deleted.md",
		"deleted file mode 100644",
		"commit abc1234",
		"Author: ignored",
	}, "\n")
	got := ChangedPaths(diff)
	for _, want := range []string{"docs/old.md", "docs/new.md", "docs/modified.md", "docs/added.md", "docs/deleted.md"} {
		found := false
		for _, path := range got {
			if path == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("ChangedPaths missing %q: %v", want, got)
		}
	}
	for _, path := range got {
		if strings.Contains(path, "commit") || strings.Contains(path, "Author") {
			t.Fatalf("metadata leaked as path: %v", got)
		}
	}
}

func TestChangedPathsIgnoresHunkFileHeaderLikeContent(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/docs/ledger.md b/docs/ledger.md",
		"index 1111111..2222222 100644",
		"--- a/docs/ledger.md",
		"+++ b/docs/ledger.md",
		"@@ -1,2 +1,4 @@",
		" context",
		"+++ b/docs/superpowers/plans/b201-plan.md",
		"--- a/docs/superpowers/plans/not-the-file.md",
		" context",
	}, "\n")
	got := ChangedPaths(diff)
	want := []string{"docs/ledger.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("hunk file-header-like content leaked into paths: got %v, want %v", got, want)
	}
}

func TestChangedPathsAcceptsFileHeaderRecords(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/docs/changed.md b/docs/changed.md",
		"--- a/docs/changed.md",
		"+++ b/docs/changed.md",
		"@@ -1 +1 @@",
		"-before",
		"+after",
	}, "\n")
	got := ChangedPaths(diff)
	want := []string{"docs/changed.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("file-header records parsed incorrectly: got %v, want %v", got, want)
	}
}

func TestChangedPathsHandlesNewFileAndRenameHeaders(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/docs/added.md b/docs/added.md",
		"new file mode 100644",
		"--- /dev/null",
		"+++ b/docs/added.md",
		"diff --git a/docs/old.md b/docs/renamed.md",
		"similarity index 95%",
		"rename from docs/old.md",
		"rename to docs/renamed.md",
	}, "\n")
	got := ChangedPaths(diff)
	want := []string{"docs/added.md", "docs/old.md", "docs/renamed.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("new-file or rename headers parsed incorrectly: got %v, want %v", got, want)
	}
}

func TestChangedPathsText(t *testing.T) {
	if got := changedPathsText(nil); got != "（无）" {
		t.Fatalf("empty changed paths = %q", got)
	}
	got := changedPathsText([]string{"docs/a.md", "docs/b.md"})
	if got != "docs/a.md\ndocs/b.md" {
		t.Fatalf("changed paths text = %q", got)
	}
}
