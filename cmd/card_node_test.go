package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCardNodeMergeMainStaysHuman(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "热修", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}

	out, _, err = runLedgerCLI(t, dir, "card", "dispatch", c.ID, "--node", "merge")
	if err != nil {
		t.Fatalf("node merge: %v", err)
	}
	if !strings.Contains(out, "needs_human") {
		t.Fatalf("main 层应转等人: %q", out)
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(show, "进行中") {
		t.Fatalf("节点入口不应认领: %q", show)
	}
}

func TestCardNodeRejectsUnknown(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "x", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID, "--node", "verify"); err == nil ||
		!strings.Contains(err.Error(), "review|merge") {
		t.Fatalf("未知节点应拒: %v", err)
	}
}
