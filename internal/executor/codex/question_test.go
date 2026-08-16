package codex_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/codex"
)

const userInputParams = `{"itemId":"tool-1","threadId":"t","turnId":"u","questions":[
  {"id":"q1","header":"选择方案","question":"用 A 还是 B？",
   "options":[{"label":"A","description":"简单"},{"label":"B","description":"通用"}]}]}`

// 问题正文必须含问题与选项，协调者据此裁决
func TestUserInputTextRendersQuestionAndOptions(t *testing.T) {
	itemID, qs, ok := codex.ParseUserInputForTest([]byte(userInputParams))
	if !ok || itemID != "tool-1" || len(qs) != 1 {
		t.Fatalf("解析失败: %s %v %v", itemID, qs, ok)
	}
	txt := codex.UserInputTextForTest(qs)
	for _, want := range []string{"选择方案", "用 A 还是 B？", "A", "简单", "B", "通用"} {
		if !strings.Contains(txt, want) {
			t.Fatalf("问题正文缺 %q:\n%s", want, txt)
		}
	}
}

// 应答体形态必须是 {"answers":{"<qid>":{"answers":["…"]}}}，且每个问题都有答案
func TestUserInputReplyCoversEveryQuestion(t *testing.T) {
	_, qs, _ := codex.ParseUserInputForTest([]byte(userInputParams))
	reply := codex.UserInputReplyForTest(qs)
	b, err := json.Marshal(reply)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Answers map[string]struct {
			Answers []string `json:"answers"`
		} `json:"answers"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("应答形态不对: %s", b)
	}
	a, ok := got.Answers["q1"]
	if !ok || len(a.Answers) == 0 || strings.TrimSpace(a.Answers[0]) == "" {
		t.Fatalf("每个问题都必须有非空答案，否则会被判工具失败: %s", b)
	}
}

// 机密问题不得把内容写进事件正文
func TestSecretQuestionIsNotRelayed(t *testing.T) {
	_, qs, _ := codex.ParseUserInputForTest([]byte(
		`{"itemId":"tool-2","questions":[{"id":"q1","header":"API Key","question":"贴一下 token","isSecret":true}]}`))
	txt := codex.UserInputTextForTest(qs)
	if strings.Contains(txt, "贴一下 token") {
		t.Fatalf("机密问题正文不得进事件库:\n%s", txt)
	}
	if !strings.Contains(txt, "API Key") {
		t.Fatalf("应保留标题让协调者知情:\n%s", txt)
	}
}

// 端到端：请求被接管 → 产出 question 事件 → 兜底不再补第二张工单
func TestUserInputEmitsSingleQuestionAndSuppressesFallback(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T9")
	codex.AttachFakeClientForTest(r)
	h := codex.NewHandlerForTest(a, r)
	if ok := h.OnServerRequest(json.RawMessage("2"), "item/tool/requestUserInput",
		json.RawMessage(userInputParams)); !ok {
		t.Fatal("提问请求必须被接管——回 -32601 等于放弃这条通道")
	}
	// 回合随后无收尾协议地结束，兜底必须闭嘴
	codex.FinishTurnForTest(a, r, "completed", "", "已调用一次提问工具；本回合结束。")

	var questions []executor.AdapterEvent
	for _, ev := range drain(t, codex.EventsForTest(r), 500*time.Millisecond) {
		if ev.Type == "question" {
			questions = append(questions, ev)
		}
	}
	if len(questions) != 1 {
		t.Fatalf("一次提问只能出一张工单，实得 %d 张: %+v", len(questions), questions)
	}
	if !strings.Contains(questions[0].Text, "用 A 还是 B？") {
		t.Fatalf("工单正文应是模型的问题，实得: %s", questions[0].Text)
	}
}
