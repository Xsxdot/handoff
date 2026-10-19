// b435_card_bind_mode_test.go —— B435 段 5：坐下命令面的席位模式与 CLI 两处投影。
//
// 职责：锁 `card bind --mode`（缺省 pull、push 拒绝且不坐下、非法值拒绝）、
// `card coordinate` 无 --mode、`rebind --self` 写 pull，以及 `card show` /
// `card list --json` 两条 CLI 出口的 driver_mode 键（空座缺键）。
// 边界：只经真 cobra 入口与真 SQLite 账本；agentd/collab 的投影在各自包测。
package cmd

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func b435ReadCard(t *testing.T, dir, id string) ledger.Card {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card, err := st.GetCard(id)
	if err != nil {
		t.Fatal(err)
	}
	return card
}

// C-311/C-312：不带 --mode 与 --mode pull 都坐成 pull。
func TestB435CardBindModeDefaultsPull(t *testing.T) {
	clearSeatSourceEnv(t)
	for name, extra := range map[string][]string{"缺省": nil, "显式 pull": {"--mode", "pull"}} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			id := mustAddCard(t, dir, "坐下缺省 pull")
			args := append([]string{"card", "bind", id, "--cli", "codex", "--session", "b435-bind"}, extra...)
			if out, _, err := runLedgerCLI(t, dir, args...); err != nil || strings.TrimSpace(out) != `{"ok":true}` {
				t.Fatalf("bind: out=%q err=%v", out, err)
			}
			card := b435ReadCard(t, dir, id)
			if card.DriverSession != "cli:codex#b435-bind" || card.DriverSource != "bind" || card.DriverMode != "pull" {
				t.Fatalf("席位 = session %q source %q mode %q，want bind/pull", card.DriverSession, card.DriverSource, card.DriverMode)
			}
		})
	}
}

// C-313a/C-313b：--mode push 被拒（非 0 且 ≠124），stderr 指向 card coordinate，席位三列仍空。
func TestB435CardBindModePushRejected(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	id := mustAddCard(t, dir, "坐下拒 push")
	_, errOut, err := runLedgerCLI(t, dir, "card", "bind", id, "--cli", "codex", "--session", "b435-push", "--mode", "push")
	if code := b435ExitCode(err); code == 0 || code == ExitTimeout {
		t.Fatalf("--mode push 应非 0 且 ≠124，得 %v", err)
	}
	if want := "handoff card coordinate " + id; !strings.Contains(errOut, want) {
		t.Fatalf("stderr 应含 %q，得 %q", want, errOut)
	}
	card := b435ReadCard(t, dir, id)
	if card.DriverSession != "" || card.DriverSource != "" || card.DriverMode != "" {
		t.Fatalf("被拒后席位三列应为空，得 %q/%q/%q", card.DriverSession, card.DriverSource, card.DriverMode)
	}
}

// C-313/C-302：非法字面量（大小写、首尾空白、空串、未知词）在坐下前被拒。
func TestB435CardBindModeInvalidRejected(t *testing.T) {
	clearSeatSourceEnv(t)
	for _, raw := range []string{"PUSH", " pull", "pull ", "", "auto"} {
		t.Run("["+raw+"]", func(t *testing.T) {
			dir := t.TempDir()
			id := mustAddCard(t, dir, "坐下拒非法模式")
			_, _, err := runLedgerCLI(t, dir, "card", "bind", id, "--cli", "codex", "--session", "b435-bad", "--mode", raw)
			if code := b435ExitCode(err); code == 0 || code == ExitTimeout {
				t.Fatalf("--mode %q 应非 0 且 ≠124，得 %v", raw, err)
			}
			if card := b435ReadCard(t, dir, id); card.DriverSession != "" || card.DriverMode != "" {
				t.Fatalf("--mode %q 被拒后不应坐下，得 %q/%q", raw, card.DriverSession, card.DriverMode)
			}
		})
	}
}

// C-314：机器人占座命令不长 --mode（它恒为 push）；bind 的 --mode 缺省 pull。
func TestB435CardCoordinateHasNoModeFlag(t *testing.T) {
	if f := cardCoordinateCmd.Flags().Lookup("mode"); f != nil {
		t.Fatalf("card coordinate 不应有 --mode，得 %+v", f)
	}
	if f := cardBindCmd.Flags().Lookup("mode"); f == nil || f.DefValue != "pull" {
		t.Fatalf("card bind 应有缺省 pull 的 --mode，得 %+v", f)
	}
}

// C-323：rebind --self 让当前会话以 pull 接班。
func TestB435CardRebindSelfWritesPull(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	id := mustAddCard(t, dir, "自换绑 pull")
	if _, _, err := runLedgerCLI(t, dir, "card", "bind", id, "--cli", "grok", "--session", "old"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "rebind", id, "--self", "--cli", "claude", "--session", "new"); err != nil {
		t.Fatal(err)
	}
	card := b435ReadCard(t, dir, id)
	if card.DriverSession != "cli:claude#new" || card.DriverMode != "pull" {
		t.Fatalf("rebind --self 后 = %q/%q，want cli:claude#new/pull", card.DriverSession, card.DriverMode)
	}
}

func b435RawKeys(t *testing.T, raw json.RawMessage) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("解码 %s: %v", raw, err)
	}
	return m
}

// C-310：card show 的 card 对象带 driver_mode；空座缺键；顶层键集不变（C-515）。
func TestB435CardShowDriverMode(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	seated := mustAddCard(t, dir, "show 坐下")
	empty := mustAddCard(t, dir, "show 空座")
	if _, _, err := runLedgerCLI(t, dir, "card", "bind", seated, "--cli", "codex", "--session", "show"); err != nil {
		t.Fatal(err)
	}
	show := func(id string) map[string]json.RawMessage {
		out, _, err := runLedgerCLI(t, dir, "card", "show", id)
		if err != nil {
			t.Fatal(err)
		}
		top := b435RawKeys(t, json.RawMessage(strings.TrimSpace(out)))
		var keys []string
		for k := range top {
			keys = append(keys, k)
		}
		if len(top) != 5 || top["card"] == nil || top["effective_base_branch"] == nil ||
			top["relations"] == nil || top["tasks"] == nil || top["events"] == nil {
			t.Fatalf("card show 顶层键集应为 card/effective_base_branch/relations/tasks/events，得 %v", keys)
		}
		return b435RawKeys(t, top["card"])
	}
	if got := string(show(seated)["driver_mode"]); got != `"pull"` {
		t.Fatalf("坐下卡 driver_mode = %s，want \"pull\"", got)
	}
	if raw, ok := show(empty)["driver_mode"]; ok {
		t.Fatalf("空座卡不应出 driver_mode 键，得 %s", raw)
	}
}

// cardViewWire：card list --json 行带 driver_mode；空座缺键。
func TestB435CardListDriverMode(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	seated := mustAddCard(t, dir, "list 坐下")
	empty := mustAddCard(t, dir, "list 空座")
	if _, _, err := runLedgerCLI(t, dir, "card", "bind", seated, "--cli", "codex", "--session", "list"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "list", "--project", "demo", "--json")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		row := b435RawKeys(t, json.RawMessage(line))
		var id string
		if err := json.Unmarshal(row["id"], &id); err != nil {
			t.Fatal(err)
		}
		rows[id] = row
	}
	if got := string(rows[seated]["driver_mode"]); got != `"pull"` {
		t.Fatalf("坐下卡列表行 driver_mode = %s，want \"pull\"（行 %v）", got, rows[seated])
	}
	if raw, ok := rows[empty]["driver_mode"]; ok {
		t.Fatalf("空座卡列表行不应出 driver_mode，得 %s", raw)
	}
}
