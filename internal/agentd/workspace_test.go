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

// 以下别名让 PrepareWorkspace 测试的意图更贴近其工作区语义（plan 中命名）。
// 与 gitAt/initGitRepo 完全同构：失败即 Fatal、返回命令输出。
func initTestRepo(t *testing.T) string { t.Helper(); return initGitRepo(t) }
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return gitAt(t, dir, args...)
}
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	return strings.TrimSpace(gitAt(t, dir, args...))
}

// writeAndCommit 在仓库里写文件并提交，返回提交后的 HEAD。
func writeAndCommit(t *testing.T, repo, name, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0o644); err != nil {
		t.Fatalf("写 %s: %v", name, err)
	}
	gitAt(t, repo, "add", name)
	gitAt(t, repo, "commit", "-q", "-m", "commit "+name)
	return strings.TrimSpace(gitAt(t, repo, "rev-parse", "HEAD"))
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

// initClonedRepo 造「上游仓库 + 克隆」这一对，返回克隆出的仓库路径。
//
// 参数：
//   - baseBranch: 在上游建出的分支名；克隆里它**只以远程跟踪 ref 存在**
//
// 为什么必须是克隆：B76 的触发前提是「base 只有远程跟踪 ref、无本地同名分支」，
// 而 initTestRepo 那种本地 git init 的仓库里本地同名分支总是存在，DWIM 不会
// 发生——这正是这个 bug 一直没被任何测试抓到的原因。
//
// 为什么把 origin 改名成 upstream：registerTestProject 要往仓库里 remote add
// origin，克隆自带的 origin 会让它撞车。改名后远程跟踪 ref 变成
// refs/remotes/upstream/<baseBranch>，DWIM 照样触发（它认的是「在所有 remote 里
// 唯一」，不是「叫 origin」），顺带证明这个缺陷与 remote 叫什么无关。
func initClonedRepo(t *testing.T, baseBranch string) string {
	t.Helper()
	up := initTestRepo(t)
	gitT(t, up, "branch", baseBranch)
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, up, "clone", "-q", up, clone)
	gitT(t, clone, "remote", "rename", "origin", "upstream")
	gitT(t, clone, "config", "user.email", "test@handoff.dev")
	gitT(t, clone, "config", "user.name", "handoff test")
	// 前提自检：克隆里不能有本地同名分支，否则用例测的就不是 B76 的场景了
	if out := gitOut(t, clone, "branch", "--list", baseBranch); out != "" {
		t.Fatalf("fixture 失效：克隆里出现了本地分支 %s（%q），触发前提不成立", baseBranch, out)
	}
	return clone
}

// TestPrepareBranchCleanAndDirty 验证分支准备的两种前置：
// 干净工作区 → 建出 handoff/<id8> 并切过去；脏工作区（已修改/未跟踪）→ ErrDirtyWorktree
// 拒绝派发，且拒绝后不得擅自建分支。
func TestPrepareBranchCleanAndDirty(t *testing.T) {
	t.Run("clean", func(t *testing.T) {
		repo := initGitRepo(t)
		branch, err := PrepareBranch(context.Background(), repo, testTaskID)
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

		_, err = PrepareBranch(context.Background(), repo, testTaskID)
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
		_, err := PrepareBranch(context.Background(), repo, testTaskID)
		if !errors.Is(err, ErrDirtyWorktree) {
			t.Fatalf("未跟踪文件同样算脏, got %v", err)
		}
	})
}

// TestDiffShowsCommits 验证在任务分支上提交后，Diff 相对基准分支能看到该文件与提交主题。
func TestDiffShowsCommits(t *testing.T) {
	repo := initGitRepo(t)
	if _, err := PrepareBranch(context.Background(), repo, testTaskID); err != nil {
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
// 开头 maxRunOutput 字节 + 一行截断提示（截断而非拒绝——与 RunCmd 输出截断
// 语义一致；提示不可省，否则协调者会把截断处当文件末尾），边界内的文件完整返回。
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
	body, notice, found := strings.Cut(content, "\n\n=====")
	if !found {
		t.Fatalf("大文件返回未带截断提示（长度 %d）", len(content))
	}
	if len(body) != maxRunOutput {
		t.Fatalf("大文件正文长度=%d, want 截断到 %d", len(body), maxRunOutput)
	}
	if !strings.Contains(notice, "已截断") {
		t.Fatalf("截断提示文案不明确: %q", notice)
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

// TestPrepareWorkspaceDefaultKeepsCurrentBehavior 验证缺省请求（无分支/无 worktree
// 参数）行为与一期 PrepareBranch 完全一致：自动开 handoff/<id8> 分支、原地工作。
func TestPrepareWorkspaceDefaultKeepsCurrentBehavior(t *testing.T) {
	repo := initTestRepo(t)
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "abcdefgh-rest"})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Branch != "handoff/abcdefgh" || ws.WorkDir != repo || ws.Managed {
		t.Fatalf("缺省行为应与一期一致: %+v", ws)
	}
}

// TestPrepareWorkspaceExistingBranch 验证 Branch 模式：切到已存在分支并留在其上。
func TestPrepareWorkspaceExistingBranch(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "branch", "feat-x")
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "t1", Branch: "feat-x"})
	if err != nil || ws.Branch != "feat-x" {
		t.Fatalf("应切到已存在分支: %+v %v", ws, err)
	}
	if cur := gitOut(t, repo, "branch", "--show-current"); cur != "feat-x" {
		t.Fatalf("HEAD 应在 feat-x，得到 %s", cur)
	}
}

// TestPrepareWorkspaceBranchNotExist 验证 Branch 模式对不存在分支拒发（ErrBadWorkspaceReq）。
func TestPrepareWorkspaceBranchNotExist(t *testing.T) {
	repo := initTestRepo(t)
	if _, err := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "t1", Branch: "ghost"}); !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("不存在的分支应拒发: %v", err)
	}
}

// TestPrepareWorkspaceNewBranchWithBase 验证 NewBranch+Base：新分支从 Base 起点而不是 HEAD。
func TestPrepareWorkspaceNewBranchWithBase(t *testing.T) {
	repo := initTestRepo(t)
	base := gitOut(t, repo, "rev-parse", "HEAD")
	writeAndCommit(t, repo, "f.txt", "x") // HEAD 前进一格
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "t1", NewBranch: "feat-y", Base: base})
	if err != nil || ws.Branch != "feat-y" {
		t.Fatal(err)
	}
	if head := gitOut(t, repo, "rev-parse", "HEAD"); head != base {
		t.Fatalf("新分支应从 base 起点: head=%s base=%s", head, base)
	}
}

// TestPrepareWorkspaceNewWorktree 验证 NewWorktree 模式：在 WorktreesDir/<id8> 建
// managed worktree、内部自动开任务分支；且同 repo 第二个任务可并行派发（一期
// 原地模式做不到的冲突点）。
func TestPrepareWorkspaceNewWorktree(t *testing.T) {
	repo := initTestRepo(t)
	wtDir := filepath.Join(t.TempDir(), "worktrees")
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "abcdefgh-x", NewWorktree: true, WorktreesDir: wtDir})
	if err != nil {
		t.Fatal(err)
	}
	if !ws.Managed || ws.WorkDir != filepath.Join(wtDir, "abcdefgh") {
		t.Fatalf("managed worktree 路径错误: %+v", ws)
	}
	if cur := gitOut(t, ws.WorkDir, "branch", "--show-current"); cur != "handoff/abcdefgh" {
		t.Fatalf("worktree 内应在任务分支: %s", cur)
	}
	// 同 repo 第二个任务并行派发不冲突（一期原地模式做不到）
	if _, err := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "second-t", NewWorktree: true, WorktreesDir: wtDir}); err != nil {
		t.Fatalf("同 repo 并行派发应成功: %v", err)
	}
}

// TestPrepareWorkspaceNewWorktreeAllowsDirtyMainRepo 验证 new-worktree 不受主仓脏
// 工作区限制：新树天然干净（为什么免脏检查，见 PrepareWorkspace doc 的 why）。
func TestPrepareWorkspaceNewWorktreeAllowsDirtyMainRepo(t *testing.T) {
	repo := initTestRepo(t)
	os.WriteFile(filepath.Join(repo, "dirty.txt"), []byte("x"), 0o644) // 主仓脏
	if _, err := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "t1", NewWorktree: true,
		WorktreesDir: filepath.Join(t.TempDir(), "w")}); err != nil {
		t.Fatalf("new-worktree 不应受主仓脏工作区限制: %v", err)
	}
}

// TestPrepareWorkspaceExistingWorktree 验证 Worktree 模式：用户自带 worktree（归属
// 校验通过）在其中开任务分支、Managed=false；非本 repo 的目录拒发。
func TestPrepareWorkspaceExistingWorktree(t *testing.T) {
	repo := initTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt1")
	gitT(t, repo, "worktree", "add", "-b", "pre-branch", wt)
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "abcdefgh-x", Worktree: wt})
	if err != nil {
		t.Fatal(err)
	}
	if ws.Managed || ws.WorkDir != wt || ws.Branch != "handoff/abcdefgh" {
		t.Fatalf("用户自带 worktree: Managed 应为 false 且在其中开任务分支: %+v", ws)
	}
	// 非 worktree 路径拒发
	if _, err := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "t2", Worktree: t.TempDir()}); !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("非本 repo worktree 应拒发: %v", err)
	}
}

// TestPrepareWorkspaceMutualExclusionAndInjection 覆盖全部参数非法组合：
// 互斥参数、Base 与已存在分支互斥、- 前缀注入面，统一 ErrBadWorkspaceReq。
func TestPrepareWorkspaceMutualExclusionAndInjection(t *testing.T) {
	repo := initTestRepo(t)
	for name, req := range map[string]WorkspaceReq{
		"branch×new-branch":     {Repo: repo, TaskID: "t", Branch: "a", NewBranch: "b"},
		"worktree×new-worktree": {Repo: repo, TaskID: "t", Worktree: "/x", NewWorktree: true},
		"base×branch":           {Repo: repo, TaskID: "t", Branch: "a", Base: "HEAD~1"},
		"分支名 - 开头":              {Repo: repo, TaskID: "t", Branch: "-evil"},
		"base - 开头":             {Repo: repo, TaskID: "t", NewBranch: "b", Base: "--evil"},
	} {
		if _, err := PrepareWorkspace(context.Background(), req); !errors.Is(err, ErrBadWorkspaceReq) {
			t.Fatalf("%s 应拒发: %v", name, err)
		}
	}
}

// TestPrepareWorkspaceCanceledContextFailsFast 验证工作区准备受 ctx 约束：
// 已取消的 ctx 必须立即失败，而不是照常把 git 跑完。
// why：现网根因是全部 git 调用写死 context.Background()，worktree add 遇网络
// 文件系统/hook/credential 交互式提示会挂死，并拖住 dispatch 的 HTTP handler。
func TestPrepareWorkspaceCanceledContextFailsFast(t *testing.T) {
	repo := initTestRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := PrepareWorkspace(ctx, WorkspaceReq{Repo: repo, TaskID: testTaskID}); err == nil {
		t.Fatal("已取消的 ctx 必须让工作区准备失败，实得 nil")
	}
}

// TestRemoveManagedWorktreeCanceledContextFailsFast 同款验证 worktree 清理路径。
func TestRemoveManagedWorktreeCanceledContextFailsFast(t *testing.T) {
	repo := initTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitT(t, repo, "worktree", "add", "-b", "side-remove", wt)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RemoveManagedWorktree(ctx, repo, wt); err == nil {
		t.Fatal("已取消的 ctx 必须让 worktree 清理失败，实得 nil")
	}
}

// TestWorktreeRejectsRepoSubdir 验证仓库子目录不被当作 worktree 接受。
// why：git-common-dir 会向上查找，/repo/internal/sub 与主仓返回同一 git 目录，
// 旧校验据此判定「归属成立」——实际改的是主仓 HEAD，且把后续审阅面
// （diff/run 的工作目录）收窄到了那个子目录。
func TestWorktreeRejectsRepoSubdir(t *testing.T) {
	repo := initTestRepo(t)
	sub := filepath.Join(repo, "internal", "sub")
	if err := os.MkdirAll(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	_, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: testTaskID, Worktree: sub,
	})
	if !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("仓库子目录必须按参数非法拒绝，实得 %v", err)
	}
}

// TestWorktreeAcceptsRealWorktree 守住收紧后不误伤真 worktree。
func TestWorktreeAcceptsRealWorktree(t *testing.T) {
	repo := initTestRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitT(t, repo, "worktree", "add", "-b", "side-accept", wt)
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: testTaskID, Worktree: wt,
	})
	if err != nil {
		t.Fatalf("真 worktree 必须被接受，实得 %v", err)
	}
	if ws.WorkDir != wt {
		t.Errorf("WorkDir = %q，期望 %q", ws.WorkDir, wt)
	}
	if ws.Managed {
		t.Error("用户自带 worktree 不应标记 Managed（那会让 done 代删别人的工作树）")
	}
}

// TestResolveBaselinePresentSkipsFetch 验证基线已在本地对象库时直接放行且不 fetch。
// 仓库故意配一个不存在的 remote：一旦实现「无条件先 fetch」，git fetch --all
// 会失败并让本用例挂掉——这就是「命中即零网络」的可执行证据。
func TestResolveBaselinePresentSkipsFetch(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "nonexistent.git"))
	bl, err := ResolveBaseline(context.Background(), repo, head)
	if err != nil {
		t.Fatalf("基线已在仓库中必须直接放行（不触发 fetch），实得 %v", err)
	}
	if bl.Start != head || bl.Ahead != 0 || bl.Fetched {
		t.Fatalf("基线即 HEAD 时应 Start=HEAD/Ahead=0/未 fetch，实得 %+v", bl)
	}
}

// TestResolveBaselineAheadCount 验证任务仓库领先基线时数得出提交数——
// 这个数字就是「新分支会丢掉哪些提交」的规模，B35 现场缺的正是它。
func TestResolveBaselineAheadCount(t *testing.T) {
	repo := initTestRepo(t)
	base := gitOut(t, repo, "rev-parse", "HEAD")
	writeAndCommit(t, repo, "a.txt", "1")
	writeAndCommit(t, repo, "b.txt", "2")
	bl, err := ResolveBaseline(context.Background(), repo, base)
	if err != nil {
		t.Fatal(err)
	}
	if bl.Start != base {
		t.Fatalf("Start 必须是入参基线，实得 %s", bl.Start)
	}
	if bl.Ahead != 2 {
		t.Fatalf("任务仓库领先 2 个提交，实得 Ahead=%d", bl.Ahead)
	}
}

// TestResolveBaselineEmptyFallsBackToHead 验证空基线（--no-sync-check / cwd 非仓库）
// 不是「没有起点」而是「起点退回任务仓库 HEAD」：这条路上也必须答得出
// 「这个任务建在哪个提交上」。
func TestResolveBaselineEmptyFallsBackToHead(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	bl, err := ResolveBaseline(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("空基线必须跳过校验，实得 %v", err)
	}
	if bl.Start != head || bl.Ahead != 0 {
		t.Fatalf("空基线应退回仓库 HEAD 且 Ahead=0，实得 %+v（HEAD=%s）", bl, head)
	}
}

// TestResolveBaselineEmptyRepoHasNoStart 验证一个提交都没有的仓库返回空 Start
// 而不是报错：空仓库上 checkout -b 本来就不能带起点，交给 git 默认行为。
func TestResolveBaselineEmptyRepoHasNoStart(t *testing.T) {
	repo := t.TempDir()
	gitAt(t, repo, "init", "-q")
	bl, err := ResolveBaseline(context.Background(), repo, "")
	if err != nil {
		t.Fatalf("空仓库不应报错，实得 %v", err)
	}
	if bl.Start != "" {
		t.Fatalf("空仓库应无起点，实得 %q", bl.Start)
	}
}

// TestResolveBaselineMissingRejects 验证基线缺失且 fetch 补不回来时拒发，
// 且错误里带上基线 sha —— 协调者据此才知道该 push 哪个提交。
func TestResolveBaselineMissingRejects(t *testing.T) {
	repo := initTestRepo(t)
	const absent = "0123456789abcdef0123456789abcdef01234567"
	_, err := ResolveBaseline(context.Background(), repo, absent)
	if !errors.Is(err, ErrBaseCommitMissing) {
		t.Fatalf("基线缺失必须返回 ErrBaseCommitMissing，实得 %v", err)
	}
	if !strings.Contains(err.Error(), absent) {
		t.Errorf("错误文本必须含基线 sha，实得 %q", err.Error())
	}
	if !strings.Contains(err.Error(), "git push") {
		t.Errorf("错误文本必须含 git push 的动作提示，实得 %q", err.Error())
	}
}

// TestResolveBaselineRejectsMalformedSHA 验证非 40 位十六进制一律拒绝：
// 基线值最终会拼进 git 参数，不校验等于开一个注入面。
func TestResolveBaselineRejectsMalformedSHA(t *testing.T) {
	repo := initTestRepo(t)
	for _, bad := range []string{"--upload-pack=evil", "HEAD", "abc123", "0123456789abcdef0123456789abcdef0123456G"} {
		if _, err := ResolveBaseline(context.Background(), repo, bad); !errors.Is(err, ErrBadWorkspaceReq) {
			t.Errorf("基线 %q 必须按参数非法拒绝，实得 %v", bad, err)
		}
	}
}

// TestResolveBaselineStartIsUsableAsBranchStart 是「决议结论真的能当起点用」的
// 闭环断言：ResolveBaseline 给出的 Start 直接喂给 PrepareWorkspace，新 worktree
// 的 HEAD 必须落在它上面。校验与使用之间的连接就是这条测试守着的东西。
func TestResolveBaselineStartIsUsableAsBranchStart(t *testing.T) {
	repo := initTestRepo(t)
	base := gitOut(t, repo, "rev-parse", "HEAD")
	writeAndCommit(t, repo, "drift.txt", "x")
	bl, err := ResolveBaseline(context.Background(), repo, base)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: testTaskID, Base: bl.Start,
		NewWorktree: true, WorktreesDir: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if head := gitOut(t, ws.WorkDir, "rev-parse", "HEAD"); head != bl.Start {
		t.Fatalf("决议起点未被用上：worktree head=%s baseline start=%s", head, bl.Start)
	}
}

// TestRemoveManagedWorktree 验证 managed worktree 清理：目录删除、任务分支保留。
func TestRemoveManagedWorktree(t *testing.T) {
	repo := initTestRepo(t)
	wtDir := filepath.Join(t.TempDir(), "w")
	ws, _ := PrepareWorkspace(context.Background(), WorkspaceReq{Repo: repo, TaskID: "abcdefgh-x", NewWorktree: true, WorktreesDir: wtDir})
	if err := RemoveManagedWorktree(context.Background(), repo, ws.WorkDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ws.WorkDir); !os.IsNotExist(err) {
		t.Fatalf("worktree 目录应已删除")
	}
	// 分支保留（spec：只删工作树不删分支）
	if out := gitOut(t, repo, "branch", "--list", "handoff/abcdefgh"); out == "" {
		t.Fatalf("任务分支不应被删除")
	}
}

// TestPrepareWorkspaceAutoBranchHonorsBase 是 B35 的回归锚点：自动分支
// handoff/<id8> 必须能从指定起点开出，而不是任务仓库当时的 HEAD。
//
// 为什么这条必须存在：B35 的现场就是「校验的基线和分支实际起点两码事」——
// 只有断言 worktree 的 HEAD 等于起点，才能证明校验结论真的被用上了。
func TestPrepareWorkspaceAutoBranchHonorsBase(t *testing.T) {
	repo := initTestRepo(t)
	base := gitOut(t, repo, "rev-parse", "HEAD")
	writeAndCommit(t, repo, "later.txt", "x") // 主仓 HEAD 前进一格，与 base 拉开距离
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: testTaskID, Base: base,
		NewWorktree: true, WorktreesDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("自动分支带 Base 必须放行: %v", err)
	}
	if ws.Branch != "handoff/12345678" {
		t.Fatalf("应是自动分支，得到 %s", ws.Branch)
	}
	if head := gitOut(t, ws.WorkDir, "rev-parse", "HEAD"); head != base {
		t.Fatalf("自动分支起点必须是 Base：head=%s base=%s（B35 根因：起点被静默换成仓库 HEAD）", head, base)
	}
}

// TestPrepareWorkspaceRecordsNewBranchTip 验证三种工作树模式下，新建分支时
// 都记下了它的尖端 sha，而切已存在分支时该字段为空。
//
// 判别力：最后一条（--branch 已存在分支 → NewBranchTip 必须为空）。缺了它，
// 一个「无条件记 tip」的实现会让补偿把用户自己的分支删掉。
func TestPrepareWorkspaceRecordsNewBranchTip(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")

	// 新工作树 + 自动分支
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "aaaaaaaa-0000-0000-0000-000000000000",
		NewWorktree: true, WorktreesDir: filepath.Join(t.TempDir(), "wt"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.NewBranchTip != head {
		t.Errorf("新建分支应记下尖端 %s，得到 %q", head, ws.NewBranchTip)
	}
	if ws.PrevRef != "" {
		t.Errorf("managed 模式 PrevRef 应为空，得到 %q", ws.PrevRef)
	}

	// 新工作树 + 已存在分支
	gitT(t, repo, "branch", "mine")
	ws2, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "bbbbbbbb-0000-0000-0000-000000000000",
		Branch: "mine", NewWorktree: true, WorktreesDir: filepath.Join(t.TempDir(), "wt2"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws2.NewBranchTip != "" {
		t.Errorf("切已存在分支时 NewBranchTip 必须为空，得到 %q", ws2.NewBranchTip)
	}
}

// TestPrepareWorkspaceRecordsPrevRefInPlace 验证原地模式记下了切走之前的分支名。
func TestPrepareWorkspaceRecordsPrevRefInPlace(t *testing.T) {
	repo := initTestRepo(t)
	before := gitOut(t, repo, "rev-parse", "--abbrev-ref", "HEAD")
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "cccccccc-0000-0000-0000-000000000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.PrevRef != before {
		t.Errorf("原地模式应记下原分支 %s，得到 %q", before, ws.PrevRef)
	}
	if ws.NewBranchTip == "" {
		t.Error("原地模式自动分支也是新建分支，NewBranchTip 不应为空")
	}
}

// TestPrepareWorkspaceRecordsPrevRefDetached 验证 detached HEAD 起步时，
// PrevRef 退回 commit sha——它同样能直接喂给 git checkout 复原。
//
// 判别力：一个只用 symbolic-ref 的实现在这里会记下空串，补偿就无从复原。
func TestPrepareWorkspaceRecordsPrevRefDetached(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "checkout", "--detach", "-q", head)
	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "dddddddd-0000-0000-0000-000000000000",
	})
	if err != nil {
		t.Fatal(err)
	}
	if ws.PrevRef != head {
		t.Errorf("detached 起步应记下 commit sha %s，得到 %q", head, ws.PrevRef)
	}
}

// TestEnsureRepoUsableAcceptsRepo 正常仓库必须放行——守卫不能把好路径也拦下来。
func TestEnsureRepoUsableAcceptsRepo(t *testing.T) {
	repo := initGitRepo(t)
	if err := EnsureRepoUsable(context.Background(), repo); err != nil {
		t.Fatalf("正常仓库 EnsureRepoUsable: %v", err)
	}
}

// TestEnsureRepoUsableRejectsNonGitPath 钉住 B45 的判据：路径存在但不是 git 仓库
// 时必须归入 ErrRepoUnusable，而不是留给后面的 worktree add 扁平成 500。
// 错误文本必须带 git 的原因，只有哨兵等于没说。
func TestEnsureRepoUsableRejectsNonGitPath(t *testing.T) {
	err := EnsureRepoUsable(context.Background(), t.TempDir())
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("非 git 目录 err = %v, want ErrRepoUnusable", err)
	}
	if err.Error() == ErrRepoUnusable.Error() {
		t.Fatalf("错误文本必须带 git 原因，不能只有哨兵: %q", err.Error())
	}
}

// TestEnsureRepoUsableGitMissing 覆盖 spec §3.2 的第二种形态：git 不在 PATH
// （gitRun 返回 exec 错误、stderr 为空）同样归入 ErrRepoUnusable，不能因为
// stderr 空就漏掉分类。
func TestEnsureRepoUsableGitMissing(t *testing.T) {
	repo := initGitRepo(t) // 必须在改 PATH 之前建仓库
	t.Setenv("PATH", "")
	err := EnsureRepoUsable(context.Background(), repo)
	if !errors.Is(err, ErrRepoUnusable) {
		t.Fatalf("git 不在 PATH 时 err = %v, want ErrRepoUnusable", err)
	}
}

// TestPrepareWorkspaceRejectsBranchIdentityMismatch 钉住 B76 的守卫：git 报成功
// 但给出的分支不是请求的那个时，必须回滚并拒发，而不是带着错分支继续。
func TestPrepareWorkspaceRejectsBranchIdentityMismatch(t *testing.T) {
	clone := initClonedRepo(t, "shared-base")
	wtDir := filepath.Join(t.TempDir(), "worktrees")

	_, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: clone, TaskID: "abcdefgh-b76", NewWorktree: true, WorktreesDir: wtDir,
		NewBranch: "feat/wanted", Base: "shared-base",
	})
	if !errors.Is(err, ErrBranchIdentityMismatch) {
		t.Fatalf("应按分支身份不符拒发, got: %v", err)
	}
	// 错误文本必须同时点名两个分支——只说「不符」的报错等于没说
	if !strings.Contains(err.Error(), "feat/wanted") || !strings.Contains(err.Error(), "shared-base") {
		t.Fatalf("错误文本应同时含请求分支与实到分支: %v", err)
	}
	// 拒发必须干净：刚建的工作树不能留下
	if _, statErr := os.Stat(filepath.Join(wtDir, "abcdefgh")); !os.IsNotExist(statErr) {
		t.Fatalf("拒发后 managed worktree 应已清理, stat err=%v", statErr)
	}
}

// TestPrepareWorkspaceRemoteOnlyBaseAllPaths 钉住三条工作树路径在「base 只有
// origin/<name>」时的一致行为：都应建出请求的分支。原地与用户树此前是硬失败。
func TestPrepareWorkspaceRemoteOnlyBaseAllPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		mk   func(t *testing.T, clone, base string) WorkspaceReq
	}{
		{"新树", func(t *testing.T, clone, base string) WorkspaceReq {
			return WorkspaceReq{Repo: clone, TaskID: "abcdefgh-nw", NewWorktree: true,
				WorktreesDir: filepath.Join(t.TempDir(), "w"), NewBranch: "feat/wanted", Base: base}
		}},
		{"原地", func(t *testing.T, clone, base string) WorkspaceReq {
			return WorkspaceReq{Repo: clone, TaskID: "abcdefgh-ip", NewBranch: "feat/wanted", Base: base}
		}},
		{"用户树", func(t *testing.T, clone, base string) WorkspaceReq {
			wt := filepath.Join(t.TempDir(), "userwt")
			gitT(t, clone, "worktree", "add", "-q", wt)
			return WorkspaceReq{Repo: clone, TaskID: "abcdefgh-uw", Worktree: wt,
				NewBranch: "feat/wanted", Base: base}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clone := initClonedRepo(t, "shared-base")
			// 调用方（manager）已把起点解析成 sha，测试同样喂 sha
			base, err := resolveCommit(context.Background(), clone, "shared-base")
			if err != nil {
				t.Fatalf("解析起点: %v", err)
			}
			ws, err := PrepareWorkspace(context.Background(), tc.mk(t, clone, base))
			if err != nil {
				t.Fatalf("应成功: %v", err)
			}
			if ws.Branch != "feat/wanted" {
				t.Fatalf("ws.Branch=%q", ws.Branch)
			}
			if cur := gitOut(t, ws.WorkDir, "branch", "--show-current"); cur != "feat/wanted" {
				t.Fatalf("工作区当前分支=%q", cur)
			}
		})
	}
}

// TestResolveCommitRemoteOnlyBranch 钉住 B76 的源头修复：base 只有 origin/<name>
// 时也必须解析得出（取远程尖端），否则修复会以「拒发」的形式打断正常派发。
func TestResolveCommitRemoteOnlyBranch(t *testing.T) {
	clone := initClonedRepo(t, "shared-base")
	want := gitOut(t, clone, "rev-parse", "upstream/shared-base")

	got, err := resolveCommit(context.Background(), clone, "shared-base")
	if err != nil {
		t.Fatalf("远程跟踪分支简写应可解析: %v", err)
	}
	if got != want {
		t.Fatalf("sha=%q, want %q", got, want)
	}
}

// TestResolveCommitAnnotatedTagPeelsToCommit 钉住 ^{commit} 剥离：annotated tag
// 的裸 rev-parse 给的是 tag 对象，直接拿去开分支会得到非预期的起点。
func TestResolveCommitAnnotatedTagPeelsToCommit(t *testing.T) {
	repo := initTestRepo(t)
	head := gitOut(t, repo, "rev-parse", "HEAD")
	gitT(t, repo, "tag", "-a", "v1", "-m", "release 1")

	got, err := resolveCommit(context.Background(), repo, "v1")
	if err != nil {
		t.Fatalf("annotated tag 应可解析: %v", err)
	}
	if got != head {
		t.Fatalf("应剥离到 commit: got=%q, want=%q", got, head)
	}
}

// TestResolveCommitMissingRejects 钉住拒发出口：起点不存在时给可操作的报错，
// 而不是让它一路走到 git 内部措辞（`is not a commit`）才炸。
func TestResolveCommitMissingRejects(t *testing.T) {
	repo := initTestRepo(t)

	_, err := resolveCommit(context.Background(), repo, "no-such-branch")
	if !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("应按 ErrBadWorkspaceReq 拒发, got: %v", err)
	}
	if !strings.Contains(err.Error(), "no-such-branch") || !strings.Contains(err.Error(), "git push") {
		t.Fatalf("错误文本应含起点原文与可操作出路: %v", err)
	}
}

// TestResolveCommitAmbiguousRemoteOnlyBranch 钉住歧义出口：两个远端都有同名
// 分支时（fork 工作流 origin+upstream 的常态），必须按歧义拒发并列出全部候选，
// 而不能降级成「起点不存在，先 git push」——起点明明在，让协调者去 push 是
// 把他引向错误的排查方向。
func TestResolveCommitAmbiguousRemoteOnlyBranch(t *testing.T) {
	up := initTestRepo(t)
	gitT(t, up, "branch", "shared-base")
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, up, "clone", "-q", up, clone)
	gitT(t, clone, "remote", "rename", "origin", "upstream")
	gitT(t, clone, "config", "user.email", "test@handoff.dev")
	gitT(t, clone, "config", "user.name", "handoff test")
	// 第二个远端指向同一个上游：fetch 后 refs/remotes/upstream/shared-base 与
	// refs/remotes/other/shared-base 同时存在，for-each-ref 唯一匹配失效
	gitT(t, clone, "remote", "add", "other", up)
	gitT(t, clone, "fetch", "-q", "other")

	_, err := resolveCommit(context.Background(), clone, "shared-base")
	if !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("歧义应按 ErrBadWorkspaceReq 拒发, got: %v", err)
	}
	if !strings.Contains(err.Error(), "upstream/shared-base") || !strings.Contains(err.Error(), "other/shared-base") {
		t.Fatalf("错误文本应列出全部候选 ref: %v", err)
	}
	if strings.Contains(err.Error(), "git push") {
		t.Fatalf("歧义不是不存在，错误文本不得误导协调者去 push: %v", err)
	}
}

// 出网 git 必须带上 -c http.proxy，且它要排在子命令**之前**——
// git 的 -c 是全局选项，放到子命令后面 git 会当成子命令的参数直接报错。
func TestGitNetArgsCarryProxyBeforeSubcommand(t *testing.T) {
	SetGitProxy("socks5://127.0.0.1:1080")
	defer SetGitProxy("")

	got := gitNetArgs("clone", "--", "url", "dest")
	if len(got) < 3 {
		t.Fatalf("参数太少: %v", got)
	}
	if got[0] != "-c" || got[1] != "http.proxy=socks5://127.0.0.1:1080" {
		t.Fatalf("代理参数不在最前: %v", got)
	}
	if got[2] != "clone" {
		t.Fatalf("子命令应紧跟在代理参数之后: %v", got)
	}
}

// 未配代理时参数一字不变——不能让所有没配代理的机器平白多两个参数。
func TestGitNetArgsUnchangedWithoutProxy(t *testing.T) {
	SetGitProxy("")
	got := gitNetArgs("fetch", "--all", "--prune")
	want := []string{"fetch", "--all", "--prune"}
	if len(got) != len(want) {
		t.Fatalf("gitNetArgs = %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("gitNetArgs = %v，期望 %v", got, want)
		}
	}
}
