package agentd

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
)

// TestResumeTurnRequestCarriesIsolatedHome 锁 B299：续接 TurnRequest 必须带
// 隔离 HOME/Workdir/Model，不能只剩 CLI+SessionID。
func TestResumeTurnRequestCarriesIsolatedHome(t *testing.T) {
	ref := keysclient.SessionRef{
		CLI: "opencode", SessionID: "ses_x",
		HomeDir: "/home/coord", Workdir: "/repo", Model: "fast",
	}
	req := resumeTurnRequest(ref, "ping")
	if req.CLI != ref.CLI || req.SessionID != ref.SessionID || req.Prompt != "ping" {
		t.Fatalf("身份/prompt 映射错误: %+v", req)
	}
	if req.HomeDir != ref.HomeDir || req.Workdir != ref.Workdir || req.Model != ref.Model {
		t.Fatalf("续接丢了隔离环境: %+v", req)
	}
	if len(req.Env) != 2 || req.Env[0] != "HANDOFF_SESSION_CLI="+ref.CLI ||
		req.Env[1] != "HANDOFF_SESSION_ID="+ref.SessionID {
		t.Fatalf("续接缺少当前会话出示环境: %+v", req.Env)
	}
}
