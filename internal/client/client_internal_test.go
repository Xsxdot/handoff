// client 包内部单元测试：直接覆盖 isPermanent/isPermanentStatus 的错误分类逻辑。
//
// 为什么单独立文件而非并入 client_test.go：分类函数是 P0-2 修复的核心判定，
// 外部测试包（client_test）无法访问未导出标识符，用 package client 白盒测试
// 直接验证「哪些错误永久、哪些瞬时」，与行为测试（TestWaitEvent*FailsFast）
// 互为补充——行为测试证明「不重试」，本文件证明「分类本身正确」。
package client

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"testing"

	"github.com/coder/websocket"
)

// TestIsPermanent 覆盖分类函数对各类错误的判定：配置类（401/403/400）、
// 任务不存在（PolicyViolation close）为永久；网络类与正常关闭为瞬时。
func TestIsPermanent(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"握手 401", &permanentError{op: "WS 拨号", code: http.StatusUnauthorized, cause: errors.New("x")}, true},
		{"握手 403", &permanentError{op: "WS 拨号", code: http.StatusForbidden, cause: errors.New("x")}, true},
		{"握手 400", &permanentError{op: "WS 拨号", code: http.StatusBadRequest, cause: errors.New("x")}, true},
		{"握手 500 瞬时", fmt.Errorf("WS 拨号失败 status=500: %w", errors.New("x")), false},
		{"网络错误瞬时", fmt.Errorf("WS 拨号失败: %w", io.ErrUnexpectedEOF), false},
		{"读取断流瞬时", fmt.Errorf("WS 读取: %w", io.ErrClosedPipe), false},
		{"PolicyViolation close", fmt.Errorf("WS 读取: %w", websocket.CloseError{Code: websocket.StatusPolicyViolation, Reason: "task not found"}), true},
		{"正常关闭瞬时", fmt.Errorf("WS 读取: %w", websocket.CloseError{Code: websocket.StatusNormalClosure}), false},
		{"GoingAway 瞬时", fmt.Errorf("WS 读取: %w", websocket.CloseError{Code: websocket.StatusGoingAway}), false},
		{"nil", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanent(tc.err); got != tc.want {
				t.Fatalf("isPermanent(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsPermanentStatus 覆盖握手状态码判定：400/401/403 永久，其余瞬时。
func TestIsPermanentStatus(t *testing.T) {
	cases := []struct {
		code int
		want bool
	}{
		{http.StatusBadRequest, true},
		{http.StatusUnauthorized, true},
		{http.StatusForbidden, true},
		{http.StatusNotFound, true},
		{http.StatusInternalServerError, false},
		{http.StatusServiceUnavailable, false},
		{http.StatusOK, false},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status %d", tc.code), func(t *testing.T) {
			if got := isPermanentStatus(tc.code); got != tc.want {
				t.Fatalf("isPermanentStatus(%d) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}

// TestPermanentErrorUnwrap 保证 permanentError 可被 errors.As 穿透到原始错误
// （外层错误链检查不因包装丢失信息）。
func TestPermanentErrorUnwrap(t *testing.T) {
	root := errors.New("handshake 401")
	pe := &permanentError{op: "WS 拨号", code: http.StatusUnauthorized, cause: root}
	if !errors.Is(pe, root) {
		t.Fatalf("permanentError 未透传 cause（errors.Is 失败）")
	}
}
