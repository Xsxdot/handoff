// card_driver_test.go 验证 release/takeover CLI 穿过真实账本与事件序列化边界。
// 边界：只覆盖本地 CLI 命令接线，不探测 agentd 或远程执行器。
package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/spf13/cobra"
)

func clearSeatSourceEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"HANDOFF_SESSION_CLI",
		"HANDOFF_SESSION_ID",
		"GROK_SESSION_ID",
		"CLAUDE_CODE_SESSION_ID",
		"CLAUDE_CODE_REMOTE_SESSION_ID",
	} {
		t.Setenv(key, "")
	}
}

func TestCurrentSeatIdentityRequiresInjectedPair(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("HANDOFF_SESSION_CLI", "codex")
	t.Setenv("HANDOFF_SESSION_ID", "thread-01")
	if got, err := currentSeatIdentity("", ""); err != nil || got != "cli:codex#thread-01" {
		t.Fatalf("当前席位身份 = %q, err=%v", got, err)
	}
	t.Setenv("HANDOFF_SESSION_ID", "")
	t.Setenv("USER", "fallback-user")
	if _, err := currentSeatIdentity("", ""); err == nil {
		t.Fatal("缺 session id 不得回退 USER")
	}
}

func TestCurrentSeatIdentitySourceOrder(t *testing.T) {
	t.Run("grok host session", func(t *testing.T) {
		clearSeatSourceEnv(t)
		t.Setenv("GROK_SESSION_ID", "grok-01")
		got, err := currentSeatIdentity("", "")
		if err != nil || got != "cli:grok#grok-01" {
			t.Fatalf("grok identity = %q, err=%v", got, err)
		}
	})

	t.Run("claude host session ignores remote", func(t *testing.T) {
		clearSeatSourceEnv(t)
		t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-01")
		t.Setenv("CLAUDE_CODE_REMOTE_SESSION_ID", "remote-01")
		got, err := currentSeatIdentity("", "")
		if err != nil || got != "cli:claude#claude-01" {
			t.Fatalf("claude identity = %q, err=%v", got, err)
		}
	})

	t.Run("complete injected pair wins over host", func(t *testing.T) {
		clearSeatSourceEnv(t)
		t.Setenv("HANDOFF_SESSION_CLI", "opencode")
		t.Setenv("HANDOFF_SESSION_ID", "agent-01")
		t.Setenv("GROK_SESSION_ID", "parent-01")
		got, err := currentSeatIdentity("", "")
		if err != nil || got != "cli:opencode#agent-01" {
			t.Fatalf("injected identity = %q, err=%v", got, err)
		}
	})

	t.Run("partial injected cli blocks host", func(t *testing.T) {
		clearSeatSourceEnv(t)
		t.Setenv("HANDOFF_SESSION_CLI", "opencode")
		t.Setenv("GROK_SESSION_ID", "parent-01")
		got, err := currentSeatIdentity("", "")
		if err == nil || got != "" || !strings.Contains(err.Error(), "HANDOFF_SESSION_ID") {
			t.Fatalf("partial injected cli = %q, err=%v", got, err)
		}
	})

	t.Run("partial injected session blocks host", func(t *testing.T) {
		clearSeatSourceEnv(t)
		t.Setenv("HANDOFF_SESSION_ID", "agent-01")
		t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-01")
		got, err := currentSeatIdentity("", "")
		if err == nil || got != "" || !strings.Contains(err.Error(), "HANDOFF_SESSION_CLI") {
			t.Fatalf("partial injected session = %q, err=%v", got, err)
		}
	})

	t.Run("no source does not fall back to user", func(t *testing.T) {
		clearSeatSourceEnv(t)
		t.Setenv("USER", "fallback-user")
		got, err := currentSeatIdentity("", "")
		if err == nil || got != "" {
			t.Fatalf("no source identity = %q, err=%v", got, err)
		}
		for _, want := range []string{"grok/claude", "--cli", "--session"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("no source error %q missing %q", err, want)
			}
		}
	})

	t.Run("two host sessions are ambiguous", func(t *testing.T) {
		clearSeatSourceEnv(t)
		t.Setenv("GROK_SESSION_ID", "grok-01")
		t.Setenv("CLAUDE_CODE_SESSION_ID", "claude-01")
		got, err := currentSeatIdentity("", "")
		if err == nil || got != "" || !strings.Contains(err.Error(), "去掉其中一个") || !strings.Contains(err.Error(), "--cli") {
			t.Fatalf("ambiguous hosts = %q, err=%v", got, err)
		}
	})

	t.Run("complete flags without environment", func(t *testing.T) {
		clearSeatSourceEnv(t)
		got, err := currentSeatIdentity("grok", "grok-flag")
		if err != nil || got != "cli:grok#grok-flag" {
			t.Fatalf("flag identity = %q, err=%v", got, err)
		}
	})

	for _, tc := range []struct {
		name         string
		cli, session string
	}{
		{name: "only cli", cli: "grok"},
		{name: "only session", session: "grok-flag"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearSeatSourceEnv(t)
			got, err := currentSeatIdentity(tc.cli, tc.session)
			if err == nil || got != "" {
				t.Fatalf("partial flags identity = %q, err=%v", got, err)
			}
		})
	}

	t.Run("flags must match host", func(t *testing.T) {
		clearSeatSourceEnv(t)
		t.Setenv("GROK_SESSION_ID", "grok-01")
		if got, err := currentSeatIdentity("grok", "other"); err == nil || got != "" {
			t.Fatalf("mismatched host flags = %q, err=%v", got, err)
		}
		if got, err := currentSeatIdentity("grok", "grok-01"); err != nil || got != "cli:grok#grok-01" {
			t.Fatalf("matching host flags = %q, err=%v", got, err)
		}
	})

	t.Run("flags must match injected pair and injected ignores host", func(t *testing.T) {
		clearSeatSourceEnv(t)
		t.Setenv("HANDOFF_SESSION_CLI", "opencode")
		t.Setenv("HANDOFF_SESSION_ID", "agent-01")
		t.Setenv("GROK_SESSION_ID", "parent-01")
		if got, err := currentSeatIdentity("grok", "parent-01"); err == nil || got != "" {
			t.Fatalf("mismatched injected flags = %q, err=%v", got, err)
		}
		if got, err := currentSeatIdentity("opencode", "agent-01"); err != nil || got != "cli:opencode#agent-01" {
			t.Fatalf("matching injected flags = %q, err=%v", got, err)
		}
	})

	for _, tc := range []struct {
		name         string
		cli, session string
	}{
		{name: "cli separator", cli: "gro:k", session: "id"},
		{name: "session separator", cli: "grok", session: "id#part"},
		{name: "cli leading whitespace", cli: " grok", session: "id"},
		{name: "session trailing whitespace", cli: "grok", session: "id "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clearSeatSourceEnv(t)
			if got, err := currentSeatIdentity(tc.cli, tc.session); err == nil || got != "" {
				t.Fatalf("invalid flags identity = %q, err=%v", got, err)
			}
		})
	}
}

func TestCardBindAcceptsExplicitSeatFlags(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	id := mustAddCard(t, dir, "手填坐下卡")
	out, _, err := runLedgerCLI(t, dir, "card", "bind", id, "--cli", "grok", "--session", "manual-bind")
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("bind with explicit identity: out=%q err=%v", out, err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card, err := st.GetCard(id)
	if err != nil || card.DriverSession != "cli:grok#manual-bind" || card.DriverSource != "bind" {
		t.Fatalf("explicit bind seat = %+v, err=%v", card, err)
	}
}

func TestCardRebindSelfAcceptsExplicitSeatFlags(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	id := mustAddCard(t, dir, "手填写绑卡")
	if _, _, err := runLedgerCLI(t, dir, "card", "bind", id, "--cli", "grok", "--session", "old-seat"); err != nil {
		t.Fatal(err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "rebind", id, "--self", "--cli", "claude", "--session", "manual-rebind")
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("rebind with explicit identity: out=%q err=%v", out, err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card, err := st.GetCard(id)
	if err != nil || card.DriverSession != "cli:claude#manual-rebind" || card.DriverSource != "bind" {
		t.Fatalf("explicit rebind seat = %+v, err=%v", card, err)
	}
}

func TestSeatIdentityFlagsAreLocalToPresentingCommands(t *testing.T) {
	for _, command := range []*cobra.Command{cardBindCmd, cardRebindCmd, cardDispatchCmd, roomSendCmd} {
		for _, name := range []string{"cli", "session"} {
			if command.Flags().Lookup(name) == nil {
				t.Fatalf("%s 缺少本地 --%s", command.Use, name)
			}
		}
	}
	for _, command := range []*cobra.Command{rootCmd, cardCmd} {
		for _, name := range []string{"cli", "session"} {
			if command.PersistentFlags().Lookup(name) != nil {
				t.Fatalf("%s 不应注册 persistent --%s", command.Use, name)
			}
		}
	}
	if cardCoordinateCmd.Flags().Lookup("cli") != nil || cardCoordinateCmd.Flags().Lookup("session") != nil {
		t.Fatal("card coordinate 不应注册席位 flag")
	}
}

func TestRebindLaunchRejectsSeatFlags(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	id := mustAddCard(t, dir, "launch 禁用 flag 卡")
	if _, _, err := runLedgerCLI(t, dir, "card", "rebind", id, "--launch", "--cli", "grok", "--session", "manual"); err == nil || !strings.Contains(err.Error(), "--launch") {
		t.Fatalf("rebind --launch with seat flags should fail: %v", err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card, err := st.GetCard(id)
	if err != nil || card.DriverSession != "" || card.DriverSource != "" {
		t.Fatalf("launch flag rejection changed seat: %+v, err=%v", card, err)
	}
}

func TestCoordinateRejectsSeatFlags(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "card", "coordinate", "B329", "--cli", "grok", "--session", "manual"); err == nil || !strings.Contains(err.Error(), "unknown flag: --cli") {
		t.Fatalf("card coordinate with seat flags should fail at Cobra: %v", err)
	}
}

func TestCardBindUsesCurrentSeat(t *testing.T) {
	clearSeatSourceEnv(t)
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

func TestCardBindUsesGrokSessionEnvironment(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("GROK_SESSION_ID", "grok-bind")
	dir := t.TempDir()
	id := mustAddCard(t, dir, "grok 环境坐下卡")
	out, _, err := runLedgerCLI(t, dir, "card", "bind", id)
	if err != nil || strings.TrimSpace(out) != `{"ok":true}` {
		t.Fatalf("grok bind: out=%q err=%v", out, err)
	}
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	card, err := st.GetCard(id)
	if err != nil || card.DriverSession != "cli:grok#grok-bind" || card.DriverSource != "bind" {
		t.Fatalf("grok bind seat = %+v, err=%v", card, err)
	}
}

func TestCardRebindSelfUsesLocalLedger(t *testing.T) {
	clearSeatSourceEnv(t)
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
	clearSeatSourceEnv(t)
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
	clearSeatSourceEnv(t)
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

// TestCardRebindSelfEvictsAgentdSession 要求本机账本 self 换绑成功后通知本机
// agentd 清掉旧 keystone 内存；CLI 只走 HTTP 接缝，不直接 import keystone。
func TestCardRebindSelfEvictsAgentdSession(t *testing.T) {
	clearSeatSourceEnv(t)
	dir := t.TempDir()
	t.Setenv("HANDOFF_SESSION_CLI", "codex")
	t.Setenv("HANDOFF_SESSION_ID", "old")
	out, _, err := runLedgerCLI(t, dir, "card", "add", "self 驱逐卡", "--project", "demo", "--workflow", "bug")
	if err != nil {
		t.Fatalf("card add: %v", err)
	}
	var c struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &c); err != nil {
		t.Fatal(err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "bind", c.ID); err != nil {
		t.Fatal(err)
	}

	forgetSeen := make(chan string, 1)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/api/cards/"+c.ID+"/coordinator/forget" {
			forgetSeen <- r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer ts.Close()
	cfg, err := config.Load(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.Listen = strings.TrimPrefix(ts.URL, "http://")
	if err := config.Save(filepath.Join(dir, "config.yaml"), cfg); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HANDOFF_SESSION_ID", "new")
	if _, _, err := runLedgerCLI(t, dir, "card", "rebind", c.ID, "--self"); err != nil {
		t.Fatalf("rebind --self: %v", err)
	}
	select {
	case path := <-forgetSeen:
		if path != "/api/cards/"+c.ID+"/coordinator/forget" {
			t.Fatalf("agentd 驱逐路径 = %q", path)
		}
	default:
		t.Fatal("self 换绑成功后必须通知 agentd Forget")
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
