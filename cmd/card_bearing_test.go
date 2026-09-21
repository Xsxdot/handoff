// card_bearing_test.go —— `handoff card seat bearing set` 的命令级缝测试（B389 §2.3）。
//
// 职责：走 root command + 真实 agentd HTTP（载体登记读面）+ 真实 SQLite 账本，
// 锁住存量 coordinate 席位补写承载的写面：机器名取自登记、席位真源不变、
// 承运 CLI 与席位 CLI 不一致时拒绝。
// 边界：不复制 ledger 的 CAS/形态执法（那在 internal/ledger/bearing_test.go 锁）。
package cmd

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// stubCarrierAgentd 起一个只回答 GET /api/squads 的假 agentd，返回一个载体登记行。
func stubCarrierAgentd(t *testing.T, dir string, carrier map[string]any) {
	t.Helper()
	newCardStepCLIEndpoint(t, dir, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/squads" || r.Method != http.MethodGet {
			t.Errorf("非预期请求: %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		body, _ := json.Marshal(map[string]any{"carriers": []any{carrier}, "squads": []any{}})
		_, _ = w.Write(body)
	}))
}

// seedLegacyBearingCard 建一张带 coordinate 席位、但承载行被删掉的存量卡
// （有席位无承载），返回卡号与席位身份。
func seedLegacyBearingCard(t *testing.T, dir, seatCLI string) (string, string) {
	t.Helper()
	if _, _, err := runLedgerCLI(t, dir, "card", "list"); err != nil {
		t.Fatalf("预热基础账本: %v", err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("打开账本: %v", err)
	}
	defer st.Close()
	card, err := st.CreateCard(ledger.NewCard{Title: "存量承载", Project: "demo", Workflow: "bug", Actor: "test"})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	seat, err := proto.EncodeSeatIdentity(seatCLI, "sess-legacy")
	if err != nil {
		t.Fatalf("编码席位: %v", err)
	}
	if err := st.BindSeat(card.ID, seat, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "local"}); err != nil {
		t.Fatalf("写存量席位: %v", err)
	}
	// 用 raw sqlite 删承载行，模拟契约 §2.3 的「有 coordinate 席位、无承载记录」。
	db, err := sql.Open("sqlite", filepath.Join(dir, "ledger.db")+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("打开 raw 账本: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`DELETE FROM seat_bearings WHERE card_id = ?`, card.ID); err != nil {
		t.Fatalf("删承载行模拟存量: %v", err)
	}
	return card.ID, seat
}

func TestCardSeatBearingSetRepairs(t *testing.T) {
	dir := t.TempDir()
	stubCarrierAgentd(t, dir, map[string]any{
		"name": "c1", "machine": "linux-01", "cli": "opencode",
		"home_dir": "/home/coordinator", "model": "m1", "credential": "standalone",
		"status": "online", "version": 1,
	})
	cardID, seat := seedLegacyBearingCard(t, dir, "opencode")

	out, errOut, err := runLedgerCLI(t, dir, "card", "seat", "bearing", "set", cardID, "--carrier", "c1")
	if err != nil {
		t.Fatalf("补写承载失败: %v stderr=%s", err, errOut)
	}
	if got := out; got != "{\"ok\":true}\n" {
		t.Fatalf("stdout=%q，want {\"ok\":true}", got)
	}

	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("复开账本: %v", err)
	}
	defer st.Close()
	bearing, ok, err := st.SeatBearingOf(cardID)
	if err != nil || !ok {
		t.Fatalf("读补写的承载: ok=%v err=%v", ok, err)
	}
	if bearing.Carrier != "c1" || bearing.Machine != "linux-01" ||
		bearing.HomeDir != "/home/coordinator" || bearing.Model != "m1" {
		t.Fatalf("承载未取自登记: %+v", bearing)
	}
	card, err := st.GetCard(cardID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if card.DriverSession != seat || card.DriverSource != string(proto.SeatSourceCoordinate) {
		t.Fatalf("补承载不得改席位真源: %+v", card)
	}
}

func TestCardSeatBearingSetRejectsCLIMismatch(t *testing.T) {
	dir := t.TempDir()
	stubCarrierAgentd(t, dir, map[string]any{
		"name": "c1", "machine": "linux-01", "cli": "grok",
		"home_dir": "/home/coordinator", "credential": "standalone",
		"status": "online", "version": 1,
	})
	cardID, seat := seedLegacyBearingCard(t, dir, "opencode")

	_, _, err := runLedgerCLI(t, dir, "card", "seat", "bearing", "set", cardID, "--carrier", "c1")
	if err == nil {
		t.Fatal("载体 CLI 与席位 CLI 不一致必须拒绝")
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("复开账本: %v", err)
	}
	defer st.Close()
	if _, ok, _ := st.SeatBearingOf(cardID); ok {
		t.Fatal("拒绝后不得写入承载行")
	}
	card, err := st.GetCard(cardID)
	if err != nil {
		t.Fatalf("读卡: %v", err)
	}
	if card.DriverSession != seat {
		t.Fatalf("拒绝后席位不得变: %s", card.DriverSession)
	}
}
