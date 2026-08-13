package proto_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
)

// Pull 与 PullState 都必须 omitempty：老 CLI 解到未知字段无所谓，但新 CLI
// 拿到 nil 要能分辨"对端没给"（老 agentd）与"对端说 false"。
func TestUpdateStatusOmitsPullWhenAbsent(t *testing.T) {
	b, err := json.Marshal(proto.UpdateStatus{Managed: true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "pull") {
		t.Errorf("未设置时不得出现 pull 字段，实得 %s", b)
	}
}

func TestUpdateStatusPullRoundTrip(t *testing.T) {
	yes := true
	in := proto.UpdateStatus{Managed: true, Pull: &yes}
	b, _ := json.Marshal(in)
	var out proto.UpdateStatus
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.Pull == nil || !*out.Pull {
		t.Fatalf("pull 未往返，实得 %v", out.Pull)
	}
}

// 老 agentd 的响应里没有 pull 字段，解出来必须是 nil 而不是 false。
// 这条区分是选路的判据：nil = 对端过旧，降级推送。
func TestUpdateStatusLegacyDecodesToNilPull(t *testing.T) {
	var out proto.UpdateStatus
	if err := json.Unmarshal([]byte(`{"managed":true}`), &out); err != nil {
		t.Fatal(err)
	}
	if out.Pull != nil {
		t.Fatalf("老 agentd 的响应应解出 nil pull，实得 %v", *out.Pull)
	}
}

func TestUpdateRespAcceptedRoundTrip(t *testing.T) {
	b, _ := json.Marshal(proto.UpdateResp{OK: true, Accepted: true, Version: "v1.0.0"})
	var out proto.UpdateResp
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if !out.Accepted || out.Version != "v1.0.0" {
		t.Fatalf("accepted/version 未往返: %+v", out)
	}
}
