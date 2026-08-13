package grok

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
)

func TestAskQuestionTextRendersQuestionsAndOptions(t *testing.T) {
	// 来自本机 grok 1.0.0 实测报文（spec §4.2.3）
	params := json.RawMessage(`{"sessionId":"s","toolCallId":"c9",` +
		`"questions":[{"question":"这个功能用哪种语言实现？","options":[` +
		`{"label":"Go","description":"用 Go 实现该功能"},` +
		`{"label":"Rust","description":"用 Rust 实现该功能"}],"multiSelect":null}],"mode":"default"}`)

	got := askQuestionText(params)
	for _, want := range []string{"这个功能用哪种语言实现？", "1) Go", "2) Rust", "用 Rust 实现该功能"} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染文本缺少 %q，实得:\n%s", want, got)
		}
	}
	if strings.HasSuffix(got, "\n") {
		t.Errorf("尾部换行未清理: %q", got)
	}
}

func TestAskQuestionTextEmptyOnGarbage(t *testing.T) {
	for name, in := range map[string]string{
		"非 JSON":      `not json`,
		"缺 questions": `{"sessionId":"s"}`,
		"空 questions": `{"questions":[]}`,
	} {
		if got := askQuestionText(json.RawMessage(in)); got != "" {
			t.Errorf("%s: 应返回空串，实得 %q", name, got)
		}
	}
}

// TestFinishTurnEmptyTextEmitsFailedResult 兜底分支的空文本守卫。
//
// 旧实现在无新提交时 emit question 携带回合文本，文本为空时产出的是一张**空工单**
// ——协调者收到一个没有内容的问题，除了瞎猜什么也做不了。零文本是故障，按故障报。
func TestFinishTurnEmptyTextEmitsFailedResult(t *testing.T) {
	a := New(nil)
	r := &runState{taskID: "t1", sessionID: "sess-1",
		repoPath: t.TempDir(), // 非 git 目录：hasNew=false，走进兜底分支
		evCh:     make(chan executor.AdapterEvent, 8), acc: newTurnAccumulator(),
		pending: map[string]pendingPerm{}}
	a.finishTurn(r, ACPResult{Result: json.RawMessage(`{"stopReason":"end_turn"}`)})

	select {
	case ev := <-r.evCh:
		if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
			t.Fatalf("零文本且无新提交应产出失败结果，实际 %s %+v", ev.Type, ev.Result)
		}
		if ev.Result.FailReason == "" {
			t.Fatalf("FailReason 必须写清现场，否则协调者不知道发生了什么")
		}
	default:
		t.Fatalf("零文本回合应产出事件")
	}
}
