// B435 席位模式经 Facade 的序列化边界（契约 C-305；plan 段 1）。
//
// 职责：真 Store → Facade.GetCard → json.Marshal(proto.Card) → 键查找，锁住
// cardWire 不漏映射 DriverMode（keystone 经 Facade 读席位，漏映射 = 一切 push 席位 no-op）。
package api

import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB435FacadeCardWireDriverMode(t *testing.T) {
	st, lc := newFacadeStore(t)
	mustBugWorkflow(t, st)
	seated, err := st.CreateCard(ledger.NewCard{Title: "有席位", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := st.CreateCard(ledger.NewCard{Title: "空座", Project: "p", Workflow: "bug", Actor: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.BindSeatMode(seated.ID, "cli:codex#t1", proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	keysOf := func(id string) map[string]json.RawMessage {
		t.Helper()
		card, err := lc.GetCard(id)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(card)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got := string(keysOf(seated.ID)["driver_mode"]); got != `"pull"` {
		t.Fatalf("有席位卡 wire 应出 driver_mode=pull，得 %q", got)
	}
	if v, ok := keysOf(empty.ID)["driver_mode"]; ok {
		t.Fatalf("空座卡 wire 必须缺 driver_mode 键（不是空串），得 %s", v)
	}
}
