package codex

// b460_cwd_test.go —— B460 Q8：codex 命令审批报文的 cwd 透传进结构化请求，权限门据此
// 解释相对落点（其余执行器载荷没有 cwd）。
import (
	"encoding/json"
	"testing"
)

func TestB460CommandApprovalCarriesCwd(t *testing.T) {
	a, ok := parseCommandApproval(json.RawMessage(`{"itemId":"i1","command":"printf x > r.md","cwd":"/data/tasks/t1/artifacts"}`))
	if !ok {
		t.Fatal("报文应可解析")
	}
	req := permRequestFromCommand(a)
	if req == nil || req.Cwd != "/data/tasks/t1/artifacts" {
		t.Fatalf("Cwd 应透传，实得 %+v", req)
	}
}
