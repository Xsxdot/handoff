// b393_timeout_bound_test.go —— B393 MINOR：超时日志/错误必须打印实际生效的
// 外层 ctx 上界，而不是 hostapi 自身的 req.Timeout/30m 缺省。
//
// 职责：钉住「外层 ctx 带了更早期限时，现场读数就是那个更早的期限」——否则
// 协调者回合（外层 coordWakeTurnTimeout）超时后，日志却写 30m，排障误导。
// 缝：hostapi.Host.RunTurn。边界：只经既有夹具；不碰真 CLI/网络。
package hostapi

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestB393TimeoutLogsEffectiveOuterBound 锁 MINOR：req.Timeout=45m 但外层 ctx
// 只有 300ms 时，超时日志与错误必须报 300ms 量级（外层生效值），不得报 45m。
//
// 红（当前 HEAD）：driveTurn 的 timeout 只来自 req.Timeout/缺省，日志与错误恒
// 写 45m0s，与外层 ctx 实际生效的 300ms 矛盾。
// 绿：实际生效上界取「req 上界」与「外层 ctx 剩余」更早者。
// 变异：删掉外层 deadline 收紧逻辑 → 复红。
func TestB393TimeoutLogsEffectiveOuterBound(t *testing.T) {
	installFakeCLI(t)
	withArgvCapture(t)
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	defer slog.SetDefault(prev)

	parent, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	h := New()
	_, err := h.RunTurn(parent, TurnRequest{
		CLI: "opencode", Prompt: "慢回合",
		Env:     []string{"FAKECLI_SLEEP=5"},
		Timeout: 45 * time.Minute,
	})
	if err == nil {
		t.Fatalf("超时回合应失败")
	}
	if !strings.Contains(err.Error(), "超时") {
		t.Fatalf("错误应标明超时: %v", err)
	}
	if strings.Contains(err.Error(), "45m0s") {
		t.Fatalf("错误报了自身 req 上界 45m0s，而非外层 ctx 生效上界: %v", err)
	}
	logs := buf.String()
	if !strings.Contains(logs, "协调者回合超时终止") {
		t.Fatalf("warn 级别下超时终止日志不可见: %s", logs)
	}
	if strings.Contains(logs, "45m0s") {
		t.Fatalf("超时日志报了自身 req 上界 45m0s，而非外层 ctx 生效上界: %s", logs)
	}
}
