package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestLocalOriginURL 验证从 cwd 读 origin；不是 git 仓库时返回空串而不是报错。
func TestLocalOriginURL(t *testing.T) {
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(old) })
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if got := localOriginURL(); got != "" {
		t.Fatalf("非 git 目录应返回空串，got %q", got)
	}
	if out, err := exec.Command("git", "init", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	want := filepath.Join(dir, "fake-origin.git")
	if out, err := exec.Command("git", "-C", dir, "remote", "add", "origin", want).CombinedOutput(); err != nil {
		t.Fatalf("remote add: %v: %s", err, out)
	}
	if got := localOriginURL(); got != want {
		t.Fatalf("localOriginURL() = %q, want %q", got, want)
	}
}

// TestRepoAddRequiresPathOrClone 验证两种形态都没给时本地即报错，不发请求。
func TestRepoAddRequiresPathOrClone(t *testing.T) {
	repoAddPath, repoAddClone, repoAddURL = "", false, ""
	err := validateRepoAddFlags()
	if err == nil {
		t.Fatal("既没给路径也没给 --clone 时应当报错")
	}
}
