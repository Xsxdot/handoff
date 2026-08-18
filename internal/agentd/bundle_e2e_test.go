// 本文件证明 bundle 链路端到端能搬运 git 对象：真 handler 出包 → 把包 fetch 进第二个仓库。
//
// 职责：
//   - 承重用例 TestBundleEndToEndCarriesCommit：状态码对了只说明 HTTP 层对了，
//     这里还要证明那条 commit 真的被 bundle 搬到了另一个仓库
//
// 边界：
//   - 不重定义既有辅助（newBundleRepo/headSHAForTest/newBundleEnv/getBundle），只复用
//   - 临时文件一律落 t.TempDir()，绝不落进被 fetch 的仓库
//   - 不需要网络，全在 t.TempDir() 里
package agentd

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 端到端：真 handler 出包 → 把包 fetch 进第二个仓库 → 断言那个 commit 真的到了。
//
// 这条是承重的：状态码对了只说明 HTTP 层对了，证明不了这条链路真能搬运 git 对象。
// 不需要网络，全在 t.TempDir() 里。
func TestBundleEndToEndCarriesCommit(t *testing.T) {
	env, taskID, remote, base := newBundleEnv(t, "feat/x")
	wantSHA := headSHAForTest(t, remote, "feat/x")

	// 协调者侧：一个只有 main 的本地仓库（模拟「我有基线、没有任务分支」）。
	//
	// **`--no-local` 是承重的**：同机克隆走 git 的 local 优化，会硬链接整个对象库，
	// 于是 `--single-branch` 只限制了 refs、feat/x 的提交照样在本地——前置条件当场
	// 不成立。这条在基线上实测过：不加 --no-local 时 cat-file -e 报 YES。
	local := t.TempDir()
	gitClone(t, remote, local)
	if hasCommitInDir(t, local, wantSHA) {
		t.Fatal("前置条件不成立：本地此时不该已有 feat/x 的提交")
	}

	// 取包
	resp, body := getBundle(t, env, taskID, base)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("取包应为 200，实得 %d，体 %s", resp.StatusCode, body)
	}
	// 包落到独立的临时目录，**不能**落进 local——那会弄脏被 fetch 的仓库
	bundlePath := filepath.Join(t.TempDir(), "task.bundle")
	if err := os.WriteFile(bundlePath, body, 0o644); err != nil {
		t.Fatalf("落盘 bundle: %v", err)
	}

	// 把包当 transport fetch 进本地仓库
	gitInDir(t, local, "fetch", bundlePath, "feat/x:feat/x")

	if !hasCommitInDir(t, local, wantSHA) {
		t.Fatalf("commit %s 应已被 bundle 搬到本地", wantSHA)
	}
	if got := strings.TrimSpace(gitInDir(t, local, "rev-parse", "feat/x")); got != wantSHA {
		t.Errorf("本地 feat/x 应指向 %s，实得 %s", wantSHA, got)
	}
}

// gitClone 把 remote 只克隆 main 分支到 dst（dst 须已存在且为空）。
//
// --no-local 强制走 git 传输协议而非硬链接对象库，见调用点的注释。
func gitClone(t *testing.T, remote, dst string) {
	t.Helper()
	c := exec.Command("git", "clone", "--quiet", "--no-local",
		"--branch", "main", "--single-branch", remote, dst)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("git clone: %v\n%s", err, out)
	}
}

// gitInDir 在 dir 里跑 git，失败即 t.Fatal，返回 stdout+stderr。
func gitInDir(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", append([]string{"-C", dir}, args...)...)
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// hasCommitInDir 报告 dir 里是否已有该 commit 对象。
func hasCommitInDir(t *testing.T, dir, sha string) bool {
	t.Helper()
	return exec.Command("git", "-C", dir, "cat-file", "-e", sha+"^{commit}").Run() == nil
}
