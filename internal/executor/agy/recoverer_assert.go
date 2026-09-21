package agy

import "github.com/Xsxdot/handoff/internal/executor"

// 编译期钉死：AGY adapter 的 Resume 满足执行契约 Recoverer。
var _ executor.Recoverer = (*Adapter)(nil)
