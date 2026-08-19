package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecisionOpenListAnswer(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "有请示的卡", "--project", "demo", "--workflow", "bug")
	var card struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &card)

	out, _, err := runLedgerCLI(t, dir, "decision", "open", "合并顺序怎么定？",
		"--card", card.ID, "--option", "done 时序", "--option", "依赖序")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var decision struct {
		ID int64 `json:"ID"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &decision); err != nil || decision.ID == 0 {
		t.Fatalf("open 输出: %q", out)
	}
	// 项目级
	if _, _, err := runLedgerCLI(t, dir, "decision", "open", "推不推汇流线？"); err != nil {
		t.Fatalf("project-level: %v", err)
	}
	// list 缺省只列 open
	out, _, err = runLedgerCLI(t, dir, "decision", "list")
	if err != nil || strings.Count(out, "\n") != 2 {
		t.Fatalf("open 列表应两行: %v %q", err, out)
	}
	// answer
	if _, _, err := runLedgerCLI(t, dir, "decision", "answer", "1", "done 时序"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	out, _, _ = runLedgerCLI(t, dir, "decision", "list")
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("答复后 open 应剩一行: %q", out)
	}
	// --all 两行且含答案
	out, _, _ = runLedgerCLI(t, dir, "decision", "list", "--all")
	if strings.Count(out, "\n") != 2 || !strings.Contains(out, "done 时序") {
		t.Fatalf("--all: %q", out)
	}
}
