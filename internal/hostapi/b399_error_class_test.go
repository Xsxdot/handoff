// b399_error_class_test.go —— B399 r2：两类可判错误必须有哨兵。
//
// 职责：钉住 hostapi 的超时与「会话不存在」分别包裹 ErrTurnTimeout/ErrSessionNotFound，
// 供 keystone 用 errors.Is 分流（超时保留会话，只有会话不存在才重建）。
// 缝：hostapi.Host.RunTurn（消费方：agentd.coordinatorRunner → keystone）。
// 边界：只经既有夹具 installFakeCLI/installFakeCLIFail；不碰真 CLI/账本。
package hostapi

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestB399TimeoutErrorCarriesSentinel 锁：超时回合的错误 errors.Is 命中 ErrTurnTimeout，
// 且不命中 ErrSessionNotFound（两分类不可混）。
//
// 红（当前 HEAD）：driver.go 超时错误无哨兵，errors.Is 恒 false。
// 绿（T1.2）：超时错误包裹 ErrTurnTimeout。
// 变异：把 errors.Join(ErrTurnTimeout, …) 改回只包 ctx.Err() → 复红。
func TestB399TimeoutErrorCarriesSentinel(t *testing.T) {
	installFakeCLI(t)
	withArgvCapture(t)
	parent, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	h := New()
	_, err := h.RunTurn(parent, TurnRequest{CLI: "opencode", Prompt: "超时", Env: []string{"FAKECLI_SLEEP=5"}})
	if err == nil {
		t.Fatalf("超时回合应失败")
	}
	if !errors.Is(err, ErrTurnTimeout) {
		t.Fatalf("超时错误未携带 ErrTurnTimeout: %v", err)
	}
	if errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("超时不得被判为会话不存在: %v", err)
	}
}

// TestB399SessionNotFoundErrorCarriesSentinel 锁：stderr 含 "Session not found" 的错误
// errors.Is 命中 ErrSessionNotFound，且不命中 ErrTurnTimeout。
//
// 红（当前 HEAD）：waitErr 分支无哨兵。
// 绿（T1.3）：stderr 含判据时包裹 ErrSessionNotFound。
// 变异：删掉 strings.Contains(tail, "Session not found") 分支 → 复红。
func TestB399SessionNotFoundErrorCarriesSentinel(t *testing.T) {
	installFakeCLIFail(t, "Session not found")
	h := New()
	_, err := h.RunTurn(context.Background(), TurnRequest{
		CLI: "opencode", SessionID: "ses_gone", Prompt: "续接",
	})
	if err == nil {
		t.Fatalf("会话不存在应失败")
	}
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("错误未携带 ErrSessionNotFound: %v", err)
	}
	if errors.Is(err, ErrTurnTimeout) {
		t.Fatalf("会话不存在不得被判为超时: %v", err)
	}
	if !strings.Contains(err.Error(), "Session not found") {
		t.Fatalf("错误文本应保留 stderr 判据: %v", err)
	}
}
