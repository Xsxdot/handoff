package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// 垫号后新建卡号严格大于水位——判据⑧的单测形。
func TestCardMinB(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "card", "min-b", "156"); err != nil {
		t.Fatalf("min-b: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "add", "垫号后的第一张卡", "--project", "demo")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var c struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &c)
	if c.ID != "B157" {
		t.Fatalf("垫号未生效，应 B157 实为 %s", c.ID)
	}
	// 水位只升不降：往回垫是无操作不是报错（幂等重跑安全）
	if _, _, err := runLedgerCLI(t, dir, "card", "min-b", "100"); err != nil {
		t.Fatalf("回垫应为无操作: %v", err)
	}
	out, _, _ = runLedgerCLI(t, dir, "card", "add", "又一张", "--project", "demo")
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &c)
	if c.ID != "B158" {
		t.Fatalf("回垫不应降水位，应 B158 实为 %s", c.ID)
	}
	// 非数字参数干净报错
	if _, _, err := runLedgerCLI(t, dir, "card", "min-b", "abc"); err == nil {
		t.Fatal("非数字应拒")
	}
}
