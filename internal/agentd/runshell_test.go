// runshell_test.go —— handoff run 的 shell 解析测试。
//
// 职责：验证不同平台的 sh 解析顺序、Git for Windows 已知安装目录兜底与明确失败。
//
// 边界：只测试解析逻辑，不启动 shell 或执行用户命令；Windows 运行期仍待真机验证。
package agentd

import (
	"errors"
	"os/exec"
	"strings"
	"testing"
)

// TestResolveRunShellUnix 钉住非 Windows 平台行为不变：就是 sh。
func TestResolveRunShellUnix(t *testing.T) {
	got, err := resolveRunShell("darwin",
		func(string) (string, error) { return "/bin/sh", nil },
		func(string) error { return nil })
	if err != nil || got != "sh" {
		t.Fatalf("非 Windows 应恒为 sh：got=%q err=%v", got, err)
	}
}

// TestResolveRunShellWindowsPrefersPath 钉住 PATH 优先。
func TestResolveRunShellWindowsPrefersPath(t *testing.T) {
	got, err := resolveRunShell("windows",
		func(name string) (string, error) {
			if name == "sh" {
				return `C:\somewhere\sh.exe`, nil
			}
			return "", exec.ErrNotFound
		},
		func(string) error { return errors.New("不该走到 stat") })
	if err != nil || got != `C:\somewhere\sh.exe` {
		t.Fatalf("PATH 上有 sh 时应直接用：got=%q err=%v", got, err)
	}
}

// TestResolveRunShellWindowsFallsBackToKnownDir 钉住兜底目录。
//
// 这是真实用户机器上的常态：Git for Windows 默认安装只把 Git\cmd 加进 PATH，
// sh.exe 所在的 Git\bin 不在 PATH 上（真机实测）。
func TestResolveRunShellWindowsFallsBackToKnownDir(t *testing.T) {
	want := `C:\Program Files\Git\bin\sh.exe`
	got, err := resolveRunShell("windows",
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(p string) error {
			if p == want {
				return nil
			}
			return errors.New("不存在")
		})
	if err != nil || got != want {
		t.Fatalf("应回落到 Git 默认安装路径：got=%q err=%v，want=%q", got, err, want)
	}
}

// TestResolveRunShellWindowsAllMissing 钉住「全落空要给可行动的话」。
func TestResolveRunShellWindowsAllMissing(t *testing.T) {
	_, err := resolveRunShell("windows",
		func(string) (string, error) { return "", exec.ErrNotFound },
		func(string) error { return errors.New("不存在") })
	if err == nil {
		t.Fatal("全落空必须报错，不得静默降级到 cmd/PowerShell")
	}
	if !strings.Contains(err.Error(), "Git for Windows") {
		t.Fatalf("错误必须指出装什么才能修：got=%v", err)
	}
}

// TestRunShellCandidatesOrder 钉住候选顺序：默认安装位置在前。
func TestRunShellCandidatesOrder(t *testing.T) {
	got := runShellCandidates("windows")
	if len(got) == 0 || got[0] != `C:\Program Files\Git\bin\sh.exe` {
		t.Fatalf("首选应是 64 位默认安装位置：got=%v", got)
	}
	if len(runShellCandidates("darwin")) != 0 {
		t.Fatal("非 Windows 不应有候选目录")
	}
}
