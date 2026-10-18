// desktop_coordinator_test.go —— B434 桌面端 wire 字面量金样本（可执行冻结）。
//
// 职责：逐字锁定跨进程/跨语言共用的字面量。对侧执法点：
//   - DesktopWakeMarker / DesktopWakeDirRel：plugins/claude-desktop-wake 的 mod
//     源码（实现节点落盘后由其 `claude plugin test` 用例逐字回放，契约 §4 条 127）；
//   - DesktopSessionPrefix / ClaudeContinueURLPrefix：驻场者门铃的 continue 深链（O2）。
//
// B435 X 删去 new 深链两字面量与 SeatDesktopVia* 三断言（契约 §7、plan b435.5 Q6 甲）。
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
		{"ClaudeContinueURLPrefix", ClaudeContinueURLPrefix, "claude://code/continue?session="},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q，契约冻结值 %q", c.name, c.got, c.want)
		}
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
