// 本文件锁定 B434 深链 raw message 协议字面量（对侧常量执法：
// web/src/app/lib/desktopShell.ts#OPEN_DEEPLINK_MESSAGE_PREFIX 与本常量逐字相等，
// 两侧各锁一次，同 ExternalBrowserMessagePrefix 的既有形态）。
// 放行/拒绝的行为表驱动测试归实现节点（契约 §4 条 D-14..D-19）。
package shell_test

import (
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

func TestExternalDeepLinkMessagePrefixLiteral(t *testing.T) {
	if shell.ExternalDeepLinkMessagePrefix != "handoff:open-deeplink:" {
		t.Fatalf("prefix = %q", shell.ExternalDeepLinkMessagePrefix)
	}
	if shell.ExternalDeepLinkMessagePrefix == shell.ExternalBrowserMessagePrefix {
		t.Fatalf("深链前缀不得与系统浏览器前缀相同")
	}
}
