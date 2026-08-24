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
	if err != nil || strings.Contains(out, "feature-impl") || strings.Contains(out, "review-generic") {
		t.Fatalf("新账本不应注入出厂模板: %v %q", err, out)
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
	out, _, err = runLedgerCLI(t, dir, "template", "list")
	if err != nil || !strings.Contains(out, "codex-impl") || strings.Contains(out, "feature-impl") {
		t.Fatalf("list 应只显示账本真实模板: %v %q", err, out)
	}
	out, _, err = runLedgerCLI(t, dir, "template", "show", "codex-impl")
	if err != nil || !strings.Contains(out, "codex") {
		t.Fatalf("show 应读取真实模板: %v %q", err, out)
	}
}
