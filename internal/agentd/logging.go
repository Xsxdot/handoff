// logging.go —— agentd 侧日志助手（B233.15：工作区实现迁出后本包仍需 log()）。
//
// 职责：返回 bootstrap 后统一配置的默认 logger。
// 边界：只做取值，不配置 handler；与迁出到 internal/workspace 的同名助手各自保留一份。
package agentd

import "log/slog"

// log 返回 slog.Default()（与 store 同款约定：bootstrap 后统一 logger）。
func log() *slog.Logger { return slog.Default() }
