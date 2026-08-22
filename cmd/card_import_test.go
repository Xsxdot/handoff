package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// card import 端到端：显式号导入 → show 核对字段；撞号在 CLI 层同样报错。
func TestCardImportEndToEnd(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "import", "B42", "存量迁入的行",
		"--project", "handoff", "--priority", "高", "--source", "backlog.md")
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatalf("解 import 输出 %q: %v", out, err)
	}
	if created.ID != "B42" {
		t.Fatalf("应保原号 B42，得 %s", created.ID)
	}

	out, _, err = runLedgerCLI(t, dir, "card", "show", "B42")
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	var shown struct {
		Card struct {
			ID, Title, Priority, Project, Status string
			WorkflowName                         string `json:"workflow"`
		} `json:"card"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &shown); err != nil {
		t.Fatalf("解 show 输出: %v", err)
	}
	if shown.Card.Title != "存量迁入的行" || shown.Card.Priority != "高" ||
		shown.Card.Project != "handoff" || shown.Card.Status != "待办" ||
		shown.Card.WorkflowName != "triage" {
		t.Fatalf("字段没落对: %+v", shown.Card)
	}

	// 撞号在命令面同样是错误退出，不是静默覆盖
	if _, _, err := runLedgerCLI(t, dir, "card", "import", "B42", "重复号",
		"--project", "handoff"); err == nil {
		t.Fatal("撞号应报错")
	}

	// 导入号高于水位时自动取号跳过它：下一张 add 是 B43
	out, _, err = runLedgerCLI(t, dir, "card", "add", "导入之后的新卡", "--project", "handoff")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &created)
	if created.ID != "B43" {
		t.Fatalf("自动取号应跳过导入号，应 B43，得 %s", created.ID)
	}
}
