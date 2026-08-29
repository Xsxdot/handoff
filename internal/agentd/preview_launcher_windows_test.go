//go:build windows

package agentd

import (
	"context"
	"testing"

	"golang.org/x/sys/windows"
)

func TestFocusPreviewWindowUsesManagedProcessWindow(t *testing.T) {
	oldEnum := previewEnumWindows
	oldPID := previewWindowProcessID
	oldVisible := previewWindowVisible
	oldRestore := previewRestoreWindow
	oldForeground := previewForegroundWindow
	t.Cleanup(func() {
		previewEnumWindows = oldEnum
		previewWindowProcessID = oldPID
		previewWindowVisible = oldVisible
		previewRestoreWindow = oldRestore
		previewForegroundWindow = oldForeground
	})

	var restored, foreground windows.HWND
	previewEnumWindows = func(callback func(windows.HWND, uintptr) uintptr) error {
		callback(7, 0)
		return nil
	}
	previewWindowProcessID = func(hwnd windows.HWND, pid *uint32) (uint32, error) {
		if hwnd != 7 {
			t.Fatalf("window=%d, want 7", hwnd)
		}
		*pid = 42
		return 1, nil
	}
	previewWindowVisible = func(hwnd windows.HWND) bool { return hwnd == 7 }
	previewRestoreWindow = func(hwnd windows.HWND) { restored = hwnd }
	previewForegroundWindow = func(hwnd windows.HWND) error {
		foreground = hwnd
		return nil
	}

	if err := focusPreviewWindow(context.Background(), 42); err != nil {
		t.Fatalf("focus preview window: %v", err)
	}
	if restored != 7 || foreground != 7 {
		t.Fatalf("restored=%d foreground=%d, want both 7", restored, foreground)
	}
}
