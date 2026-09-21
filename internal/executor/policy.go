package executor

import "errors"

// ErrUnrepresentablePolicy 表示快照含无法在原生配置精确实施的条件。
var ErrUnrepresentablePolicy = errors.New("快照含无法在原生配置实施的明确禁止")
