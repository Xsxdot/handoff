package agy

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/prochost"
)

func TestReapAndProcHandle(t *testing.T) {
	tmpDir := t.TempDir()
	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	lockPath := filepath.Join(tmpDir, lockFileName)
	writeProcInfo(tmpDir, &procInfo{
		Handle:    prochost.Handle{PID: 12345, LockPath: lockPath},
		SessionID: "s1",
	})

	h, err := ad.ProcHandle("T1", tmpDir)
	if err != nil || h.PID != 12345 {
		t.Fatalf("ProcHandle 失败: %v, pid=%d", err, h.PID)
	}

	if err := ad.Reap("T1", tmpDir); err != nil {
		t.Fatalf("Reap 失败: %v", err)
	}
}
