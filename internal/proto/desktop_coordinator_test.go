// desktop_coordinator_test.go —— B434 桌面端 wire 字面量金样本（可执行冻结）。
//
// 职责：逐字锁定跨进程/跨语言共用的字面量。对侧执法点：
//   - DesktopWakeMarker / DesktopWakeDirRel：plugins/claude-desktop-wake 的 mod
//     源码（实现节点落盘后由其 `claude plugin test` 用例逐字回放，契约 §4 条 127）；
//   - ClaudeNewQMaxChars / ClaudeNewSessionURLPrefix：web/src/app/lib/claudeDeepLink.ts；
//   - SeatDesktopVia*：简报模板写进 `card bind --via` 的取值与事件载荷。
//
// 改任一字面量 = 契约变更，须重走 contract 节点。
package proto

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestB434DesktopWireLiterals(t *testing.T) {
	cases := []struct{ name, got, want string }{
		{"DesktopWakeMarker", DesktopWakeMarker, "[handoff]"},
		{"DesktopWakeDirRel", DesktopWakeDirRel, ".handoff/desktop-wake"},
		{"DesktopSessionPrefix", DesktopSessionPrefix, "local_"},
		{"ClaudeNewSessionURLPrefix", ClaudeNewSessionURLPrefix, "claude://code/new?"},
		{"ClaudeContinueURLPrefix", ClaudeContinueURLPrefix, "claude://code/continue?session="},
		{"SeatDesktopViaPage", SeatDesktopViaPage, "page"},
		{"SeatDesktopViaLauncher", SeatDesktopViaLauncher, "launcher"},
		{"SeatDesktopViaManual", SeatDesktopViaManual, "manual"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q，契约冻结值 %q", c.name, c.got, c.want)
		}
	}
	if ClaudeNewQMaxChars != 14336 {
		t.Errorf("ClaudeNewQMaxChars = %d，契约冻结值 14336", ClaudeNewQMaxChars)
	}
}

// TestB434CoordinatorWakeRespLetterUnopenedWire 锁契约条 175/176：letter_unopened
// 只在 true 时出现在 wire 上，既有转交回包形状不变。
func TestB434CoordinatorWakeRespLetterUnopenedWire(t *testing.T) {
	plain, err := json.Marshal(CoordinatorWakeResp{HandledBy: "mac-02"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "letter_unopened") {
		t.Fatalf("LetterUnopened=false 不应出键: %s", plain)
	}
	unopened, err := json.Marshal(CoordinatorWakeResp{HandledBy: "mac-02", LetterUnopened: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(unopened), `"letter_unopened":true`) {
		t.Fatalf("LetterUnopened=true 必须出键: %s", unopened)
	}
}

// TestB434DesktopDeliverReqWireKeys 锁契约条 211/212（r5）：拉起器投递请求的 wire
// 键集恰为 {cli_session, desktop_session, card}，不再携带来信正文。
func TestB434DesktopDeliverReqWireKeys(t *testing.T) {
	raw, err := json.Marshal(DesktopDeliverReq{CLISession: "c1", DesktopSession: "local_x", Card: "B434"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"cli_session": true, "desktop_session": true, "card": true}
	if len(fields) != len(want) {
		t.Fatalf("DesktopDeliverReq 键集 = %s，契约冻结 {cli_session, desktop_session, card}", raw)
	}
	for k := range fields {
		if !want[k] {
			t.Fatalf("DesktopDeliverReq 出现契约外的键 %q: %s", k, raw)
		}
	}
}
