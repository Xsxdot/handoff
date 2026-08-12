package cmd

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestProjectAddRejectsNonRepo 验证 cwd 不是 git 仓库时本地就拒，报文说明原因。
// 为什么在本地拦：项目身份只依赖本机信息，多跑一次网络毫无意义。
func TestProjectAddRejectsNonRepo(t *testing.T) {
	t.Chdir(t.TempDir()) // 临时目录不是 git 仓库
	var out bytes.Buffer
	projectAddCmd.SetOut(&out)
	projectAddCmd.SetErr(&out)
	err := projectAddCmd.RunE(projectAddCmd, nil)
	if err == nil {
		t.Fatal("非 git 目录应被拒")
	}
	if !strings.Contains(err.Error(), "origin") {
		t.Errorf("报文应说明身份由 origin 派生，got %q", err.Error())
	}
}

// TestLocalOriginURL 验证从 cwd 读 origin；不是 git 仓库时返回空串而不是报错。
//
// 原属 cmd/repo_test.go（B62 cutover 后 localOriginURL 随 project.go 存活），
// 保留以防覆盖回归。
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
