// testhelpers_test.go —— B233.13 迁置后编排包测试共享的夹具助手。
//
// 职责：承载从 gateway 测试迁出的通用助手（真实 git 仓库夹具、临时 store、
// 任务种子），使 Manager 白盒测试在 internal/orchestration 内自足。
//
// 边界：只做测试夹具，不含断言；不引用 gateway 未导出符号。gateway 侧保留
// 同名等价助手（两包各自独立，避免测试包之间互相 import）。
package orchestration

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"unsafe"

	"github.com/Xsxdot/handoff/internal/agentd"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// hubAnswerWaiters 读取 agentd.Hub 上某个 ticket 的应答等待者数量。
//
// 为什么用反射+unsafe 而不是给 hub 加导出方法：本卡只补测试，不改 gateway
// 生产面（plan §2.4 明确不改 hub.go）。answers 是未导出字段，测试取字段地址后
// 沿 hub 自身的锁读取，既拿到同步信号又不引入 -race 数据竞争。
func hubAnswerWaiters(h *agentd.Hub, ticketID string) int {
	hv := reflect.ValueOf(h).Elem()
	mu := (*sync.Mutex)(unsafe.Pointer(hv.FieldByName("mu").UnsafeAddr()))
	mu.Lock()
	defer mu.Unlock()
	answers := *(*map[string][]chan string)(unsafe.Pointer(hv.FieldByName("answers").UnsafeAddr()))
	return len(answers[ticketID])
}

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

// initGitRepo 在 t.TempDir() 里造一个带初始提交的干净仓库。
func initGitRepo(t *testing.T) string {
	t.Helper()
	return initGitRepoIn(t, t.TempDir())
}

// initGitRepoIn 在指定目录 dir 里造一个带初始提交的干净仓库。
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
func initClonedRepo(t *testing.T, baseBranch string) string {
	t.Helper()
	up := initTestRepo(t)
	gitT(t, up, "branch", baseBranch)
	clone := filepath.Join(t.TempDir(), "clone")
	gitT(t, up, "clone", "-q", up, clone)
	gitT(t, clone, "remote", "rename", "origin", "upstream")
	gitT(t, clone, "config", "user.email", "test@handoff.dev")
	gitT(t, clone, "config", "user.name", "handoff test")
	if out := gitOut(t, clone, "branch", "--list", baseBranch); out != "" {
		t.Fatalf("fixture 失效：克隆里出现了本地分支 %s（%q），触发前提不成立", baseBranch, out)
	}
	return clone
}

// newOriginAndClone 造一个裸 origin 与已推送初始提交的克隆。
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

// commitOnOriginBranch 在指定远端分支上追加提交。
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

// newTestStore 打开临时目录下的真实 store（SQLite 落盘）。
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// seedWaitingReviewTask 创建任务并迁到 waiting_review。
func seedWaitingReviewTask(t *testing.T, st *store.Store, id string) {
	t.Helper()
	createRunningTask(t, st, id)
	if err := st.UpdateTaskState(id, proto.TaskStateWaitingReview); err != nil {
		t.Fatalf("置为 waiting_review: %v", err)
	}
}
