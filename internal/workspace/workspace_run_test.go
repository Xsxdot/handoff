// workspace_run_test.go —— RunCmd 与 /api/tasks/<id>/run 路由的工作目录判据测试。
//
// 职责：钉死「工作树已被回收」时的错误类型、状态码与「不启动任何子进程」。
// 边界：不覆盖 run 的超时、进程组回收与输出截断，那些由既有用例负责。
package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunCmdMissingWorkdirIsTypedAndStartsNothing 覆盖工作树已被回收的场景。
//
// why 这个用例存在：以前 RunCmd 直接 cmd.Start()，chdir 失败被内核报成
// 「fork/exec /bin/sh: no such file or directory」，指向一个完全无辜的 sh，
// 排查时被引到错误方向。
func TestRunCmdMissingWorkdirIsTypedAndStartsNothing(t *testing.T) {
	base := t.TempDir()
	gone := filepath.Join(base, "已被回收的工作树")
	sentinel := filepath.Join(base, "副作用.txt")

	// 命令本身有可观测副作用：若进程真被启动，这个文件就会出现
	_, exitCode, err := RunCmd(context.Background(), gone, "echo x > "+sentinel)

	if !errors.Is(err, ErrWorkdirGone) {
		t.Fatalf("期望 ErrWorkdirGone，实为 %v", err)
	}
	if exitCode != -1 {
		t.Fatalf("启动前失败时 exitCode 应为 -1，实为 %d", exitCode)
	}
	if !strings.Contains(err.Error(), gone) {
		t.Fatalf("错误文案应点名路径 %q，实为 %q", gone, err.Error())
	}
	if _, statErr := os.Stat(sentinel); statErr == nil {
		t.Fatal("工作目录不存在时不得启动任何子进程，但副作用文件出现了")
	}
}

// TestRunCmdExistingWorkdirUnchanged 是回归：目录在时行为不变。
func TestRunCmdExistingWorkdirUnchanged(t *testing.T) {
	repo := t.TempDir()
	stdout, exitCode, err := RunCmd(context.Background(), repo, "echo hello")
	if err != nil {
		t.Fatalf("目录存在时不应报错: %v", err)
	}
	if exitCode != 0 {
		t.Fatalf("exitCode 应为 0，实为 %d", exitCode)
	}
	if !strings.Contains(stdout, "hello") {
		t.Fatalf("stdout 应含 hello，实为 %q", stdout)
	}
}
