// gittesthelpers_test.go —— agentd 包内测试共用的 git 夹具（B233.15：迁出实现后本包仍需）。
//
// 职责：在 t.TempDir() 里造真实 git 仓库与克隆，供本包测试使用。
// 边界：只做夹具搭建，不触碰包外或真实工作区。
package agentd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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

// commitOnOriginBranch 在指定远端分支上追加提交，供工作分支快进并集夹具使用。
func commitOnOriginBranch(t *testing.T, origin, branch, name, content string) string {
	t.Helper()
	writerParent := t.TempDir()
	writer := filepath.Join(writerParent, "writer")
	gitAt(t, writerParent, "clone", "-q", origin, writer)
	gitAt(t, writer, "checkout", "-q", branch)
	gitAt(t, writer, "config", "user.email", "test@handoff.dev")
	gitAt(t, writer, "config", "user.name", "handoff test")
	sha := writeAndCommit(t, writer, name, content)
	gitAt(t, writer, "push", "-q", "origin", branch)
	return sha
}

// newWorktree 在 repo 下建一个 managed 风格的工作树并返回其路径。
func newWorktree(t *testing.T, repo, name, branch string) string {
	t.Helper()
	dir := filepath.Join(filepath.Dir(repo), name)
	gitAt(t, repo, "worktree", "add", "-q", dir, "-b", branch)
	return dir
}

// canonPath 把路径归一到可比较形态（agentd 测试私有副本；工作区包另有一份）。
func canonPath(p string) string {
	p = filepath.Clean(p)
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return filepath.Clean(r)
	}
	if r, err := filepath.EvalSymlinks(filepath.Dir(p)); err == nil {
		return filepath.Join(r, filepath.Base(p))
	}
	return p
}

// mustWriteFile 写文件，失败即 Fatal（agentd 测试私有副本）。
func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写文件 %s: %v", path, err)
	}
}

// testBigFileBytes 是「超过工作区读取上限」的测试文件大小（上限 1 MiB，取 2 MiB 安全越过）。
const testBigFileBytes = 1 << 21
