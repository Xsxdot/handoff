package agentd

// b446_launch_self_seat_test.go —— B446：协调者在 launch 首轮里自己坐下（`card bind` / `card rebind --self`
// 落成 bind/pull 零承载席位）后，launch 收尾必须把这同一会话的席位改写成 coordinate/push + 承载并视为成功，
// 不回 409、不标 needs_human（真机 B444 R6 卡 D5）。席位被别的会话占时仍走 409 + needs_human，
// 由 TestCoordLaunchSeatWriteFailureLeavesNeedsHuman 锁住。

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// selfSeatingRunner 模拟协调者首轮回合内自己坐下：空座走 `card bind`（BindSeatMode bind/pull、零承载），
// 有人走 `card rebind --self`（RebindSeatMode bind/pull），身份就是本回合自报的会话。
type selfSeatingRunner struct {
	env     *ledgerEnv
	card    string
	session string
}

func (r *selfSeatingRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	identity := "cli:opencode#" + r.session
	c, err := r.env.ledger.GetCard(r.card)
	if err != nil {
		return keysclient.TurnResult{}, err
	}
	if c.DriverSession == "" {
		err = r.env.ledger.BindSeatMode(r.card, identity, proto.SeatSourceBind, proto.SeatModePull, ledger.SeatBearing{})
	} else {
		err = r.env.ledger.RebindSeatMode(r.card, identity, proto.SeatSourceBind, proto.SeatModePull, c.DriverSession, ledger.SeatBearing{})
	}
	if err != nil {
		return keysclient.TurnResult{}, err
	}
	return keysclient.TurnResult{SessionID: r.session, Output: "ok"}, nil
}

func (r *selfSeatingRunner) Resume(ref keysclient.SessionRef, _ string) (keysclient.TurnResult, error) {
	return keysclient.TurnResult{SessionID: ref.SessionID, Output: "ok"}, nil
}

func TestB446LaunchFinalizeAdoptsSelfSeat(t *testing.T) {
	for _, tc := range []struct {
		name, path, body string
		preSeat          bool
	}{
		{name: "coordinate 空座首轮自坐", path: "/coordinator/launch", body: `{}`},
		{name: "rebind --launch 首轮自换绑", path: "/coordinator/rebind", body: `{"mode":"launch"}`, preSeat: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _ := newNoPTYCoordEnv(t)
			seedCoordinatorSquad(t, env)
			cardID := createCoordCard(t, env)
			if tc.preSeat {
				if err := env.ledger.BindSeatMode(cardID, "cli:opencode#sess-old", proto.SeatSourceCoordinate, proto.SeatModePush,
					ledger.SeatBearing{Carrier: "c1", Machine: "local"}); err != nil {
					t.Fatalf("预置旧席位: %v", err)
				}
			}
			runner := &selfSeatingRunner{env: env, card: cardID, session: "sess-self"}
			ks := keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger,
				attachLocator{expandHome: hostapi.ExpandHomePath})
			ks.SetSessionRefResolver(coordinatorSessionRefResolver{server: env.srv, expandHomeDir: hostapi.ExpandHomePath})
			env.srv.SetKeystone(ks)

			code, body := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+cardID+tc.path, tc.body)
			if code != http.StatusOK {
				t.Fatalf("同会话自坐后收尾应 200，状态=%d body=%s", code, body)
			}
			if reason, err := env.ledger.NeedsOf(cardID); err != nil || strings.TrimSpace(reason) != "" {
				t.Fatalf("同会话自坐不应标 needs_human：reason=%q err=%v", reason, err)
			}
			card, err := env.ledger.GetCard(cardID)
			if err != nil {
				t.Fatalf("读回卡: %v", err)
			}
			if card.DriverSession != "cli:opencode#sess-self" ||
				card.DriverSource != string(proto.SeatSourceCoordinate) || card.DriverMode != string(proto.SeatModePush) {
				t.Fatalf("收尾应把自坐席位改写为 coordinate/push，got session=%q source=%q mode=%q",
					card.DriverSession, card.DriverSource, card.DriverMode)
			}
			bearing, ok, err := env.ledger.SeatBearingOf(cardID)
			if err != nil || !ok || bearing.Carrier == "" {
				t.Fatalf("收尾应补齐承载记录：bearing=%+v ok=%v err=%v", bearing, ok, err)
			}
		})
	}
}
