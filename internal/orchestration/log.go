// log.go —— 编排包的包级日志入口。
//
// 职责：与 gateway 的 workspace.go#log 同语义（slog.Default），供迁入的纯资源
// 解析/回收代码沿用既有的 log().Warn/Info 形态。
// 边界：只做默认 logger 取值，不配置 handler（配置归组装点）。
package orchestration

import "log/slog"

func log() *slog.Logger { return slog.Default() }
