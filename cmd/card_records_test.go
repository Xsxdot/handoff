// 回合末四分法两个写入口（card accept / card needs）的 CLI 测试。
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// newTestCard 建一张卡并返回 id，供本文件各用例复用。
func newTestCard(t *testing.T, dir, title string) string {
	t.Helper()
	out, _, err := runLedgerCLI(t, dir, "card", "add", title, "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	var got struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("解析建卡输出 %q: %v", out, err)
	}
	return got.ID
}

// TestCardAcceptRecordsVerified 已验必须落事件且带证据。
func TestCardAcceptRecordsVerified(t *testing.T) {
	dir := t.TempDir()
	id := newTestCard(t, dir, "验收卡")
	if _, _, err := runLedgerCLI(t, dir, "card", "accept", id, "--evidence", "go test ./... 全绿"); err != nil {
		t.Fatalf("card accept: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "show", id)
	if err != nil {
		t.Fatalf("card show: %v", err)
	}
	if !strings.Contains(out, "acceptance_recorded") {
		t.Fatalf("事件流缺 acceptance_recorded: %q", out)
	}
	if !strings.Contains(out, "go test ./... 全绿") {
		t.Fatalf("事件流缺证据原文: %q", out)
	}
}

// TestCardAcceptRequiresEvidence 「已验」而不给证据必须拒绝——本项目的
// 取证文化：已验是一个断言，无证据的断言不许落账。
func TestCardAcceptRequiresEvidence(t *testing.T) {
	dir := t.TempDir()
	id := newTestCard(t, dir, "无证据卡")
	_, _, err := runLedgerCLI(t, dir, "card", "accept", id)
	if err == nil {
		t.Fatalf("已验不带证据应报错")
	}
	if !strings.Contains(err.Error(), "证据") {
		t.Fatalf("错误文案应提到证据，实际: %v", err)
	}
	out, _, showErr := runLedgerCLI(t, dir, "card", "show", id)
	if showErr != nil {
		t.Fatalf("card show: %v", showErr)
	}
	if strings.Contains(out, "acceptance_recorded") {
		t.Fatalf("拒绝时不得落事件: %q", out)
	}
}

// TestCardAcceptUnverified 未验可以不带证据（对应 backlog 的 done(未验)）。
func TestCardAcceptUnverified(t *testing.T) {
	dir := t.TempDir()
	id := newTestCard(t, dir, "未验卡")
	if _, _, err := runLedgerCLI(t, dir, "card", "accept", id, "--unverified"); err != nil {
		t.Fatalf("card accept --unverified: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "show", id)
	if err != nil {
		t.Fatalf("card show: %v", err)
	}
	if !strings.Contains(out, "acceptance_recorded") {
		t.Fatalf("事件流缺 acceptance_recorded: %q", out)
	}
}
