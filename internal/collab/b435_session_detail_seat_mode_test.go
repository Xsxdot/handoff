// b435_session_detail_seat_mode_test.go —— B435 段 5：会话详情卡投影的 seat_mode（C-327）。
//
// 职责：真 Store → Facade → collab SessionDetail → json.Marshal，锁 summary.cards[].seat_mode：
// 坐下卡出值，空座卡缺键（以 map[string]json.RawMessage 区分缺键与空串）。
package collab

import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB435SessionDetailSeatMode(t *testing.T) {
	svc, st, _ := newSessionFixture(t)
	sess, err := svc.CreateSession("B435 seat_mode", "user:tester", "user:tester")
	if err != nil {
		t.Fatal(err)
	}
	seated := sessionCard(t, st, "详情坐下卡")
	empty := sessionCard(t, st, "详情空座卡")
	for _, id := range []string{seated.ID, empty.ID} {
		if err := svc.JoinCard(sess.ID, id, "user:tester"); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.BindSeatMode(seated.ID, "cli:codex#detail", proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	detail, err := svc.SessionDetail(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	var wire struct {
		Summary struct {
			Cards []map[string]json.RawMessage `json:"cards"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]json.RawMessage{}
	for _, row := range wire.Summary.Cards {
		var id string
		if err := json.Unmarshal(row["card_id"], &id); err != nil {
			t.Fatal(err)
		}
		rows[id] = row
	}
	if got := string(rows[seated.ID]["seat_mode"]); got != `"pull"` {
		t.Fatalf("坐下卡 seat_mode = %s，want \"pull\"（%s）", got, raw)
	}
	if v, ok := rows[empty.ID]["seat_mode"]; ok {
		t.Fatalf("空座卡不应出 seat_mode，得 %s", v)
	}
}
