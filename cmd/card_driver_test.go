// card_driver_test.go 验证 release/takeover CLI 穿过真实账本与事件序列化边界。
// 边界：只覆盖本地 CLI 命令接线，不探测 agentd 或远程执行器。
package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestCardDriverCommandsTakeoverAndRelease(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "驱动卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var card struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &card); err != nil {
		t.Fatalf("decode card: %v", err)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "takeover", card.ID)
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("takeover 应无确认且输出 ok: out=%q err=%v", out, err)
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", card.ID)
	var snapshot struct {
		Card ledger.Card `json:"card"`
	}
	if err != nil || json.Unmarshal([]byte(strings.TrimSpace(show)), &snapshot) != nil || snapshot.Card.DriverSession == "" {
		t.Fatalf("takeover 后应有驱动会话: out=%q err=%v", show, err)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "release", card.ID)
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("release 应输出 ok: out=%q err=%v", out, err)
	}
	show, _, err = runLedgerCLI(t, dir, "card", "show", card.ID)
	snapshot = struct {
		Card ledger.Card `json:"card"`
	}{}
	if err != nil || json.Unmarshal([]byte(strings.TrimSpace(show)), &snapshot) != nil || snapshot.Card.DriverSession != "" || !snapshot.Card.DriverHeartbeatAt.IsZero() {
		t.Fatalf("release 后应清空驱动与认领时刻: out=%q err=%v card=%+v", show, err, snapshot.Card)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer st.Close()
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 100)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	found := false
	for _, event := range events {
		if event.Type != ledger.EvDriverTakeover {
			continue
		}
		found = true
		var payload struct {
			From *string `json:"from"`
			To   *string `json:"to"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatalf("takeover payload: %v", err)
		}
		if payload.From == nil || payload.To == nil || *payload.To == "" {
			t.Fatalf("takeover payload 缺 from/to: %+v", payload)
		}
	}
	if !found {
		t.Fatal("CLI takeover 必须落 driver_takeover 事件")
	}
}

// TestCardRebindSuccessAndTakeoverEvent 换绑成功：session 覆写 + 落恰一条
// EvDriverTakeover（payload from/to 穿过真实 JSON 序列化）。--carrier 的落库
// 写入归 C1 已锁（binding_test.go 同包 raw SQL），本卡只锁 CLI→事件链路。
func TestCardRebindSuccessAndTakeoverEvent(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "换绑卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("card add: %v", err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ClaimCard(c.ID, "cli:old@h"); err != nil {
		t.Fatalf("预占: %v", err)
	}
	stdout, _, err := runLedgerCLI(t, dir, "card", "rebind", c.ID,
		"--to", "cli:new@h", "--expect", "cli:old@h", "--carrier", "console/alpha 主控台 v2")
	if err != nil || strings.TrimSpace(stdout) != `{"ok":true}` {
		t.Fatalf("rebind 应成功输出 ok: out=%q err=%v", stdout, err)
	}
	card, err := st.GetCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.DriverSession != "cli:new@h" {
		t.Fatalf("换绑后 session 应覆写: %q", card.DriverSession)
	}
	events, err := st.EventsFromAsc([]string{c.ID}, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range events {
		if e.Type != ledger.EvDriverTakeover {
			continue
		}
		var payload struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			t.Fatalf("takeover payload: %v", err)
		}
		if payload.From != "cli:old@h" || payload.To != "cli:new@h" {
			t.Fatalf("takeover payload from/to = %q/%q", payload.From, payload.To)
		}
		found = true
	}
	if !found {
		t.Fatal("CLI 换绑必须落 driver_takeover 事件")
	}
}

// TestCardRebindConflictNonZeroExitAndCAS 换绑 CAS 冲突：退出码非零（err 非
// nil）且 stderr/err 点名当前绑定并带「当前绑定」CAS 语义。
func TestCardRebindConflictNonZeroExitAndCAS(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "换绑冲突卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("card add: %v", err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.ClaimCard(c.ID, "cli:old@h"); err != nil {
		t.Fatalf("预占: %v", err)
	}
	st.Close()
	_, stderr, err := runLedgerCLI(t, dir, "card", "rebind", c.ID,
		"--to", "cli:new@h", "--expect", "cli:wrong@h")
	if err == nil {
		t.Fatal("CAS 冲突必须失败（退出码非零）")
	}
	if !strings.Contains(stderr+err.Error(), "cli:old@h") || !strings.Contains(stderr+err.Error(), "当前绑定") {
		t.Fatalf("冲突报文必须点名当前绑定并带 CAS 语义: stderr=%q err=%v", stderr, err)
	}
}

// TestCardRebindHelpCarrierOpaqueWording --carrier 帮助文本用「不透明载体标识」
// 措辞（breakdown 澄清一：本期只存不解释，格式定义权归 B156.3）。
func TestCardRebindHelpCarrierOpaqueWording(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "rebind", "--help")
	if err != nil {
		t.Fatalf("card rebind --help: %v", err)
	}
	if !strings.Contains(out, "不透明载体标识") {
		t.Fatalf("--carrier 帮助文本应含「不透明载体标识」: %q", out)
	}
}
