package orchestration

// b452_cardless_prompt_test.go —— V2 执行机侧：CLI 无卡渲染出的提示词经真实 Manager.Dispatch 原样进
// StartReq.PlanContent（模板正文 + 「## 本次任务」），只读形态再追加「## 产物目录」（Q7 乙）。

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB452CardlessPromptReachesPlanContent(t *testing.T) {
	e := newB452Env(t, nil)
	prompt := ledgerstep.RenderCardlessPrompt("你是只读调查员 {{CARD}}。", "查 Foo 的调用方")
	task, err := e.m.Dispatch(context.Background(), DispatchReq{ProjectID: e.pid, Prompt: prompt,
		Mode: proto.TaskModeReadonly})
	if err != nil {
		t.Fatalf("派发: %v", err)
	}
	t.Cleanup(func() { _, _ = e.m.Stop(context.Background(), task.ID) })
	got := e.rec.last().PlanContent
	if !strings.HasPrefix(got, "你是只读调查员 。\n\n## 本次任务\n\n查 Foo 的调用方") {
		t.Fatalf("PlanContent 应以无卡渲染结果开头，得：\n%s", got)
	}
	if !strings.Contains(got, "## 产物目录") || !strings.Contains(got, TaskArtifactsDir(e.m.cfg.DataDir, task.ID)) {
		t.Fatalf("只读形态 PlanContent 应追加产物目录小节，得：\n%s", got)
	}
}
