package opencode

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Xsxdot/handoff/internal/prochost"
)

// TestProcInfoWatermarkRoundTrip 验水位字段能写进去也能读回来。
func TestProcInfoWatermarkRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := &procInfo{
		Handle:         prochost.Handle{PID: 4242, LockPath: filepath.Join(dir, "proc.lock")},
		Port:           7788,
		Password:       "deadbeef",
		LastTurnMsgID:  "msg_abc123",
		WatermarkArmed: true,
	}
	if err := writeProcInfo(dir, in); err != nil {
		t.Fatalf("写恢复凭据失败: %v", err)
	}
	out, err := readProcInfo(dir)
	if err != nil {
		t.Fatalf("读恢复凭据失败: %v", err)
	}
	if out.LastTurnMsgID != "msg_abc123" {
		t.Fatalf("水位未往返：want msg_abc123, got %q", out.LastTurnMsgID)
	}
	if !out.WatermarkArmed {
		t.Fatal("armed 标记未往返：write true 却读回 false")
	}
}

// TestProcInfoOldFormatReadsAsEmptyWatermark 验旧格式（无水位字段）的 proc.json
// 仍然可读，水位读出空串而不是报错。
//
// why：本字段是给存量任务加的。若旧文件被判「字段不完整」，agentd 升级后所有
// 在跑的任务会一起变成「恢复凭据缺失」→ 判死 → 转 waiting_review，是升级即事故。
func TestProcInfoOldFormatReadsAsEmptyWatermark(t *testing.T) {
	dir := t.TempDir()
	old := map[string]any{
		"handle":   map[string]any{"pid": 4242, "lock_path": filepath.Join(dir, "proc.lock")},
		"port":     7788,
		"password": "deadbeef",
	}
	b, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(dir, procInfoFileName), b, 0o600); err != nil {
		t.Fatalf("写旧格式凭据失败: %v", err)
	}
	out, err := readProcInfo(dir)
	if err != nil {
		t.Fatalf("旧格式凭据应可读，却报错: %v", err)
	}
	if out.LastTurnMsgID != "" {
		t.Fatalf("旧格式的水位应为空串，got %q", out.LastTurnMsgID)
	}
	if out.WatermarkArmed {
		t.Fatal("旧格式的 armed 标记应为 false（legacy 任务，升级保护依赖它）")
	}
	if out.Port != 7788 {
		t.Fatalf("旧格式其余字段应正常读出，port got %d", out.Port)
	}
}
