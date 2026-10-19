// b435_ledgerapi_driver_mode_test.go —— B435 段 5：agentd 卡详情与卡列表路由的 driver_mode 投影。
//
// 职责：经真 HTTP 路由（ledgerCardWire / ledgerCardViewWire）锁 driver_mode 键：
// 坐下卡出值，空座卡缺键（以 map[string]json.RawMessage 区分缺键与空串）。
package agentd

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB435LedgerAPICardDriverMode(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	seated := seedCard(t, env, "API 坐下卡")
	empty := seedCard(t, env, "API 空座卡")
	if err := env.ledger.BindSeatMode(seated.ID, "cli:codex#api", proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{}); err != nil {
		t.Fatal(err)
	}
	detailCard := func(id string) map[string]json.RawMessage {
		code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards/"+id)
		if code != http.StatusOK {
			t.Fatalf("卡详情 %s 应 200: %d %s", id, code, body)
		}
		var resp struct {
			Card map[string]json.RawMessage `json:"card"`
		}
		if err := json.Unmarshal([]byte(body), &resp); err != nil {
			t.Fatal(err)
		}
		return resp.Card
	}
	if got := string(detailCard(seated.ID)["driver_mode"]); got != `"pull"` {
		t.Fatalf("卡详情 driver_mode = %s，want \"pull\"", got)
	}
	if raw, ok := detailCard(empty.ID)["driver_mode"]; ok {
		t.Fatalf("空座卡详情不应出 driver_mode，得 %s", raw)
	}

	code, body := ledgerGet(t, env.testAgentdEnv, "/api/cards")
	if code != http.StatusOK {
		t.Fatalf("卡列表应 200: %d %s", code, body)
	}
	var list struct {
		Cards []map[string]json.RawMessage `json:"cards"`
	}
	if err := json.Unmarshal([]byte(body), &list); err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]json.RawMessage{}
	for _, row := range list.Cards {
		var id string
		if err := json.Unmarshal(row["id"], &id); err != nil {
			t.Fatal(err)
		}
		rows[id] = row
	}
	if got := string(rows[seated.ID]["driver_mode"]); got != `"pull"` {
		t.Fatalf("卡列表行 driver_mode = %s，want \"pull\"", got)
	}
	if raw, ok := rows[empty.ID]["driver_mode"]; ok {
		t.Fatalf("空座卡列表行不应出 driver_mode，得 %s", raw)
	}
}
