// Package logx 提供统一的 slog 日志初始化入口。
//
// 职责：
//   - 根据 HANDOFF_LOG_LEVEL 环境变量解析日志级别（默认 info）
//   - 构建同时写 JSON 文件与 stderr 文本的双路 logger
//
// 边界：
//   - 不管理日志轮转（交给 logrotate/newsyslog）
//   - 不引入第三方日志库，仅使用标准库 log/slog
package logx

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Setup 创建并返回带 component 标签的 logger。
//
// 参数：
//   - component: 日志中固定的组件标识（如 "agentd"）
//   - logPath: JSON 日志文件路径；为空时只写 stderr 文本日志
//
// 返回：
//   - 同时写 JSON 文件与 stderr 文本的 logger
//
// 注意：
//   - 日志级别由环境变量 HANDOFF_LOG_LEVEL 控制（debug/info/warn/error，默认 info）
//   - 日志文件打开失败时降级为仅 stderr 并输出 Warn，不影响程序运行
func Setup(component, logPath string) *slog.Logger {
	lvl := parseLevel(os.Getenv("HANDOFF_LOG_LEVEL"))
	hs := []slog.Handler{slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})}
	if logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			hs = append(hs, slog.NewJSONHandler(f, &slog.HandlerOptions{Level: lvl}))
		} else {
			// 文件不可写时降级：保留 stderr 文本日志并显式告警，避免静默丢日志
			slog.Warn("日志文件打开失败，降级为仅 stderr", "path", logPath, "err", err)
		}
	}
	return slog.New(&multiHandler{hs: hs}).With("component", component)
}

// parseLevel 将环境变量字符串解析为 slog.Level，非法或空值回退到 info。
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// multiHandler 将一条日志记录广播给多个 handler，保证同一格式记录在每路输出完整一致。
type multiHandler struct {
	hs []slog.Handler
}

// Enabled 只要任一子 handler 启用该级别即视为启用。
func (m *multiHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	for _, h := range m.hs {
		if h.Enabled(ctx, lvl) {
			return true
		}
	}
	return false
}

// Handle 将记录依次交给所有子 handler，任一失败即返回错误。
func (m *multiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, h := range m.hs {
		if err := h.Handle(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// WithAttrs 将属性广播给所有子 handler 后返回新的 multiHandler。
func (m *multiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	hs := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		hs[i] = h.WithAttrs(attrs)
	}
	return &multiHandler{hs: hs}
}

// WithGroup 将分组广播给所有子 handler 后返回新的 multiHandler。
func (m *multiHandler) WithGroup(name string) slog.Handler {
	hs := make([]slog.Handler, len(m.hs))
	for i, h := range m.hs {
		hs[i] = h.WithGroup(name)
	}
	return &multiHandler{hs: hs}
}
