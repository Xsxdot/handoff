package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCardDispatchClaimAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	dp := filepath.Join(dir, "block-a.md")
	if err := os.WriteFile(dp, []byte("# 执行纪律\n1. 逐 task 派 subagent。"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, _, err := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "update", c.ID, "--accept", "测试全绿"); err != nil {
		t.Fatal(err)
	}

	var gotPrompt, gotProject string
	restore := swapDispatchTransport(func(prompt, branch, target, project string) (string, error) {
		gotPrompt, gotProject = prompt, project
		return "T-fake-1", nil
	})
	defer restore()

	out, _, err = runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", dp)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out, "T-fake-1") {
		t.Fatalf("输出应含 task id: %q", out)
	}
	if !strings.HasPrefix(gotPrompt, "# 执行纪律") || !strings.Contains(gotPrompt, "要派的卡") ||
		!strings.Contains(gotPrompt, "测试全绿") {
		t.Fatalf("prompt 拼装: %q", gotPrompt)
	}
	if gotProject != "demo" {
		t.Fatalf("派发未带 project: %q", gotProject)
	}

	show, _, err := runLedgerCLI(t, dir, "card", "show", c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(show, `"Status":"进行中"`) && !strings.Contains(show, `"status":"进行中"`) {
		t.Fatalf("未认领: %q", show)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID, "--template", "feature-impl",
		"--target", "mac-02", "--discipline-override", dp); err == nil ||
		!strings.Contains(err.Error(), "认领") {
		t.Fatalf("重复派发应报已认领: %v", err)
	}
	if !strings.Contains(show, "dispatched") || !strings.Contains(show, "discipline_hash") {
		t.Fatalf("快照事件缺失: %q", show)
	}
}
