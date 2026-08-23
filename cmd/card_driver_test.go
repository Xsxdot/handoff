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
