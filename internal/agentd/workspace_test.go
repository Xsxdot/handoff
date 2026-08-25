// workspace 函数（PrepareBranch/Diff/ReadFile/RunCmd）在真实 git 仓库上的行为测试。
//
// 全部测试在 t.TempDir() 里 git init + 造初始提交，不触碰真实工作区；
// git 调用经 gitAt 辅助函数，失败即 t.Fatal（测试环境问题，不是被测行为）。
package agentd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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

// newOriginAndClone 造一个裸 origin 与已推送初始提交的克隆，供基线分支
// 补拉测试使用。两个仓库都在 t.TempDir() 下，避免污染被测仓库。
func newOriginAndClone(t *testing.T) (origin, clone string) {
	t.Helper()
	bareParent := t.TempDir()
	origin = filepath.Join(bareParent, "origin.git")
	gitAt(t, bareParent, "init", "--bare", "-q", origin)
	seed := initGitRepo(t)
	gitAt(t, seed, "remote", "add", "origin", origin)
	gitAt(t, seed, "push", "-q", "origin", "main")
	gitAt(t, origin, "symbolic-ref", "HEAD", "refs/heads/main")
	cloneParent := t.TempDir()
	clone = filepath.Join(cloneParent, "clone")
	gitAt(t, cloneParent, "clone", "-q", origin, clone)
	gitAt(t, clone, "config", "user.email", "test@handoff.dev")
	gitAt(t, clone, "config", "user.name", "handoff test")
	return origin, clone
}

// commitOnOrigin 通过临时克隆向 origin 的 main 推一个提交，返回新提交 sha。
// 直接在裸仓里无法创建提交，临时克隆也必须放在 t.TempDir()，不能建在仓库内。
func commitOnOrigin(t *testing.T, origin, name, content string) string {
	t.Helper()
	writerParent := t.TempDir()
	writer := filepath.Join(writerParent, "writer")
	gitAt(t, writerParent, "clone", "-q", origin, writer)
	gitAt(t, writer, "config", "user.email", "test@handoff.dev")
	gitAt(t, writer, "config", "user.name", "handoff test")
	sha := writeAndCommit(t, writer, name, content)
	gitAt(t, writer, "push", "-q", "origin", "main")
	return sha
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

// TestBranches 验证分支列表按名称返回本地分支，且不含 HEAD 指针。
func TestBranches(t *testing.T) {
	repo := initTestRepo(t)
	// 加一个特性分支
	gitAt(t, repo, "branch", "feature/x")
	got, err := Branches(repo)
	if err != nil {
		t.Fatalf("Branches: %v", err)
	}
	want := map[string]bool{"feature/x": false}
	for _, b := range got {
		if _, ok := want[b]; ok {
			want[b] = true
		}
		if strings.HasPrefix(b, "-") || b == "" {
			t.Errorf("非法分支名混入：%q", b)
		}
	}
	for b, seen := range want {
		if !seen {
			t.Errorf("缺少分支 %s（得到 %v）", b, got)
		}
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
	if !strings.Contains(content.Content, "# repo") {
		t.Fatalf("ReadFile 内容=%q, want 含 # repo", content.Content)
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
		if !strings.Contains(content.Content, "# repo") {
			t.Fatalf("仓内链接内容=%q, want 含 # repo", content.Content)
		}
		content, err = ReadFile(repo, "sublink/ok.txt")
		if err != nil {
			t.Fatalf("仓内目录链接应可读, got %v", err)
		}
		if content.Content != "sub\n" {
			t.Fatalf("仓内目录链接内容=%q, want sub\\n", content.Content)
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
		if !strings.Contains(content.Content, "# repo") {
			t.Fatalf("内容=%q, want 含 # repo", content.Content)
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
// 开头 maxRunOutput 字节并标记 Truncated（截断而非拒绝——与 RunCmd 输出截断
// 语义一致），边界内的文件完整返回。截断提示已迁至 handleTaskFile 端点
// （TestTaskFileKeepsTruncatedNotice 守住 CLI 契约）。
func TestReadFileSizeCap(t *testing.T) {
	repo := initGitRepo(t)
	big := filepath.Join(repo, "big.bin")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), maxRunOutput+4096), 0o644); err != nil {
		t.Fatalf("写大文件: %v", err)
	}
	got, err := ReadFile(repo, "big.bin")
	if err != nil {
		t.Fatalf("ReadFile 大文件: %v", err)
	}
	if !got.Truncated {
		t.Fatalf("大文件应标记 Truncated=true")
	}
	if len(got.Content) != maxRunOutput {
		t.Fatalf("大文件正文长度=%d, want 截断到 %d", len(got.Content), maxRunOutput)
	}
	if got.Size != int64(maxRunOutput+4096) {
		t.Fatalf("Size=%d, want 磁盘真实大小 %d", got.Size, maxRunOutput+4096)
	}

	small, err := ReadFile(repo, "README.md")
	if err != nil {
		t.Fatalf("ReadFile 小文件: %v", err)
	}
	if !strings.Contains(small.Content, "# repo") {
		t.Fatalf("小文件内容=%q, want 含 # repo", small.Content)
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

func TestPrepareWorkspaceDoesNotRunRepositoryHook(t *testing.T) {
	repo := initTestRepo(t)
	marker := filepath.Join(t.TempDir(), "hook-ran")
	t.Setenv("B227_HOOK_MARKER", marker)
	hooksDir := filepath.Join(repo, ".git", "hooks")
	hook := filepath.Join(hooksDir, "post-checkout")
	script := "#!/bin/sh\nprintf 'hook-ran\\n' >> \"$B227_HOOK_MARKER\"\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatalf("写 post-checkout hook: %v", err)
	}
	gitT(t, repo, "config", "core.hooksPath", hooksDir)

	control := filepath.Join(t.TempDir(), "control")
	gitT(t, repo, "worktree", "add", "-q", "-b", "hook-control", control)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("对照组 hook 应运行并创建 marker: %v", err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatalf("清理对照 marker: %v", err)
	}
	gitT(t, repo, "worktree", "remove", "--force", control)

	ws, err := PrepareWorkspace(context.Background(), WorkspaceReq{
		Repo: repo, TaskID: "hook-target-0000", NewWorktree: true,
		WorktreesDir: filepath.Join(t.TempDir(), "worktrees"),
	})
	if err != nil {
		t.Fatalf("带 hooks 覆盖的 PrepareWorkspace: %v", err)
	}
	if !ws.Managed {
		t.Fatalf("target 必须是 managed worktree: %+v", ws)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target worktree 不得运行恶意 hook，stat err=%v", err)
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

// TestResolveBaseBranchAlwaysFetches 分支路径必须无条件补拉。
//
// 为什么不能照抄提交路径的「本地没有才拉」：分支名在本地**永远解析得到**
// （那正是陈旧的那一份），拿「解析得到」当「不用拉」的信号，等于让这个 bug
// 永远走不到修复路径。
func TestResolveBaseBranchAlwaysFetches(t *testing.T) {
	origin, clone := newOriginAndClone(t)
	// 在 origin 上再推一个提交，clone 此时还不知道。
	newSHA := commitOnOrigin(t, origin, "second.txt", "2")

	got, err := ResolveBaseBranch(context.Background(), clone, "origin", "main")
	if err != nil {
		t.Fatalf("ResolveBaseBranch: %v", err)
	}
	if got != newSHA {
		t.Fatalf("应解析到 origin 上的最新提交 %s，实得 %s（说明没补拉）", newSHA, got)
	}
}

// TestResolveDefaultBaseBranchUsesOriginHead 钉住卡派发的默认基线来源：
// 必须取 origin/HEAD 指向的分支名，让后续 D2 从该远端分支补拉尖端；不能
// 直接把执行机当前 HEAD 当成卡派发的起点。
func TestResolveDefaultBaseBranchUsesOriginHead(t *testing.T) {
	_, clone := newOriginAndClone(t)
	got, err := resolveDefaultBaseBranch(context.Background(), clone)
	if err != nil {
		t.Fatalf("解析 origin/HEAD: %v", err)
	}
	if got != "main" {
		t.Fatalf("默认分支=%q，期望 origin/HEAD 指向的 main", got)
	}
}

// TestResolveDefaultBaseBranchRejectsMissingOriginHead 钉住失败闭环：没有
// origin/HEAD 时必须拒发并带真因，不能静默退回本地 main/master 或 HEAD。
func TestResolveDefaultBaseBranchRejectsMissingOriginHead(t *testing.T) {
	repo := initTestRepo(t)
	if _, err := resolveDefaultBaseBranch(context.Background(), repo); err == nil {
		t.Fatal("没有 origin/HEAD 时必须拒绝解析默认分支")
	} else if !strings.Contains(err.Error(), "origin/HEAD") {
		t.Fatalf("错误必须说明 origin/HEAD 缺失真因：%v", err)
	}
}

// TestResolveDispatchBaseLocalBranchUsesConfiguredRemote 本地 heads 存在时，D2 应取
// branch.<name>.remote，而不是无条件猜 origin。
func TestResolveDispatchBaseLocalBranchUsesConfiguredRemote(t *testing.T) {
	origin, clone := newOriginAndClone(t)
	upstream, _ := newOriginAndClone(t)
	gitT(t, clone, "remote", "add", "upstream", upstream)
	gitT(t, clone, "fetch", "-q", "upstream")

	originSHA := commitOnOrigin(t, origin, "origin.txt", "origin")
	upstreamSHA := commitOnOrigin(t, upstream, "upstream.txt", "upstream")
	gitT(t, clone, "config", "branch.main.remote", "upstream")

	got, fetched, err := resolveDispatchBase(context.Background(), clone, "main", false)
	if err != nil {
		t.Fatalf("resolveDispatchBase: %v", err)
	}
	if !fetched {
		t.Fatalf("本地分支的配置远端应触发 D2 fetch")
	}
	if originSHA == upstreamSHA {
		t.Fatalf("夹具必须让两个远端停在不同提交")
	}
	if got != upstreamSHA {
		t.Fatalf("应解析到配置远端 upstream 的最新提交 %s，实得 %s", upstreamSHA, got)
	}
	if got == originSHA {
		t.Fatalf("不应解析到 origin 的提交 %s：配置远端回归网未生效", originSHA)
	}
}

// TestResolveDispatchBaseLocalBranchUsesLocalRef 验证 local_base_branch=true 只读
// refs/heads/<branch>，即使 origin 不可达也能解析本地尖端且绝不 fetch。
func TestResolveDispatchBaseLocalBranchUsesLocalRef(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "branch", "work")
	gitT(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing-origin.git"))
	want := gitOut(t, repo, "rev-parse", "refs/heads/work^{commit}")

	got, fetched, err := resolveDispatchBase(context.Background(), repo, "work", true)
	if err != nil {
		t.Fatalf("resolveDispatchBase(local): %v", err)
	}
	if fetched {
		t.Fatal("本地工作分支解析不得触发 fetch")
	}
	if got != want {
		t.Fatalf("本地工作分支尖端=%s，期望=%s", got, want)
	}
}

// TestResolveDispatchBaseLocalBranchMissingRejects 验证本地 ref 缺失时使用可行动的
// ErrBadWorkspaceReq 拒发文案，而不是退回 HEAD 或访问远端。
func TestResolveDispatchBaseLocalBranchMissingRejects(t *testing.T) {
	repo := initTestRepo(t)
	gitT(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "missing-origin.git"))

	_, fetched, err := resolveDispatchBase(context.Background(), repo, "missing", true)
	if fetched {
		t.Fatal("本地工作分支缺失时不得触发 fetch")
	}
	if !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("缺失本地工作分支应返回 ErrBadWorkspaceReq，实得 %v", err)
	}
	for _, want := range []string{"工作分支只存在于创建它的那台机器", "先 push 到 origin", "--base"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("错误应包含 %q，实得 %v", want, err)
		}
	}
}

// TestDispatchNormalBaseSemanticsUnchanged 反向锁住 local_base_branch=false 的三条
// 既有语义：普通分支仍 D2 补拉、普通空 Base 仍取本地 HEAD、卡空基线仍读 origin/HEAD。
func TestDispatchNormalBaseSemanticsUnchanged(t *testing.T) {
	t.Run("普通分支仍补拉", func(t *testing.T) {
		origin, clone := newOriginAndClone(t)
		want := commitOnOrigin(t, origin, "second.txt", "second")
		got, fetched, err := resolveDispatchBase(context.Background(), clone, "main", false)
		if err != nil {
			t.Fatalf("resolveDispatchBase: %v", err)
		}
		if !fetched || got != want {
			t.Fatalf("普通分支应 D2 补拉到 %s，got=%s fetched=%v", want, got, fetched)
		}
	})
	t.Run("普通空基线仍取 HEAD", func(t *testing.T) {
		repo := initTestRepo(t)
		want := gitOut(t, repo, "rev-parse", "HEAD")
		got, err := ResolveBaseline(context.Background(), repo, "")
		if err != nil {
			t.Fatalf("ResolveBaseline: %v", err)
		}
		if got.Start != want || got.Fetched {
			t.Fatalf("普通空基线应取 HEAD=%s，got=%+v", want, got)
		}
	})
	t.Run("卡空基线仍读 origin HEAD", func(t *testing.T) {
		_, clone := newOriginAndClone(t)
		got, err := resolveDefaultBaseBranch(context.Background(), clone)
		if err != nil {
			t.Fatalf("resolveDefaultBaseBranch: %v", err)
		}
		if got != "main" {
			t.Fatalf("卡空基线应读 main，实得 %q", got)
		}
	})
}

// TestResolveBaseBranchMissingBranch origin 上没有该分支时拒绝，且带原文。
func TestResolveBaseBranchMissingBranch(t *testing.T) {
	_, clone := newOriginAndClone(t)
	_, err := ResolveBaseBranch(context.Background(), clone, "origin", "no-such-branch")
	if err == nil {
		t.Fatalf("不存在的分支应报错")
	}
	if !strings.Contains(err.Error(), "no-such-branch") {
		t.Fatalf("错误里应带分支名: %v", err)
	}
}

// TestResolveDispatchBaseCommitISHKeepsOldPath --base 的短 sha、tag 与 origin/<分支>
// 都是既有 commit-ish 形态，不能被 D2 当成普通分支去 fetch。
// 把 origin URL 改成不可用地址：若误走补拉路径，测试会立刻失败；旧解析路径只读
// 本地对象/ref，仍应成功。
func TestResolveDispatchBaseCommitISHKeepsOldPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		base func(t *testing.T, repo string) string
	}{
		{name: "短 sha", base: func(t *testing.T, repo string) string {
			full := gitOut(t, repo, "rev-parse", "HEAD")
			return full[:7]
		}},
		{name: "origin 分支全名", base: func(t *testing.T, repo string) string {
			return "origin/main"
		}},
		{name: "tag", base: func(t *testing.T, repo string) string {
			gitT(t, repo, "tag", "v-test")
			return "v-test"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, clone := newOriginAndClone(t)
			base := tc.base(t, clone)
			want := gitOut(t, clone, "rev-parse", base+"^{commit}")
			gitT(t, clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing-origin.git"))

			got, fetched, err := resolveDispatchBase(context.Background(), clone, base, false)
			if err != nil {
				t.Fatalf("resolveDispatchBase(%q): %v", base, err)
			}
			if fetched {
				t.Fatalf("%q 不应触发 origin fetch", base)
			}
			if got != want {
				t.Fatalf("解析结果=%s，期望=%s", got, want)
			}
		})
	}
}

// TestResolveDispatchBaseAmbiguousRemoteOnlyBranch B76：本地没有同名 heads、origin
// 与 upstream 却都有同名分支时，不能走 D2 静默拿某一棵，必须保留多远端拒发。
func TestResolveDispatchBaseAmbiguousRemoteOnlyBranch(t *testing.T) {
	up := initTestRepo(t)
	gitT(t, up, "branch", "shared-base")
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, up, "clone", "-q", up, clone)
	gitT(t, clone, "remote", "add", "upstream", up)
	gitT(t, clone, "fetch", "-q", "upstream")

	_, fetched, err := resolveDispatchBase(context.Background(), clone, "shared-base", false)
	if fetched {
		t.Fatalf("多远端歧义不得触发 D2 fetch")
	}
	if !errors.Is(err, ErrBadWorkspaceReq) {
		t.Fatalf("多远端歧义应按 ErrBadWorkspaceReq 拒发，实得 %v", err)
	}
	if !strings.Contains(err.Error(), "多个远端") {
		t.Fatalf("错误必须保留多远端语义，实得 %v", err)
	}
}

// TestResolveDispatchBaseRemoteOnlyUpstreamStillFetches 只有 upstream 一棵远端有分支
// 时，必须从 upstream 补拉而不是写死 fetch origin；在 upstream 上追加提交来证明
// 读到的是补拉后的尖端，而不是陈旧的 remote-tracking ref。
func TestResolveDispatchBaseRemoteOnlyUpstreamStillFetches(t *testing.T) {
	origin, clone := newOriginAndClone(t)
	gitT(t, clone, "remote", "rename", "origin", "upstream")
	gitT(t, clone, "checkout", "-q", "--detach", "HEAD")
	gitT(t, clone, "branch", "-D", "main")
	newSHA := commitOnOrigin(t, origin, "second.txt", "2")

	got, fetched, err := resolveDispatchBase(context.Background(), clone, "main", false)
	if err != nil {
		t.Fatalf("resolveDispatchBase: %v", err)
	}
	if !fetched {
		t.Fatalf("upstream-only remote-tracking branch 必须触发 D2 fetch")
	}
	if got != newSHA {
		t.Fatalf("应解析到 upstream 最新提交 %s，实得 %s", newSHA, got)
	}
}

// TestResolveDispatchBaseRemoteOnlyOriginStillFetches 只有 origin/<分支> 远程跟踪
// ref、没有本地 heads 时仍要补拉；不能为了避开 B76 歧义把这条正常路径一并漏掉。
func TestResolveDispatchBaseRemoteOnlyOriginStillFetches(t *testing.T) {
	origin, clone := newOriginAndClone(t)
	gitT(t, clone, "checkout", "-q", "--detach", "HEAD")
	gitT(t, clone, "branch", "-D", "main")
	newSHA := commitOnOrigin(t, origin, "second.txt", "2")

	got, fetched, err := resolveDispatchBase(context.Background(), clone, "main", false)
	if err != nil {
		t.Fatalf("resolveDispatchBase: %v", err)
	}
	if !fetched {
		t.Fatalf("origin-only remote-tracking branch 必须触发 D2 fetch")
	}
	if got != newSHA {
		t.Fatalf("应解析到 origin 最新提交 %s，实得 %s", newSHA, got)
	}
}

// TestListDirBasic 覆盖列举的四条硬约束：只列一层、目录在前、字典序、rel 为空即根。
func TestListDirBasic(t *testing.T) {
	repo := t.TempDir()
	mustMkdirAll(t, filepath.Join(repo, "internal", "agentd"))
	mustMkdirAll(t, filepath.Join(repo, "cmd"))
	mustWriteFile(t, filepath.Join(repo, "go.mod"), "module x\n")
	mustWriteFile(t, filepath.Join(repo, "README.md"), "hi")

	entries, err := ListDir(repo, "")
	if err != nil {
		t.Fatalf("ListDir 根目录: %v", err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, fmt.Sprintf("%s/%v", e.Name, e.IsDir))
	}
	want := []string{"cmd/true", "internal/true", "README.md/false", "go.mod/false"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("根目录列举 = %v, want %v", got, want)
	}

	// 只列一层：internal 下只应有 agentd，不含 internal/agentd 的内容
	sub, err := ListDir(repo, "internal")
	if err != nil {
		t.Fatalf("ListDir internal: %v", err)
	}
	if len(sub) != 1 || sub[0].Name != "agentd" || !sub[0].IsDir {
		t.Errorf("internal 列举 = %+v, want 只有目录 agentd", sub)
	}

	// 普通文件带 size
	root, err := ListDir(repo, ".")
	if err != nil {
		t.Fatalf("ListDir .: %v", err)
	}
	for _, e := range root {
		if e.Name == "go.mod" && e.Size != int64(len("module x\n")) {
			t.Errorf("go.mod size = %d, want %d", e.Size, len("module x\n"))
		}
	}
}

// TestListDirRejectsEscape 断言列举与 ReadFile 共用同一条逃逸红线。
func TestListDirRejectsEscape(t *testing.T) {
	repo := t.TempDir()
	for _, rel := range []string{"..", "../etc", "/etc", filepath.Join("a", "..", "..")} {
		if _, err := ListDir(repo, rel); !errors.Is(err, ErrPathEscape) {
			t.Errorf("ListDir(%q) err = %v, want ErrPathEscape", rel, err)
		}
	}
}

// TestListDirOnFileIsNotDir 断言把文件当目录列举时给出可辨识的错误（映射 400）。
func TestListDirOnFileIsNotDir(t *testing.T) {
	repo := t.TempDir()
	mustWriteFile(t, filepath.Join(repo, "go.mod"), "module x\n")
	if _, err := ListDir(repo, "go.mod"); !errors.Is(err, ErrPathNotDir) {
		t.Errorf("ListDir(go.mod) err = %v, want ErrPathNotDir", err)
	}
}

// TestListDirMissing 断言不存在的子目录返回 fs.ErrNotExist（映射 404）。
func TestListDirMissing(t *testing.T) {
	repo := t.TempDir()
	if _, err := ListDir(repo, "nope"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ListDir(nope) err = %v, want fs.ErrNotExist", err)
	}
}

// mustMkdirAll 建目录，失败即 Fatal。
func mustMkdirAll(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录 %s: %v", dir, err)
	}
}

// mustWriteFile 写文件，失败即 Fatal。
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件 %s: %v", path, err)
	}
}

func TestCreateEntryFileAndDir(t *testing.T) {
	repo := t.TempDir()
	got, err := CreateEntry(repo, "", "handler.go", "file")
	if err != nil {
		t.Fatalf("建文件: %v", err)
	}
	if got.Name != "handler.go" || got.IsDir {
		t.Fatalf("返回项不对: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(repo, "handler.go")); err != nil {
		t.Fatalf("文件没落盘: %v", err)
	}
	if _, err := CreateEntry(repo, "", "internal", "dir"); err != nil {
		t.Fatalf("建目录: %v", err)
	}
	fi, err := os.Stat(filepath.Join(repo, "internal"))
	if err != nil || !fi.IsDir() {
		t.Fatalf("目录没落盘: %v", err)
	}
}

func TestCreateEntryRejects(t *testing.T) {
	repo := t.TempDir()
	if _, err := CreateEntry(repo, "", "a.go", "file"); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name, parent, entry, kind string
		want                      error
	}{
		{"同名", "", "a.go", "file", ErrEntryExists},
		{"名字含斜杠", "", "x/y.go", "file", ErrBadEntryName},
		{"名字为空", "", "", "file", ErrBadEntryName},
		{"名字是点点", "", "..", "dir", ErrBadEntryName},
		{"父目录逃逸", "..", "a.go", "file", ErrPathEscape},
		{"命中 git 目录", ".git", "config", "file", ErrGitDirWrite},
		{"父目录不存在", "nope", "a.go", "file", ErrEntryNotFound},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := CreateEntry(repo, c.parent, c.entry, c.kind)
			if !errors.Is(err, c.want) {
				t.Fatalf("要 %v，得到 %v", c.want, err)
			}
		})
	}
}

func TestRenameEntry(t *testing.T) {
	repo := t.TempDir()
	if _, err := CreateEntry(repo, "", "old.go", "file"); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameEntry(repo, "old.go", "new.go"); err != nil {
		t.Fatalf("改名: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "new.go")); err != nil {
		t.Fatalf("新名字不在: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "old.go")); !os.IsNotExist(err) {
		t.Fatal("旧名字还在")
	}
	if _, err := CreateEntry(repo, "", "taken.go", "file"); err != nil {
		t.Fatal(err)
	}
	if _, err := RenameEntry(repo, "new.go", "taken.go"); !errors.Is(err, ErrEntryExists) {
		t.Fatal("撞名应当被拒")
	}
	if _, err := RenameEntry(repo, "new.go", "a/b.go"); !errors.Is(err, ErrBadEntryName) {
		t.Fatal("新名字含斜杠应当被拒（本期不做跨目录移动）")
	}
	if _, err := RenameEntry(repo, ".git", "x"); !errors.Is(err, ErrGitDirWrite) {
		t.Fatal("改名 .git 应当被拒")
	}
}

func TestDeleteEntry(t *testing.T) {
	repo := t.TempDir()
	if _, err := CreateEntry(repo, "", "gone.go", "file"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteEntry(repo, "gone.go"); err != nil {
		t.Fatalf("删文件: %v", err)
	}
	// 非空目录也要能删
	if _, err := CreateEntry(repo, "", "d", "dir"); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateEntry(repo, "d", "inner.go", "file"); err != nil {
		t.Fatal(err)
	}
	if err := DeleteEntry(repo, "d"); err != nil {
		t.Fatalf("删非空目录: %v", err)
	}
	if err := DeleteEntry(repo, "nope"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatal("删不存在的应当 ErrEntryNotFound")
	}
	if err := DeleteEntry(repo, ".git"); !errors.Is(err, ErrGitDirWrite) {
		t.Fatal("删 .git 应当被拒")
	}
	if err := DeleteEntry(repo, ""); !errors.Is(err, ErrBadEntryName) {
		t.Fatal("删工作树根本身应当被拒")
	}
}

func TestEntryOpsSymlinkEscape(t *testing.T) {
	// 与 TestReadFileSymlinkEscape 同款手法：仓库内放一个指向仓库外的链接，
	// 四个动作都必须被 os.OpenRoot 挡下，而不是顺着链接操作到仓库外
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "victim.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, "link")); err != nil {
		t.Skipf("本平台建不了符号链接: %v", err)
	}
	if _, err := CreateEntry(repo, "link", "new.txt", "file"); err == nil {
		t.Fatal("经链接在仓库外建文件竟然成功了")
	}
	if err := DeleteEntry(repo, "link/victim.txt"); err == nil {
		t.Fatal("经链接删仓库外文件竟然成功了")
	}
	// RenameEntry 与 CopyEntry 的 rel 逃逸拦截不在 cleanEntryRel（那层只做词汇
	// 层 Clean），而在 os.Root 对链接的实际解析——root.Stat 顺着 link 解析到
	// 仓库外报 "path escapes from parent"，两处都应落 ErrPathEscape
	if _, err := RenameEntry(repo, "link/victim.txt", "y.txt"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("经链接改名仓库外文件应当 ErrPathEscape，得到: %v", err)
	}
	if _, err := CopyEntry(repo, "link/victim.txt"); !errors.Is(err, ErrPathEscape) {
		t.Fatalf("经链接复制仓库外文件应当 ErrPathEscape，得到: %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "victim.txt")); err != nil {
		t.Fatal("仓库外的文件被动了")
	}
}

func TestCopyEntryNaming(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "foo.go"), []byte("package main"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := CopyEntry(repo, "foo.go")
	if err != nil {
		t.Fatalf("第一次复制: %v", err)
	}
	if first.Name != "foo copy.go" {
		t.Fatalf("第一份副本要叫 %q，得到 %q", "foo copy.go", first.Name)
	}
	second, err := CopyEntry(repo, "foo.go")
	if err != nil {
		t.Fatalf("第二次复制: %v", err)
	}
	if second.Name != "foo copy 2.go" {
		t.Fatalf("第二份副本要叫 %q，得到 %q", "foo copy 2.go", second.Name)
	}
	// 内容要真的复制过去
	b, err := os.ReadFile(filepath.Join(repo, "foo copy.go"))
	if err != nil || string(b) != "package main" {
		t.Fatalf("副本内容不对: %q %v", b, err)
	}
	// 无扩展名
	if err := os.WriteFile(filepath.Join(repo, "Makefile"), []byte("all:"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CopyEntry(repo, "Makefile")
	if err != nil || got.Name != "Makefile copy" {
		t.Fatalf("无扩展名副本要叫 %q，得到 %q（err=%v）", "Makefile copy", got.Name, err)
	}
}

func TestCopyEntryDirRecursive(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "d", "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "d", "sub", "x.go"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := CopyEntry(repo, "d")
	if err != nil {
		t.Fatalf("复制目录: %v", err)
	}
	if got.Name != "d copy" || !got.IsDir {
		t.Fatalf("目录副本不对: %+v", got)
	}
	b, err := os.ReadFile(filepath.Join(repo, "d copy", "sub", "x.go"))
	if err != nil || string(b) != "x" {
		t.Fatalf("递归内容没复制过去: %q %v", b, err)
	}
	// 带点的目录名整体当 base，不拆扩展名（spec §3.4，Mac Finder 同款）
	if err := os.MkdirAll(filepath.Join(repo, "a.b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "a.b", "inner.go"), []byte("i"), 0o644); err != nil {
		t.Fatal(err)
	}
	dotted, err := CopyEntry(repo, "a.b")
	if err != nil {
		t.Fatalf("复制带点目录: %v", err)
	}
	if dotted.Name != "a.b copy" {
		t.Fatalf("带点目录副本要叫 %q，得到 %q", "a.b copy", dotted.Name)
	}
}

func TestCopyEntryRejects(t *testing.T) {
	repo := t.TempDir()
	if err := CopyEntryRejectHelper(repo); err != nil {
		t.Fatal(err)
	}
	if _, err := CopyEntry(repo, "nope"); !errors.Is(err, ErrEntryNotFound) {
		t.Fatal("复制不存在的应当 ErrEntryNotFound")
	}
	if _, err := CopyEntry(repo, ".git"); !errors.Is(err, ErrGitDirWrite) {
		t.Fatal("复制 .git 应当被拒")
	}
	if _, err := CopyEntry(repo, "../x"); !errors.Is(err, ErrPathEscape) {
		t.Fatal("逃逸路径应当被拒")
	}
}

// CopyEntryRejectHelper 建一个 .git 目录，让上面的 .git 用例有东西可撞。
func CopyEntryRejectHelper(repo string) error {
	return os.MkdirAll(filepath.Join(repo, ".git"), 0o755)
}

func TestSearchInDirHitsAndSkips(t *testing.T) {
	repo := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mk("a.go", "package main\nfunc needle() {}\n")
	mk("sub/b.go", "// needle 在注释里\n")
	mk(".git/config", "needle\n")
	mk("node_modules/c.js", "needle\n")

	got, err := SearchInDir(context.Background(), repo, "", "needle", 0)
	if err != nil {
		t.Fatalf("搜索: %v", err)
	}
	rels := map[string]bool{}
	for _, h := range got.Hits {
		rels[h.Rel] = true
	}
	if !rels["a.go"] || !rels["sub/b.go"] {
		t.Fatalf("正常文件没命中: %+v", got.Hits)
	}
	if rels[".git/config"] || rels["node_modules/c.js"] {
		t.Fatalf(".git / node_modules 必须被跳过: %+v", got.Hits)
	}
	// 行号从 1 起
	for _, h := range got.Hits {
		if h.Rel == "a.go" && h.Line != 2 {
			t.Fatalf("行号要从 1 起算，needle 在第 2 行，得到 %d", h.Line)
		}
	}
}

func TestSearchInDirLimit(t *testing.T) {
	repo := t.TempDir()
	var sb strings.Builder
	for i := 0; i < 50; i++ {
		sb.WriteString("needle\n")
	}
	if err := os.WriteFile(filepath.Join(repo, "many.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SearchInDir(context.Background(), repo, "", "needle", 10)
	if err != nil {
		t.Fatalf("搜索: %v", err)
	}
	if len(got.Hits) != 10 {
		t.Fatalf("limit=10 要恰好 10 条，得到 %d", len(got.Hits))
	}
	if !got.Truncated {
		t.Fatal("撞到上限必须标 Truncated——否则「10 条」会被读成「只有 10 处」")
	}
}

func TestSearchInDirScopeAndRejects(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "only", "in.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "out.txt"), []byte("needle\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SearchInDir(context.Background(), repo, "only", "needle", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Hits) != 1 || got.Hits[0].Rel != "only/in.txt" {
		t.Fatalf("范围没生效: %+v", got.Hits)
	}
	if _, err := SearchInDir(context.Background(), repo, "", "", 0); err == nil {
		t.Fatal("空关键词应当被拒")
	}
	if _, err := SearchInDir(context.Background(), repo, "../x", "needle", 0); !errors.Is(err, ErrPathEscape) {
		t.Fatal("逃逸范围应当被拒")
	}
}

func TestSearchInDirDefaultLimit(t *testing.T) {
	repo := t.TempDir()
	var sb strings.Builder
	for i := 0; i < searchDefaultLimit+50; i++ {
		sb.WriteString("needle\n")
	}
	if err := os.WriteFile(filepath.Join(repo, "many.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SearchInDir(context.Background(), repo, "", "needle", 0)
	if err != nil {
		t.Fatalf("搜索: %v", err)
	}
	if len(got.Hits) != searchDefaultLimit {
		t.Fatalf("limit<=0 时默认取 %d，得到 %d", searchDefaultLimit, len(got.Hits))
	}
	if !got.Truncated {
		t.Fatal("命中数超过默认上限必须标 Truncated")
	}
}

func TestSearchInDirLimitCapped(t *testing.T) {
	repo := t.TempDir()
	var sb strings.Builder
	for i := 0; i < searchMaxLimit+50; i++ {
		sb.WriteString("needle\n")
	}
	if err := os.WriteFile(filepath.Join(repo, "many.txt"), []byte(sb.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := SearchInDir(context.Background(), repo, "", "needle", searchMaxLimit*10)
	if err != nil {
		t.Fatalf("搜索: %v", err)
	}
	if len(got.Hits) > searchMaxLimit {
		t.Fatalf("limit 超过 %d 要收敛到 %d，得到 %d", searchMaxLimit, searchMaxLimit, len(got.Hits))
	}
	if !got.Truncated {
		t.Fatal("命中数超过收敛后的上限必须标 Truncated")
	}
}

// TestRemoveManagedWorktreeRetries 钉住重试语义。
//
// 为什么改函数内部而不是调用点：实际有四个调用点（workspace.go 的派发失败补偿、
// manager.go 的 done/stop/失配三处），改函数一处覆盖全部，也不会漏掉将来新增的。
//
// 为什么不去等子进程：child.pid 虽然有，但用 pid 等存活会重新引入 pid 复用误判
// ——那正是整个 prochost 用文件锁而非 pid 判存活的原因。
func TestRemoveManagedWorktreeRetries(t *testing.T) {
	oldAttempts, oldBackoff := removeWorktreeAttempts, removeWorktreeBackoff
	t.Cleanup(func() { removeWorktreeAttempts, removeWorktreeBackoff = oldAttempts, oldBackoff })
	removeWorktreeAttempts, removeWorktreeBackoff = 3, time.Millisecond

	calls := 0
	oldRun := worktreeRemoveFn
	t.Cleanup(func() { worktreeRemoveFn = oldRun })
	worktreeRemoveFn = func(ctx context.Context, repo, workdir string) (string, error) {
		calls++
		if calls < 3 {
			return "fatal: 'x' contains modified or untracked files", errors.New("exit 128")
		}
		return "", nil
	}

	if err := RemoveManagedWorktree(context.Background(), "/repo", "/wt"); err != nil {
		t.Fatalf("第三次应成功：%v", err)
	}
	if calls != 3 {
		t.Fatalf("应重试到成功为止：calls=%d want=3", calls)
	}
}

// TestRemoveManagedWorktreeExhausted 钉住耗尽后仍返回错误（调用方据此只 Warn 不阻断）。
func TestRemoveManagedWorktreeExhausted(t *testing.T) {
	oldAttempts, oldBackoff := removeWorktreeAttempts, removeWorktreeBackoff
	t.Cleanup(func() { removeWorktreeAttempts, removeWorktreeBackoff = oldAttempts, oldBackoff })
	removeWorktreeAttempts, removeWorktreeBackoff = 2, time.Millisecond

	calls := 0
	oldRun := worktreeRemoveFn
	t.Cleanup(func() { worktreeRemoveFn = oldRun })
	worktreeRemoveFn = func(ctx context.Context, repo, workdir string) (string, error) {
		calls++
		return "被占用", errors.New("exit 128")
	}

	err := RemoveManagedWorktree(context.Background(), "/repo", "/wt")
	if err == nil {
		t.Fatal("耗尽后必须返回错误")
	}
	if calls != 2 {
		t.Fatalf("应按 removeWorktreeAttempts 次数重试：calls=%d want=2", calls)
	}
}
