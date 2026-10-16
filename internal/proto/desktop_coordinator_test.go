// desktop_coordinator_test.go —— B434 桌面端 wire 字面量金样本（可执行冻结）。
//
// 职责：逐字锁定跨进程/跨语言共用的字面量。对侧执法点：
//   - DesktopWakeMarker / DesktopWakeDirRel：plugins/claude-desktop-wake 的 mod
//     源码（实现节点落盘后由其 `claude plugin test` 用例逐字回放，契约 §4 D-23）；
//   - ClaudeNewQMaxChars / ClaudeNewSessionURLPrefix：web/src/app/lib/claudeDeepLink.ts；
//   - SeatDesktopVia*：简报模板写进 `card bind --via` 的取值与事件载荷。
//
// 改任一字面量 = 契约变更，须重走 contract 节点。
package proto

import "testing"

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
