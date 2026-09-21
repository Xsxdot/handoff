// b393_wakeround_ledger_test.go —— B393 唤醒回合账本在途读数的缝级测试。
//
// 职责：经真实 ledger Facade + 真席位夹具走 agentd.wakeCoordinatorRound，
// 断言唤醒回合前后各落恰一行 wake_round 注释（spec §4.3：在途/结束读数）。
// 缝：agentd.Server.wakeCoordinatorRound（消费循环调用 Awake 的真实入口）。
// 边界：不复制 keystone 唤醒规则；只观察账本里落的注释行。
package agentd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// TestB393WakeRoundWritesLedgerEvent 锁 spec §4.3：一次唤醒回合必须在账本落下
// dedupe_key 前缀 "wake_round" 的注释行（start 与 end），供「在途」读数。
//
// 红（当前 HEAD）：wakeCoordinatorRound 不写任何 wake_round 行。
// 绿（T2.3）：start 行（phase=start）与 end 行（phase=end）各恰一行。
func TestB393WakeRoundWritesLedgerEvent(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	if _, err := env.srv.wakeCoordinatorRound(context.Background(), cardID, []keystone.WakeEvent{{
		Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal",
	}}); err != nil {
		t.Fatalf("唤醒回合: %v", err)
	}
	_, resumes, _ := runner.snapshot()
	if len(resumes) != 1 {
		t.Fatalf("应恰跑一次 Resume，实得 %d", len(resumes))
	}

	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读账本事件: %v", err)
	}
	phases := map[string]int{}
	for _, ev := range events {
		if ev.Type != ledger.EvComment {
			continue
		}
		var payload struct {
			Body      string `json:"body"`
			DedupeKey string `json:"dedupe_key"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			continue
		}
		if !strings.HasPrefix(payload.DedupeKey, WakeRoundDedupePrefix) {
			continue
		}
		var round WakeRoundEvent
		if err := json.Unmarshal([]byte(payload.Body), &round); err != nil {
			t.Fatalf("wake_round 注释正文不是合法 WakeRoundEvent: %v\n%s", err, payload.Body)
		}
		phases[round.Phase]++
	}
	if phases["start"] != 1 {
		t.Fatalf("应恰一行 start 在途读数，实得 %d（全部注释相位: %v）", phases["start"], phases)
	}
	if phases["end"] != 1 {
		t.Fatalf("应恰一行 end 结算读数，实得 %d（全部注释相位: %v）", phases["end"], phases)
	}
}
