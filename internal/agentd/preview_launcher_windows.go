//go:build windows

// 本文件实现 Windows 侧 Chromium launcher：可执行文件探测、独立参数启动、
// 已托管进程聚焦和进程停止。它不调用系统默认浏览器，也不复用用户 profile。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"

	"golang.org/x/sys/windows"
)

var (
	previewEnumWindows = func(callback func(windows.HWND, uintptr) uintptr) error {
		return windows.EnumWindows(windows.NewCallback(callback), nil)
	}
	previewWindowProcessID = windows.GetWindowThreadProcessId
	previewWindowVisible   = windows.IsWindowVisible
	previewRestoreWindow   = func(hwnd windows.HWND) {
		previewUser32.NewProc("ShowWindow").Call(uintptr(hwnd), uintptr(windows.SW_RESTORE))
	}
	previewForegroundWindow = func(hwnd windows.HWND) error {
		result, _, callErr := previewUser32.NewProc("SetForegroundWindow").Call(uintptr(hwnd))
		if result != 0 {
			return nil
		}
		if callErr == nil {
			callErr = errors.New("SetForegroundWindow 返回失败")
		}
		return callErr
	}
)

var previewUser32 = windows.NewLazySystemDLL("user32.dll")

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
	for _, candidate := range []string{"chrome.exe", "chromium.exe", "msedge.exe", "chrome"} {
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
	return "", fmt.Errorf("Windows Chromium 未找到: %w", last)
}

func (l *previewOSLauncher) Start(ctx context.Context, executable string, spec PreviewLaunchSpec) (PreviewBrowserHandle, error) {
	if err := ctx.Err(); err != nil {
		return PreviewBrowserHandle{}, err
	}
	cmd := exec.Command(executable, previewLaunchArgs(spec)...)
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

func (l *previewOSLauncher) Focus(ctx context.Context, pid int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.mu.Lock()
	cmd := l.commands[pid]
	l.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return fmt.Errorf("preview Chromium pid=%d 不在托管集合", pid)
	}
	if err := focusPreviewWindow(ctx, pid); err != nil {
		return err
	}
	l.log.Info("preview Chromium 聚焦请求成功", "operation", "preview_focus", "pid", pid)
	return nil
}

func focusPreviewWindow(ctx context.Context, pid int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var found windows.HWND
	err := previewEnumWindows(func(hwnd windows.HWND, _ uintptr) uintptr {
		var windowPID uint32
		if _, err := previewWindowProcessID(hwnd, &windowPID); err != nil {
			return 1
		}
		if int(windowPID) != pid || !previewWindowVisible(hwnd) {
			return 1
		}
		found = hwnd
		return 0
	})
	if err != nil {
		return fmt.Errorf("枚举 preview Chromium 窗口 pid=%d: %w", pid, err)
	}
	if found == 0 {
		return fmt.Errorf("preview Chromium pid=%d 没有可见窗口", pid)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	previewRestoreWindow(found)
	if err := previewForegroundWindow(found); err != nil {
		return fmt.Errorf("设置 preview Chromium 前台窗口 pid=%d: %w", pid, err)
	}
	return nil
}

func (l *previewOSLauncher) Stop(ctx context.Context) error {
	l.mu.Lock()
	commands := make([]*exec.Cmd, 0, len(l.commands))
	for _, cmd := range l.commands {
		commands = append(commands, cmd)
	}
	l.mu.Unlock()
	for _, cmd := range commands {
		if cmd.Process == nil {
			continue
		}
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			l.log.Warn("停止 preview Chromium 失败", "operation", "preview_stop", "pid", cmd.Process.Pid, "cause", err)
		}
	}
	l.log.Info("preview Chromium 停止请求已发送", "operation", "preview_stop", "count", len(commands))
	return nil
}
