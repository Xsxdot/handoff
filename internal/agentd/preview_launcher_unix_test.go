//go:build !windows

// 本文件验证 Unix launcher 的浏览器候选发现边界；不启动真实浏览器，也不调用系统默认浏览器。
package agentd

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestPreviewUnixFindExecutableIncludesBrowserFamilies(t *testing.T) {
	for _, want := range []string{"google-chrome", "microsoft-edge", "arc", "brave-browser", "chromium"} {
		if !containsPreviewCandidate(previewBrowserCandidates(), want) {
			t.Fatalf("browser candidate %q is missing", want)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "microsoft-edge")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write fake browser: %v", err)
	}
	t.Setenv("PATH", dir)

	launcher := newPreviewOSLauncher(slog.New(slog.NewTextHandler(io.Discard, nil)))
	got, err := launcher.FindExecutable(context.Background())
	if err != nil {
		t.Fatalf("FindExecutable: %v", err)
	}
	if got != path {
		t.Fatalf("executable=%q, want %q", got, path)
	}
}

func containsPreviewCandidate(candidates []string, want string) bool {
	for _, candidate := range candidates {
		if candidate == want {
			return true
		}
	}
	return false
}
