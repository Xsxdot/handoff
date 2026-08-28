// ledger_wire_test.go：锁定卡片列表 DTO 的字段存在性。
//
// 边界：这里只验证 JSON wire 形状；真实账本行到列表响应的投影由 agentd 测试覆盖。
package proto

import (
	"encoding/json"
	"testing"
)

func TestCardViewWireCarriesWorkflowVersion(t *testing.T) {
	raw, err := json.Marshal(CardView{})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["workflow_version"]; !ok {
		t.Fatalf("CardView wire 缺 workflow_version: %s", raw)
	}
}
