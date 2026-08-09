// loginpath 测试：验证登录 shell 解析出的 PATH 被正确合并进当前进程环境。
package agentd

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMergeLoginShellPATHAppendsMissingDirs 验证登录 shell 里有、当前 PATH 里没有的
// 目录被追加；已有目录不重复追加；原有顺序不被打乱。
// why：真实踩坑是「用户终端里能跑 go，agentd 拉起的 executor 找不到 go」——
// agentd 常由非登录 shell 拉起，拿不到 .zprofile/.bash_profile 里的 PATH。
func TestMergeLoginShellPATHAppendsMissingDirs(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	orig := loginShellPATH
	t.Cleanup(func() { loginShellPATH = orig })
	loginShellPATH = func(context.Context, string) (string, error) {
		return "/usr/bin:/usr/local/go/bin:/opt/homebrew/bin", nil
	}

	MergeLoginShellPATH(context.Background(), slog.Default())

	got := os.Getenv("PATH")
	if !strings.HasPrefix(got, "/usr/bin:/bin") {
		t.Errorf("原有 PATH 必须保持在前（不动 systemd/launchd 注入的优先级），实得 %q", got)
	}
	for _, want := range []string{"/usr/local/go/bin", "/opt/homebrew/bin"} {
		if !strings.Contains(got, want) {
			t.Errorf("PATH 应追加 %s，实得 %q", want, got)
		}
	}
	if strings.Count(got, "/usr/bin") != 1 {
		t.Errorf("已存在的目录不应重复追加，实得 %q", got)
	}
}

// TestLoginShellPATHToleratesNonZeroExit 验证默认实现「只取 stdout、不以退出码
// 判定成败、stderr 不得混入」。
// why：交互式 shell（-i）在非 TTY 下会输出作业控制告警并可能非零退出，但 PATH
// 已经打出来了；按退出码判失败会让这条修复在真实机器上白做，而把 stderr 并进来
// 会直接把告警文本拼进 PATH。
func TestLoginShellPATHToleratesNonZeroExit(t *testing.T) {
	fake := filepath.Join(t.TempDir(), "fakeshell")
	script := "#!/bin/sh\nprintf %s /opt/x:/opt/y\necho 'warning: no job control' >&2\nexit 1\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := loginShellPATH(context.Background(), fake)
	if err != nil {
		t.Fatalf("非零退出但 stdout 有内容时不应报错，实得 %v", err)
	}
	if got != "/opt/x:/opt/y" {
		t.Errorf("PATH = %q，期望 /opt/x:/opt/y（stderr 的告警不得混入）", got)
	}
}

// TestMergeLoginShellPATHKeepsPATHOnFailure 验证解析失败时 PATH 原样不动、不拦启动。
func TestMergeLoginShellPATHKeepsPATHOnFailure(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	orig := loginShellPATH
	t.Cleanup(func() { loginShellPATH = orig })
	loginShellPATH = func(context.Context, string) (string, error) {
		return "", errors.New("shell 不存在")
	}

	MergeLoginShellPATH(context.Background(), slog.Default())

	if got := os.Getenv("PATH"); got != "/usr/bin:/bin" {
		t.Errorf("解析失败时 PATH 必须原样保留，实得 %q", got)
	}
}
