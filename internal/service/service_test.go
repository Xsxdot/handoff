// service 包的跨平台契约测试：接口完整性与哨兵错误。
//
// 这里只钉住「三个平台都实现了完整接口」和「没装时的错误可判别」，
// 具体命令序列在各平台自己的测试里。
package service

import (
	"errors"
	"strings"
	"testing"
)

// 三个平台都必须实现完整的 Manager。少一个方法在这里就编译不过，
// 而不是等到某个平台上运行时才发现。
func TestAllManagersImplementInterface(t *testing.T) {
	var _ Manager = (*launchdManager)(nil)
	var _ Manager = (*systemdManager)(nil)
	var _ Manager = (*windowsManager)(nil)
}

// ErrNotInstalled 必须可被 errors.Is 判别：上层要靠它区分「没装」与
// 「装了但操作失败」，两者的处置完全不同（前者去 install，后者去查日志）。
func TestErrNotInstalledIsIdentifiable(t *testing.T) {
	wrapped := errNotInstalled("/some/unit")
	if !errors.Is(wrapped, ErrNotInstalled) {
		t.Fatalf("errNotInstalled 的返回必须能被 errors.Is 认出 ErrNotInstalled，得到: %v", wrapped)
	}
	if !strings.Contains(wrapped.Error(), "/some/unit") {
		t.Errorf("报错必须带上单元路径，否则用户不知道该去看哪个文件: %v", wrapped)
	}
}
