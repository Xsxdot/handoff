package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCardStepRejectsUnknown(t *testing.T) {
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
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID, "--step", "verify"); err == nil ||
		!strings.Contains(err.Error(), "verify") || !strings.Contains(err.Error(), "bug") {
		t.Fatalf("未知节点应带节点名与工作流名: %v", err)
	}
}
