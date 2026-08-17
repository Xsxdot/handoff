package shell_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

// launchd / systemd 都要求绝对路径。返回相对路径等于装了一个
// 永远起不来的 service，而且失败要到用户机器上才暴露。
func TestResolveBinPathIsAlwaysAbsolute(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "handoff")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := shell.ResolveBinPath(bin)
	if err != nil {
		t.Fatalf("ResolveBinPath 失败：%v", err)
	}
	if !filepath.IsAbs(got) {
		t.Fatalf("返回了相对路径 %q", got)
	}
}

func TestResolveBinPathRejectsMissing(t *testing.T) {
	_, err := shell.ResolveBinPath(filepath.Join(t.TempDir(), "不存在"))
	if err == nil {
		t.Fatal("目标不存在时必须报错")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Errorf("报错应当带上被查找的路径，实际：%v", err)
	}
}

// 符号链接必须解开：~/.local/bin/handoff 常是指向别处的软链，
// 把软链写进 plist 后用户一改链接 agentd 就起不来。
func TestResolveBinPathFollowsSymlink(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real-handoff")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "handoff")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("本环境不支持符号链接：%v", err)
	}
	got, err := shell.ResolveBinPath(link)
	if err != nil {
		t.Fatalf("ResolveBinPath 失败：%v", err)
	}
	if filepath.Base(got) != "real-handoff" {
		t.Errorf("符号链接没有被解开：%q", got)
	}
}

// 把路径写成目录（如 ~/.local/bin 本身）也必须报错：Stat 会通过、
// 但不是常规文件。真实场景「用户把路径写错成目录」恰命中此分支。
func TestResolveBinPathRejectsDirectory(t *testing.T) {
	dir := t.TempDir()
	_, err := shell.ResolveBinPath(dir)
	if err == nil {
		t.Fatal("目录路径必须报错")
	}
	if !strings.Contains(err.Error(), "不是常规文件") {
		t.Errorf("报错应说明不是常规文件，实际：%v", err)
	}
}
