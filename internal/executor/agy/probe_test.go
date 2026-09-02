package agy

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/prochost"
)

func TestProbe(t *testing.T) {
	tmpDir := t.TempDir()
	ad := New(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// 凭据缺失时返回 error (unknown)
	_, err := ad.Probe(executor.ProbeReq{TaskID: "T1", TaskDir: tmpDir})
	if err == nil {
		t.Fatalf("凭据缺失时应返回 error")
	}

	// 写入 proc.json 但进程不在
	lockPath := filepath.Join(tmpDir, lockFileName)
	writeProcInfo(tmpDir, &procInfo{
		Handle:    prochost.Handle{PID: 99999999, LockPath: lockPath},
		SessionID: "s1",
	})
	out, err := ad.Probe(executor.ProbeReq{TaskID: "T1", TaskDir: tmpDir})
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	if out.Alive {
		t.Fatalf("未持锁且进程不存在时不应判定存活")
	}
}
