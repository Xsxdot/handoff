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

// initGitRepo 造一个带初始提交的干净仓库（main 分支 + README.md），返回仓库路径。
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
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

// TestRunCmdTimeoutAndTruncate 验证 run 命令的护栏：
// 正常输出、非零退出码、stderr 合并、超时被杀（124）、输出截断 1MB。
func TestRunCmdTimeoutAndTruncate(t *testing.T) {
	orig := runCmdTimeout
	runCmdTimeout = 200 * time.Millisecond
	defer func() { runCmdTimeout = orig }()

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
