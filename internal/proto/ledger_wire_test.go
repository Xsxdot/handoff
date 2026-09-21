// ledger_wire_test.go：锁定卡片列表 DTO 的字段存在性。
//
// 边界：这里只验证 JSON wire 形状；真实账本行到列表响应的投影由 agentd 测试覆盖。
package proto

import (
	"encoding/json"
	"reflect"
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

// TestCoordinatorWakeWire 锁 B389 §4-25/26/27 的 wire 形状：转交响应复用
// CoordinatorLaunchResp 并回报 handled_by；请求含 seat 与带 seq 的 events。
func TestCoordinatorWakeWire(t *testing.T) {
	resp := CoordinatorWakeResp{
		CoordinatorLaunchResp: CoordinatorLaunchResp{Woke: true, SessionID: "s1"},
		HandledBy:             "linux-01",
	}
	raw, err := json.Marshal(resp)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"woke", "session_id", "handled_by"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("CoordinatorWakeResp wire 缺 %q: %s", key, raw)
		}
	}

	req := CoordinatorWakeReq{
		Seat:   "cli:opencode#s1",
		Holder: "h1",
		Events: []LedgerEvent{{Seq: 7, CardID: "B1", Type: "task_mirrored"}},
	}
	raw, err = json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var reqFields struct {
		Seat   string `json:"seat"`
		Holder string `json:"holder"`
		Events []struct {
			Seq int64 `json:"seq"`
		} `json:"events"`
	}
	if err := json.Unmarshal(raw, &reqFields); err != nil {
		t.Fatal(err)
	}
	if reqFields.Seat != "cli:opencode#s1" || reqFields.Holder != "h1" ||
		len(reqFields.Events) != 1 || reqFields.Events[0].Seq != 7 {
		t.Fatalf("CoordinatorWakeReq wire 形状不符: %s", raw)
	}
}

// TestCoordinatorWakeReqRoundTrip 是纯数据 DTO 的 roundtrip 属性：编码再解码须
// 恒等（含空 Events 与含 seq 两种），一条顶一族序列化边界。
func TestCoordinatorWakeReqRoundTrip(t *testing.T) {
	for _, req := range []CoordinatorWakeReq{
		{},
		{Seat: "cli:grok#s2", Events: []LedgerEvent{{Seq: 9, CardID: "B2", Type: "room_message", Payload: json.RawMessage(`{"body":"hi"}`)}}},
		{Seat: "cli:opencode#s3", Holder: "host#1", Events: []LedgerEvent{{Seq: 1, Payload: json.RawMessage(`null`)}, {Seq: 2, Payload: json.RawMessage(`{"n":1}`)}}},
	} {
		raw, err := json.Marshal(req)
		if err != nil {
			t.Fatalf("marshal %+v: %v", req, err)
		}
		var got CoordinatorWakeReq
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", raw, err)
		}
		if !reflect.DeepEqual(req, got) {
			t.Fatalf("roundtrip 漂移: got %+v want %+v", got, req)
		}
	}
}
