// b393_wakeround_roundkey_test.go —— B393 复评缺口：wake_round 键按轮次分组 + 转交失败本机落行。
//
// 职责：钉住 (1) 同卡连续两轮唤醒各自得一组 start/end（键含轮次标识，不被
// EnsureComment 跨轮吞掉）；(2) 本机转交对端失败时，本机侧留一行 phase=fail
// 且标明转交目标机器。
// 缝：agentd.Server.wakeCoordinatorRound / wakeCoordinatorRoundRaw（含转交分支）。
// 边界：只观察账本 wake_round 注释；不复制 keystone 规则、不动路由/承载语义。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/testhttp"
)

// readWakeRoundCommentRows 收集该卡全部 wake_round 注释的 dedupe_key 与解码正文。
func readWakeRoundCommentRows(t *testing.T, env *ledgerEnv, cardID string) []struct {
	Key   string
	Round WakeRoundEvent
} {
	t.Helper()
	events, err := env.ledger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatalf("读账本事件: %v", err)
	}
	var out []struct {
		Key   string
		Round WakeRoundEvent
	}
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
		out = append(out, struct {
			Key   string
			Round WakeRoundEvent
		}{Key: payload.DedupeKey, Round: round})
	}
	return out
}

// TestB393WakeRoundKeysGroupPerRound 锁复评缺口 (1)：同卡连续两轮唤醒，账本
// 必须可见两组 start/end（键含轮次标识）。
//
// 红（当前 HEAD）：start 键固定 "wake_round:start"、end 键只含 session——
// EnsureComment 按键幂等，第二轮 start/end 被吞，只见第一轮（失明）。
// 绿：两轮各得一组，phases 计数 start==2 且 end==2，且四个 dedupe_key 互异。
// 变异：把 start 键改回固定值 → 复现「只看到第一轮」。
func TestB393WakeRoundKeysGroupPerRound(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	for i := 0; i < 2; i++ {
		if _, err := env.srv.wakeCoordinatorRound(context.Background(), cardID, []keystone.WakeEvent{{
			Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal",
		}}); err != nil {
			t.Fatalf("第 %d 轮唤醒: %v", i+1, err)
		}
	}

	rows := readWakeRoundCommentRows(t, env, cardID)
	phases := map[string]int{}
	keys := map[string]bool{}
	for _, row := range rows {
		phases[row.Round.Phase]++
		if keys[row.Key] {
			t.Fatalf("dedupe_key 重复（未按轮次分组）: %s", row.Key)
		}
		keys[row.Key] = true
	}
	if phases["start"] != 2 {
		t.Fatalf("同卡两轮应两行 start，实得 %d（键未按轮次分组=只看到第一轮；rows=%+v）", phases["start"], rows)
	}
	if phases["end"] != 2 {
		t.Fatalf("同卡两轮应两行 end，实得 %d（rows=%+v）", phases["end"], rows)
	}
}

// TestB393TransferWakeFailureWritesLocalFail 锁复评缺口 (2)：本机认领→转交→
// 对端失败时，本机侧必须留一行 phase=fail 且标明转交到哪台机器。
//
// 红（当前 HEAD）：transferCoordinatorWake 两个失败出口只打日志、不落账。
// 绿：本机账本出现 wake_round fail，Err 含目标机器名与失败原因。
// 变异：去掉转交失败分支的 EnsureComment → 复红。
func TestB393TransferWakeFailureWritesLocalFail(t *testing.T) {
	remote := testhttp.NewServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusBadGateway, errors.New("对端唤醒执行失败"))
	}))
	t.Cleanup(remote.Close)
	env := newNoPTYLedgerEnvWithTargets(t, map[string]config.Target{
		"linux-01": {Addr: remote.URL, Token: testToken},
	})
	SetupAutomationForTest(t, env.srv, env.ledger)
	seedQueueCoordinator(t, env)
	runner := &bearingTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	seat, _ := proto.EncodeSeatIdentity("opencode", "sess-remote")
	if err := env.ledger.BindSeat(cardID, seat, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "linux-01"}); err != nil {
		t.Fatalf("落远端承载: %v", err)
	}
	raws := []proto.LedgerEvent{{Seq: 1, CardID: cardID, Type: ledger.EvTaskMirrored}}

	_, err := env.srv.wakeCoordinatorRoundRaw(context.Background(), cardID,
		[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal"}}, raws)
	if err == nil {
		t.Fatalf("对端 502 时转交唤醒必须返回错误")
	}

	rows := readWakeRoundCommentRows(t, env, cardID)
	var failErr string
	fails := 0
	for _, row := range rows {
		if row.Round.Phase == "fail" {
			fails++
			if row.Round.Err != nil {
				failErr = *row.Round.Err
			}
		}
	}
	if fails != 1 {
		t.Fatalf("转交对端失败应恰一行本机 phase=fail，实得 %d（rows=%+v）", fails, rows)
	}
	if !strings.Contains(failErr, "linux-01") {
		t.Fatalf("转交失败行必须标明目标机器 linux-01，实得 Err=%q", failErr)
	}
	if strings.TrimSpace(failErr) == "" {
		t.Fatalf("转交失败行 Err 不得为空")
	}
}

// TestB393TransferNoTargetWritesLocalFail 锁转交取客户端失败出口：目标未登记时
// 本机同样留一行标明机器的 fail（clientForTarget 分支）。
//
// 红（当前 HEAD）：clientForTarget 失败只打日志、不落账。
// 绿：本机账本出现 wake_round fail 且 Err 含机器名。
func TestB393TransferNoTargetWritesLocalFail(t *testing.T) {
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	seedQueueCoordinator(t, env)
	runner := &bearingTraceRunner{}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	cardID := createCoordCard(t, env)
	seat, _ := proto.EncodeSeatIdentity("opencode", "sess-remote")
	if err := env.ledger.BindSeat(cardID, seat, proto.SeatSourceCoordinate,
		ledger.SeatBearing{Carrier: "coord-carrier", Machine: "linux-01"}); err != nil {
		t.Fatalf("落远端承载: %v", err)
	}
	raws := []proto.LedgerEvent{{Seq: 1, CardID: cardID, Type: ledger.EvTaskMirrored}}

	_, err := env.srv.wakeCoordinatorRoundRaw(context.Background(), cardID,
		[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "terminal"}}, raws)
	if err == nil {
		t.Fatalf("目标未登记时转交唤醒必须返回错误")
	}

	rows := readWakeRoundCommentRows(t, env, cardID)
	var failErr string
	fails := 0
	for _, row := range rows {
		if row.Round.Phase == "fail" {
			fails++
			if row.Round.Err != nil {
				failErr = *row.Round.Err
			}
		}
	}
	if fails != 1 {
		t.Fatalf("转交取客户端失败应恰一行本机 phase=fail，实得 %d（rows=%+v）", fails, rows)
	}
	if !strings.Contains(failErr, "linux-01") {
		t.Fatalf("转交失败行必须标明目标机器 linux-01，实得 Err=%q", failErr)
	}
}
