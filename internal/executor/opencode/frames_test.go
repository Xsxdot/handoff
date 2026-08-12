package opencode

import (
	"strings"
	"testing"
)

// reasoning part 的增量必须被认成思维链，且 part 标识沿用 opencode 自己的
// messageID:partID——它跨事件稳定，比本地自增的 p01 更能对齐上游。
func TestReasoningPartRoutedAsReasoning(t *testing.T) {
	key := partKey("msg_1", "prt_9")
	if !strings.Contains(key, "msg_1") || !strings.Contains(key, "prt_9") {
		t.Fatalf("partKey 应同时含 messageID 与 partID，实得 %q", key)
	}
	if got := frameKind("reasoning"); got != kindReasoning {
		t.Errorf("reasoning part 应归为思维链，实得 %v", got)
	}
	if got := frameKind("tool"); got != kindSkip {
		t.Errorf("tool part 的文本增量不产帧，实得 %v", got)
	}
	if got := frameKind("step-start"); got != kindSkip {
		t.Errorf("step-start 不产帧，实得 %v", got)
	}
	if got := frameKind("text"); got != kindText {
		t.Errorf("text part 应归为正文，实得 %v", got)
	}
}
