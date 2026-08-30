//go:build darwin

package agentd

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPreviewDarwinFocusDoesNotUseXdotool(t *testing.T) {
	launcher := newPreviewOSLauncher(slog.New(slog.NewTextHandler(io.Discard, nil))).(*previewOSLauncher)
	err := launcher.Focus(context.Background(), 1)
	if err == nil {
		t.Fatal("missing pid must fail")
	}
	if strings.Contains(err.Error(), "xdotool") {
		t.Fatalf("darwin focus must not mention xdotool: %v", err)
	}

	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start sleep: %v", err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	pid := cmd.Process.Pid
	launcher.mu.Lock()
	launcher.commands[pid] = cmd
	launcher.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := launcher.Focus(ctx, pid); err != nil && strings.Contains(err.Error(), "xdotool") {
		t.Fatalf("darwin focus used xdotool: %v", err)
	}
}
