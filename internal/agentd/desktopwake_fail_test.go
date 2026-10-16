// desktopwake_fail_test.go —— B434 G2：本机失败路径与桌面端失败段（契约 §3.7、D5；
// 条 103、104、106–111、172–174、187；拆解稿 §2.4 第 4 条、审阅轮 3 建议 1）。
//
// 职责：从缝 5 调用方 consumeAutomationEventsOnce 与缝 4 wakeDesktopSeat 进，用 opener spy
// 注入失败（desktopOpenerSpy.err）、把信箱根指向普通文件注入写信失败，锁「未落盘不标 seen
// 并退避重试 / 已落盘标 seen 不重投 / 一段恰一条 needs_human / 不经准入」。
// 边界：跨机转交失败在 desktopwake_remote_test；夹具来自 desktopwake_slice/_letter/_mailbox_test。
package agentd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// desktopNeedsHumanOn 返回卡上全部 needs_human 事件。
func desktopNeedsHumanOn(t *testing.T, env *ledgerEnv, cardID string) []proto.LedgerEvent {
	t.Helper()
	evs, err := env.srv.autoLedger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	var out []proto.LedgerEvent
	for _, ev := range evs {
		if ev.Type == ledger.EvNeedsHuman {
			out = append(out, ev)
		}
	}
	return out
}

// assertOneDesktopFailMark 断言卡上恰 want 条 needs_human，且每条都是 agentd 以
// 「桌面端协调者唤醒失败：」开头的标、reason 不含「准入」（条 100/106/109/174）。
func assertOneDesktopFailMark(t *testing.T, env *ledgerEnv, cardID string, want int) {
	t.Helper()
	marks := desktopNeedsHumanOn(t, env, cardID)
	if len(marks) != want {
		t.Fatalf("卡 %s 应恰 %d 条 needs_human，实得 %d：%v", cardID, want, len(marks), marks)
	}
	for _, m := range marks {
		reason, _ := desktopNeedsHumanReason(m)
		if m.Actor != "agentd" || !strings.HasPrefix(reason, desktopWakeFailReasonPrefix) || strings.Contains(reason, "准入") {
			t.Fatalf("needs_human 形状不对：actor=%q reason=%q", m.Actor, reason)
		}
	}
}

// automationCursorOf 读消费循环自动化游标。
func automationCursorOf(env *ledgerEnv) int64 {
	env.srv.automationMu.Lock()
	defer env.srv.automationMu.Unlock()
	return env.srv.automationCursor
}

// admissionStallOf 复制准入停摆计数表（条 173：桌面端失败不得改它）。
func admissionStallOf(env *ledgerEnv) map[string]int {
	env.srv.automationMu.Lock()
	defer env.srv.automationMu.Unlock()
	return maps.Clone(env.srv.automationStall)
}

// TestB434DesktopOpenerFailureSegment 锁本机 opener 失败（条 109–111）与失败段语义（条 103、
// 104、187、173）：失败 → 信在、标 seen、complete、恰一条 needs_human；失败段自己落的
// needs_human（actor agentd）下一轮不唤醒席位；段内再失败不再落标；成功清段后再失败重新落一条。
func TestB434DesktopOpenerFailureSegment(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	cardID := createCoordCard(t, env)
	bindDesktopSeat(t, env, cardID, "unopened", "local_unopened")
	drainAutomation(t, env)
	stallBefore := admissionStallOf(env)
	consume := func() error {
		_, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
		return err
	}

	spy.err = errors.New("注入：open 失败")
	seq1 := openDecisionSeq(t, env, cardID, "第一问")
	if err := consume(); !desktopUnopened(err) {
		t.Fatalf("opener 失败应报未打开，实得 %v", err)
	}
	if n := len(desktopLettersIn(t, home)); n != 1 {
		t.Fatalf("条 110：opener 失败后信应仍在，实得 %d 封", n)
	}
	if !automationSeenHas(env, seq1) || automationCursorOf(env) < seq1 {
		t.Fatalf("条 111：opener 失败该批应标 seen 且游标越过 seq %d（cursor=%d）", seq1, automationCursorOf(env))
	}
	assertOneDesktopFailMark(t, env, cardID, 1)

	spy.err = nil
	if err := consume(); err != nil { // 本轮只有失败段自己落的 needs_human（actor agentd）
		t.Fatalf("回声轮: %v", err)
	}
	if n := len(desktopLettersIn(t, home)); n != 1 || len(spy.calls()) != 1 {
		t.Fatalf("条 187/111：失败段的 needs_human 不得唤醒、第一批不得重投：信 %d 封、opener %v", n, spy.calls())
	}

	spy.err = errors.New("注入：open 又失败")
	openDecisionSeq(t, env, cardID, "第二问")
	if err := consume(); !desktopUnopened(err) {
		t.Fatalf("段内第二次失败: %v", err)
	}
	assertOneDesktopFailMark(t, env, cardID, 1) // 条 103：同段不再落

	spy.err = nil
	openDecisionSeq(t, env, cardID, "第三问")
	if err := consume(); err != nil {
		t.Fatalf("成功轮: %v", err)
	}
	spy.err = errors.New("注入：恢复后再失败")
	openDecisionSeq(t, env, cardID, "第四问")
	if err := consume(); !desktopUnopened(err) {
		t.Fatalf("新段失败: %v", err)
	}
	assertOneDesktopFailMark(t, env, cardID, 2) // 条 104：成功清段后重新落一条
	if after := admissionStallOf(env); !maps.Equal(after, stallBefore) {
		t.Fatalf("条 173：桌面端失败不得改 automationStall：%v → %v", stallBefore, after)
	}
}

// TestB434DesktopWriteFailureHoldsBatch 锁本机写信失败（条 106–108、103、105 的本机对照、173/174）
// 与 plan §11.3 退避：失败 → 零 opener、不标 seen、游标停在该批之前、恰一条 needs_human；
// 退避窗内不重试；窗口过后仍失败不再落标；信箱恢复后保住的事件进新信并标 seen、清段。
func TestB434DesktopWriteFailureHoldsBatch(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	clock := fixDesktopClock(env)
	cardID := createCoordCard(t, env)
	bindDesktopSeat(t, env, cardID, "unwritable", "local_unwritable")
	drainAutomation(t, env)
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	env.srv.desktopHome = blocker // MkdirAll(<文件>/.handoff/desktop-wake) 必败
	stallBefore := admissionStallOf(env)
	seq := openDecisionSeq(t, env, cardID, "写不进去")
	consume := func() error {
		_, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
		return err
	}

	if err := consume(); !desktopUndelivered(err) {
		t.Fatalf("写信失败应报未投递，实得 %v", err)
	}
	if len(spy.calls()) != 0 {
		t.Fatalf("条 107：写信失败不得调 opener：%v", spy.calls())
	}
	if automationSeenHas(env, seq) || automationCursorOf(env) >= seq {
		t.Fatalf("条 108：写信失败该批不得标 seen、游标不得越过 seq %d（cursor=%d）", seq, automationCursorOf(env))
	}
	assertOneDesktopFailMark(t, env, cardID, 1)

	if err := consume(); err != nil { // 退避窗内：不重试、不报错
		t.Fatalf("退避窗内应静默跳过，实得 %v", err)
	}
	if automationSeenHas(env, seq) {
		t.Fatal("退避窗内不得标 seen")
	}
	clock.Advance(desktopWakeRetryMax)
	if err := consume(); !desktopUndelivered(err) {
		t.Fatalf("窗口过后应重试并再次未投递，实得 %v", err)
	}
	assertOneDesktopFailMark(t, env, cardID, 1) // 条 103

	env.srv.desktopHome = home
	clock.Advance(desktopWakeRetryMax)
	if err := consume(); err != nil {
		t.Fatalf("信箱恢复后应投递成功，实得 %v", err)
	}
	letters := desktopLettersIn(t, home)
	if len(letters) != 1 || !strings.Contains(letters[0], fmt.Sprintf(" seq=%d kind=", seq)) || !automationSeenHas(env, seq) {
		t.Fatalf("条 105（本机对照）：保住的 seq %d 应进新信并标 seen：%q", seq, letters)
	}
	if after := admissionStallOf(env); !maps.Equal(after, stallBefore) {
		t.Fatalf("条 173：%v → %v", stallBefore, after)
	}
	env.srv.automationMu.Lock()
	_, inSegment := env.srv.desktopWakeStall[cardID]
	env.srv.automationMu.Unlock()
	if inSegment {
		t.Fatal("条 104：成功后失败段应清零")
	}
}

// TestB434DesktopFailureClassesExclusive 锁审阅轮 3 建议 1 与条 172：三类本机失败各恰落在
// 「未投递 / 未打开」之一，且 wakeDesktopSeat 自身零 MarkNeedsHuman。
func TestB434DesktopFailureClassesExclusive(t *testing.T) {
	for _, tc := range []struct {
		name         string
		setup        func(env *ledgerEnv, spy *desktopOpenerSpy) string // 返回 CLI 会话 id
		wantUnopened bool
	}{
		{"opener失败", func(_ *ledgerEnv, spy *desktopOpenerSpy) string { spy.err = errors.New("注入"); return "cls-open" }, true},
		{"信箱键不安全", func(*ledgerEnv, *desktopOpenerSpy) string { return "../cls-key" }, false},
		{"信箱不可写", func(env *ledgerEnv, _ *desktopOpenerSpy) string {
			blocker := filepath.Join(t.TempDir(), "file")
			_ = os.WriteFile(blocker, nil, 0o600)
			env.srv.desktopHome = blocker
			return "cls-write"
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, spy, _ := newDesktopSliceEnv(t)
			cardID := createCoordCard(t, env)
			seat := bindDesktopSeat(t, env, cardID, tc.setup(env, spy), "local_cls")
			seq := openDecisionSeq(t, env, cardID, "分类")
			_, err := env.srv.wakeDesktopSeat(context.Background(), cardID, seat, localDesk(t, env, cardID),
				[]keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: cardID}},
				[]proto.LedgerEvent{rawEventAt(t, env, seq)}, "rid-cls")
			if desktopUnopened(err) == desktopUndelivered(err) || desktopUnopened(err) != tc.wantUnopened {
				t.Fatalf("err=%v：unopened=%v undelivered=%v，want unopened=%v（二者恰其一）",
					err, desktopUnopened(err), desktopUndelivered(err), tc.wantUnopened)
			}
			if marks := desktopNeedsHumanOn(t, env, cardID); len(marks) != 0 {
				t.Fatalf("条 172：wakeDesktopSeat 不得自己落 needs_human：%v", marks)
			}
		})
	}
}

// TestB434DesktopWakeSkipsCoordinatorAdmission 锁拆解稿 §2.4 第 4 条：协调者小队名额被占满时，
// 桌面端席位唤醒照常写信（不经 AdmitSeatCarrier、不进准入停摆）。
func TestB434DesktopWakeSkipsCoordinatorAdmission(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	svc := mustScheduling(t, env.srv)
	if _, err := svc.AdmitSeatCarrier("coord", "coord-carrier"); err != nil {
		t.Fatalf("占满协调者名额: %v", err)
	}
	if _, err := svc.AdmitSeatCarrier("coord", "coord-carrier"); !errors.Is(err, scheduling.ErrNoSlot) {
		t.Fatalf("夹具自检：名额应已满（ErrNoSlot），实得 %v", err)
	}
	cardID := createCoordCard(t, env)
	bindDesktopSeat(t, env, cardID, "no-admit", "local_no-admit")
	openDecisionSeq(t, env, cardID, "名额满时的裁决")
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatalf("桌面端唤醒不应受准入影响：%v", err)
	}
	if len(desktopLettersIn(t, home)) != 1 || len(spy.calls()) != 1 {
		t.Fatalf("名额满时桌面端仍应写信并开 opener：opener=%v", spy.calls())
	}
	if stall := admissionStallOf(env); stall[cardID] != 0 {
		t.Fatalf("桌面端唤醒不得进准入停摆：%v", stall)
	}
}

// TestB434DesktopRebindClearsFailureSegment 锁契约 r12 第 4 条末句（plan §14 ⑦）：席位换绑或
// seat_desktop_set 事件流经消费循环时清掉该卡的桌面端失败段——旧段的退避窗与「已落标」计数
// 不被新席位继承。
func TestB434DesktopRebindClearsFailureSegment(t *testing.T) {
	setup := func(t *testing.T) (*ledgerEnv, *desktopTestClock, string, string) {
		env, _, _, _ := newDesktopSliceEnv(t)
		clock := fixDesktopClock(env)
		cardID := createCoordCard(t, env)
		seat := bindDesktopSeat(t, env, cardID, "rebind-old", "local_rebind-old")
		drainAutomation(t, env)
		blocker := filepath.Join(t.TempDir(), "not-a-dir")
		if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		env.srv.desktopHome = blocker
		openDecisionSeq(t, env, cardID, "换绑前写不进去")
		if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); !desktopUndelivered(err) {
			t.Fatalf("夹具：首轮应未投递，实得 %v", err)
		}
		assertOneDesktopFailMark(t, env, cardID, 1)
		return env, clock, cardID, seat
	}
	inSegment := func(env *ledgerEnv, cardID string) bool {
		env.srv.automationMu.Lock()
		defer env.srv.automationMu.Unlock()
		_, ok := env.srv.desktopWakeStall[cardID]
		return ok
	}

	t.Run("seat_desktop_set", func(t *testing.T) {
		env, _, cardID, seat := setup(t)
		host, err := os.Hostname()
		if err != nil {
			t.Fatal(err)
		}
		if err := env.ledger.SetSeatDesktop(cardID, seat, ledger.SeatDesktop{
			Session: "local_rebind-new", Machine: host, Via: proto.SeatDesktopViaManual}); err != nil {
			t.Fatal(err)
		}
		// 时钟未推进：旧段退避窗仍在。段被清 → 本轮立即重试（仍写不进去）并作为新段重新落一条标。
		if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); !desktopUndelivered(err) {
			t.Fatalf("seat_desktop_set 后应立即重试（旧退避窗不继承），实得 %v", err)
		}
		assertOneDesktopFailMark(t, env, cardID, 2)
	})

	t.Run("换绑", func(t *testing.T) {
		env, _, cardID, seat := setup(t)
		next, err := proto.EncodeSeatIdentity("claude", "rebind-next")
		if err != nil {
			t.Fatal(err)
		}
		if err := env.ledger.RebindSeat(cardID, next, proto.SeatSourceBind, seat, ledger.SeatBearing{}); err != nil {
			t.Fatalf("换绑: %v", err)
		}
		if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
			t.Fatalf("换绑后消费: %v", err)
		}
		if inSegment(env, cardID) {
			t.Fatal("换绑事件流经消费循环后，该卡旧失败段应被清掉")
		}
	})

	// review m1：解绑后从空座以 coordinate 坐下只落 driver_seat_bound（无 seat_desktop_set、
	// 无 driver_takeover）。保住的批次随后会经 coordinate 路径唤醒并在成功出口再清一次段，
	// 所以这里以日志 reason 区分「被 driver_seat_bound 清」与「被唤醒成功清」。
	t.Run("解绑后coordinate坐下", func(t *testing.T) {
		env, _, cardID, _ := setup(t)
		var logs bytes.Buffer
		env.srv.log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
		if err := env.ledger.ClearSeat(cardID); err != nil {
			t.Fatal(err)
		}
		coord, err := proto.EncodeSeatIdentity("opencode", "rebind-coord")
		if err != nil {
			t.Fatal(err)
		}
		if err := env.ledger.BindSeat(cardID, coord, proto.SeatSourceCoordinate,
			ledger.SeatBearing{Carrier: "coord-carrier", Machine: "local"}); err != nil {
			t.Fatalf("coordinate 坐下: %v", err)
		}
		_, _, _ = env.srv.consumeAutomationEventsOnce(context.Background())
		const want = "reason=席位换绑或桌面端记录重写（driver_seat_bound）"
		if !strings.Contains(logs.String(), want) {
			t.Fatalf("driver_seat_bound 流经消费循环应清掉旧失败段（日志 %s），实得：\n%s", want, logs.String())
		}
	})

	// review m4：运行锁过期抢占也落 driver_takeover（载荷 from/to 是运行锁持有者），但席位没换——
	// 不得清段：时钟未推进时仍在退避窗内，本轮不重试、不新落标。
	t.Run("运行锁过期抢占不清段", func(t *testing.T) {
		env, _, cardID, _ := setup(t)
		if _, acq, err := env.ledger.AcquireRunLock(cardID, "review", "run:old#1#1", -time.Minute); err != nil || !acq {
			t.Fatalf("取运行锁: acq=%v err=%v", acq, err)
		}
		if _, acq, err := env.ledger.AcquireRunLock(cardID, "review", "run:new#2#2", 5*time.Minute); err != nil || !acq {
			t.Fatalf("抢占运行锁: acq=%v err=%v", acq, err)
		}
		lastEventOfType(t, env, cardID, ledger.EvDriverTakeover) // 夹具自检：确有抢占事件
		if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
			t.Fatalf("运行锁抢占不得清段（应仍在退避窗内静默跳过），实得 %v", err)
		}
		if !inSegment(env, cardID) {
			t.Fatal("运行锁抢占后失败段应仍在")
		}
		assertOneDesktopFailMark(t, env, cardID, 1)
	})
}

// TestB434DesktopWakeRetryDelayTable 锁契约 r12 第 4 条 / §11.3 的退避取值：第 n 次连续「信未
// 落盘」后等 2s·2^(n-1)，封顶 60s（review I-2a：行为测试只用 desktopWakeRetryMax 推进时钟，
// 量级本身无人看着）。
func TestB434DesktopWakeRetryDelayTable(t *testing.T) {
	for _, tc := range []struct {
		failures int
		want     time.Duration
	}{
		{1, 2 * time.Second}, {2, 4 * time.Second}, {5, 32 * time.Second}, {6, time.Minute}, {20, time.Minute},
	} {
		if got := desktopWakeRetryDelay(tc.failures); got != tc.want {
			t.Errorf("desktopWakeRetryDelay(%d) = %v，want %v", tc.failures, got, tc.want)
		}
	}
}
