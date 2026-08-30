//go:build unix && !darwin

// Linux/BSD 侧 preview Chromium 聚焦：xdotool search --pid + windowactivate。
// 边界：Darwin 不编译本文件。
package agentd

import (
	"context"
	"fmt"
	"os/exec"
)

func (l *previewOSLauncher) Focus(ctx context.Context, pid int) error {
	l.mu.Lock()
	cmd := l.commands[pid]
	l.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("preview Chromium pid=%d 不在托管集合", pid)
	}
	tool, err := exec.LookPath("xdotool")
	if err != nil {
		return fmt.Errorf("聚焦 preview Chromium pid=%d 需要 xdotool: %w", pid, err)
	}
	if err := exec.CommandContext(ctx, tool, "search", "--pid", fmt.Sprint(pid), "windowactivate", "--sync").Run(); err != nil {
		return fmt.Errorf("聚焦 preview Chromium pid=%d: %w", pid, err)
	}
	l.log.Info("preview Chromium 聚焦成功", "operation", "preview_focus", "pid", pid)
	return nil
}
