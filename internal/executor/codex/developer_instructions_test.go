package codex_test

import (
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor/codex"
	"github.com/Xsxdot/handoff/internal/executor/turn"
)

func TestThreadStartCarriesDeveloperInstructions(t *testing.T) {
	params := codex.ThreadStartParamsForTest("cwd", "gpt-5.6-luna",
		turn.ProtocolRules+"\n\n"+"# 执行纪律\n单上下文版内容")
	di, ok := params["developerInstructions"].(string)
	if !ok {
		t.Fatal("params 里没有 developerInstructions")
	}
	if !strings.Contains(di, "提问纪律") {
		t.Error("协议铁律没进 developerInstructions")
	}
	if !strings.Contains(di, "单上下文版内容") {
		t.Error("纪律块没进 developerInstructions")
	}
}

func TestThreadResumeCarriesDeveloperInstructions(t *testing.T) {
	params := codex.ThreadResumeParamsForTest("th-1", "/repo", "指令原文")
	if params["developerInstructions"] != "指令原文" {
		t.Fatalf("thread/resume 漏传 developerInstructions，实得 %v", params["developerInstructions"])
	}
	for _, k := range []string{"threadId", "cwd", "approvalPolicy", "approvalsReviewer"} {
		if _, ok := params[k]; !ok {
			t.Errorf("thread/resume 丢了 %s", k)
		}
	}
}
