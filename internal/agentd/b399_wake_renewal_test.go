// b399_wake_renewal_test.go —— B399 r2：回合内认领续租、上界取值、失败收口。
//
// 职责：钉住 (1) 回合存活期间 wake_claims 被续租（5m 租约不失效，他机拿不到）；
// (2) 回合收尾即停续、认领按 TTL 过期可被他机接管；(3) 上界取 hostapi 缺省 30m；
// (4) 失败路径写 class + 「唤醒回合结束」日志行，且超时恰一次 needs_human。
// 缝：agentd.Server.consumeAutomationEventsOnce（消费循环入口）与
// agentd.Server.drainIgnitionRequest（队列出队入口）。
// 边界：不复制 keystone 分流规则；只观察认领行、账本 wake_round 行与日志。
package agentd

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// blockingRunner 的 Resume 阻塞到测试关闭 release，模拟「回合仍在跑」。
type blockingRunner struct {
	release chan struct{}
}

func (r *blockingRunner) Launch(keysclient.SessionSpec, string) (keysclient.TurnResult, error) {
	return keysclient.TurnResult{SessionID: "sess-b"}, nil
}

func (r *blockingRunner) Resume(ref keysclient.SessionRef, _ string) (keysclient.TurnResult, error) {
	<-r.release
	return keysclient.TurnResult{SessionID: ref.SessionID}, nil
}

// TestB399WakeClaimRenewedDuringRound 锁 r2 §5：回合存活期间认领被续租，
// 租期（500ms）过后他机仍拿不到该 (card,seq)。
//
// 红（删掉 T3.4 的 startWakeClaimRenewal 调用）：500ms 后认领过期，他机拿到 → 复红。
// 绿：续租使认领不过期，他机 ClaimWake 返回 false。
func TestB399WakeClaimRenewedDuringRound(t *testing.T) {
	prevTTL, prevInt := wakeClaimTTL, wakeClaimRenewInterval
	wakeClaimTTL, wakeClaimRenewInterval = 500*time.Millisecond, 25*time.Millisecond
	defer func() { wakeClaimTTL, wakeClaimRenewInterval = prevTTL, prevInt }()

	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	release := make(chan struct{})
	env.srv.SetKeystone(keystone.New(&blockingRunner{release: release},
		&fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))
	seq := appendMirroredForConsumer(t, env.ledger, cardID, "renew", "completed", 1, `{"text":"done"}`)

	processed := make(chan int, 1)
	go func() {
		n, _, _ := env.srv.consumeAutomationEventsOnce(context.Background())
		processed <- n
	}()

	// 等回合确实开始（认领已落行），再等到超过原 500ms TTL。
	waitFor(t, func() bool {
		db, err := sql.Open("sqlite", env.ledgerPath+"?_pragma=busy_timeout(5000)")
		if err != nil {
			return false
		}
		defer db.Close()
		var n int
		return db.QueryRow(`SELECT count(*) FROM wake_claims WHERE seq = ? AND done_at IS NULL`, seq).Scan(&n) == nil && n > 0
	})
	time.Sleep(900 * time.Millisecond)

	got, err := env.ledger.ClaimWake(seq, cardID, "other-holder", wakeClaimTTL)
	if err != nil {
		t.Fatalf("他机认领探测: %v", err)
	}
	if got {
		t.Fatalf("回合存活期间认领被续租，他机不得拿到（若 got=true 说明续租未生效）")
	}

	close(release)
	if n := <-processed; n != 1 {
		t.Fatalf("回合应正常收尾 processed=1，实得 %d", n)
	}
}

// TestB399WakeClaimRenewalStopsAfterRound 锁 r2 §5：回合收尾即停续，认领按
// TTL（300ms）过期后他机可接管（崩溃恢复窗口不变）。
//
// 红（续租 goroutine 未随回合停止）：认领被一直续租，450ms 后他机仍拿不到 → 复红。
// 绿：停续后 300ms TTL 过期，他机 ClaimWake 返回 true。
func TestB399WakeClaimRenewalStopsAfterRound(t *testing.T) {
	prevTTL, prevInt := wakeClaimTTL, wakeClaimRenewInterval
	wakeClaimTTL, wakeClaimRenewInterval = 300*time.Millisecond, 25*time.Millisecond
	defer func() { wakeClaimTTL, wakeClaimRenewInterval = prevTTL, prevInt }()

	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	// 准入恒满员：回合在 AdmitSeatCarrier 即失败，认领留在飞（B390 P2 不收尾）。
	stub := &alwaysNoSlotScheduling{Service: mustScheduling(t, env.srv)}
	env.srv.SetScheduling(stub)
	seq := appendMirroredForConsumer(t, env.ledger, cardID, "renew-stop", "completed", 1, `{"text":"done"}`)

	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err == nil {
		t.Fatalf("准入满员应返回错误")
	}
	time.Sleep(450 * time.Millisecond) // > 300ms TTL；续租已在回合返回时停止

	got, err := env.ledger.ClaimWake(seq, cardID, "other-holder", wakeClaimTTL)
	if err != nil {
		t.Fatalf("他机认领探测: %v", err)
	}
	if !got {
		t.Fatalf("回合收尾后认领应按 TTL 过期、他机可接管（got=false 说明续租未随回合停止）")
	}
}

// TestB399WakeTurnBoundIsHostapiDefault 锁 r2 §5：上界取 hostapi 缺省（30m），
// 不再由 DriverLeaseTTL 派生。
//
// 红（当前 HEAD）：coordWakeTurnTimeout = DriverLeaseTTL/2 = 2m30s。
// 绿（T3.1）：== hostapi.DefaultTurnTimeout。
// 变异：改回 ledger.DriverLeaseTTL / 2 → 复红。
func TestB399WakeTurnBoundIsHostapiDefault(t *testing.T) {
	if coordWakeTurnTimeout != hostapi.DefaultTurnTimeout {
		t.Fatalf("上界应取 hostapi 缺省 %v，实得 %v", hostapi.DefaultTurnTimeout, coordWakeTurnTimeout)
	}
	if coordWakeTurnTimeout == ledger.DriverLeaseTTL/2 {
		t.Fatalf("上界不得再由 DriverLeaseTTL 派生")
	}
}

// TestB399TimeoutWritesClassAndEndAndOneNeedsHuman 锁 r2 用户裁定 2/4：超时轮
// 落恰一条 needs_human（同轮重试不刷屏）、phase=fail 注释带 class=timeout、日志含
// 「唤醒回合结束」。
//
// 红（当前 HEAD）：writeWakeRoundFail 无 class、不落 needs_human、无结束行。
// 绿（T3.5/T3.6）：class=timeout 的 fail 行恰 1、needs_human 恰 1、日志含结束行。
// 变异：删掉 T3.6 的 `&& wrote` → needs_human 5 条 → 复红。
func TestB399TimeoutWritesClassAndEndAndOneNeedsHuman(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	runner := &fallbackConsumerRunner{failResume: true, resumeTimeout: true}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))

	var buf bytes.Buffer
	prev := env.srv.log
	env.srv.log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn}))
	defer func() { env.srv.log = prev }()

	req := scheduling.IgnitionRequest{
		Card: cardID, Squad: "coord", Node: "implement", Actor: "test", Ready: true,
	}
	for i := 0; i < 5; i++ {
		_ = env.srv.drainIgnitionRequest(context.Background(), req)
	}

	// 断言 A：phase=fail 且 Class=timeout 的注释恰 1 行。
	fails := 0
	for _, r := range readWakeRoundComments(t, env, cardID) {
		if r.Phase == "fail" && r.Class != nil && *r.Class == wakeFailClassTimeout {
			fails++
		}
	}
	if fails != 1 {
		t.Fatalf("超时轮应恰一行 class=timeout 的 fail，实得 %d", fails)
	}
	// 断言 B：needs_human 恰 1 条（跨 5 次重试不刷屏）。
	if n := b390NeedsHumanCount(t, env, cardID); n != 1 {
		t.Fatalf("超时轮应恰一条 needs_human，实得 %d", n)
	}
	// 断言 C：日志含「唤醒回合结束」且带 round_id/class=timeout。
	logs := buf.String()
	if !strings.Contains(logs, "唤醒回合结束") {
		t.Fatalf("失败路径缺「唤醒回合结束」日志行:\n%s", logs)
	}
	if !strings.Contains(logs, "class=timeout") {
		t.Fatalf("结束行应带 class=timeout:\n%s", logs)
	}
}

// TestB399WakeFailClassThreeStates 锁 spec §6 接缝3断言③：超时/会话不存在/其他
// 三态经 wakeFailClass 可区分（T3.9-4 原只断言 timeout 一态）。
//
// 纯映射直接断言：当前 wakeFailClass 分支正确时本测绿；与下面 e2e 成对，
// 保证 e2e 的 session_not_found 红不是映射表本身缺支。
func TestB399WakeFailClassThreeStates(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"超时", fmt.Errorf("回合超时: %w", keysclient.ErrTurnTimeout), wakeFailClassTimeout},
		{"会话不存在", fmt.Errorf("resume 失败: %w", keysclient.ErrSessionNotFound), wakeFailClassSessionNotFound},
		{"其他", errors.New("resume failed"), wakeFailClassOther},
		{"nil", nil, wakeFailClassOther},
	}
	for _, tc := range cases {
		if got := wakeFailClass(tc.err); got != tc.want {
			t.Fatalf("%s: wakeFailClass=%q want %q", tc.name, got, tc.want)
		}
	}
}

// TestB399SessionNotFoundRebuildFailWritesClass 锁 r2 §5③ 端到端：resume 报
// Session not found、重建也失败 → phase=fail 注释 class=session_not_found。
//
// 红（当前 HEAD）：keystone.go:179 用 %v 包 resumeErr，链断，wakeFailClass 落 other。
// 绿（%w 修复）：class=session_not_found。
// 变异：把 resumeErr 的 %w 改回 %v → 复红。
func TestB399SessionNotFoundRebuildFailWritesClass(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	runner := &fallbackConsumerRunner{failResume: true, resumeNotFound: true, failLaunch: true}
	env.srv.SetKeystone(keystone.New(runner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))

	req := scheduling.IgnitionRequest{
		Card: cardID, Squad: "coord", Node: "implement", Actor: "test", Ready: true,
	}
	if err := env.srv.drainIgnitionRequest(context.Background(), req); err == nil {
		t.Fatalf("resume+重建均失败应返回错误")
	}

	found := 0
	for _, r := range readWakeRoundComments(t, env, cardID) {
		if r.Phase == "fail" && r.Class != nil && *r.Class == wakeFailClassSessionNotFound {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("重建失败轮应恰一行 class=session_not_found，实得 %d", found)
	}
}
