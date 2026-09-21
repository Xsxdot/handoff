// 会话身份统一记法的金样本与反例（B358.9 契约 §3.1）。
//
// 锁三类行为：
//   - MemberIdentity 拼装（合法两 kind + 非法 kind/名字反例）；
//   - ParseMemberIdentity 往返与 fail-closed 反例（空串、旧脸 web:/cli:、残缺名）；
//   - IdentityResp wire 形状（三键恒出，无 omitempty）。
//
// 改记法先回 contract 节点。
package proto

import (
	"encoding/json"
	"testing"
)

func TestMemberIdentityGolden(t *testing.T) {
	cases := []struct {
		kind, name, want string
	}{
		{IdentityKindUser, "sycm", "user:sycm"},
		{IdentityKindAgent, "opencode", "agent:opencode"},
	}
	for _, c := range cases {
		got, err := MemberIdentity(c.kind, c.name)
		if err != nil {
			t.Fatalf("MemberIdentity(%q,%q): %v", c.kind, c.name, err)
		}
		if got != c.want {
			t.Fatalf("MemberIdentity(%q,%q) = %q，want %q", c.kind, c.name, got, c.want)
		}
		k, n, err := ParseMemberIdentity(got)
		if err != nil || k != c.kind || n != c.name {
			t.Fatalf("往返不一致: %q -> (%q,%q,%v)", got, k, n, err)
		}
	}
}

func TestMemberIdentityFailClosed(t *testing.T) {
	// 拼装反例：非法 kind、空名、带空白、带冒号。
	if _, err := MemberIdentity("web", "h"); err == nil {
		t.Fatal("kind=web 必须拒绝（机器位不是成员身份）")
	}
	if _, err := MemberIdentity(IdentityKindUser, ""); err == nil {
		t.Fatal("空名必须拒绝")
	}
	if _, err := MemberIdentity(IdentityKindUser, "  sycm "); err == nil {
		t.Fatal("带首尾空白必须拒绝")
	}
	if _, err := MemberIdentity(IdentityKindUser, "a:b"); err == nil {
		t.Fatal("名字含冒号必须拒绝")
	}
	// 解析反例（fail-closed，禁旧脸回落）：空串 / 旧传输层临时脸 / 残缺名。
	for _, raw := range []string{
		"", "web:127.0.0.1", "cli:sycm@mac", "sycm", "user:", ":sycm",
		"user:  ", "cli:opencode#s1", "agent:op encode",
	} {
		if _, _, err := ParseMemberIdentity(raw); err == nil {
			t.Fatalf("ParseMemberIdentity(%q) 必须拒绝", raw)
		}
	}
	if ValidateMemberIdentity("web:127.0.0.1") {
		t.Fatal("ValidateMemberIdentity 不得放行机器位 web:")
	}
	if !ValidateMemberIdentity("user:sycm") {
		t.Fatal("ValidateMemberIdentity 应放行 user:sycm")
	}
}

func TestIdentityRespWireShape(t *testing.T) {
	// 三键恒出（无 omitempty）：空串也要在线——前端据此区分「空值」与「后端太老」。
	raw, err := json.Marshal(IdentityResp{})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"member", "device", "configured"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("IdentityResp 缺键 %q（三键必须恒出）: %s", key, raw)
		}
	}
	if got["configured"] != false {
		t.Fatalf("零值 configured 应编码 false: %s", raw)
	}
	// 非零在线。
	raw, err = json.Marshal(IdentityResp{Member: "user:sycm", Device: "mac / Safari", Configured: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if got["member"] != "user:sycm" || got["device"] != "mac / Safari" || got["configured"] != true {
		t.Fatalf("IdentityResp 非零编码漂移: %s", raw)
	}
}
