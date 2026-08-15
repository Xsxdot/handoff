// workspace_write_test.go —— 在线编辑写入的契约钉测（B81 Task 3）。
//
// 职责：钉住 WriteFile 的冲突保护、拒绝面、mode 保留与临时文件清理。
//
// 边界：不触真实工作区，全部在 t.TempDir() 里造文件。
package agentd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sha256hex 是测试里算基线哈希的小工具，与 ReadFile 的算法保持一致。
func sha256hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// seedFile 在临时工作树里放一个文件，返回工作树根与它的基线哈希。
func seedFile(t *testing.T, name, body string, mode fs.FileMode) (string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return dir, sha256hex(body)
}

// TestWriteFileHappyPath 验证基线匹配时写入成功，返回新哈希与新大小，磁盘确已更新。
func TestWriteFileHappyPath(t *testing.T) {
	dir, base := seedFile(t, "go.mod", "module handoff\n", 0o644)
	next := "module handoff\n\ngo 1.26.1\n"
	got, err := WriteFile(dir, "go.mod", next, base)
	if err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got.SHA256 != sha256hex(next) {
		t.Errorf("返回的 SHA256=%q, want %q", got.SHA256, sha256hex(next))
	}
	if got.Size != int64(len(next)) {
		t.Errorf("返回的 Size=%d, want %d", got.Size, len(next))
	}
	onDisk, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != next {
		t.Errorf("磁盘内容=%q, want %q", onDisk, next)
	}
}

// TestWriteFileConflict 验证基线对不上时返回 ErrBaseMismatch，且**带回磁盘现状**
// （省掉前端一次往返，冲突条要靠它显示「磁盘上现在是什么」）。
func TestWriteFileConflict(t *testing.T) {
	dir, _ := seedFile(t, "a.txt", "executor 改过的内容\n", 0o644)
	cur, err := WriteFile(dir, "a.txt", "我的改动\n", sha256hex("我读到的旧内容\n"))
	if !errors.Is(err, ErrBaseMismatch) {
		t.Fatalf("err=%v, want ErrBaseMismatch", err)
	}
	if cur.Content != "executor 改过的内容\n" {
		t.Errorf("冲突返回的 Content=%q, want 磁盘现状", cur.Content)
	}
	if cur.SHA256 != sha256hex("executor 改过的内容\n") {
		t.Errorf("冲突返回的 SHA256 必须是磁盘现状的哈希，才能当下一次的基线")
	}
	onDisk, _ := os.ReadFile(filepath.Join(dir, "a.txt"))
	if string(onDisk) != "executor 改过的内容\n" {
		t.Error("冲突时磁盘必须原封不动")
	}
}

// TestWriteFileEmptyBaseIsConflict 验证空基线一律当冲突处理：调用方没读过就想写，
// 正是覆盖别人改动的场景。
func TestWriteFileEmptyBaseIsConflict(t *testing.T) {
	dir, _ := seedFile(t, "a.txt", "x\n", 0o644)
	if _, err := WriteFile(dir, "a.txt", "y\n", ""); !errors.Is(err, ErrBaseMismatch) {
		t.Fatalf("err=%v, want ErrBaseMismatch", err)
	}
}

// TestWriteFileRejects 逐条钉住拒绝面。
func TestWriteFileRejects(t *testing.T) {
	t.Run("git 目录", func(t *testing.T) {
		dir, base := seedFile(t, ".git/config", "[core]\n", 0o644)
		if _, err := WriteFile(dir, ".git/config", "[core]\n\tpager = sh -c evil\n", base); !errors.Is(err, ErrGitDirWrite) {
			t.Fatalf("err=%v, want ErrGitDirWrite", err)
		}
	})
	t.Run("git 指针文件本身", func(t *testing.T) {
		dir, base := seedFile(t, ".git", "gitdir: /elsewhere\n", 0o644)
		if _, err := WriteFile(dir, ".git", "gitdir: /evil\n", base); !errors.Is(err, ErrGitDirWrite) {
			t.Fatalf("err=%v, want ErrGitDirWrite", err)
		}
	})
	t.Run("符号链接", func(t *testing.T) {
		dir, base := seedFile(t, "real.txt", "x\n", 0o644)
		if err := os.Symlink("real.txt", filepath.Join(dir, "link.txt")); err != nil {
			t.Skipf("本平台建不了符号链接: %v", err)
		}
		if _, err := WriteFile(dir, "link.txt", "y\n", base); !errors.Is(err, ErrSymlinkTarget) {
			t.Fatalf("err=%v, want ErrSymlinkTarget", err)
		}
	})
	t.Run("目录", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteFile(dir, "sub", "x", "deadbeef"); !errors.Is(err, ErrPathIsDir) {
			t.Fatalf("err=%v, want ErrPathIsDir", err)
		}
	})
	t.Run("现盘是二进制", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "logo.png"), []byte("\x89PNG\x00\x00"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteFile(dir, "logo.png", "text", "deadbeef"); !errors.Is(err, ErrBinaryFile) {
			t.Fatalf("err=%v, want ErrBinaryFile", err)
		}
	})
	t.Run("现盘超限", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "big.txt"), bytes.Repeat([]byte("x"), maxRunOutput+1), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := WriteFile(dir, "big.txt", "small", "deadbeef"); !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("err=%v, want ErrFileTooLarge", err)
		}
	})
	t.Run("新内容超限", func(t *testing.T) {
		dir, base := seedFile(t, "a.txt", "x\n", 0o644)
		huge := strings.Repeat("y", maxRunOutput+1)
		if _, err := WriteFile(dir, "a.txt", huge, base); !errors.Is(err, ErrFileTooLarge) {
			t.Fatalf("err=%v, want ErrFileTooLarge", err)
		}
	})
	t.Run("路径逃逸", func(t *testing.T) {
		dir, _ := seedFile(t, "a.txt", "x\n", 0o644)
		if _, err := WriteFile(dir, "../outside.txt", "y", "deadbeef"); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("err=%v, want ErrPathEscape", err)
		}
	})
	t.Run("不存在", func(t *testing.T) {
		dir := t.TempDir()
		if _, err := WriteFile(dir, "nope.txt", "y", "deadbeef"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("err=%v, want fs.ErrNotExist", err)
		}
	})
}

// TestWriteFileKeepsMode 验证可执行位不被写丢——那是个静默故障：脚本还在，
// 但下次跑它会 permission denied。
func TestWriteFileKeepsMode(t *testing.T) {
	dir, base := seedFile(t, "run.sh", "#!/bin/sh\necho a\n", 0o755)
	if _, err := WriteFile(dir, "run.sh", "#!/bin/sh\necho b\n", base); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("mode=%v, want 0755", fi.Mode().Perm())
	}
}

// TestWriteFileNoTmpLeftBehind 验证被拒的写入不在工作树里留 tmp 文件。
// 留下的话会进 git status，下一次 dispatch 的「工作区必须干净」检查直接拒发。
func TestWriteFileNoTmpLeftBehind(t *testing.T) {
	dir, _ := seedFile(t, "a.txt", "x\n", 0o644)
	_, _ = WriteFile(dir, "a.txt", "y\n", "对不上的哈希")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("留下了临时文件 %s", e.Name())
		}
	}
}

// TestIsGitPath 钉住 .git 判据的边界：.gitignore 这类前缀相同但不同的路径不能误伤。
func TestIsGitPath(t *testing.T) {
	yes := []string{".git", ".git/config", ".git/hooks/pre-commit", "./.git/HEAD"}
	no := []string{".gitignore", ".gitattributes", "a/.gitmodules", "src/git/x.go"}
	for _, p := range yes {
		if !isGitPath(filepath.Clean(p)) {
			t.Errorf("isGitPath(%q)=false, want true", p)
		}
	}
	for _, p := range no {
		if isGitPath(filepath.Clean(p)) {
			t.Errorf("isGitPath(%q)=true, want false", p)
		}
	}
}
