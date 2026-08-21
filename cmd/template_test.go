package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateListShowPut(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "template", "list")
	if err != nil || !strings.Contains(out, "feature-impl") || !strings.Contains(out, "review-generic") {
		t.Fatalf("list: %v %q", err, out)
	}
	out, _, err = runLedgerCLI(t, dir, "template", "show", "review-generic")
	if err != nil || !strings.Contains(out, "handoff-verdict") {
		t.Fatalf("show 应含输出契约: %v %q", err, out)
	}
	p := filepath.Join(dir, "tpl.json")
	if err := os.WriteFile(p, []byte(`{"executor":"codex","purpose":"implement","branch_prefix":"cards",
		"prompt":"x {{CARD}}","discipline":"implement"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err = runLedgerCLI(t, dir, "template", "put", "codex-impl", "--file", p)
	if err != nil || !strings.Contains(out, `"version":1`) {
		t.Fatalf("put: %v %q", err, out)
	}
}
