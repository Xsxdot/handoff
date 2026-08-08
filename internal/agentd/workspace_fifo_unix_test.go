//go:build unix

// workspace_fifo_unix_test.go —— 仓库内 FIFO 的读取行为（unix 专属）。
//
// 职责：验证 ReadFile 不会因仓库内的特殊文件而永久阻塞。
//
// 边界：仅 unix（Windows 无 mkfifo 语义）。
package agentd

import (
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestReadFileFifoDoesNotHang 验证仓库内的 FIFO 不会让读取永久阻塞。
//
// 缺陷形态：IsRegular 检查排在 Open 之后，而对没有写端的 FIFO，openat 本身
// 就会一直挂住——ErrNotRegularFile 对 FIFO 根本不可达，handler goroutine 与
// fd 永久泄漏，而 executor 可以随手 mkfifo。
func TestReadFileFifoDoesNotHang(t *testing.T) {
	repo := t.TempDir()
	fifo := filepath.Join(repo, "pipe")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("当前平台无法创建 FIFO: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(repo, "pipe")
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO 应被拒绝，实际读取成功")
		}
		if !strings.Contains(err.Error(), "普通文件") {
			t.Errorf("FIFO 应以「不是普通文件」拒绝, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("读取仓库内 FIFO 永久阻塞：handler goroutine 与 fd 泄漏")
	}
}
