package proto

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// 外部监听器消费一行 JSON；固定金样本防字段、省键和嵌套形状漂移。
func TestSessionBacklogWireGolden(t *testing.T) {
	got, err := json.Marshal(SessionBacklog{
		Type: "session_backlog", Member: "agent:main", FromSeq: 10, ToSeq: 14,
		Hits: []SessionBacklogHit{
			{Hit: SessionCite{Seq: 12, Room: "session:1", Actor: "user:sy", Body: "@agent:main 看这里"}},
			{Hit: SessionCite{Seq: 14, Room: "session:1", Actor: "user:sy", Body: "回复"}, Referenced: &SessionCite{Seq: 11, Room: "session:1", Actor: "agent:main", Body: "原消息"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/session_backlog.json")
	if err != nil {
		t.Fatal(err)
	}
	var gotWire, wantWire any
	if err := json.Unmarshal(got, &gotWire); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantWire); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotWire, wantWire) {
		t.Fatalf("backlog wire 不符金样本: got=%s want=%s", got, want)
	}
}
