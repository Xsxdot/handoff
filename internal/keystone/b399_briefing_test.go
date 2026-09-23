// b399_briefing_test.go —— B399 r2 §3③：简报只带增量，不指示模型「先读卡」。
//
// 职责：钉住 Wake 送入 Runner 的 prompt 只含卡最小指针（卡号/标题/基线）+ 本轮事件，
// 不含「读卡」类开场指令，也不塞入未在本轮事件里的旧上下文。
// 缝：keystone.Service.Wake（生产入口；prompt 观察点是 fakeRunner.resumes）。
// 边界：只断言 prompt 文本，不复制 agentd 回合规则。
package keystone_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
)

// TestB399BriefingCarriesIncrementOnly 锁简报瘦身。
//
// 红（当前 HEAD）：简报首句含「醒来第一件事：读卡…」、末句含「点火前看上一节点产出」。
// 绿（T2.3）：两串消失；卡指针与本轮事件保留。
// 变异：把首句改回「醒来第一件事：读卡…」→ 复红。
func TestB399BriefingCarriesIncrementOnly(t *testing.T) {
	svc, cardID, runner := resumeClassSeed(t)

	if _, err := svc.Wake(context.Background(), cardID, []keystone.WakeEvent{
		{Kind: keystone.WakeTaskTerminal, Card: cardID, Summary: "本轮增量事件"},
	}, keysclient.SessionSpec{CLI: "opencode", HomeDir: "/h", Workdir: "/w"}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	if len(runner.resumes) != 1 {
		t.Fatalf("应恰一次 Resume，实得 %d", len(runner.resumes))
	}
	got := runner.resumes[0]
	for _, banned := range []string{"读卡", "查依赖", "醒来第一件事", "点火前看上一节点产出"} {
		if strings.Contains(got, banned) {
			t.Fatalf("简报不得含开场指令 %q:\n%s", banned, got)
		}
	}
	for _, want := range []string{"- 卡号：" + cardID, "- 标题：", "- 有效基线分支：", "本轮增量事件"} {
		if !strings.Contains(got, want) {
			t.Fatalf("简报缺 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "旧事件不应出现") {
		t.Fatalf("简报不得塞入未在本轮事件里的旧上下文:\n%s", got)
	}
}
