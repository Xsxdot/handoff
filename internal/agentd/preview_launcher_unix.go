//go:build !windows

// 本文件实现 Unix 侧 Chromium launcher：可执行文件探测、独立进程组启动、
// 进程组停止。聚焦按 OS 拆到 preview_launcher_focus_*.go。它不调用系统默认浏览器，也不复用用户 profile。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
)

type previewOSLauncher struct {
	log      *slog.Logger
	mu       sync.Mutex
	commands map[int]*exec.Cmd
}

func newPreviewOSLauncher(log *slog.Logger) PreviewLauncher {
	return &previewOSLauncher{log: log, commands: make(map[int]*exec.Cmd)}
}

func (l *previewOSLauncher) FindExecutable(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var last error
	for _, candidate := range previewBrowserCandidates() {
		path, err := exec.LookPath(candidate)
		if err == nil {
			l.log.Info("preview 浏览器可执行文件探测成功", "operation", "preview_find", "executable", path)
			return path, nil
		}
		last = err
	}
	if last == nil {
		last = errors.New("没有候选浏览器")
	}
	l.log.Warn("preview 浏览器可执行文件探测失败", "operation", "preview_find", "cause", last)
	return "", fmt.Errorf("Unix Chromium 未找到: %w", last)
}

func previewBrowserCandidates() []string {
	candidates := []string{
		"google-chrome",
		"google-chrome-stable",
		"chrome",
		"microsoft-edge",
		"microsoft-edge-stable",
		"msedge",
		"arc",
		"brave-browser",
		"brave-browser-stable",
		"brave",
		"chromium",
		"chromium-browser",
	}
	if runtime.GOOS == "darwin" {
		for _, app := range []string{
			"Google Chrome.app/Contents/MacOS/Google Chrome",
			"Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"Arc.app/Contents/MacOS/Arc",
			"Brave Browser.app/Contents/MacOS/Brave Browser",
			"Chromium.app/Contents/MacOS/Chromium",
		} {
			candidates = append(candidates, filepath.Join("/Applications", app))
			if home, err := os.UserHomeDir(); err == nil {
				candidates = append(candidates, filepath.Join(home, "Applications", app))
			}
		}
	}
	return candidates
}

func (l *previewOSLauncher) Start(ctx context.Context, executable string, spec PreviewLaunchSpec) (PreviewBrowserHandle, error) {
	if err := ctx.Err(); err != nil {
		return PreviewBrowserHandle{}, err
	}
	cmd := exec.Command(executable, previewLaunchArgs(spec)...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return PreviewBrowserHandle{}, fmt.Errorf("启动 Chromium: %w", err)
	}
	pid := cmd.Process.Pid
	done := make(chan error, 1)
	l.mu.Lock()
	l.commands[pid] = cmd
	l.mu.Unlock()
	go func() {
		err := cmd.Wait()
		l.mu.Lock()
		delete(l.commands, pid)
		l.mu.Unlock()
		done <- err
		close(done)
	}()
	l.log.Info("preview Chromium 已启动", "operation", "preview_start", "pid", pid, "entry_url", spec.EntryURL)
	return PreviewBrowserHandle{PID: pid, Done: done}, nil
}

func (l *previewOSLauncher) Stop(ctx context.Context) error {
	l.mu.Lock()
	pids := make([]int, 0, len(l.commands))
	for pid := range l.commands {
		pids = append(pids, pid)
	}
	l.mu.Unlock()
	for _, pid := range pids {
		if err := l.StopPID(ctx, pid); err != nil {
			l.log.Warn("停止 preview Chromium 进程组失败", "operation", "preview_stop", "pid", pid, "cause", err)
		}
	}
	l.log.Info("preview Chromium 进程组停止请求已发送", "operation", "preview_stop", "count", len(pids))
	return nil
}

// StopPID terminates one managed Chromium process group. Unix browsers are
// started with Setpgid so descendants are collected without touching siblings.
func (l *previewOSLauncher) StopPID(ctx context.Context, pid int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if pid <= 0 {
		return fmt.Errorf("preview Chromium pid=%d 非法", pid)
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("停止 preview Chromium 进程组 pid=%d: %w", pid, err)
	}
	l.log.Info("preview Chromium 进程组停止请求已发送", "operation", "preview_stop_pid", "pid", pid)
	return nil
}
