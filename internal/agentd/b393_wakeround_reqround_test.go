// b393_wakeround_reqround_test.go —— B393 review-4：队列轮次键按请求身份 + 成功收尾清轮。
//
// 职责：钉住 (1) 同卡两个不同 IgnitionRequest（implement→review）各自成轮，
// 后一请求的 fail/start 不被前一轮键吞掉；(2) 成功收尾后 clearWakeQueueRound
// 摘除轮身份，下一条 IgnitionRequest 开新一轮（不是复用旧 roundID）。
// 缝：agentd.Server.drainIgnitionRequest（retain/clear 的唯一生产调用点）。
// 边界：只观察账本 wake_round 注释的 phase 计数与 round 键；不复制 keystone 规则。
package agentd

import (
	"context"
	"testing"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// readWakeRoundKeys 返回该卡全部 wake_round 注释的 dedupe_key（保留顺序）。
func readWakeRoundKeys(t *testing.T, env *ledgerEnv, cardID string) []string {
	t.Helper()
	var keys []string
	for _, row := range readWakeRoundCommentRows(t, env, cardID) {
		keys = append(keys, row.Key)
	}
	return keys
}

// TestB393WakeRoundQueueGroupsPerRequest 锁 review-4 major：队列轮次键按
// IgnitionRequest 身份分组——同卡两个不同请求各自成轮，后一请求的 fail 不得
// 被前一轮键吞掉（plan r2 / server.go 注释契约：同一 IgnitionRequest 为一轮）。
//
// 红（修复前）：键按 card 分组 → 两请求共享 roundID → 第二行 fail 被
// EnsureComment 幂等吞掉，只落 1 行（探针：implement→review 失败只 1 行 fail）。
// 绿：两行 fail；两行 start 的 dedupe_key 互异（roundID 不同）。
// 变异：把键改回 req.Card → 复红（只 1 行 fail）。
func TestB393WakeRoundQueueGroupsPerRequest(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)

	failRunner := &fallbackConsumerRunner{failResume: true, failLaunch: true}
	env.srv.SetKeystone(keystone.New(failRunner, &fakeCoordNarrator{}, env.srv.autoLedger, attachLocator{}))

	reqImplement := scheduling.IgnitionRequest{
		Card: cardID, Squad: "coord", Node: "implement",
		Actor: "test", Ready: true,
	}
	reqReview := scheduling.IgnitionRequest{
		Card: cardID, Squad: "coord", Node: "review",
		Actor: "test", Ready: true,
	}
	if err := env.srv.drainIgnitionRequest(context.Background(), reqImplement); err == nil {
		t.Fatalf("implement 请求唤醒必须返回错误")
	}
	if err := env.srv.drainIgnitionRequest(context.Background(), reqReview); err == nil {
		t.Fatalf("review 请求唤醒必须返回错误")
	}

	phases := countWakeRoundPhases(readWakeRoundComments(t, env, cardID))
	if phases["fail"] != 2 {
		t.Fatalf("同卡两个不同请求应各落一行 phase=fail（键按请求身份分组），实得 %d", phases["fail"])
	}
	startKeys := map[string]bool{}
	startCount := 0
	for _, row := range readWakeRoundCommentRows(t, env, cardID) {
		if row.Round.Phase == "start" {
			startCount++
			startKeys[row.Key] = true
		}
	}
	if startCount != 2 || len(startKeys) != 2 {
		t.Fatalf("两请求应各得一行 start 且键互异，实得 start=%d 唯一键=%d keys=%v",
			startCount, len(startKeys), readWakeRoundKeys(t, env, cardID))
	}
}

// TestB393WakeQueueRoundClearsAfterSuccess 锁 review-4 minor：成功收尾后
// clearWakeQueueRound 摘除轮身份，下一条 IgnitionRequest 开新一轮。
//
// 红（删 clear 调用）：第二次出队复用同一 roundID → start/end 键幂等吞掉 →
// 只各 1 行（既有全部队列/自动化测试对删该调用仍绿——本锁补上这一刀）。
// 绿：两次成功出队各得一组 start/end（round 键互异，各 2 行）。
// 变异：删 clearWakeQueueRound 调用 → 复红。
func TestB393WakeQueueRoundClearsAfterSuccess(t *testing.T) {
	env, _ := newNoPTYAutomationEnv(t)
	cardID := createCoordCard(t, env)
	prebindConsumerSession(t, env, cardID)
	// 成功路径：queueTraceRunner.Resume 返回成功；startCardStep 的节点解析
	// 失败不影响本锁——clear 在 wake 成功后、startCardStep 之前已执行。
	env.srv.runStepFn = func(context.Context, *ledgerstep.StepRunner, string, string) {}

	req := scheduling.IgnitionRequest{
		Card: cardID, Squad: "coord", Node: "implement",
		Actor: "test", Ready: true,
	}
	// 第一次：wake 成功 → clear 执行。startCardStep 可能失败，忽略其返回。
	_ = env.srv.drainIgnitionRequest(context.Background(), req)
	// 第二次：若 clear 未执行则复用旧 roundID，start/end 被幂等吞掉。
	_ = env.srv.drainIgnitionRequest(context.Background(), req)

	phases := countWakeRoundPhases(readWakeRoundComments(t, env, cardID))
	if phases["start"] != 2 {
		t.Fatalf("成功收尾后下一条 IgnitionRequest 应开新一轮（两行 start），实得 %d（keys=%v）",
			phases["start"], readWakeRoundKeys(t, env, cardID))
	}
	if phases["end"] != 2 {
		t.Fatalf("成功收尾后下一条 IgnitionRequest 应开新一轮（两行 end），实得 %d（keys=%v）",
			phases["end"], readWakeRoundKeys(t, env, cardID))
	}
}
