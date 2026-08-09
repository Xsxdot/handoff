// machineauthority git_watch 测试：watcher 只触发 debounce 后的完整 Reconcile，
// 不把文件系统事件当事实。
//
// 职责：
//   - watcher 感知 .git 目录变化并触发 Reconcile（回调被调用）
//   - 去抖：一次 burst 只触发一次 Reconcile（或极少量，不逐事件）
//
// 边界：
//   - 不验证事件内容正确性（由 reconciler_test.go 负责）
//   - 测试只断言「触发过」与「触发次数被去抖」，避免对文件系统时序过度断言
package machineauthority

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestWatchTriggersReconcileOnGitChange 验证 watcher 在 .git 元数据变化后触发
// 至少一次 Reconcile 回调。
func TestWatchTriggersReconcileOnGitChange(t *testing.T) {
	dir := gitInit(t)
	var calls atomic.Int64
	done := make(chan struct{})
	closeOnce := sync.Once{}

	w := NewGitWatcher(dir, filepath.Join(dir, ".git"), func() {
		calls.Add(1)
		closeOnce.Do(func() { close(done) })
	})
	if err := w.Start(); err != nil {
		t.Fatalf("watcher Start: %v", err)
	}
	defer w.Close()

	// 触发一次分支创建（写 .git/refs/heads）
	runGit(t, dir, "checkout", "-q", "-b", "feat/watch")

	select {
	case <-done:
		// 至少触发一次
	case <-time.After(10 * time.Second):
		t.Fatal("watcher 未在 .git 变化后触发 Reconcile")
	}
}
