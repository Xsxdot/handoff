// clone_lock_test.go —— 克隆不占用仓库 fetch 锁的性质测试（B233.15 从 agentd 迁入）。
//
// 职责：钉住「fetch 锁只保护 fetch 与目标 ref 读取，不扩散到 clone」。
// 边界：只测 CloneRepo 与 fetch 锁的独立性。
package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestCloneRepoDoesNotWaitForRepoFetchLock 持有克隆目标路径对应的 fetch 锁时仍允许
// clone 完成，锁只保护 fetch 与目标 ref 读取，不扩散到项目登记的 clone。
func TestCloneRepoDoesNotWaitForRepoFetchLock(t *testing.T) {
	src := initGitRepo(t)
	destParent := t.TempDir()
	dest := filepath.Join(destParent, "clone")

	resultCh := make(chan error, 1)
	err := withRepoFetchLock(dest, func() error {
		go func() {
			_, cloneErr := CloneRepo(context.Background(), destParent, src, dest, false)
			resultCh <- cloneErr
		}()
		select {
		case cloneErr := <-resultCh:
			return cloneErr
		case <-time.After(5 * time.Second):
			return fmt.Errorf("clone 在 fetch 锁持有期间未完成")
		}
	})
	if err != nil {
		t.Fatalf("fetch 锁不应阻塞 clone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err != nil {
		t.Fatalf("clone 目标应存在 .git: %v", err)
	}
}
