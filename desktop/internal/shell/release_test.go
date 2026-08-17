package shell_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/desktop/internal/shell"
)

func TestDecideReleaseThreeStates(t *testing.T) {
	cases := []struct {
		name     string
		existing string // 已有安装的路径，空=没有
		existVer string // 已有安装的版本，空=判不出
		embedVer string
		want     shell.ReleaseDecision
	}{
		{"没有安装就释出", "", "", "v1.2.0", shell.DecisionInstall},
		{"已有且更新就直接用", "/home/u/.local/bin/handoff", "v1.3.0", "v1.2.0", shell.DecisionUseExisting},
		{"已有且同版就直接用", "/home/u/.local/bin/handoff", "v1.2.0", "v1.2.0", shell.DecisionUseExisting},
		{"已有但更旧只提示", "/home/u/.local/bin/handoff", "v1.1.0", "v1.2.0", shell.DecisionNotifyOutdated},
		// 判不出已有版本时必须偏保守：用它，不覆盖。
		// 猜错的代价不对称——不覆盖最坏是用户少了个新特性，
		// 覆盖错了是把用户手装的二进制换掉。
		{"已有但版本判不出就直接用", "/home/u/.local/bin/handoff", "", "v1.2.0", shell.DecisionUseExisting},
		// 内嵌版本判不出（开发构建未注入）也必须偏保守：不知道内嵌的多新，
		// 就不能假设覆盖旧版安全，直接用用户的。
		{"已有但内嵌版本判不出就偏保守直接用", "/home/u/.local/bin/handoff", "v1.1.0", "", shell.DecisionUseExisting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := shell.DecideRelease(tc.existing, tc.existVer, tc.embedVer)
			if got != tc.want {
				t.Errorf("DecideRelease(%q,%q,%q)=%v, want %v",
					tc.existing, tc.existVer, tc.embedVer, got, tc.want)
			}
		})
	}
}

// 释出必须落在 0755，否则 launchd 拉不起来，
// 症状是「装好了但 agentd 起不来」，排查成本很高。
func TestReleaseBinaryWritesExecutable(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "bin", "handoff")
	if err := shell.ReleaseBinary(dst, strings.NewReader("#!/bin/sh\nexit 0\n")); err != nil {
		t.Fatalf("ReleaseBinary 失败：%v", err)
	}
	fi, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("释出后目标不存在：%v", err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Errorf("权限 %v，want 0755", fi.Mode().Perm())
	}
}

// 承重：已有文件在任何情况下都不得被 ReleaseBinary 覆盖。
// DecideRelease 说了不释出还是走到这里，属于调用方 bug——
// 此时必须报错，而不是「顺手帮他覆盖了」。
func TestReleaseBinaryRefusesToOverwrite(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "handoff")
	if err := os.WriteFile(dst, []byte("用户自己装的"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := shell.ReleaseBinary(dst, strings.NewReader("新的")); err == nil {
		t.Fatal("目标已存在时 ReleaseBinary 必须报错")
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "用户自己装的" {
		t.Fatalf("原文件被改动了：%q", b)
	}
}
