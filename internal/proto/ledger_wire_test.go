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

func TestCardViewWireCarriesSeatOnlyWhenOccupied(t *testing.T) {
	occupied, err := json.Marshal(CardView{DriverSession: "cli:codex#thread-01", DriverSource: string(SeatSourceBind)})
	if err != nil {
		t.Fatal(err)
	}
	var occupiedFields map[string]json.RawMessage
	if err := json.Unmarshal(occupied, &occupiedFields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"driver_session", "driver_source"} {
		if _, ok := occupiedFields[key]; !ok {
			t.Fatalf("occupied CardView wire 缺 %q: %s", key, occupied)
		}
	}

	empty, err := json.Marshal(CardView{})
	if err != nil {
		t.Fatal(err)
	}
	var emptyFields map[string]json.RawMessage
	if err := json.Unmarshal(empty, &emptyFields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"driver_session", "driver_source"} {
		if _, ok := emptyFields[key]; ok {
			t.Fatalf("empty CardView wire 不应含 %q: %s", key, empty)
		}
	}
}

func TestCoordinatorRebindRequestLaunchFixture(t *testing.T) {
	raw, err := json.Marshal(CoordinatorRebindReq{Mode: "launch"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(raw), `{"mode":"launch"}`; got != want {
		t.Fatalf("CoordinatorRebindReq wire = %s, want %s", got, want)
	}
}
