package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStepFlagHelpMentionsNodeName(t *testing.T) {
	flag := cardDispatchCmd.Flags().Lookup("step")
	if flag == nil {
		t.Fatalf("找不到 --step flag")
	}
	if flag.Usage == "" {
		t.Fatalf("--step 的说明为空")
	}
	if !strings.Contains(flag.Usage, "节点名") {
		t.Fatalf("--step 应说明接收节点名: %s", flag.Usage)
	}
	for _, stale := range []string{strings.Join([]string{"review", "merge"}, "|"), strings.Join([]string{"环节", "只认"}, "")} {
		if strings.Contains(flag.Usage, stale) {
			t.Fatalf("--step 的说明还写着写死的白名单 %q: %s", stale, flag.Usage)
		}
	}
}

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
