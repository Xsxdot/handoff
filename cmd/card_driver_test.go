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

func TestCurrentSeatIdentityRequiresInjectedPair(t *testing.T) {
	t.Setenv("HANDOFF_SESSION_CLI", "codex")
	t.Setenv("HANDOFF_SESSION_ID", "thread-01")
	if got, err := currentSeatIdentity(); err != nil || got != "cli:codex#thread-01" {
		t.Fatalf("当前席位身份 = %q, err=%v", got, err)
	}
	t.Setenv("HANDOFF_SESSION_ID", "")
	t.Setenv("USER", "fallback-user")
	if _, err := currentSeatIdentity(); err == nil {
		t.Fatal("缺 session id 不得回退 USER")
	}
}

func TestCardBindUsesCurrentSeat(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HANDOFF_SESSION_CLI", "codex")
	t.Setenv("HANDOFF_SESSION_ID", "thread-bind")
	out, _, err := runLedgerCLI(t, dir, "card", "add", "坐下卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatal(err)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "bind", created.ID)
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("bind: out=%q err=%v", out, err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card, err := st.GetCard(created.ID)
	if err != nil || card.DriverSession != "cli:codex#thread-bind" || card.DriverSource != "bind" {
		t.Fatalf("bind 席位 = %+v, err=%v", card, err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "bind", created.ID); err == nil {
		t.Fatal("已有席位再次 bind 应失败")
	}
}

func TestCardRebindSelfUsesLocalLedger(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HANDOFF_SESSION_CLI", "codex")
	t.Setenv("HANDOFF_SESSION_ID", "thread-old")
	out, _, err := runLedgerCLI(t, dir, "card", "add", "self 换绑卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "bind", created.ID); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HANDOFF_SESSION_ID", "thread-new")
	out, _, err = runLedgerCLI(t, dir, "card", "rebind", created.ID, "--self")
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("rebind --self: out=%q err=%v", out, err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card, err := st.GetCard(created.ID)
	if err != nil || card.DriverSession != "cli:codex#thread-new" || card.DriverSource != "bind" {
		t.Fatalf("self 换绑席位 = %+v, err=%v", card, err)
	}
}

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
	if _, _, err = runLedgerCLI(t, dir, "card", "takeover", card.ID); err == nil {
		t.Fatal("takeover 不得再写协调者席位")
	}
	show, _, err := runLedgerCLI(t, dir, "card", "show", card.ID)
	var snapshot struct {
		Card ledger.Card `json:"card"`
	}
	if err != nil || json.Unmarshal([]byte(strings.TrimSpace(show)), &snapshot) != nil || snapshot.Card.DriverSession != "" || snapshot.Card.DriverSource != "" {
		t.Fatalf("takeover 后不得有席位: out=%q err=%v card=%+v", show, err, snapshot.Card)
	}
	out, _, err = runLedgerCLI(t, dir, "card", "release", card.ID)
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("空座 release 应幂等输出 ok: out=%q err=%v", out, err)
	}
	t.Setenv("HANDOFF_SESSION_CLI", "codex")
	t.Setenv("HANDOFF_SESSION_ID", "release-seat")
	if _, _, err := runLedgerCLI(t, dir, "card", "bind", card.ID); err != nil {
		t.Fatalf("bind: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "release", card.ID); err == nil {
		t.Fatal("非空席位 release 必须失败")
	}
	show, _, err = runLedgerCLI(t, dir, "card", "show", card.ID)
	if err != nil || !strings.Contains(show, `"driver_session":"cli:codex#release-seat"`) {
		t.Fatalf("失败 release 不得清席位: out=%q err=%v", show, err)
	}
}

// TestCardRebindSelfAndTakeoverEvent 换绑成功：只接受当前会话出示，且落恰一条
// EvDriverTakeover（payload from/to 穿过真实 JSON 序列化）。
func TestCardRebindSelfAndTakeoverEvent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HANDOFF_SESSION_CLI", "codex")
	t.Setenv("HANDOFF_SESSION_ID", "old")
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
	if _, _, err := runLedgerCLI(t, dir, "card", "bind", c.ID); err != nil {
		t.Fatalf("预占: %v", err)
	}
	t.Setenv("HANDOFF_SESSION_ID", "new")
	stdout, _, err := runLedgerCLI(t, dir, "card", "rebind", c.ID, "--self")
	if err != nil || strings.TrimSpace(stdout) != `{"ok":true}` {
		t.Fatalf("rebind 应成功输出 ok: out=%q err=%v", stdout, err)
	}
	card, err := st.GetCard(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if card.DriverSession != "cli:codex#new" || card.DriverSource != "bind" {
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
		if payload.From != "cli:codex#old" || payload.To != "cli:codex#new" {
			t.Fatalf("takeover payload from/to = %q/%q", payload.From, payload.To)
		}
		found = true
	}
	if !found {
		t.Fatal("CLI 换绑必须落 driver_takeover 事件")
	}
}

// TestCardRebindRequiresExplicitModeAndRejectsLegacyFlags 换绑接班者只能由
// --self/--launch 二选一给出，旧任意 session flag 不再形成后门。
func TestCardRebindRequiresExplicitModeAndRejectsLegacyFlags(t *testing.T) {
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
	_, stderr, err := runLedgerCLI(t, dir, "card", "rebind", c.ID)
	if err == nil {
		t.Fatal("缺少换绑模式必须失败")
	}
	if !strings.Contains(stderr+err.Error(), "二选一") {
		t.Fatalf("缺少模式报文必须指向二选一: stderr=%q err=%v", stderr, err)
	}
	if _, stderr, err := runLedgerCLI(t, dir, "card", "rebind", c.ID, "--to", "任意会话"); err == nil || !strings.Contains(stderr+err.Error(), "unknown flag") {
		t.Fatalf("旧 --to flag 必须失败: stderr=%q err=%v", stderr, err)
	}
}

func TestCardRebindHelpUsesExplicitModes(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "rebind", "--help")
	if err != nil {
		t.Fatalf("card rebind --help: %v", err)
	}
	if !strings.Contains(out, "--self") || !strings.Contains(out, "--launch") || strings.Contains(out, "--carrier") {
		t.Fatalf("换绑帮助应只展示显式模式: %q", out)
	}
}
