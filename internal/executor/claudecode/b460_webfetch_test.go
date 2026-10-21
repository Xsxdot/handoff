package claudecode

// b460_webfetch_test.go —— B460 S4 V5：claude 的 WebFetch / WebSearch 权限请求结构化为
// Tool=webfetch（WebFetch 带 URL）。入参字段名按 claude 工具公开形状（url / query）合成，
// 真机样本核对（U3）待协调者 R4b。
import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

func TestB460ClaudeWebFetchStructured(t *testing.T) {
	text, req := permTextAndRequest("WebFetch", json.RawMessage(`{"url":"https://go.dev/doc/","prompt":"标题"}`))
	if req == nil || req.Tool != executor.PermToolWebFetch || req.URL != "https://go.dev/doc/" {
		t.Fatalf("WebFetch 应结构化带 URL，实得 %+v", req)
	}
	if text != "WebFetch: https://go.dev/doc/" {
		t.Fatalf("text = %q", text)
	}
	text, req = permTextAndRequest("WebSearch", json.RawMessage(`{"query":"go generics"}`))
	if req == nil || req.Tool != executor.PermToolWebFetch || req.URL != "" {
		t.Fatalf("WebSearch 应结构化为 webfetch（URL 空），实得 %+v", req)
	}
	if text != "WebSearch: go generics" {
		t.Fatalf("text = %q", text)
	}
	for _, tool := range []string{"WebFetch", "WebSearch"} {
		if _, req := permTextAndRequest(tool, json.RawMessage(`{"nope":1}`)); req != nil {
			t.Fatalf("%s 入参缺字段必须 nil，实得 %+v", tool, req)
		}
	}
}
