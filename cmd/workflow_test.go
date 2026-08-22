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

func TestWorkflowPutAcceptsNodesOnlyFile(t *testing.T) {
	dir := t.TempDir()
	defPath := filepath.Join(dir, "nodes-only.json")
	if err := os.WriteFile(defPath, []byte(`{"nodes":[
  {"name":"待办","next":"进行中","dispatch":false},
  {"name":"进行中","next":"已完成","dispatch":false},
  {"name":"已完成","dispatch":false}
]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := runLedgerCLI(t, dir, "workflow", "put", "nodes-only", "--file", defPath)
	if err != nil {
		t.Fatalf("put nodes-only: %v %q", err, out)
	}

	st, err := openLedger()
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	wf, err := st.GetWorkflow("nodes-only", 0)
	if err != nil {
		t.Fatalf("get nodes-only workflow: %v", err)
	}
	if len(wf.Def.States) != 3 {
		t.Fatalf("states projection has %d states, want 3: %+v", len(wf.Def.States), wf.Def.States)
	}
}

func TestWorkflowPutRejectsEmptyDefinitionWithFieldNames(t *testing.T) {
	dir := t.TempDir()
	defPath := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(defPath, []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}

	out, stderr, err := runLedgerCLI(t, dir, "workflow", "put", "empty", "--file", defPath)
	if err == nil {
		t.Fatalf("empty definition should be rejected, stdout=%q stderr=%q", out, stderr)
	}
	message := err.Error() + " " + out + " " + stderr
	if !strings.Contains(strings.ToLower(message), "nodes") || !strings.Contains(strings.ToLower(message), "states") {
		t.Fatalf("error should mention nodes and states, got %q", message)
	}
}
