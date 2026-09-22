// b393_wakeround_fail_test.go —— B393 唤醒回合失败态的账本终态行。
//
// 职责：钉住 agentd.wakeCoordinatorRound 在 keystone.Wake 返回错误时，必须往
// 账本落 phase="fail" 的 wake_round 注释（带原因摘要）；键按轮次分组——同卡
// 每轮各得一行 fail，一轮一组为上限（不跨轮吞行、也不在轮内刷屏）。
// 另钉队列 2s 重试路径：同一 IgnitionRequest 反复出队失败时，重试不得新增行。
// 缝：agentd.Server.wakeCoordinatorRound（消费循环调用 Wake 的真实入口）；
//
//	agentd.Server.drainIgnitionRequest（队列出队→唤醒的重试入口）。
//
// 边界：只观察账本里落的注释行；不复制 keystone 的重建/升级规则。
package agentd

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/scheduling"
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

// countWakeRoundPhases 统计 wake_round 注释里各 phase 的行数。
func countWakeRoundPhases(rounds []WakeRoundEvent) map[string]int {
	phases := map[string]int{}
	for _, r := range rounds {
		phases[r.Phase]++
	}
	return phases
}

// TestB393WakeRoundFailureWritesFailEvent 锁 B393 MAJOR-1 + 复评缺口 (1)：
// resume 与重建均失败时，账本必须留下 phase="fail" 的 wake_round 注释且 Err 带
// 原因摘要；同卡再次失败是新一轮，须再得一行（键按轮次分组，不再是跨轮吞行）。
//
// 红（当前 HEAD）：失败分支只打日志即 return，WakeRoundEvent 的 phase="fail"
// 在生产无写入点，phases["fail"]==0。
// 绿：两轮失败各一行 fail（键含轮次标识）。
// 变异：去掉失败分支的 EnsureComment 落行 → 复红；把 fail 键改回固定值 →
// 第二轮 fail 被吞（fails==1 而非 2）→ 复红。
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

	// 同卡再次失败 = 新一轮：键按轮次分组，应再得一行 fail（不是跨轮吞行）。
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
	if fails != 2 {
		t.Fatalf("同卡两轮失败应两行 phase=fail（键按轮次分组），实得 %d（全部注释: %+v）", fails, rounds)
	}
}

// TestB393WakeRoundQueueRetryDoesNotFloodFailRows 锁复评 major：D4（远端无
// raws）/本机 Wake 失败经 drainIgnitionRequest 的 2s 队列重试时，fail 行以
// 「一轮一组」为上限——重试不得新增行。
//
// 红（修复前）：每轮 nextWakeRoundID 新键 → 5 次重试落 5 行 fail（探针读数）。
// 绿：5 次 drainIgnitionRequest 失败后仍恰一行 fail（同一轮复用 roundID）。
// 变异：去掉队列路径的 roundID 复用 → 每次新键 → 5 行 → 复红。
func TestB393WakeRoundQueueRetryDoesNotFloodFailRows(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	// 本机 Wake 失败路径：resume+rebuild 双失败，每次出队必走 fail 落行。
	failRunner := &fallbackConsumerRunner{failResume: true, failLaunch: true}
	env.srv.SetKeystone(keystone.New(failRunner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))

	req := scheduling.IgnitionRequest{
		Card: cardID, Squad: "coord", Node: "implement",
		Actor: "test", Ready: true,
	}
	for i := 0; i < 5; i++ {
		err := env.srv.drainIgnitionRequest(context.Background(), req)
		if err == nil {
			t.Fatalf("第 %d 次队列出队唤醒必须返回错误", i+1)
		}
	}

	phases := countWakeRoundPhases(readWakeRoundComments(t, env, cardID))
	if phases["fail"] != 1 {
		t.Fatalf("队列 5 次重试应恰一行 phase=fail（一轮一组，重试不新增行），实得 %d", phases["fail"])
	}
	// start 同样一轮一组：5 次重试不得刷出 5 行 start。
	if phases["start"] > 1 {
		t.Fatalf("队列重试不得新增 start 行，一轮至多一组，实得 start=%d", phases["start"])
	}
}
