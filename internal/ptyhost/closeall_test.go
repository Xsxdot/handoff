//go:build unix

// closeall_test.go —— 显式停止路径的批量收口。
//
// 职责：验证 CloseAll 能在**没有 agentd 参与**的情况下扫出活会话并杀掉它们。
// 边界：不测 cmd 层怎么调它，也不测信号关停（那条路根本不该走到这里）。
package ptyhost_test

import (
	"os"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/ptyhost"
	"github.com/Xsxdot/handoff/internal/ptyhost/sessdir"
)

func TestCloseAllKillsLiveSessions(t *testing.T) {
	root, _, id, done := startClientHost(t)

	closed, err := ptyhost.CloseAll(root, testLog(), 3*time.Second)
	if err != nil {
		t.Fatalf("CloseAll: %v", err)
	}
	if closed != 1 {
		t.Fatalf("closed = %d，期望 1", closed)
	}

	// ptyhost 自己收摊：进程退出且目录清掉，才算真的停了
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ptyhost 没有退出")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(sessdir.Dir(root, id)); os.IsNotExist(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := os.Stat(sessdir.Dir(root, id)); !os.IsNotExist(err) {
		t.Fatalf("CloseAll 之后会话目录应清掉: %v", err)
	}
}

// 根目录不存在是**正常**的：这台机器可能从没开过终端。
// 报错会让 handoff service stop 在一台干净机器上白白吓人一跳。
func TestCloseAllMissingRootIsNotAnError(t *testing.T) {
	closed, err := ptyhost.CloseAll(t.TempDir()+"/never-created", testLog(), time.Second)
	if err != nil {
		t.Fatalf("根目录不存在不该报错: %v", err)
	}
	if closed != 0 {
		t.Fatalf("closed = %d，期望 0", closed)
	}
}
