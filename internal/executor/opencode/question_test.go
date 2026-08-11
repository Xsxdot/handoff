package opencode

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestParseQuestionAnswersByNumber(t *testing.T) {
	qs := []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}}}}
	got, err := parseQuestionAnswers(qs, "1.2")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 1 || len(got[0]) != 1 || got[0][0] != "乙" {
		t.Fatalf("got = %v，期望 [[乙]]", got)
	}
}

func TestParseQuestionAnswersSingleQuestionBareNumber(t *testing.T) {
	qs := []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}}}}
	got, err := parseQuestionAnswers(qs, "2")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got[0][0] != "乙" {
		t.Fatalf("got = %v，期望 [[乙]]（单问允许省略问号）", got)
	}
}

func TestParseQuestionAnswersByLabel(t *testing.T) {
	qs := []QuestionInfo{{Options: []QuestionOption{{Label: "照此实现"}, {Label: "保守处理"}}}}
	got, err := parseQuestionAnswers(qs, "  保守处理 ")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if got[0][0] != "保守处理" {
		t.Fatalf("got = %v，期望 [[保守处理]]", got)
	}
}

func TestParseQuestionAnswersCustomPassthrough(t *testing.T) {
	qs := []QuestionInfo{{Custom: true, Options: []QuestionOption{{Label: "甲"}}}}
	got, err := parseQuestionAnswers(qs, "我要第三种做法")
	if err != nil {
		t.Fatalf("custom=true 应当透传自由文本: %v", err)
	}
	if got[0][0] != "我要第三种做法" {
		t.Fatalf("got = %v，期望原文透传", got)
	}
}

func TestParseQuestionAnswersRejectsUnmatchedWhenNoCustom(t *testing.T) {
	qs := []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}}}}
	if _, err := parseQuestionAnswers(qs, "不存在的答案"); err == nil {
		t.Fatal("custom=false 且不匹配时必须报错重问，不许猜")
	}
}

func TestParseQuestionAnswersMultiSelect(t *testing.T) {
	qs := []QuestionInfo{{Multiple: true,
		Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}, {Label: "丙"}}}}
	got, err := parseQuestionAnswers(qs, "1.1, 1.3")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got[0]) != 2 || got[0][0] != "甲" || got[0][1] != "丙" {
		t.Fatalf("got = %v，期望 [[甲 丙]]", got)
	}
}

func TestParseQuestionAnswersMultiQuestion(t *testing.T) {
	qs := []QuestionInfo{
		{Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}}},
		{Options: []QuestionOption{{Label: "A"}, {Label: "B"}}},
	}
	got, err := parseQuestionAnswers(qs, "1.2; 2.1")
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(got) != 2 || got[0][0] != "乙" || got[1][0] != "A" {
		t.Fatalf("got = %v，期望 [[乙] [A]]", got)
	}
}

func TestParseQuestionAnswersCountMismatchRejected(t *testing.T) {
	qs := []QuestionInfo{
		{Options: []QuestionOption{{Label: "甲"}}},
		{Options: []QuestionOption{{Label: "A"}}},
	}
	if _, err := parseQuestionAnswers(qs, "1.1"); err == nil {
		t.Fatal("两问只给一答必须报错重问")
	}
}

func TestSendRoutesToQuestionReplyWhenPending(t *testing.T) {
	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")
	r.pendingQuestionID = "req_1"
	r.pendingQuestions = []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}, {Label: "乙"}}}}

	if err := a.Send(context.Background(), "task-1", "1.2"); err != nil {
		t.Fatalf("Send 返回错误: %v", err)
	}
	if gotPath != "/question/req_1/reply" {
		t.Fatalf("path = %q，期望打到 reply 端点而不是 prompt", gotPath)
	}
	if !strings.Contains(gotBody, "乙") {
		t.Errorf("body = %q，期望含折算后的 label 乙", gotBody)
	}
	if r.pendingQuestionID != "" {
		t.Error("应答成功后必须清掉挂起请求")
	}
}

func TestSendFallsBackToPromptWhenNoPendingQuestion(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")

	if err := a.Send(context.Background(), "task-1", "继续干"); err != nil {
		t.Fatalf("Send 返回错误: %v", err)
	}
	if !strings.Contains(gotPath, "/session/ses_a/") {
		t.Fatalf("path = %q，期望走 session prompt 端点而非 question", gotPath)
	}
	if strings.Contains(gotPath, "/question/") {
		t.Fatalf("无挂起提问时不得打到 question 端点，实际 %q", gotPath)
	}
}

func TestSendUnparsableAnswerRepromptsAndKeepsPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("答复折算不出来时不应触达服务端，却收到了 %s", r.URL.Path)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")
	r.pendingQuestionID = "req_1"
	r.pendingQuestions = []QuestionInfo{{Options: []QuestionOption{{Label: "甲"}}}}

	if err := a.Send(context.Background(), "task-1", "驴唇不对马嘴"); err != nil {
		t.Fatalf("重问路径不应返回错误（错误要以工单形式给审核者）: %v", err)
	}
	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" {
		t.Fatalf("事件 = %+v ok=%v，期望重发 question 工单", ev, ok)
	}
	if r.pendingQuestionID != "req_1" {
		t.Error("重问期间挂起请求必须保留，否则下一次答复就无处可投")
	}
}

func TestSendCustomRejectedByServerRepromptsAndKeepsPending(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	a := newTestAdapter(t)
	dir := t.TempDir()
	r := a.newRun("task-1", dir, dir)
	r.session = "ses_a"
	r.api = NewAPI(srv.URL, "pw")
	r.pendingQuestionID = "req_1"
	r.pendingQuestions = []QuestionInfo{{Custom: true, Options: []QuestionOption{{Label: "甲"}}}}

	if err := a.Send(context.Background(), "task-1", "我自己想的答案"); err != nil {
		t.Fatalf("服务端拒绝自定义答案应降级重问而不是报错: %v", err)
	}
	ev, ok := drainOne(r)
	if !ok || ev.Type != "question" || !strings.Contains(ev.Text, "不接受自定义答案") {
		t.Fatalf("期望重发工单并说明原因，实际: %+v ok=%v", ev, ok)
	}
	if r.pendingQuestionID != "req_1" {
		t.Error("挂起请求必须保留")
	}
}
