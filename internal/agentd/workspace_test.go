// workspace 函数（PrepareBranch/Diff/ReadFile/RunCmd）在真实 git 仓库上的行为测试。
//
// 全部测试在 t.TempDir() 里 git init + 造初始提交，不触碰真实工作区；
// git 调用经 gitAt 辅助函数，失败即 t.Fatal（测试环境问题，不是被测行为）。
package agentd

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// testTaskID 是测试用的 UUID 风格任务 ID（前 8 位 12345678 → 分支 handoff/12345678）。
const testTaskID = "12345678-9abc-def0-1234-567890abcdef"

// gitAt 在 dir 里执行 git 命令，失败即 Fatal。
func gitAt(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// initGitRepo 在 t.TempDir() 里造一个带初始提交的干净仓库（main 分支 + README.md），
// 返回仓库路径。
func initGitRepo(t *testing.T) string {
	t.Helper()
	return initGitRepoIn(t, t.TempDir())
}

// initGitRepoIn 在指定目录 dir 里造一个带初始提交的干净仓库（main 分支 + README.md），
// 返回仓库路径。symlink 逃逸用例需要控制仓库的父目录（在外侧放目标文件/链接），
// 故不能只依赖 initGitRepo 的 t.TempDir。
func initGitRepoIn(t *testing.T, dir string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建仓库目录 %s: %v", dir, err)
	}
	gitAt(t, dir, "init", "-q")
	gitAt(t, dir, "checkout", "-b", "main")
	gitAt(t, dir, "config", "user.email", "test@handoff.dev")
	gitAt(t, dir, "config", "user.name", "handoff test")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# repo\n"), 0o644); err != nil {
		t.Fatalf("写 README: %v", err)
	}
	gitAt(t, dir, "add", ".")
	gitAt(t, dir, "commit", "-q", "-m", "init")
	return dir
}

// TestPrepareBranchCleanAndDirty 验证分支准备的两种前置：
// 干净工作区 → 建出 handoff/<id8> 并切过去；脏工作区（已修改/未跟踪）→ ErrDirtyWorktree
// 拒绝派发，且拒绝后不得擅自建分支。
func TestPrepareBranchCleanAndDirty(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		repo := initGitRepo(t)
		branch, err := PrepareBranch(repo, testTaskID)
		if err != nil {
			t.Fatalf("PrepareBranch(clean): %v", err)
		}
		if branch != "handoff/12345678" {
			t.Fatalf("branch=%q, want handoff/12345678", branch)
		}
		if got := strings.TrimSpace(gitAt(t, repo, "rev-parse", "--abbrev-ref", "HEAD")); got != branch {
			t.Fatalf("HEAD 分支=%q, want %q", got, branch)
		}
	})

	t.Run("dirty_modified", func(t *testing.T) {
		repo := initGitRepo(t)
		f, err := os.OpenFile(filepath.Join(repo, "README.md"), os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			t.Fatalf("打开 README: %v", err)
		}
		f.WriteString("dirty\n")
		f.Close()

		_, err = PrepareBranch(repo, testTaskID)
		if !errors.Is(err, ErrDirtyWorktree) {
			t.Fatalf("脏工作区应拒绝派发, got %v", err)
		}
		if got := strings.TrimSpace(gitAt(t, repo, "rev-parse", "--abbrev-ref", "HEAD")); got != "main" {
			t.Fatalf("拒绝后 HEAD=%q, want main（不得建分支）", got)
		}
	})

	t.Run("dirty_untracked", func(t *testing.T) {
		repo := initGitRepo(t)
		if err := os.WriteFile(filepath.Join(repo, "stray.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("写未跟踪文件: %v", err)
		}
		_, err := PrepareBranch(repo, testTaskID)
		if !errors.Is(err, ErrDirtyWorktree) {
			t.Fatalf("未跟踪文件同样算脏, got %v", err)
		}
	})
}

// TestDiffShowsCommits 验证在任务分支上提交后，Diff 相对基准分支能看到该文件与提交主题。
func TestDiffShowsCommits(t *testing.T) {
	repo := initGitRepo(t)
	if _, err := PrepareBranch(repo, testTaskID); err != nil {
		t.Fatalf("PrepareBranch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(repo, "impl.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("写 impl.go: %v", err)
	}
	gitAt(t, repo, "add", "impl.go")
	gitAt(t, repo, "commit", "-q", "-m", "feat: add impl")

	diff, err := Diff(repo, "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "impl.go") {
		t.Fatalf("diff 应包含文件名 impl.go:\n%s", diff)
	}
	if !strings.Contains(diff, "feat: add impl") {
		t.Fatalf("diff 应包含提交主题:\n%s", diff)
	}
}

// TestDiffRejectsDashPrefixedBase 覆盖 L-4：以 "-" 开头的 base 会被 git 解释为
// 选项而非 rev（如 --output=... 让 git 把 diff 写到任意路径，git 参数注入），
// Diff 必须拒绝（ErrBadBaseBranch）且不得让 git 真正执行到写文件——
// 目标文件路径放 t.TempDir() 内，若 git 被注入执行则文件会被创建。
func TestDiffRejectsDashPrefixedBase(t *testing.T) {
	repo := initGitRepo(t)
	evil := filepath.Join(t.TempDir(), "evil-output")
	_, err := Diff(repo, "--output="+evil)
	if !errors.Is(err, ErrBadBaseBranch) {
		t.Fatalf("base 以 - 开头应拒绝（ErrBadBaseBranch）, got %v", err)
	}
	if _, statErr := os.Stat(evil); !os.IsNotExist(statErr) {
		t.Fatalf("git 参数注入生效：--output 目标文件被创建（%v）——必须拒绝 - 前缀 base", statErr)
	}
}

// TestDiffRejectsEmptyBase 验证空 base 直接拒绝：Diff 没有合法的空 rev 语义
// （空 base 会退化成 "git diff ...HEAD" 的裸 diff，不是「相对基准分支」）。
func TestDiffRejectsEmptyBase(t *testing.T) {
	repo := initGitRepo(t)
	if _, err := Diff(repo, ""); !errors.Is(err, ErrBadBaseBranch) {
		t.Fatalf("空 base 应拒绝, got %v", err)
	}
}

// TestReadFileEscapeRejected 验证正常路径可读，逃逸路径（../ 前缀、绝对路径、
// 多层归一化后仍逃逸）一律返回 ErrPathEscape。
func TestReadFileEscapeRejected(t *testing.T) {
	repo := initGitRepo(t)

	content, err := ReadFile(repo, "README.md")
	if err != nil {
		t.Fatalf("ReadFile 正常路径: %v", err)
	}
	if !strings.Contains(content, "# repo") {
		t.Fatalf("ReadFile 内容=%q, want 含 # repo", content)
	}

	for _, p := range []string{"../etc/passwd", "/etc/passwd", "sub/../../etc/passwd", "..", ""} {
		if _, err := ReadFile(repo, p); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("ReadFile(%q) 应拒绝路径逃逸, got %v", p, err)
		}
	}
}

// TestReadFileSymlinkEscape 验证符号链接逃逸防御（P1-5）：
// 指向仓库外的文件/目录链接一律 ErrPathEscape（读不到仓外内容，信息泄漏被阻断）；
// 仓内相对目标的链接正常可读（os.OpenRoot 跟随仓内链接）；仓库根自身是符号链接时
// 照常工作（OpenRoot 跟随根链接）；绝对目标的链接一律拒绝（os.Root 契约：
// "Symbolic links must not be absolute"，即使目标在仓内）。
func TestReadFileSymlinkEscape(t *testing.T) {
	t.Run("symlink_to_outside_file", func(t *testing.T) {
		base := t.TempDir()
		repo := initGitRepoIn(t, filepath.Join(base, "repo"))
		// 仓外敏感文件（如 ~/.ssh/id_rsa 的替身）：链接逃逸读它必须失败
		if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte("secret"), 0o644); err != nil {
			t.Fatalf("写仓外文件: %v", err)
		}
		// 相对目标：../../secret.txt 从 repo/evil 出发指向 base/secret.txt，逃出仓库
		if err := os.Symlink("../../secret.txt", filepath.Join(repo, "evil")); err != nil {
			t.Fatalf("建逃逸链接: %v", err)
		}
		if _, err := ReadFile(repo, "evil"); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("指向仓外的文件链接应拒绝, got %v", err)
		}
	})

	t.Run("symlink_to_outside_dir", func(t *testing.T) {
		base := t.TempDir()
		repo := initGitRepoIn(t, filepath.Join(base, "repo"))
		outdir := filepath.Join(base, "outdir")
		if err := os.MkdirAll(outdir, 0o755); err != nil {
			t.Fatalf("建仓外目录: %v", err)
		}
		if err := os.WriteFile(filepath.Join(outdir, "secret.txt"), []byte("secret"), 0o644); err != nil {
			t.Fatalf("写仓外文件: %v", err)
		}
		if err := os.Symlink("../../outdir", filepath.Join(repo, "dirlink")); err != nil {
			t.Fatalf("建目录逃逸链接: %v", err)
		}
		// 经链接读仓外目录里的文件：中间目录组件逃逸，OpenRoot 在打开时拒绝
		if _, err := ReadFile(repo, "dirlink/secret.txt"); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("经目录链接逃逸应拒绝, got %v", err)
		}
	})

	t.Run("symlink_internal_allowed", func(t *testing.T) {
		repo := initGitRepo(t)
		// 仓内相对目标：允许（开源仓库常见的文件链接，如 docs/README.md -> ../README.md）
		if err := os.Symlink("README.md", filepath.Join(repo, "notes.md")); err != nil {
			t.Fatalf("建仓内链接: %v", err)
		}
		if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
			t.Fatalf("建 sub: %v", err)
		}
		if err := os.WriteFile(filepath.Join(repo, "sub", "ok.txt"), []byte("sub\n"), 0o644); err != nil {
			t.Fatalf("写 sub/ok.txt: %v", err)
		}
		// 中间目录组件是仓内链接：同样跟随
		if err := os.Symlink("sub", filepath.Join(repo, "sublink")); err != nil {
			t.Fatalf("建仓内目录链接: %v", err)
		}
		content, err := ReadFile(repo, "notes.md")
		if err != nil {
			t.Fatalf("仓内文件链接应可读, got %v", err)
		}
		if !strings.Contains(content, "# repo") {
			t.Fatalf("仓内链接内容=%q, want 含 # repo", content)
		}
		content, err = ReadFile(repo, "sublink/ok.txt")
		if err != nil {
			t.Fatalf("仓内目录链接应可读, got %v", err)
		}
		if content != "sub\n" {
			t.Fatalf("仓内目录链接内容=%q, want sub\\n", content)
		}
	})

	t.Run("repo_root_is_symlink", func(t *testing.T) {
		base := t.TempDir()
		repo := initGitRepoIn(t, filepath.Join(base, "real"))
		rootlink := filepath.Join(base, "rootlink")
		if err := os.Symlink(repo, rootlink); err != nil {
			t.Fatalf("建仓库根链接: %v", err)
		}
		// 仓库根自身是链接：OpenRoot 跟随之，读链接指向的真实仓库，正常可用
		content, err := ReadFile(rootlink, "README.md")
		if err != nil {
			t.Fatalf("仓库根为链接时应可读, got %v", err)
		}
		if !strings.Contains(content, "# repo") {
			t.Fatalf("内容=%q, want 含 # repo", content)
		}
	})

	t.Run("absolute_target_rejected", func(t *testing.T) {
		repo := initGitRepo(t)
		// 绝对目标的链接：os.Root 契约明确 "Symbolic links must not be absolute"，
		// 即使目标在仓内也拒绝——保守语义（多数平台无 openat2，逐段解析对绝对目标
		// 无法保证不逃逸，stdlib 统一拒绝）
		if err := os.Symlink(filepath.Join(repo, "README.md"), filepath.Join(repo, "abs.md")); err != nil {
			t.Fatalf("建绝对目标链接: %v", err)
		}
		if _, err := ReadFile(repo, "abs.md"); !errors.Is(err, ErrPathEscape) {
			t.Fatalf("绝对目标链接应拒绝, got %v", err)
		}
	})
}

// TestReadFileSizeCap 验证读取大小上限（P1-5）：超过 maxRunOutput 的文件只返回
// 开头 maxRunOutput 字节（截断而非拒绝——与 RunCmd 输出截断语义一致），
// 边界内的文件完整返回。
func TestReadFileSizeCap(t *testing.T) {
	repo := initGitRepo(t)
	big := filepath.Join(repo, "big.bin")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), maxRunOutput+4096), 0o644); err != nil {
		t.Fatalf("写大文件: %v", err)
	}
	content, err := ReadFile(repo, "big.bin")
	if err != nil {
		t.Fatalf("ReadFile 大文件: %v", err)
	}
	if len(content) != maxRunOutput {
		t.Fatalf("大文件返回长度=%d, want 截断到 %d", len(content), maxRunOutput)
	}

	small, err := ReadFile(repo, "README.md")
	if err != nil {
		t.Fatalf("ReadFile 小文件: %v", err)
	}
	if !strings.Contains(small, "# repo") {
		t.Fatalf("小文件内容=%q, want 含 # repo", small)
	}
}

// TestReadFileDirectoryRejected 验证 fetch 目录返回明确错误（组 9 Minor #7 遗留）：
// 目录（含经仓内目录链接指向的目录）返回 ErrPathIsDir 而非「读取失败」。
func TestReadFileDirectoryRejected(t *testing.T) {
	repo := initGitRepo(t)
	if err := os.MkdirAll(filepath.Join(repo, "sub"), 0o755); err != nil {
		t.Fatalf("建 sub: %v", err)
	}
	if _, err := ReadFile(repo, "sub"); !errors.Is(err, ErrPathIsDir) {
		t.Fatalf("读目录应返回 ErrPathIsDir, got %v", err)
	}
	if err := os.Symlink("sub", filepath.Join(repo, "dirlink")); err != nil {
		t.Fatalf("建目录链接: %v", err)
	}
	// 经仓内目录链接指向的目录同样是目录（OpenRoot 跟随链接后按类型甄别）
	if _, err := ReadFile(repo, "dirlink"); !errors.Is(err, ErrPathIsDir) {
		t.Fatalf("经链接读目录应返回 ErrPathIsDir, got %v", err)
	}
}

// TestRunCmdTimeoutAndTruncate 验证 run 命令的护栏：
// 正常输出、非零退出码、stderr 合并、超时被杀（124）、输出截断 1MB。
func TestRunCmdTimeoutAndTruncate(t *testing.T) {
	orig := RunCmdTimeout
	RunCmdTimeout = 200 * time.Millisecond
	defer func() { RunCmdTimeout = orig }()

	repo := initGitRepo(t)
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		stdout, code, err := RunCmd(ctx, repo, "echo hello")
		if err != nil {
			t.Fatalf("RunCmd: %v", err)
		}
		if code != 0 || stdout != "hello\n" {
			t.Fatalf("stdout=%q code=%d, want hello\\n 0", stdout, code)
		}
	})

	t.Run("exit_code", func(t *testing.T) {
		_, code, err := RunCmd(ctx, repo, "exit 3")
		if err != nil {
			t.Fatalf("非零退出不应返回错误, got %v", err)
		}
		if code != 3 {
			t.Fatalf("exit 3 的退出码=%d, want 3", code)
		}
	})

	t.Run("stderr_merged", func(t *testing.T) {
		stdout, _, err := RunCmd(ctx, repo, "echo err >&2 && echo out")
		if err != nil {
			t.Fatalf("RunCmd: %v", err)
		}
		if !strings.Contains(stdout, "err") || !strings.Contains(stdout, "out") {
			t.Fatalf("stdout+stderr 应合并, got %q", stdout)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		start := time.Now()
		_, code, err := RunCmd(ctx, repo, "sleep 30")
		if err == nil {
			t.Fatalf("超时命令应返回错误")
		}
		if code != 124 {
			t.Fatalf("超时退出码=%d, want 124", code)
		}
		if elapsed := time.Since(start); elapsed > 10*time.Second {
			t.Fatalf("超时终止过慢: %v", elapsed)
		}
	})

	t.Run("truncate", func(t *testing.T) {
		// 输出 5MB（yes/head 毫秒级完成，远小于测试注入的 200ms 超时）：
		// 断言返回串恰为上限且命令完整执行（code 0）——有界回收下
		// agentd 驻留内存不随输出规模增长，排空在后台持续到子进程写完
		stdout, code, err := RunCmd(ctx, repo, "yes x | head -c 5000000")
		if err != nil {
			t.Fatalf("RunCmd: %v", err)
		}
		if code != 0 {
			t.Fatalf("exit code=%d, want 0（输出再大命令也应正常完成）", code)
		}
		if len(stdout) != maxRunOutput {
			t.Fatalf("输出长度=%d, want 截断到 %d", len(stdout), maxRunOutput)
		}
	})
}

// TestRunOutputBufferBounded 直接验证有界回收器本体：
// 持续写入远超上限的数据后，驻留字节恰为上限不再增长（超出部分排空丢弃），
// 总计数继续累计，且每次 Write 都返回完整长度（不中断排空）。
func TestRunOutputBufferBounded(t *testing.T) {
	var b runOutputBuffer
	b.limit = 1 << 10 // 1 KiB
	chunk := bytes.Repeat([]byte("x"), 4096)
	const writes = 1024
	for i := 0; i < writes; i++ {
		if n, err := b.Write(chunk); err != nil || n != len(chunk) {
			t.Fatalf("第 %d 次 Write n=%d err=%v, want %d nil", i, n, err, len(chunk))
		}
	}
	if b.buf.Len() != b.limit {
		t.Fatalf("驻留字节=%d, want 恰为上限 %d（超出部分不得驻留内存）", b.buf.Len(), b.limit)
	}
	if want := int64(writes) * int64(len(chunk)); b.total != want {
		t.Fatalf("total=%d, want %d（排空计数应含全部写入）", b.total, want)
	}
	if !b.capped {
		t.Fatalf("发生过截断应标记 capped")
	}
}
