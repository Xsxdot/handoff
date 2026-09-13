// logging.go —— 编制域结构化 logger 入口（B233.17 从 scheduling 公开包迁入嵌套 internal）。
//
// 职责：返回带 mod=scheduling 标签的默认 logger。
//
// 边界：
//   - 只依赖 stdlib log/slog
//   - **无跨域消费者**：只被 scheduling 公开包的 statusLog() 转发，故可进嵌套
package logging

import "log/slog"

// StatusLog 返回编制域结构化 logger（mod=scheduling）。包级日志入口的实现细节，
// 无跨域消费者；公开 scheduling 包经 statusLog() 转发，保持调用点不变。
func StatusLog() *slog.Logger { return slog.Default().With("mod", "scheduling") }
