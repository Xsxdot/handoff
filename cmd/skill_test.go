// handoff skill 的 CLI 行为测试。全部经 HOME 注入与包级 skillContent 注入，
// 不联网、不动真实家目录。
package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/skill"
)

// runSkill 执行一次 handoff skill，返回 stdout 与错误。
//
// 为什么经 rootCmd 而不直接 Execute skillCmd：cobra v1.10.2 的 ExecuteC 只认
// 根命令，直接对 skillCmd.SetArgs + Execute 会被 Root().ExecuteC() 忽略并退回
// 根命令打印 help（root_test.go:235 已有注释，本文件写时也实跑验证过）——所以
// 照 runUpgrade/runStatus 的纪律，完整路径（"skill ..."）经 rootCmd.SetArgs 传。
func runSkill(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetFlags(t)
	var buf bytes.Buffer
	rootCmd.SetOut(&buf)
	rootCmd.SetErr(&buf)
	rootCmd.SetArgs(append([]string{"skill"}, args...))
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	err := rootCmd.ExecuteContext(context.Background())
	return buf.String(), err
}

// TestSkillInstallReportsEverySite：命令层必须把每个落点的处置逐行打出来，
// 包括跳过的。只打成功的那几行，用户就无从知道 codex 那份为什么没更新。
func TestSkillInstallReportsEverySite(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	t.Setenv("HOME", home)
	SetSkillContent("测试内容")

	out, err := runSkill(t, "install")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{".handoff/skill", ".claude", ".codex", "opencode", ".grok"} {
		if !strings.Contains(out, want) {
			t.Fatalf("输出缺少落点 %s:\n%s", want, out)
		}
	}
}

// TestSkillReportsStale：改坏一处后 handoff skill 必须准确点名。
func TestSkillReportsStale(t *testing.T) {
	home := t.TempDir()
	os.MkdirAll(filepath.Join(home, ".grok"), 0o755)
	t.Setenv("HOME", home)
	SetSkillContent("新内容")
	skill.Install("新内容", home)
	p := filepath.Join(home, ".grok", "skills", "handoff")
	os.RemoveAll(p)
	os.MkdirAll(p, 0o755)
	os.WriteFile(filepath.Join(p, "SKILL.md"), []byte("旧"), 0o644)

	out, err := runSkill(t)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, ".grok") || !strings.Contains(out, "旧") {
		t.Fatalf("应点名 .grok 那处旧了:\n%s", out)
	}
}

// TestSkillContentEmptyIsRefused 是一条防漏接线的断言：main.go 忘了调
// SetSkillContent 时，handoff skill install 会静静地装一份空文件——
// 症状是「装成功了但 skill 是空的」，肉眼极难发现。
func TestSkillContentEmptyIsRefused(t *testing.T) {
	SetSkillContent("")
	if _, err := runSkill(t, "install"); err == nil {
		t.Fatal("内嵌内容为空必须拒绝安装，而不是装一份空文件")
	}
}
