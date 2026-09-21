// b393_visible_test.go —— B393 唤醒回合可见性的红色回路。
//
// 职责：钉住 hostapi.Host.RunTurn 在 warn 级别下必须留下回合起止两行日志
// （spec §4.3：不设 HANDOFF_LOG_LEVEL 也要看得见）。
// 缝：hostapi.Host.RunTurn（target.json 已声明的 d_gateway→d_execution 门面）。
// 边界：只经既有夹具 installFakeCLI/withArgvCapture；不碰真 CLI/网络。
package hostapi

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

// TestB393WakeRoundVisibleAtWarn 锁 spec §4.3：warn 级别下唤醒回合起止必须可见。
//
// 红（本节点已跑）：captured warn-level log bytes=0，driver.go:127/170 是 Info。
// 绿：T2.1 后起止两行入 buf。变异：把级别改回 Info → 复红。
func TestB393WakeRoundVisibleAtWarn(t *testing.T) {
	installFakeCLI(t)
	withArgvCapture(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	h := New()
	if _, err := h.RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", SessionID: "ses_visible", Prompt: "唤醒简报",
	}); err != nil {
		t.Fatalf("回合失败: %v", err)
	}
	if !strings.Contains(buf.String(), "协调者回合开始") {
		t.Fatalf("warn 级别下「回合开始」不可见（driver.go:127 是 Info）")
	}
	if !strings.Contains(buf.String(), "协调者回合完成") {
		t.Fatalf("warn 级别下「回合完成」不可见（driver.go:170 是 Info）")
	}
}
