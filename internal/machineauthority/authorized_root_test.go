// machineauthority AuthorizedRoot 安全边界测试。
//
// 职责：
//   - 锁定 absolute、上跳、NUL 与越界 symlink 的拒绝语义
//   - 证明指向授权根内部的 symlink 仍可读取
//
// 边界：
//   - 使用真实临时目录与 os.Root，不以 filepath 前缀 fake 代替内核边界
package machineauthority

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/workspaceapi"
)

func TestAuthorizedRootRejectsTraversalAndEscapingSymlinks(t *testing.T) {
	rootDir := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(rootDir, "inside.txt"), []byte("inside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(rootDir, "inside-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(rootDir, "outside-link")); err != nil {
		t.Fatal(err)
	}

	root, err := OpenAuthorizedRoot(rootDir)
	if err != nil {
		t.Fatalf("OpenAuthorizedRoot: %v", err)
	}
	defer root.Close()

	for _, name := range []string{"/etc/passwd", "../secret.txt", "a/../../secret", "bad\x00name", `dir\\file`} {
		t.Run(name, func(t *testing.T) {
			_, err := root.ReadFile(name)
			assertResourceCode(t, err, workspaceapi.ErrorPathOutsideWorkspace)
		})
	}
	if _, err := root.ReadFile("outside-link"); err == nil {
		t.Fatal("越界 symlink 读取应失败")
	} else {
		assertResourceCode(t, err, workspaceapi.ErrorPathOutsideWorkspace)
	}
	got, err := root.ReadFile("inside-link")
	if err != nil || string(got) != "inside" {
		t.Fatalf("根内 symlink read = %q, %v", got, err)
	}
}

func assertResourceCode(t *testing.T, err error, want workspaceapi.ErrorCode) {
	t.Helper()
	var resourceErr *workspaceapi.Error
	if !errors.As(err, &resourceErr) {
		t.Fatalf("error = %T %v, want *workspaceapi.Error", err, err)
	}
	if resourceErr.Code != want {
		t.Fatalf("code = %s, want %s", resourceErr.Code, want)
	}
}
