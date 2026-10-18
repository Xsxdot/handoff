// card_needs_actor_b434_test.go —— B434.2：handoff card needs 打标 actor 改为「席位身份优先、
// 回落 ledgerActor 且不报错」（契约 §3.8.1「席位自打的前提」、条 189/190）。
//
// 职责：走 root command + 真实账本，读 needs_human 事件的 actor 列断言。
// 边界：唤醒规则 4（actor == driver_session 不叫醒）在 G2 的 internal/agentd 测试锁；
// 本文件只锁 CLI 写进账本的 actor 值。必须 env -u CLAUDE_CODE_SESSION_ID 下跑。
package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// b434Ledger 打开 dir 下的测试账本（调用方负责 Close）。B435 X 自已删的
// card_desktop_seat_b434_test.go 迁入（本文件是唯一留存的使用者）。
func b434Ledger(t *testing.T, dir string) *ledger.Store {
	t.Helper()
	st, err := ledger.Open(filepath.Join(dir, "ledger.db"))
	if err != nil {
		t.Fatalf("打开测试账本: %v", err)
	}
	return st
}

// b434Events 返回该卡指定类型的事件（seq 升序）。
func b434Events(t *testing.T, dir, id, typ string) []ledger.Event {
	t.Helper()
	st := b434Ledger(t, dir)
	defer st.Close()
	evs, err := st.EventsFromAsc([]string{id}, 0, 1000)
	if err != nil {
		t.Fatalf("读事件 %s: %v", id, err)
	}
	var out []ledger.Event
	for _, ev := range evs {
		if ev.Type == typ {
			out = append(out, ev)
		}
	}
	return out
}

// 条 189：能出示席位身份时，落账 actor 等于 currentSeatIdentity 的返回值；不打回落提示。
func TestB434CardNeedsUsesSeatIdentityAsActor(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "needs-seat-01")
	dir := t.TempDir()
	id := mustAddCard(t, dir, "席位打标卡")
	_, errOut, err := runLedgerCLI(t, dir, "card", "needs", id, "等用户裁决判据")
	if err != nil {
		t.Fatalf("card needs: %v", err)
	}
	evs := b434Events(t, dir, id, ledger.EvNeedsHuman)
	if len(evs) != 1 || evs[0].Actor != "cli:claude#needs-seat-01" {
		t.Fatalf("needs_human actor = %+v, want cli:claude#needs-seat-01", evs)
	}
	if strings.Contains(errOut, "等人标记记在") {
		t.Fatalf("出示成功时不应打回落提示: %q", errOut)
	}
}

// 条 190：出示失败（无会话身份 / 注入不完整）→ actor = ledgerActor()，命令退出 0，stderr 一行提示。
func TestB434CardNeedsFallsBackToLedgerActor(t *testing.T) {
	cases := map[string]func(t *testing.T){
		"无会话身份": func(t *testing.T) {},
		"注入不完整": func(t *testing.T) { t.Setenv("HANDOFF_SESSION_CLI", "codex") },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			clearSeatSourceEnv(t)
			setup(t)
			dir := t.TempDir()
			id := mustAddCard(t, dir, "终端打标卡")
			out, errOut, err := runLedgerCLI(t, dir, "card", "needs", id, "等用户授权")
			if err != nil || ExitCode(err) != 0 {
				t.Fatalf("回落不得使命令失败: err=%v", err)
			}
			if !strings.Contains(out, "已标记等人") {
				t.Fatalf("stdout 行为应不变: %q", out)
			}
			evs := b434Events(t, dir, id, ledger.EvNeedsHuman)
			if len(evs) != 1 || evs[0].Actor != ledgerActor() {
				t.Fatalf("needs_human actor = %+v, want %q", evs, ledgerActor())
			}
			if !strings.Contains(errOut, "等人标记记在 "+ledgerActor()) {
				t.Fatalf("回落应在 stderr 留一行提示: %q", errOut)
			}
		})
	}
}

// --clear 不变：即使能出示席位身份，清标 actor 仍是 ledgerActor()。
func TestB434CardNeedsClearKeepsLedgerActor(t *testing.T) {
	clearSeatSourceEnv(t)
	t.Setenv("CLAUDE_CODE_SESSION_ID", "needs-seat-02")
	dir := t.TempDir()
	id := mustAddCard(t, dir, "清标卡")
	if _, _, err := runLedgerCLI(t, dir, "card", "needs", id, "先停一下"); err != nil {
		t.Fatalf("打标: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "needs", id, "--clear"); err != nil {
		t.Fatalf("清标: %v", err)
	}
	evs := b434Events(t, dir, id, ledger.EvNeedsCleared)
	if len(evs) != 1 || evs[0].Actor != ledgerActor() {
		t.Fatalf("needs_cleared actor = %+v, want %q（--clear 不变）", evs, ledgerActor())
	}
}
