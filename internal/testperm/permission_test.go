// Package testperm 的测试覆盖权限探针三态决策和恢复时序。
//
// 职责：证明限制生效、限制失效、无关探针错误分别走继续、skip、fatal。
// 边界：不检查 euid，不依赖特定内核能力；恢复时序通过子测试和真实文件 mode 验证。
package testperm

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecideProbe(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		wantAction probeAction
		wantText   string
	}{
		{name: "write succeeded", err: nil, wantAction: probeSkip, wantText: "探针成功"},
		{name: "permission denied", err: &fs.PathError{Op: "open", Path: "p", Err: fs.ErrPermission}, wantAction: probeContinue, wantText: "限制已生效"},
		{name: "unrelated error", err: errors.New("file disappeared"), wantAction: probeFatal, wantText: "无关错误"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decideProbe("写", "/tmp/probe", tt.err)
			if got.action != tt.wantAction {
				t.Fatalf("action = %d, want %d", got.action, tt.wantAction)
			}
			if !strings.Contains(got.message, tt.wantText) {
				t.Fatalf("message = %q, want substring %q", got.message, tt.wantText)
			}
		})
	}
}

// 只断言「skip 之后 mode 已还原」这一个可观测事实。
//
// why 不叫 RestoresBeforeSkip：立即 restore() 与 t.Cleanup 的还原从外部看不出先后
// （Cleanup 是 LIFO，helper 在 t.TempDir 之后注册，还原必然先于目录删除），
// 断言不了的时序不该写进用例名——否则读者以为它守着这条，实际掐掉立即 restore()
// 它照样绿（B219 审查实测）。
func TestApplyProbeRestoresAfterSkip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe-file")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !t.Run("probe succeeded", func(t *testing.T) {
		apply(t, path, 0o600, 0o400, "写", func() error { return nil })
	}) {
		t.Fatal("probe succeeded 分支不应失败")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("skip 前必须恢复原 mode，got %#o", got)
	}
}

func TestApplyProbeKeepsRestrictionUntilCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "probe-file")
	if err := os.WriteFile(path, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !t.Run("permission denied", func(t *testing.T) {
		apply(t, path, 0o600, 0o400, "写", func() error { return fs.ErrPermission })
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o400 {
			t.Fatalf("限制生效分支应保持受限 mode，got %#o", got)
		}
	}) {
		t.Fatal("permission denied 分支不应失败")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("子测试 cleanup 后必须恢复原 mode，got %#o", got)
	}
}
