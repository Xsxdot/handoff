package shell

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeProjectDirAcceptsExistingDir(t *testing.T) {
	dir := t.TempDir()
	got, err := NormalizeProjectDir(dir)
	if err != nil {
		t.Fatalf("报错: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("返回值不是绝对路径: %q", got)
	}
}

func TestNormalizeProjectDirRejectsMissing(t *testing.T) {
	_, err := NormalizeProjectDir(filepath.Join(t.TempDir(), "不存在"))
	if err == nil {
		t.Fatal("目录不存在却没报错")
	}
}

// 选到文件而不是目录：报文必须说清「这是文件不是目录」，
// 只说「无效路径」会让人以为是路径拼错了。
func TestNormalizeProjectDirRejectsFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "a.txt")
	if err := os.WriteFile(f, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NormalizeProjectDir(f)
	if err == nil {
		t.Fatal("选中文件却没报错")
	}
	if !strings.Contains(err.Error(), "目录") {
		t.Fatalf("报文没说清是目录问题，实际 = %q", err)
	}
}

func TestNormalizeProjectDirRejectsEmpty(t *testing.T) {
	if _, err := NormalizeProjectDir("   "); err == nil {
		t.Fatal("空输入却没报错")
	}
}
