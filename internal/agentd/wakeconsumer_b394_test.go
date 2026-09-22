// wakeconsumer_b394_test.go —— B394 回声自激的缝级回归回路。
//
// 职责：锁住「清标（needs_cleared）不是唤醒源」——清标是注意力平面的状态翻转，
// 与 needs_human 同族；若当唤醒源，协调者自己的账务动作会把它自己叫醒（清→重打乒乓）。
// 缝：Server.consumeAutomationEventsOnce（真实 ledger Facade + 真实 keystone + 假 runner），
// 不直调 automationWakeEvent。
// 边界：不复制 keystone briefing/重建规则；不测 card wait 展示面（另有既有测试）。
package agentd

import (
	"context"
	"testing"
)

// TestB394ClearNeedsDoesNotWake 锁 B394 冻结条目 1：needs_cleared 不唤醒协调者。
func TestB394ClearNeedsDoesNotWake(t *testing.T) {
	env, runner := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	// 前置：needs_human 不唤醒（B389 已收口）。同时证明本回路不是「什么都没醒」的假绿——
	// 后面 decision_opened 支（T4）会给正例。
	if err := env.ledger.MarkNeedsHuman(cardID, "协调者回合失败兜底", "keystone"); err != nil {
		t.Fatal(err)
	}
	if processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background()); err != nil || escalated || processed != 0 {
		t.Fatalf("needs_human 不应唤醒：processed=%d escalated=%v err=%v", processed, escalated, err)
	}

	// 被测行为：清标（actor 用协调者 CLI 身份，与真机 cli:root@handoff 同串）不得起新唤醒轮。
	if err := env.ledger.ClearNeedsHuman(cardID, "cli:root@handoff"); err != nil {
		t.Fatal(err)
	}
	processed, escalated, err := env.srv.consumeAutomationEventsOnce(context.Background())
	if err != nil || escalated {
		t.Fatalf("清标消费轮失败：processed=%d escalated=%v err=%v", processed, escalated, err)
	}
	if processed != 0 {
		t.Fatalf("清标不得唤醒协调者（回声自激）：processed=%d", processed)
	}
	if _, resumes, _ := runner.snapshot(); len(resumes) != 0 {
		t.Fatalf("清标不得起 Resume 回合（回声自激）：resumes=%v", resumes)
	}
}
