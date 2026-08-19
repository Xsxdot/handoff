// files_test.go —— 纪律块文件操作面的测试：列举、读、写（新建/覆盖/冲突）与名字校验。
package discipline_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/discipline"
)

// sha256 of "hello\n"
const helloSHA = "5891b5b522d5df086d0ff0b110fbd9d21bb4fc7163af34d08286a2e846f6be03"

func TestListEmptyWhenDirMissing(t *testing.T) {
	// 目录不存在不是错误：<DataDir>/discipline 没有任何东西自动创建，
	// 首次打开设置页时它本来就不存在。
	files, err := discipline.List(filepath.Join(t.TempDir(), "nope"))
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("len = %d, want 0", len(files))
	}
}

func TestListReturnsNameSizeHashSorted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	} // 子目录必须被跳过

	files, err := discipline.List(dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("len = %d, want 2（子目录应被跳过）", len(files))
	}
	if files[0].Name != "a.md" || files[1].Name != "b.md" {
		t.Fatalf("顺序 = %q/%q, want a.md/b.md", files[0].Name, files[1].Name)
	}
	if files[1].Size != 6 || files[1].SHA256 != helloSHA {
		t.Errorf("b.md = size %d sha %s", files[1].Size, files[1].SHA256)
	}
}

func TestReadReturnsContentAndHash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	content, sha, size, err := discipline.Read(dir, "a.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if content != "hello\n" || sha != helloSHA || size != 6 {
		t.Errorf("Read = %q / %s / %d", content, sha, size)
	}
}

func TestReadMissingIsNotExist(t *testing.T) {
	if _, _, _, err := discipline.Read(t.TempDir(), "a.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("err = %v, want fs.ErrNotExist", err)
	}
}

func TestWriteCreatesDirAndFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "discipline")
	sha, size, err := discipline.Write(dir, "a.md", "hello\n", "")
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if sha != helloSHA || size != 6 {
		t.Errorf("Write = %s / %d", sha, size)
	}
	b, err := os.ReadFile(filepath.Join(dir, "a.md"))
	if err != nil || string(b) != "hello\n" {
		t.Fatalf("落盘内容 = %q, err=%v", b, err)
	}
}

func TestWriteNewOnExistingIsErrExists(t *testing.T) {
	// base 为空串 = 「新建」，此时目标必须不存在，否则会静默覆盖别人的文件
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := discipline.Write(dir, "a.md", "new", ""); !errors.Is(err, discipline.ErrExists) {
		t.Fatalf("err = %v, want ErrExists", err)
	}
}

func TestWriteBaseMismatchReturnsCurrentHash(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sha, _, err := discipline.Write(dir, "a.md", "new", "deadbeef")
	if !errors.Is(err, discipline.ErrBaseMismatch) {
		t.Fatalf("err = %v, want ErrBaseMismatch", err)
	}
	if sha != helloSHA {
		t.Errorf("冲突时应回磁盘现状哈希，得到 %s", sha)
	}
}

func TestWriteBaseMatchOverwrites(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.md"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := discipline.Write(dir, "a.md", "new", helloSHA); err != nil {
		t.Fatalf("Write: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "a.md"))
	if string(b) != "new" {
		t.Fatalf("内容 = %q, want new", b)
	}
}

func TestWriteRejectsBadNames(t *testing.T) {
	for _, name := range []string{"", ".", "..", "a/b.md", "sub" + string(filepath.Separator) + "x.md"} {
		if _, _, err := discipline.Write(t.TempDir(), name, "x", ""); !errors.Is(err, discipline.ErrBadName) {
			t.Errorf("name=%q err = %v, want ErrBadName", name, err)
		}
	}
}

func TestWriteRejectsOversize(t *testing.T) {
	big := make([]byte, discipline.MaxBlockSize+1)
	if _, _, err := discipline.Write(t.TempDir(), "a.md", string(big), ""); !errors.Is(err, discipline.ErrTooLarge) {
		t.Fatalf("err = %v, want ErrTooLarge", err)
	}
}

func TestBuiltinsAndDefaultTier(t *testing.T) {
	bs := discipline.Builtins()
	if len(bs) != 2 {
		t.Fatalf("len = %d, want 2", len(bs))
	}
	if bs[0].Tier != discipline.TierSubagent || bs[1].Tier != discipline.TierSingleContext {
		t.Fatalf("顺序 = %q/%q", bs[0].Tier, bs[1].Tier)
	}
	if bs[0].Content == "" || bs[1].Content == "" {
		t.Fatal("内置正文不能为空")
	}
	for exec, want := range map[string]string{
		"opencode": discipline.TierSubagent,
		"claude":   discipline.TierSubagent,
		"codex":    discipline.TierSingleContext,
		"grok":     discipline.TierSingleContext,
		"fake":     discipline.TierSingleContext, // 未登记一律保守取单上下文版
	} {
		if got := discipline.DefaultTierFor(exec); got != want {
			t.Errorf("DefaultTierFor(%q) = %q, want %q", exec, got, want)
		}
	}
}
