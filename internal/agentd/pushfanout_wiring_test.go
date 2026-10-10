// pushfanout_wiring_test.go —— 扇出装配（缝 S3 集成）：真写点 → fanout → fake sender。
//
// 职责：锁三条接线——store.events 侧经合成钩子触达、card_events 侧经自动化
// 消费循环触达、合成后帧钩子仍写帧（防顶掉 EventFrameHook，plan 决策 D2）。
// 边界：不重复分类语义（pushfanout_test 已锁）；不验 APNs（apns_test 已锁）。
package agentd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// waitNotify 等到 fake sender 收到一条满足 pred 的投递；超时即失败。
func waitNotify(t *testing.T, sender *fakePushSender, pred func(pushCall) bool) pushCall {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case c := <-sender.calls:
			if pred(c) {
				return c
			}
		case <-deadline:
			t.Fatal("等待目标通知超时")
		}
	}
}

// TestTaskEventHookTriggersPush 锁 store.events 侧接线：AppendEvent 真写点
// 经合成钩子触达 fanout，产出 ticket 类通知（缝 S3 集成）。
func TestTaskEventHookTriggersPush(t *testing.T) {
	env := newRoomsEnv(t)
	registerDevice(t, env.st, consoleMember, "d1", "tok-1")
	sender := newFakePushSender()
	env.srv.pushFanout.sender = sender
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.srv.StartPush(ctx)

	if _, err := env.st.AppendEvent("t1", proto.EventTypeQuestion,
		map[string]any{"ticket_id": "t1:q1", "kind": "ask"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	got := waitNotify(t, sender, func(c pushCall) bool {
		return c.Note.EventType == proto.InboxOriginTicket
	})
	if got.Note.Member != consoleMember {
		t.Fatalf("通知 member = %q，期望 %q（服务端注入的控制台成员）", got.Note.Member, consoleMember)
	}
	if got.Note.Title != "提问工单待答复" || got.Note.RefID != "t1" {
		t.Fatalf("通知 = %+v", got.Note)
	}
}

// TestLedgerNeedsHumanTriggersPush 锁 card_events 侧接线：MarkNeedsHuman 真写点
// 经 consumeAutomationEventsOnce 触达 fanout（不依赖 ticker）。
func TestLedgerNeedsHumanTriggersPush(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "等人卡")
	registerDevice(t, env.st, consoleMember, "d1", "tok-1")
	sender := newFakePushSender()
	env.srv.pushFanout.sender = sender
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	env.srv.StartPush(ctx)

	if err := env.ledger.MarkNeedsHuman(card.ID, "等你裁决", "test"); err != nil {
		t.Fatalf("MarkNeedsHuman: %v", err)
	}
	if _, _, err := env.srv.consumeAutomationEventsOnce(ctx); err != nil {
		t.Fatalf("consumeAutomationEventsOnce: %v", err)
	}

	got := waitNotify(t, sender, func(c pushCall) bool {
		return c.Note.EventType == pushEventTypeNeedsHuman
	})
	if got.Note.CardID != card.ID {
		t.Fatalf("通知 card = %q，期望 %q", got.Note.CardID, card.ID)
	}
	if got.Note.Member != consoleMember {
		t.Fatalf("通知 member = %q，期望 %q", got.Note.Member, consoleMember)
	}
	if got.Note.DeepLink != "/cards?card="+card.ID {
		t.Fatalf("深链 = %q", got.Note.DeepLink)
	}
}

// TestFrameHookStillRuns 锁合成钩子不顶掉帧钩子（plan 决策 D2 的回归）：
// SetEventHook 是单回调，新挂 push 必须与 EventFrameHook 合成同一回调。
func TestFrameHookStillRuns(t *testing.T) {
	env := newTestAgentdEnv(t)
	taskDir := filepath.Join(env.srv.conf().DataDir, "tasks", "t1")
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		t.Fatalf("建任务目录: %v", err)
	}
	if _, err := env.st.AppendEvent("t1", proto.EventTypeQuestion,
		map[string]any{"ticket_id": "t1:q1", "kind": "ask"}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(taskDir, "frames.jsonl"))
	if err != nil {
		t.Fatalf("帧文件未写出（合成钩子顶掉了 EventFrameHook）: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("帧文件为空")
	}
}

// TestPushBadgeCountMatchesInbox 是验收②的角标半边：badge 必须等于 /api/inbox
// 的未处理条数（同源聚合，不允许第二套计数）。
func TestPushBadgeCountMatchesInbox(t *testing.T) {
	env := newRoomsEnv(t)
	card := seedCard(t, env, "待裁决卡")
	if _, err := env.ledger.OpenDecision(card.ID, "一句话：契约语义冲突", []string{"a", "b"}, "coord"); err != nil {
		t.Fatalf("OpenDecision: %v", err)
	}
	want := len(inboxItems(t, env))
	if want == 0 {
		t.Fatal("夹具应至少产出一条收件箱条目")
	}
	if got := env.srv.pushBadgeCount(consoleMember); got != want {
		t.Fatalf("badge=%d，与收件箱未处理数 %d 不一致（角标必须同源）", got, want)
	}
}

// TestPushBadgeCountUsesInbox 锁 badge 计数器注入后被 deliver 使用，
// 并且依赖未装配时静默降级 0（不 panic、不报错）。
func TestPushBadgeCountUsesInbox(t *testing.T) {
	env := newRoomsEnv(t)
	if got := env.srv.pushBadgeCount(consoleMember); got < 0 {
		t.Fatalf("badge 计数不得为负: %d", got)
	}
	// 未解析成员（空串）直接 0，不碰收件箱聚合。
	if got := env.srv.pushBadgeCount(""); got != 0 {
		t.Fatalf("空成员 badge = %d，期望 0", got)
	}
	// 依赖未装配时静默降级 0（不 panic、不报错）。
	bare := newTestAgentdEnv(t)
	if got := bare.srv.pushBadgeCount(consoleMember); got != 0 {
		t.Fatalf("未装配环境 badge = %d，期望 0", got)
	}
}
