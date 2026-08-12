// pathenv 测试：验证四层来源的合并顺序、去重、以及每一层失败时的降级行为。
package pathenv

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubSources 把三个测试缝换成可编排的假实现，t.Cleanup 负责还原。
//
// 参数：
//   - home: homeDir 的返回值；空串表示「取不到 HOME」
//   - exist: 视为存在的目录集合（dirExists 只对集合内的路径返回 true）
func stubSources(t *testing.T, home string, exist ...string) {
	t.Helper()
	oldHome, oldExists, oldRel, oldAbs := homeDir, dirExists, homeRelDirs, absDirs
	t.Cleanup(func() { homeDir, dirExists, homeRelDirs, absDirs = oldHome, oldExists, oldRel, oldAbs })

	set := map[string]bool{}
	for _, d := range exist {
		set[d] = true
	}
	dirExists = func(p string) bool { return set[p] }
	homeDir = func() (string, error) {
		if home == "" {
			return "", errors.New("取不到 HOME")
		}
		return home, nil
	}
}

// stubLoginShell 把登录 shell 那一层换成固定返回值。
func stubLoginShell(t *testing.T, out string, err error) {
	t.Helper()
	old := loginShellPATH
	t.Cleanup(func() { loginShellPATH = old })
	loginShellPATH = func(context.Context, string) (string, error) { return out, err }
}

// nopLogger 是丢弃一切的 logger：测试断言的是 PATH 与返回值，不是日志文本。
func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// 已知目录表里存在的目录被追加，不存在的不追加，且原有 PATH 顺序不变。
//
// why：这是 B71 的核心——~/.opencode/bin 不在任何 rc 文件里，登录 shell 那一层
// 够不着它，只有已知目录表能兜住。
func TestApplyAppendsExistingKnownDirs(t *testing.T) {
	t.Setenv("PATH", "/usr/bin:/bin")
	home := t.TempDir()
	opencode := filepath.Join(home, ".opencode/bin")
	stubSources(t, home, opencode, "/opt/homebrew/bin")
	homeRelDirs = []string{".opencode/bin", ".grok/bin"}
	absDirs = []string{"/opt/homebrew/bin", "/snap/bin"}

	added := Apply(context.Background(), Options{}, nopLogger())

	got := os.Getenv("PATH")
	if !strings.HasPrefix(got, "/usr/bin:/bin") {
		t.Errorf("原有 PATH 必须保持在前，实得 %q", got)
	}
	for _, want := range []string{opencode, "/opt/homebrew/bin"} {
		if !strings.Contains(got, want) {
			t.Errorf("PATH 应含 %s，实得 %q", want, got)
		}
	}
	if strings.Contains(got, ".grok/bin") || strings.Contains(got, "/snap/bin") {
		t.Errorf("不存在的目录不应追加，实得 %q", got)
	}
	if len(added) != 2 {
		t.Errorf("added 应为 2 个目录，实得 %v", added)
	}
}

// 已在继承 PATH 里的目录不重复追加，也不出现在 added 里。
func TestApplySkipsDirsAlreadyOnPath(t *testing.T) {
	t.Setenv("PATH", "/opt/homebrew/bin:/usr/bin")
	stubSources(t, t.TempDir(), "/opt/homebrew/bin")
	homeRelDirs = nil
	absDirs = []string{"/opt/homebrew/bin"}

	added := Apply(context.Background(), Options{}, nopLogger())

	if len(added) != 0 {
		t.Errorf("已在 PATH 上的目录不该算新增，实得 %v", added)
	}
	if strings.Count(os.Getenv("PATH"), "/opt/homebrew/bin") != 1 {
		t.Errorf("目录被重复追加：%q", os.Getenv("PATH"))
	}
}

// ExtraDirs（config.path_dirs）排在内置已知目录表之前——用户显式声明的优先于内置猜测。
func TestApplyExtraDirsRankBeforeKnownDirs(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	extra := t.TempDir()
	known := t.TempDir()
	stubSources(t, t.TempDir(), extra, known)
	homeRelDirs = nil
	absDirs = []string{known}

	added := Apply(context.Background(), Options{ExtraDirs: []string{extra}}, nopLogger())

	if len(added) != 2 || added[0] != extra || added[1] != known {
		t.Fatalf("added 顺序应为 [extra, known]，实得 %v", added)
	}
	if strings.Index(os.Getenv("PATH"), extra) > strings.Index(os.Getenv("PATH"), known) {
		t.Errorf("path_dirs 应排在已知目录表之前，实得 %q", os.Getenv("PATH"))
	}
}

// ExtraDirs 里不存在的目录被跳过（用户写错路径时要有信号，而不是静默）。
func TestApplySkipsMissingExtraDir(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	stubSources(t, t.TempDir())
	homeRelDirs, absDirs = nil, nil

	added := Apply(context.Background(), Options{ExtraDirs: []string{"/no/such/dir"}}, nopLogger())

	if len(added) != 0 {
		t.Errorf("不存在的 path_dirs 条目不该进 PATH，实得 %v", added)
	}
}

// IncludeLoginShell=false 时绝不执行登录 shell（init 走这条路，省掉最多 3 秒）。
func TestApplySkipsLoginShellWhenDisabled(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	stubSources(t, t.TempDir())
	homeRelDirs, absDirs = nil, nil
	old := loginShellPATH
	t.Cleanup(func() { loginShellPATH = old })
	loginShellPATH = func(context.Context, string) (string, error) {
		t.Fatal("IncludeLoginShell=false 时不该执行登录 shell")
		return "", nil
	}

	Apply(context.Background(), Options{}, nopLogger())
}

// 登录 shell 解析失败时，其余三层照常生效、PATH 不被破坏。
func TestApplyDegradesWhenLoginShellFails(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("SHELL", "/bin/zsh")
	known := t.TempDir()
	stubSources(t, t.TempDir(), known)
	homeRelDirs = nil
	absDirs = []string{known}
	stubLoginShell(t, "", errors.New("shell 不存在"))

	added := Apply(context.Background(), Options{IncludeLoginShell: true}, nopLogger())

	if len(added) != 1 || added[0] != known {
		t.Fatalf("登录 shell 失败不应影响其余层，实得 %v", added)
	}
	if !strings.HasPrefix(os.Getenv("PATH"), "/usr/bin") {
		t.Errorf("PATH 被破坏：%q", os.Getenv("PATH"))
	}
}

// 取不到 HOME 时跳过全部 ~ 系条目，绝对路径条目仍然生效。
//
// why：老 systemd 不为 User= 设置 HOME，那台机器不该因此一个目录都补不上。
func TestApplyWithoutHomeStillAddsAbsoluteDirs(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	stubSources(t, "", "/opt/homebrew/bin")
	homeRelDirs = []string{".opencode/bin"}
	absDirs = []string{"/opt/homebrew/bin"}

	added := Apply(context.Background(), Options{}, nopLogger())

	if len(added) != 1 || added[0] != "/opt/homebrew/bin" {
		t.Fatalf("取不到 HOME 时绝对路径条目仍应生效，实得 %v", added)
	}
}

// 内置表必须覆盖 opencode 官方安装器的落点——B71 的故障现场就是它。
func TestKnownTableCoversOpencodeInstaller(t *testing.T) {
	for _, d := range homeRelDirs {
		if d == ".opencode/bin" {
			return
		}
	}
	t.Fatalf("内置已知目录表必须含 .opencode/bin（B71 故障现场），实得 %v", homeRelDirs)
}

// 默认实现只取 stdout、不以退出码判定成败、stderr 不得混入。
//
// why（B7 原有覆盖，迁移时不能丢）：交互式 shell（-i）在非 TTY 下会打作业控制
// 告警并可能非零退出，但 PATH 已经打出来了；按退出码判失败会让这条修复在真实
// 机器上白做，而把 stderr 并进来会直接把告警文本拼进 PATH。
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
