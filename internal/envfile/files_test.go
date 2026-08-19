// files_test.go —— env 文件操作面的测试：列举、读、写（新建/覆盖/冲突）与名字校验。
package envfile_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/envfile"
)

// helloSHA 是 "hello\n" 的 sha256，用于钉住 Read/Write 的哈希口径。
const helloSHA = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"

func TestListEmptyWhenDirMissing(t *testing.T) {
	// 目录不存在不是错误：<DataDir>/env 没有任何东西自动创建，
	// 首次打开设置页时它本来就不存在，报错会把「还没建」画成「读不了」。
	files, err := envfile.List(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("len = %d, want 0", len(files))
	}
}

func TestListSortedWithSizeAndHash(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "b.env"), "hello\n")
	mustWrite(t, filepath.Join(dir, "a.env"), "X=1\n")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	files, err := envfile.List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len = %d, want 2（子目录必须被跳过）", len(files))
	}
	if files[0].Name != "a.env" || files[1].Name != "b.env" {
		t.Fatalf("顺序 = %v，想要按名字升序", []string{files[0].Name, files[1].Name})
	}
	if files[1].Size != 6 || files[1].SHA256 != helloSHA {
		t.Fatalf("b.env = %d/%s，想要 6/%s", files[1].Size, files[1].SHA256, helloSHA)
	}
}

func TestReadRejectsBadName(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../x.env", "a/b.env", "", ".", ".."} {
		if _, _, _, err := envfile.Read(dir, name); !errors.Is(err, envfile.ErrBadName) {
			t.Fatalf("Read(%q) err = %v，想要 ErrBadName", name, err)
		}
	}
}

func TestReadMissingIsNotExist(t *testing.T) {
	if _, _, _, err := envfile.Read(t.TempDir(), "gone.env"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v，想要 fs.ErrNotExist", err)
	}
}

func TestWriteCreatesDirAndFileWith0600(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "env")
	sha, size, err := envfile.Write(dir, "a.env", "hello\n", "")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if sha != helloSHA || size != 6 {
		t.Fatalf("sha/size = %s/%d，想要 %s/6", sha, size, helloSHA)
	}
	// env 文件常含凭据，权限基线必须是 0600，不给同机别的账号留缝。
	fi, err := os.Stat(filepath.Join(dir, "a.env"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("perm = %o，想要 600", perm)
	}
}

func TestWriteNewOnExistingIsErrExists(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.env"), "hello\n")
	// base 为空串 = 新建；撞名必须显式失败，避免「新建」把别人的文件静默覆盖。
	if _, _, err := envfile.Write(dir, "a.env", "X=1\n", ""); !errors.Is(err, envfile.ErrExists) {
		t.Fatalf("err = %v，想要 ErrExists", err)
	}
}

func TestWriteStaleBaseIsErrBaseMismatch(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.env"), "hello\n")
	if _, _, err := envfile.Write(dir, "a.env", "X=1\n", "deadbeef"); !errors.Is(err, envfile.ErrBaseMismatch) {
		t.Fatalf("err = %v，想要 ErrBaseMismatch", err)
	}
}

func TestWriteTooLarge(t *testing.T) {
	big := make([]byte, envfile.MaxFileSize+1)
	for i := range big {
		big[i] = 'x'
	}
	if _, _, err := envfile.Write(t.TempDir(), "a.env", string(big), ""); !errors.Is(err, envfile.ErrTooLarge) {
		t.Fatalf("err = %v，想要 ErrTooLarge", err)
	}
}

func TestWriteOverwriteWithMatchingBase(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "a.env"), "hello\n")
	sha, _, err := envfile.Write(dir, "a.env", "X=1\n", helloSHA)
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	content, cur, _, err := envfile.Read(dir, "a.env")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != "X=1\n" || cur != sha {
		t.Fatalf("content/sha = %q/%s，想要 \"X=1\\n\"/%s", content, cur, sha)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
