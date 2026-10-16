// desktopwake_needs_test.go —— B434 G2：分流改接与 needs_human 唤醒（契约 §3.8、§3.8.1；
// 条 66、85–88、177–179、182–188、191–193）。
//
// 职责：从缝 5（resolveWakeCard、消费循环 consumeAutomationEventsOnce）与其唯一映射入口
// automationWakeEvents（消费循环与转交收件共用）锁桌面端席位的分流与 needs_human 排除规则。
// 边界：失败段自身落的 needs_human 不回声（条 187）在 desktopwake_fail_test；夹具来自
// desktopwake_slice_test / _letter_test。
package agentd

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// lastEventOfType 返回卡上最后一条 typ 事件（账本原始事件）。
func lastEventOfType(t *testing.T, env *ledgerEnv, cardID, typ string) proto.LedgerEvent {
	t.Helper()
	evs, err := env.srv.autoLedger.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == typ {
			return evs[i]
		}
	}
	t.Fatalf("卡 %s 无 %s 事件", cardID, typ)
	return proto.LedgerEvent{}
}

// TestB434ResolveWakeCardDesktopSeats 锁条 85–88（契约 §3.8 前两行，P2=A）。V 已落码；本测试
// 首跑即绿，牙由台账 L6 变异 M1/M2/M3 证明。
func TestB434ResolveWakeCardDesktopSeats(t *testing.T) {
	env, _, _, _ := newDesktopSliceEnv(t)
	desk := createCoordCard(t, env)
	bindDesktopSeat(t, env, desk, "rw-desk", "local_rw-desk")
	if got := env.srv.resolveWakeCard(desk); got != desk {
		t.Fatalf("条 85：有桌面端记录的 bind 事件卡应返回自身，实得 %q", got)
	}
	root := createCoordCard(t, env)
	prebindConsumerSession(t, env, root)
	plain := splitCoordChild(t, env, root)
	bindDesktopSeat(t, env, plain, "rw-plain", "")
	if got := env.srv.resolveWakeCard(plain); got != "" {
		t.Fatalf("条 86：无桌面端记录的 bind 事件卡应返回空（不冒泡到 coordinate 父卡 %s），实得 %q", root, got)
	}
	leaf := splitCoordChild(t, env, desk)
	if got := env.srv.resolveWakeCard(leaf); got != desk {
		t.Fatalf("条 87：空座子卡应冒泡到有桌面端记录的 bind 父卡 %s，实得 %q", desk, got)
	}
	grand := splitCoordChild(t, env, plain)
	if got := env.srv.resolveWakeCard(grand); got != root {
		t.Fatalf("条 88：无记录 bind 祖先应跳过、继续上走到 coordinate 祖先 %s，实得 %q", root, got)
	}
}

// TestB434SeatDesktopSetDoesNotWake 锁条 66：seat_desktop_set 经映射入口零唤醒，消费循环零信。
func TestB434SeatDesktopSetDoesNotWake(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	cardID := createCoordCard(t, env)
	bindDesktopSeat(t, env, cardID, "set-cli", "local_set")
	wakes, err := env.srv.automationWakeEvents(lastEventOfType(t, env, cardID, ledger.EvSeatDesktopSet))
	if err != nil || len(wakes) != 0 {
		t.Fatalf("条 66：seat_desktop_set 应零唤醒，实得 %v err=%v", wakes, err)
	}
	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 0 || len(mdFilesUnder(t, home)) != 0 || len(spy.calls()) != 0 {
		t.Fatalf("条 66：processed=%d err=%v files=%v opener=%v", processed, err, mdFilesUnder(t, home), spy.calls())
	}
}

// TestB434NeedsHumanWakesDesktopSeat 锁条 177–179、188、191：人在终端打的标（actor
// cli:<USER>@<host>）→ 映射恰一条 WakeTaskTerminal（Card=事件卡）→ 消费循环恰写一封信，
// 「## 需要人 seq <n>」下一行起是 needs_human 围栏、author=打标者、reason 全文（>400 字、含 ```）。
func TestB434NeedsHumanWakesDesktopSeat(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	cardID := createCoordCard(t, env)
	bindDesktopSeat(t, env, cardID, "needs-cli", "local_needs")
	drainAutomation(t, env)
	reason := "请看 ```make deploy``` 之后" + strings.Repeat("急", 500)
	if err := env.ledger.MarkNeedsHuman(cardID, reason, "cli:sycm@host"); err != nil {
		t.Fatal(err)
	}
	ev := lastEventOfType(t, env, cardID, ledger.EvNeedsHuman)
	wakes, err := env.srv.automationWakeEvents(ev)
	if err != nil || len(wakes) != 1 {
		t.Fatalf("条 177：应恰一条唤醒，实得 %v err=%v", wakes, err)
	}
	if wakes[0].Kind != keystone.WakeTaskTerminal || wakes[0].Card != cardID || wakes[0].Summary != "needs_human: "+reason {
		t.Fatalf("条 177/178：唤醒 = %+v", wakes[0])
	}
	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || processed != 1 {
		t.Fatalf("条 179：processed=%d err=%v", processed, err)
	}
	lines := strings.Split(onlyDesktopLetter(t, home), "\n")
	i := slices.Index(lines, fmt.Sprintf("## 需要人 seq %d", ev.Seq))
	if i < 0 || lines[i+1] != fmt.Sprintf("```handoff-quote author=cli:sycm@host seq=%d kind=needs_human", ev.Seq) {
		t.Fatalf("条 191/269：需要人段形状不对：\n%s", strings.Join(lines, "\n"))
	}
	if body := strings.Join(lines[i+2:i+2+strings.Count(reason, "\n")+1], "\n"); body != escapeDesktopQuoteBody(reason) {
		t.Fatalf("条 191：reason 全文应原样（转义后）在围栏内：%q", body)
	}
	if got := spy.calls(); len(got) != 1 {
		t.Fatalf("opener 应恰一次：%v", got)
	}
}

// TestB434NeedsHumanBubblesToDesktopParent 锁条 177 的祖先情形与 r10 第 9 条：空座子卡上人打的标
// 叫醒桌面端父卡，唤醒 Card 仍是事件卡，来信段标题带子卡号。
func TestB434NeedsHumanBubblesToDesktopParent(t *testing.T) {
	env, _, _, home := newDesktopSliceEnv(t)
	parentID := createCoordCard(t, env)
	bindDesktopSeat(t, env, parentID, "needs-parent", "local_needs-parent")
	childID := splitCoordChild(t, env, parentID)
	drainAutomation(t, env)
	if err := env.ledger.MarkNeedsHuman(childID, "子卡要人", "cli:sycm@host"); err != nil {
		t.Fatal(err)
	}
	ev := lastEventOfType(t, env, childID, ledger.EvNeedsHuman)
	wakes, err := env.srv.automationWakeEvents(ev)
	if err != nil || len(wakes) != 1 || wakes[0].Card != childID {
		t.Fatalf("唤醒应指向事件卡 %s：%v err=%v", childID, wakes, err)
	}
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("## 需要人 seq %d（卡 %s）", ev.Seq, childID); !strings.Contains(onlyDesktopLetter(t, home), want+"\n") {
		t.Fatalf("缺冒泡段标题 %q", want)
	}
}

// TestB434NeedsHumanExclusions 锁条 182–184 与反面对照：系统组件 actor（三个精确值 + node:
// 前缀）与席位自打 → 映射零唤醒、消费循环零信零 opener；裸词人类 actor "test" 照常唤醒
// （防有人把「系统 actor」改成形状判据）。
func TestB434NeedsHumanExclusions(t *testing.T) {
	for _, tc := range []struct {
		name  string
		actor func(seat string) string
		wake  bool
	}{
		{"agentd", func(string) string { return "agentd" }, false},
		{"keystone", func(string) string { return "keystone" }, false},
		{"coordinator", func(string) string { return "coordinator" }, false},
		{"node前缀", func(string) string { return "node:review" }, false},
		{"席位自打", func(seat string) string { return seat }, false},
		{"裸词人类actor", func(string) string { return "test" }, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, _, spy, home := newDesktopSliceEnv(t)
			cardID := createCoordCard(t, env)
			seat := bindDesktopSeat(t, env, cardID, "excl-cli", "local_excl")
			drainAutomation(t, env)
			if err := env.ledger.MarkNeedsHuman(cardID, "排除规则", tc.actor(seat)); err != nil {
				t.Fatal(err)
			}
			wakes, err := env.srv.automationWakeEvents(lastEventOfType(t, env, cardID, ledger.EvNeedsHuman))
			if err != nil || (len(wakes) == 1) != tc.wake {
				t.Fatalf("actor %q：唤醒 %v err=%v，want wake=%v", tc.actor(seat), wakes, err, tc.wake)
			}
			if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := len(desktopLettersIn(t, home)); (got == 1) != tc.wake || len(spy.calls()) != got {
				t.Fatalf("actor %q：信 %d 封、opener %v，want wake=%v", tc.actor(seat), got, spy.calls(), tc.wake)
			}
		})
	}
	// review I-1：规则 4「协调者自打」比的是**被叫醒席位**（resolveWakeCard 的目标卡）的身份，
	// 不是事件卡自己的席位——父卡桌面端席位在空座子卡上打标，事件卡席位为空，目标是父卡。
	t.Run("父卡席位在空座子卡上自打", func(t *testing.T) {
		env, _, spy, home := newDesktopSliceEnv(t)
		parentID := createCoordCard(t, env)
		seat := bindDesktopSeat(t, env, parentID, "excl-parent", "local_excl-parent")
		childID := splitCoordChild(t, env, parentID)
		drainAutomation(t, env)
		if err := env.ledger.MarkNeedsHuman(childID, "父卡协调者在子卡上打的标", seat); err != nil {
			t.Fatal(err)
		}
		ev := lastEventOfType(t, env, childID, ledger.EvNeedsHuman)
		if wakes, err := env.srv.automationWakeEvents(ev); err != nil || len(wakes) != 0 {
			t.Fatalf("父卡席位 %s 在子卡 %s 自打应零唤醒，实得 %v err=%v", seat, childID, wakes, err)
		}
		if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if n := len(desktopLettersIn(t, home)); n != 0 || len(spy.calls()) != 0 {
			t.Fatalf("父卡席位自打不得写信/开 opener：信 %d 封、opener %v", n, spy.calls())
		}
	})
}

// TestB434NeedsHumanNonDesktopSeatsStaySilent 锁条 185、186、192：coordinate 席位与无记录 bind
// 席位上人打的标零唤醒；纯函数 automationWakeEvent 对 needs_human 恒不唤醒。
func TestB434NeedsHumanNonDesktopSeatsStaySilent(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	coordCard := createCoordCard(t, env)
	prebindConsumerSession(t, env, coordCard)
	plainCard := createCoordCard(t, env)
	bindDesktopSeat(t, env, plainCard, "plain-cli", "")
	drainAutomation(t, env)
	for _, card := range []string{coordCard, plainCard} {
		if err := env.ledger.MarkNeedsHuman(card, "人在终端打的标", "cli:sycm@host"); err != nil {
			t.Fatal(err)
		}
		ev := lastEventOfType(t, env, card, ledger.EvNeedsHuman)
		if wakes, err := env.srv.automationWakeEvents(ev); err != nil || len(wakes) != 0 {
			t.Fatalf("条 185/186：卡 %s 应零唤醒，实得 %v err=%v", card, wakes, err)
		}
		if _, yes, _ := automationWakeEvent(ev); yes {
			t.Fatal("条 192：纯函数 automationWakeEvent 对 needs_human 应恒不唤醒")
		}
	}
	processed, _, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if _, resumes, _ := runner.snapshot(); err != nil || processed != 0 || len(resumes) != 0 {
		t.Fatalf("processed=%d err=%v resumes=%v", processed, err, resumes)
	}
}

// TestB434NeedsClearedDoesNotWakeDesktop 锁条 193：桌面端席位上人清标（needs_cleared）零唤醒。
func TestB434NeedsClearedDoesNotWakeDesktop(t *testing.T) {
	env, _, spy, home := newDesktopSliceEnv(t)
	cardID := createCoordCard(t, env)
	bindDesktopSeat(t, env, cardID, "clear-cli", "local_clear")
	if err := env.ledger.MarkNeedsHuman(cardID, "系统先打", "agentd"); err != nil {
		t.Fatal(err)
	}
	drainAutomation(t, env)
	if err := env.ledger.ClearNeedsHuman(cardID, "cli:sycm@host"); err != nil {
		t.Fatal(err)
	}
	if wakes, err := env.srv.automationWakeEvents(lastEventOfType(t, env, cardID, ledger.EvNeedsCleared)); err != nil || len(wakes) != 0 {
		t.Fatalf("条 193：needs_cleared 应零唤醒，实得 %v err=%v", wakes, err)
	}
	if _, _, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(desktopLettersIn(t, home)) != 0 || len(spy.calls()) != 0 {
		t.Fatal("条 193：清标不得写信/开 opener")
	}
}
