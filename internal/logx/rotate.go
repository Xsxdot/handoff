// rotate.go —— agentd.log 的按大小轮转 handler。
//
// 职责：单条记录写入后检查文件大小，超过 maxBytes 就把当前文件改名为 path.1、
// 旧备份顺延，最多保留 backups 个备份（加当前文件共 backups+1 份）。
// 边界：只服务于 logx.Setup 的文件分支；不管理除 path 及其 .N 备份外的文件；
// 轮转处理内不递归打日志（避免日志放大与死循环）。
package logx

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
)

const (
	// logRotationMaxBytes 触发轮转的单文件上限（契约 F21：100MB）。
	logRotationMaxBytes = 100 * 1024 * 1024
	// logRotationMaxBackups 轮转备份数；加当前文件共 5 份（F21「最多保留 5 份」，
	// 第 6 份挤掉最旧一份）。
	logRotationMaxBackups = 4
)

// rotationState 是共享到全部派生 handler 的写端：文件句柄、当前大小与轮转规则。
// 实现 io.Writer 供内部 slog.JSONHandler 直接写；写后由 maybeRotate 按大小轮转。
type rotationState struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
}

func (s *rotationState) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.file.Write(p)
	s.size += int64(n)
	return n, err
}

// maybeRotate 在单条记录写完后调用（契约 §3.4③ 允许「写入后检查」粒度）。
func (s *rotationState) maybeRotate() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.size <= s.maxBytes {
		return nil
	}
	if s.file != nil {
		_ = s.file.Close()
	}
	_ = os.Remove(fmt.Sprintf("%s.%d", s.path, s.backups))
	for i := s.backups - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", s.path, i), fmt.Sprintf("%s.%d", s.path, i+1))
	}
	if err := os.Rename(s.path, s.path+".1"); err != nil && !os.IsNotExist(err) {
		return err
	}
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	s.file = f
	s.size = 0
	return nil
}

// rotatingHandler 把 JSON 记录写进 path，并在每条写入后按大小轮转。
// 派生 handler（WithAttrs/WithGroup）与本体共享同一个 rotationState，
// 因此 component 等属性照常落盘，且写端始终唯一（F20）。
type rotatingHandler struct {
	state *rotationState
	inner slog.Handler
}

func newRotatingHandler(path string, maxBytes int64, backups int, opts *slog.HandlerOptions) (*rotatingHandler, error) {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	var size int64
	if info, statErr := f.Stat(); statErr == nil {
		size = info.Size()
	}
	st := &rotationState{path: path, maxBytes: maxBytes, backups: backups, file: f, size: size}
	return &rotatingHandler{state: st, inner: slog.NewJSONHandler(st, opts)}, nil
}

// Enabled 与内部 JSON handler 同口径（级别由 opts.Level 决定）。
func (h *rotatingHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

// Handle 交给内部 JSON handler 写完后按大小检查轮转。
func (h *rotatingHandler) Handle(ctx context.Context, r slog.Record) error {
	if err := h.inner.Handle(ctx, r); err != nil {
		return err
	}
	return h.state.maybeRotate()
}

// WithAttrs / WithGroup 保留属性并复用同一写端与轮转状态（component 等必须落盘）。
func (h *rotatingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &rotatingHandler{state: h.state, inner: h.inner.WithAttrs(attrs)}
}

func (h *rotatingHandler) WithGroup(name string) slog.Handler {
	return &rotatingHandler{state: h.state, inner: h.inner.WithGroup(name)}
}
