// b393_wakeround_fail_test.go —— B393 唤醒回合失败态的账本终态行。
//
// 职责：钉住 agentd.wakeCoordinatorRound 在 keystone.Wake 返回错误时，必须往
// 账本落恰一行 phase="fail" 的 wake_round 注释（带原因摘要），且同卡重复失败
// 不产生重复行（沿用 EvComment/EnsureComment 的 dedupe 约定）。
// 缝：agentd.Server.wakeCoordinatorRound（消费循环调用 Wake 的真实入口）。
// 边界：只观察账本里落的注释行；不复制 keystone 的重建/升级规则。
package agentd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// readWakeRoundComments 收集该卡上所有 dedupe_key 以 WakeRoundDedupePrefix 开头
// 的注释，解码为 WakeRoundEvent 切片。
func readWakeRoundComments(t *testing.T, env *ledgerEnv, cardID string) []WakeRoundEvent {
	t.Helper()
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读账本事件: %v", err)
	}
	var out []WakeRoundEvent
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
		out = append(out, round)
	}
	return out
}

// TestB393WakeRoundFailureWritesFailEvent 锁 B393 MAJOR-1：resume 与重建均失败
// 时，账本必须留下恰一行 phase="fail" 的 wake_round 注释，且 Err 带原因摘要；
// 同卡再次失败不得追加第二行（dedupe 前缀约定）。
//
// 红（当前 HEAD）：失败分支只打日志即 return，WakeRoundEvent 的 phase="fail"
// 在生产无写入点，phases["fail"]==0。
// 绿：失败分支落一行 wake_round:fail 注释。
// 变异：去掉失败分支的 EnsureComment 落行 → 复红。
func TestB393WakeRoundFailureWritesFailEvent(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	// 换成 resume 与重建都失败的 runner，让 Wake 走完整失败路径。
	failRunner := &fallbackConsumerRunner{failResume: true, failLaunch: true}
	env.srv.SetKeystone(keystone.New(failRunner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))

	if _, err := env.srv.wakeCoordinatorRound(context.Background(), cardID, []keystone.WakeEvent{{
		Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal",
	}}); err == nil {
		t.Fatalf("resume 与重建均失败时唤醒必须返回错误")
	}

	rounds := readWakeRoundComments(t, env, cardID)
	fails := 0
	var failErr string
	for _, round := range rounds {
		if round.Phase == "fail" {
			fails++
			if round.Err != nil {
				failErr = *round.Err
			}
		}
	}
	if fails != 1 {
		t.Fatalf("失败态应恰一行 phase=fail，实得 %d（全部注释: %+v）", fails, rounds)
	}
	if strings.TrimSpace(failErr) == "" {
		t.Fatalf("phase=fail 的 Err 不得为空（须带失败原因摘要）: %+v", rounds)
	}

	// 同卡再次失败：dedupe 前缀保证仍恰一行。
	if _, err := env.srv.wakeCoordinatorRound(context.Background(), cardID, []keystone.WakeEvent{{
		Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal-again",
	}}); err == nil {
		t.Fatalf("第二次双失败唤醒必须返回错误")
	}
	rounds = readWakeRoundComments(t, env, cardID)
	fails = 0
	for _, round := range rounds {
		if round.Phase == "fail" {
			fails++
		}
	}
	if fails != 1 {
		t.Fatalf("重复失败不得追加行，应仍恰一行 phase=fail，实得 %d（全部注释: %+v）", fails, rounds)
	}
}
