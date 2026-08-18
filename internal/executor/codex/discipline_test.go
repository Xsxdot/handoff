package codex_test

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/codex"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestStartInjectsDisciplineIntoPrompt(t *testing.T) {
	got := codex.RenderStartPromptForTest(executor.StartReq{
		Task:        proto.Task{ID: "T1"},
		PlanContent: "计划正文",
		Discipline:  "# 执行纪律\n单上下文版内容",
	})
	if !strings.Contains(got, "单上下文版内容") {
		t.Fatalf("纪律块没进 prompt，实得：%.120s", got)
	}
	if !strings.Contains(got, "计划正文") {
		t.Error("计划正文丢了")
	}
}
