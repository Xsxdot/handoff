// b435_b434_retired_test.go —— B435.5（X）d_gateway 退役面的行为锁（plan b435.5 段 1；契约 §7、C-238）。
//
// 职责：
//   - 门禁绕过族：三条 B434 路由（GET/POST …/coordinator/desktop-launch、POST /api/desktop/deliver）
//     从真 mux 上消失——未配账本时也是 404/405，不再经 withLedger 返回 503（即不再打开账本）。
//   - C-238：needs_human 对 pull 席位卡与 push 席位卡都零唤醒（keystone 零回合、信箱零文件、opener
//     零调用）；转交收件端带该事件 → 200 且 woke=false。同夹具配一个「@push 卡恰一次唤醒」对照，
//     防「什么都不唤醒」的稳定假绿。
//
// 边界：只测本包（agentd）的真实入口；全仓源码守卫在仓根 b435_retired_b434_guard_test.go。
package agentd

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB435RetiredDesktopRoutesGone(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	card := createCoordCard(t, env)
	routes := []struct{ method, path string }{
		{http.MethodGet, "/api/cards/" + card + "/coordinator/desktop-launch"},
		{http.MethodPost, "/api/cards/" + card + "/coordinator/desktop-launch"},
		{http.MethodPost, "/api/desktop/deliver"},
	}
	for _, withLedger := range []bool{true, false} {
		if !withLedger {
			env.srv.SetLedger(nil)
		}
		for _, r := range routes {
			req, err := http.NewRequest(r.method, env.ts.URL+r.path, strings.NewReader(`{"card":"`+card+`"}`))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Authorization", "Bearer "+env.token)
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
				t.Errorf("ledger=%v %s %s：退役路由应 404/405，得 %d（503 = 仍经 withLedger 打开账本）",
					withLedger, r.method, r.path, resp.StatusCode)
			}
		}
	}
}

func TestB435NeedsHumanZeroWakeC238(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	pushCard, pullCard := createCoordCard(t, env), createCoordCard(t, env)
	pushSeat := b435BindPush(t, env, pushCard, "c238-push")
	b435BindPull(t, env, pullCard, "c238-pull")
	sessionID := b435Session(t, env, pushCard, pullCard)
	home, spy := o2Wire(t, env.srv)
	drainAutomation(t, env)

	var pushEv proto.LedgerEvent
	for _, card := range []string{pushCard, pullCard} {
		if err := env.ledger.MarkNeedsHuman(card, "人在终端打的标", "cli:sycm@host"); err != nil {
			t.Fatal(err)
		}
		evs, err := env.srv.autoLedger.EventsFromAsc([]string{card}, 0, 10000)
		if err != nil {
			t.Fatal(err)
		}
		ev := evs[len(evs)-1]
		if ev.Type != ledger.EvNeedsHuman {
			t.Fatalf("卡 %s 末条事件应为 needs_human，得 %s", card, ev.Type)
		}
		if wakes, err := env.srv.automationWakeEvents(ev); err != nil || len(wakes) != 0 {
			t.Fatalf("C-238：卡 %s 的 needs_human 映射应为空，得 %v err=%v", card, wakes, err)
		}
		if card == pushCard {
			pushEv = ev
		}
	}
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("C-238：needs_human 不得唤醒任何席位: %v", got)
	}
	if letters := o2Letters(t, home); len(letters) != 0 || len(spy.calls()) != 0 {
		t.Fatalf("C-238：信箱零文件、opener 零调用，得 信=%d opener=%v", len(letters), spy.calls())
	}

	body, _ := json.Marshal(proto.CoordinatorWakeReq{Seat: pushSeat, Events: []proto.LedgerEvent{pushEv}})
	code, resp := ledgerPost(t, env.testAgentdEnv, "/api/cards/"+pushCard+"/coordinator/wake", string(body))
	if code != http.StatusOK {
		t.Fatalf("C-238：转交收件带 needs_human 应 200，得 %d %s", code, resp)
	}
	var got proto.CoordinatorWakeResp
	if err := json.Unmarshal([]byte(resp), &got); err != nil || got.Woke {
		t.Fatalf("C-238：转交收件应 woke=false，得 %+v err=%v", got, err)
	}
	if got := b435Resumes(runner); len(got) != 0 {
		t.Fatalf("C-238：转交收件不得起回合: %v", got)
	}

	// 对照：同夹具 @push 卡恰一次唤醒。
	sendSessionMessage(t, env.srv.rooms, sessionID, "user:tester", "叫 push 卡", []string{pushCard}, 0)
	b435Consume(t, env)
	if got := b435Resumes(runner); len(got) != 1 || !b435WokeCard(got[0], pushCard) {
		t.Fatalf("对照：@push 卡应恰一次唤醒: %v", got)
	}
}
