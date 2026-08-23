package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCardPrefixEndToEnd(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "card", "prefix", "charter", "C"); err != nil {
		t.Fatalf("prefix: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "add", "charter 首卡", "--project", "charter")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("解 add 输出 %q: %v", out, err)
	}
	if card.ID != "C1" {
		t.Fatalf("显式前缀后应生成 C1，得 %s", card.ID)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "prefix", "charter", "X"); err == nil || !strings.Contains(err.Error(), "已有卡") {
		t.Fatalf("已有卡后改前缀应拒绝，实得 %v", err)
	}
}
