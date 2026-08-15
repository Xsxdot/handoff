// export_test.go —— 把包内未导出的解析函数暴露给 claudecode_test 外部测试包。
//
// 边界：只做转发，不含任何逻辑。它随测试一起编译，不进产物。
package claudecode

import (
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ParseAssistantUsageForTest 暴露 assistant 消息的模型/用量解析。
func ParseAssistantUsageForTest(msg json.RawMessage) (string, *proto.Usage, bool) {
	return parseAssistantUsage(msg)
}
