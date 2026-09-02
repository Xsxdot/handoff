//go:build darwin

// Darwin 侧 preview Chromium 聚焦：System Events unix id，不用 xdotool。
// 边界：pid 必须在本 launcher 托管集合；第一次 OpenPreview 走 Start 不走这里。
package agentd

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
)

func (l *previewOSLauncher) Focus(ctx context.Context, pid int) error {
	l.mu.Lock()
	cmd := l.commands[pid]
	l.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("preview Chromium pid=%d 不在托管集合", pid)
	}
	tool, err := exec.LookPath("osascript")
	if err != nil {
		return fmt.Errorf("聚焦 preview Chromium pid=%d 需要 osascript: %w", pid, err)
	}
	script := `tell application "System Events" to set frontmost of the first process whose unix id is ` + strconv.Itoa(pid) + ` to true`
	if err := exec.CommandContext(ctx, tool, "-e", script).Run(); err != nil {
		return fmt.Errorf("聚焦 preview Chromium pid=%d: %w", pid, err)
	}
	l.log.Info("preview Chromium 聚焦成功", "operation", "preview_focus", "pid", pid)
	return nil
}
