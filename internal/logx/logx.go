// Package logx 提供统一的 slog 日志初始化入口。
//
// 职责：
//   - 根据 HANDOFF_LOG_LEVEL 解析日志级别（默认 warn）
//   - 带 logPath 时只写文件 JSON 并按其大小轮转；不带时只写 stderr 文本
//
// 边界：
//   - 不引入第三方日志库，仅使用标准库 log/slog
package logx

import (
	"log/slog"
	"os"
	"strings"
)

// Setup 创建并返回带 component 标签的 logger。
//
// 参数：
//   - component: 日志中固定的组件标识（如 "agentd"）
//   - logPath: 日志文件路径；为空时只写 stderr 文本
//
// 返回：
//   - 带 logPath：只写该文件的 JSON logger（带 100MB×5 轮转）；
//     不带：只写 stderr 文本
//
// 注意：
//   - 级别由 HANDOFF_LOG_LEVEL 控制（debug/info/warn/error，默认 warn，F19）
//   - 带 logPath 时绝不再挂 stderr handler：同一记录只落盘一次（F20，拍板 P2）
//   - 文件打开失败降级为仅 stderr 并输出 Warn，不影响程序运行
func Setup(component, logPath string) *slog.Logger {
	lvl := parseLevel(os.Getenv("HANDOFF_LOG_LEVEL"))
	opts := &slog.HandlerOptions{Level: lvl}
	var h slog.Handler
	if logPath == "" {
		h = slog.NewTextHandler(os.Stderr, opts)
	} else if rh, err := newRotatingHandler(logPath, logRotationMaxBytes, logRotationMaxBackups, opts); err == nil {
		h = rh
	} else {
		// 文件不可写时降级：保留 stderr 文本日志并显式告警，避免静默丢日志。
		slog.Warn("日志文件打开失败，降级为仅 stderr", "path", logPath, "err", err)
		h = slog.NewTextHandler(os.Stderr, opts)
	}
	return slog.New(h).With("component", component)
}

// parseLevel 将环境变量字符串解析为 slog.Level，非法或空值回退到 warn（F19）。
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}
