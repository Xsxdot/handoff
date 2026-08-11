package opencode

import (
	"strings"
	"testing"
)

func TestRenderQuestionTicketSingleQuestion(t *testing.T) {
	got := renderQuestionTicket([]QuestionInfo{{
		Question: "回合边界判据用哪个信号？",
		Header:   "回合边界",
		Options: []QuestionOption{
			{Label: "照此实现", Description: "加 Finish 字段"},
			{Label: "空值按保守处理", Description: "宁可不补也不误判"},
		},
	}})
	for _, want := range []string{
		"回合边界判据用哪个信号？",
		"1.1 照此实现 — 加 Finish 字段",
		"1.2 空值按保守处理 — 宁可不补也不误判",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染结果缺 %q\n实际:\n%s", want, got)
		}
	}
}

func TestRenderQuestionTicketMarksMultipleAndCustom(t *testing.T) {
	got := renderQuestionTicket([]QuestionInfo{{
		Question: "选哪些？", Multiple: true, Custom: true,
		Options: []QuestionOption{{Label: "A", Description: "甲"}},
	}})
	if !strings.Contains(got, "可多选") {
		t.Errorf("multiple 未标注:\n%s", got)
	}
	if !strings.Contains(got, "可自定义") {
		t.Errorf("custom 未标注:\n%s", got)
	}
}

func TestRenderQuestionTicketNumbersAcrossQuestions(t *testing.T) {
	got := renderQuestionTicket([]QuestionInfo{
		{Question: "第一问", Options: []QuestionOption{{Label: "A"}}},
		{Question: "第二问", Options: []QuestionOption{{Label: "B"}, {Label: "C"}}},
	})
	for _, want := range []string{"问题 1", "1.1 A", "问题 2", "2.1 B", "2.2 C"} {
		if !strings.Contains(got, want) {
			t.Errorf("渲染结果缺 %q\n实际:\n%s", want, got)
		}
	}
}

func TestRenderQuestionTicketNoOptionsStillReadable(t *testing.T) {
	got := renderQuestionTicket([]QuestionInfo{{Question: "开放问题，随便答"}})
	if !strings.Contains(got, "开放问题，随便答") {
		t.Errorf("无选项时问题正文丢失:\n%s", got)
	}
	if !strings.Contains(got, "直接作答") {
		t.Errorf("无选项时应提示直接作答:\n%s", got)
	}
}

func TestMapQuestionAskedEmitsTicketAndKeepsTurn(t *testing.T) {
	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	// 回合里已经累积了文本：提问不结束回合，这份缓冲必须原样保留
	r.turnOrder = []string{"k1"}
	r.partSeen = map[string]string{"k1": "已经写了一些正文"}

	props := []byte(`{"id":"req_1","sessionID":"ses_a","questions":[
		{"question":"选哪个？","header":"选型",
		 "options":[{"label":"A","description":"甲"},{"label":"B","description":"乙"}]}]}`)
	a.mapQuestionAsked(r, props)

	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" {
		t.Fatalf("事件 = %+v ok=%v，期望一条 question", ev, ok)
	}
	if !strings.Contains(ev.Text, "1.1 A") {
		t.Errorf("工单缺选项编号:\n%s", ev.Text)
	}
	if r.pendingQuestionID != "req_1" {
		t.Errorf("pendingQuestionID = %q，期望 req_1", r.pendingQuestionID)
	}
	// 回合未结束：缓冲与水位都不能动
	if len(r.turnOrder) != 1 || r.partSeen["k1"] != "已经写了一些正文" {
		t.Error("提问不结束回合，回合缓冲不得被清空")
	}
}

func TestMapQuestionAskedDedupesByRequestID(t *testing.T) {
	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	props := []byte(`{"id":"req_1","sessionID":"ses_a","questions":[{"question":"Q"}]}`)

	a.mapQuestionAsked(r, props)
	a.mapQuestionAsked(r, props) // SSE 重连重放同一事件

	if _, ok := drainOne(r); !ok {
		t.Fatal("第一次应当出单")
	}
	if ev, ok := drainOne(r); ok {
		t.Fatalf("重放必须去重，却又收到 %+v", ev)
	}
}

func TestMapQuestionAskedEmptyQuestionsStillWakesReviewer(t *testing.T) {
	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"

	a.mapQuestionAsked(r, []byte(`{"id":"req_1","sessionID":"ses_a","questions":[]}`))

	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" {
		t.Fatalf("事件 = %+v ok=%v，期望 question（不得静默丢弃）", ev, ok)
	}
	if !strings.Contains(ev.Text, "req_1") {
		t.Errorf("降级文本应带请求 id 供 attach 排查:\n%s", ev.Text)
	}
}

func TestQuestionAskedIsTaskScoped(t *testing.T) {
	if !taskScopedEvents["question.asked"] {
		t.Error("question.asked 必须是任务级事件：它直接产出面向审核者的工单")
	}
}
