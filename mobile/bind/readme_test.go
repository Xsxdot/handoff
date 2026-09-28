// readme_test.go 锁 B392 §9.8 交接文档：mobile/README.md 必须明写壳侧会话消费
// 顺序、旧 cookie 的精确删除条件（name=handoff_session + host + Path=/，不区分
// 端口）、失败不导航，以及「Go 只返回 cookie 值，不操作平台 cookie store」的职责边界。
package bind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadmeDocumentsShellCallOrder 断言 README 含 §4.4 调用顺序标记（按出现
// 位置有序）、旧 cookie 删除条件、cookie 属性与失败语义。顺序标记缺失或倒序即红。
func TestReadmeDocumentsShellCallOrder(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(packageDir(t), "..", "README.md"))
	if err != nil {
		t.Fatalf("读 mobile/README.md: %v", err)
	}
	doc := string(raw)
	order := []string{
		"`SwitchMachine(target)`",
		"清除该 loopback host",
		"`SessionCookie(target)`",
		"写回 cookie jar",
		"导航到第 1 步返回的 origin",
	}
	pos := -1
	for _, marker := range order {
		idx := strings.Index(doc, marker)
		if idx < 0 {
			t.Fatalf("README 缺少调用顺序标记 %q", marker)
		}
		if idx < pos {
			t.Fatalf("README 调用顺序标记 %q 次序错误", marker)
		}
		pos = idx
	}
	for _, want := range []string{
		"name=handoff_session + host + Path=/", // 旧 cookie 删除条件（含 host、Path）
		"不区分端口",                                // 覆盖该 host 所有端口
		"不得导航",
		"不操作平台 cookie store",
		"Path=/",
		"HttpOnly=true",
		"SameSite=Lax",
		"Secure=false",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("README 缺少 %q", want)
		}
	}
}
