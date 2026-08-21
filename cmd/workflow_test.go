package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowListShowPut(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "workflow", "list")
	if err != nil || !strings.Contains(out, "feature") {
		t.Fatalf("list: %v %q", err, out)
	}
	out, _, err = runLedgerCLI(t, dir, "workflow", "show", "feature")
	if err != nil || !strings.Contains(out, "已出spec") {
		t.Fatalf("show: %v %q", err, out)
	}
	// put 新版本
	defPath := filepath.Join(dir, "def.json")
	if err := os.WriteFile(defPath,
		[]byte(`{"states":["待办","进行中","已完成"],"gates":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err = runLedgerCLI(t, dir, "workflow", "put", "hotfix", "--file", defPath)
	if err != nil || !strings.Contains(out, `"version":1`) {
		t.Fatalf("put: %v %q", err, out)
	}
}
