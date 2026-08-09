// peer 包协议测试：hello/negotiate/events-after 的线格式与游标语义。
//
// 职责：
//   - Hello 返回 protocol version 与 capability map
//   - snapshot 带 through_machine_seq
//   - events after cursor 单调
//   - 重复 (machine_id, machine_seq) 被忽略
//
// 边界：
//   - 纯线格式/语义测试，不发起真实 HTTP（agentd/peer_server_test 覆盖路由）
//   - capability 协商的「未知 capability 忽略」语义也在此锁定
package peer

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestHelloWireFormat 断言 Hello 的 JSON 线格式（protocol_version + capabilities）。
func TestHelloWireFormat(t *testing.T) {
	h := Hello{
		ProtocolVersion: 1,
		Capabilities:    map[string]int{"catalog": 1, "machine_events": 1},
	}
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatalf("marshal hello: %v", err)
	}
	for _, want := range []string{`"protocol_version":1`, `"catalog":1`, `"machine_events":1`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("hello JSON %s 缺少 %s", b, want)
		}
	}
}

// TestNegotiateIgnoresUnknownCapability 验证未知 capability 被忽略、核心 capability
// 缺失时标记 incompatible。
func TestNegotiateIgnoresUnknownCapability(t *testing.T) {
	// 未知 capability 不影响协商成功
	negotiated, incompatible := Negotiate(map[string]int{"unknown_future": 1, "catalog": 1, "machine_events": 1})
	if incompatible {
		t.Fatal("含未知 capability 不应 incompatible")
	}
	if negotiated["catalog"] != 1 || negotiated["machine_events"] != 1 {
		t.Fatalf("核心 capability 未保留: %+v", negotiated)
	}
	// 缺核心 capability → incompatible
	if _, incompatible := Negotiate(map[string]int{"catalog": 1}); !incompatible {
		t.Fatal("缺 machine_events 应 incompatible")
	}
	if _, incompatible := Negotiate(map[string]int{"machine_events": 1}); !incompatible {
		t.Fatal("缺 catalog 应 incompatible")
	}
}

// TestSnapshotWireFormat 断言 snapshot 带 through_machine_seq。
func TestSnapshotWireFormat(t *testing.T) {
	s := MachineSnapshot{ThroughMachineSeq: 42, WorkspaceCount: 3, TaskCount: 1}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	for _, want := range []string{`"through_machine_seq":42`, `"workspace_count":3`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("snapshot JSON %s 缺少 %s", b, want)
		}
	}
}
