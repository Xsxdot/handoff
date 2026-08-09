package grok_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor/grok"
)

// TestMapUpdateRoutesByKind 验证四类 session/update 的分流：
// 正文进回合文本、thought 与 tool_call 只进 render.log、私有通知忽略。
func TestMapUpdateRoutesByKind(t *testing.T) {
	f, err := os.Open("testdata/updates.jsonl")
	if err != nil {
		t.Fatalf("读 testdata 失败: %v", err)
	}
	defer f.Close()

	h := grok.NewTurnAccumulatorForTest()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		h.FeedRawForTest([]byte(line))
	}

	// 回合正文只应含 agent_message_chunk 的内容
	body := h.TurnTextForTest()
	if !strings.Contains(body, "我要改 foo.go") {
		t.Errorf("回合正文缺 agent_message_chunk 内容: %q", body)
	}
	if strings.Contains(body, "思考中") {
		t.Errorf("推理流不得进回合正文（会污染 trailer 解析）: %q", body)
	}
	if strings.Contains(body, "run_terminal_command") {
		t.Errorf("工具调用不得进回合正文: %q", body)
	}

	// render.log 应同时含正文、推理与工具动作
	render := h.RenderTextForTest()
	for _, want := range []string{"我要改 foo.go", "思考中", "echo hi"} {
		if !strings.Contains(render, want) {
			t.Errorf("render.log 缺 %q，实际: %q", want, render)
		}
	}
}

// TestTurnTextEndsWithTrailerSoParseWorks 验证累积后的正文能被 turn.ParseTrailer 判为 ask。
func TestTurnTextEndsWithTrailerSoParseWorks(t *testing.T) {
	f, _ := os.Open("testdata/updates.jsonl")
	defer f.Close()
	h := grok.NewTurnAccumulatorForTest()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			h.FeedRawForTest([]byte(line))
		}
	}
	kind, tr := h.ClassifyForTest()
	if kind != "ask" {
		t.Fatalf("分类 = %q，期望 ask（正文以 {\"ask\":...} 收尾）", kind)
	}
	if tr.Question != "用哪个库？" {
		t.Errorf("Question = %q", tr.Question)
	}
}

// TestSessionNewAuthErrorGivesActionableMessage 固定 spec §8：凭据问题重试无用，
// 必须给出「跑 grok login」的可操作指引，而不是一个裸的 ACP 错误码。
func TestSessionNewAuthErrorGivesActionableMessage(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		switch req.Method {
		case "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"protocolVersion":1}}`}
		case "session/new":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) +
				`,"error":{"code":-32000,"message":"Authentication required","data":"no auth method id provided"}}`}
		}
		return nil
	}, nil)
	err := grok.StartSessionForTest(wsURL(srv), t.TempDir())
	if err == nil {
		t.Fatal("auth 失败必须返回错误")
	}
	if !strings.Contains(err.Error(), "grok login") {
		t.Errorf("错误必须可操作（提示跑 grok login），实际: %v", err)
	}
}

// TestPermissionEventTextNotTruncatedForSecurity 钉住 permTextHardLimit 的语义：
// adapter 发出的 AdapterEvent.Text 是权限描述的唯一真相源，manager 的黑名单扫描、
// 模型审批与工单全文都吃它。若在 adapter 层提前截短，危险片段落在截断点之后就会
// 被静默放行——今天这台机器上已真实发生过（rm -rf 位于第 200 字符之后的请求）。
func TestPermissionEventTextNotTruncatedForSecurity(t *testing.T) {
	// 敏感串必须放在 200 字符之后（旧 permTextLimit=200 会把它截掉）。
	// title 用通用名（run_terminal_command）不带命令——命令必须从 rawInput 全文取出，
	// 这同时钉住 toolLine 的 200 截断不得用于权限描述（B6 根因）。
	longCmd := strings.Repeat("a", 300) + " && rm -rf /important/dir"
	params := `{"sessionId":"s","toolCall":{"toolCallId":"c1","title":"run_terminal_command",` +
		`"rawInput":{"command":"` + longCmd + `"}},"options":[{"optionId":"allow-once","kind":"allow_once"}]}}`
	permReqBody := `{"jsonrpc":"2.0","id":0,"method":"session/request_permission","params":` + params

	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		switch req.Method {
		case "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{}}`}
		case "trigger/perm":
			return []string{permReqBody}
		}
		return nil
	}, nil)
	a, r := grok.NewAdapterWithRunForTest("t1")
	cli, err := grok.DialACP(context.Background(), wsURL(srv), grok.NewHandlerForTest(a, r), nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	r.AttachClientForTest(cli)
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Call(ctx, "initialize", map[string]any{}); err != nil {
		t.Fatalf("initialize 失败: %v", err)
	}
	_ = cli.Notify("trigger/perm", map[string]any{})

	select {
	case ev := <-r.EventsForTest():
		if ev.Type != "permission" {
			t.Fatalf("事件类型 = %q，期望 permission", ev.Type)
		}
		// 断言内容而非长度：黑名单要扫到的是这个串本身
		if !strings.Contains(ev.Text, "rm -rf /important/dir") {
			t.Fatalf("权限描述被截断，黑名单将扫不到敏感串。Text:\n%s", ev.Text)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到权限事件")
	}
}

// TestRejectedListShowsDescriptionNotID 钉住 perm.go 的 noteRejected 语义：
// 被拒清单必须给出人类可读的描述（模型刚才想干什么），而不是一串 toolCallId。
func TestRejectedListShowsDescriptionNotID(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		switch req.Method {
		case "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{}}`}
		case "trigger/perm":
			return []string{permReq("0", "c1", "ls -la /tmp")}
		}
		return nil
	}, nil)
	a, r := grok.NewAdapterWithRunForTest("t1")
	cli, err := grok.DialACP(context.Background(), wsURL(srv), grok.NewHandlerForTest(a, r), nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	r.AttachClientForTest(cli)
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Call(ctx, "initialize", map[string]any{}); err != nil {
		t.Fatalf("initialize 失败: %v", err)
	}
	_ = cli.Notify("trigger/perm", map[string]any{})

	// 等权限事件落地（OnPermission 已 notePending）
	select {
	case <-r.EventsForTest():
	case <-time.After(3 * time.Second):
		t.Fatal("未收到权限事件")
	}

	if err := a.RespondPermission(ctx, "t1", "c1", "reject"); err != nil {
		t.Fatalf("RespondPermission 失败: %v", err)
	}
	rej := r.RejectedForTest()
	if len(rej) != 1 {
		t.Fatalf("被拒清单长度 = %d，期望 1", len(rej))
	}
	if !strings.Contains(rej[0], "ls -la /tmp") {
		t.Errorf("被拒清单应含权限描述，实际: %q", rej[0])
	}
	if strings.Contains(rej[0], "c1") {
		t.Errorf("被拒清单不得是 toolCallId，实际: %q", rej[0])
	}
}

// TestAskQuestionReplyContainsOutcome 钉住 OnAskQuestion 的应答形态：回裸 `{}`
// 会被 grok 判为「缺 outcome 字段的工具错误」报回模型、模型随即重问，审核者收到
// 两张重复 question 工单（2026-08-09 真机实测）。应答 result 必须含 outcome 字段
// 且不是空对象。
func TestAskQuestionReplyContainsOutcome(t *testing.T) {
	const askReq = `{"jsonrpc":"2.0","id":0,"method":"_x.ai/ask_user_question","params":` +
		`{"sessionId":"s","toolCallId":"c9","questions":[{"question":"用哪种语言？",` +
		`"options":[{"label":"Go","description":"用 Go"}],"multiSelect":null}],"mode":"default"}}`
	replies := make(chan string, 4)
	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		switch req.Method {
		case "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{}}`}
		case "trigger/ask":
			return []string{askReq}
		}
		return nil
	}, replies)

	a, r := grok.NewAdapterWithRunForTest("t1")
	r.SetTaskDirForTest(t.TempDir())
	cli, err := grok.DialACP(context.Background(), wsURL(srv), grok.NewHandlerForTest(a, r), nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	r.AttachClientForTest(cli)
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Call(ctx, "initialize", map[string]any{}); err != nil {
		t.Fatalf("initialize 失败: %v", err)
	}
	_ = cli.Notify("trigger/ask", map[string]any{})

	// 在 replies 里找到对 id=0（ask_user_question 请求）的应答，断言 result 含 outcome
	deadline := time.After(3 * time.Second)
	for {
		select {
		case raw := <-replies:
			var m struct {
				ID     json.RawMessage `json:"id"`
				Result json.RawMessage `json:"result"`
			}
			if json.Unmarshal([]byte(raw), &m) != nil || string(m.ID) != "0" {
				continue
			}
			var result map[string]json.RawMessage
			if json.Unmarshal(m.Result, &result) != nil {
				t.Fatalf("应答 result 非法 JSON: %s", m.Result)
			}
			if len(result) == 0 {
				t.Fatalf("应答 result 不得是空对象（回 {} 会被 grok 判为缺 outcome 字段）: %s", m.Result)
			}
			if _, ok := result["outcome"]; !ok {
				t.Fatalf("应答 result 必须含 outcome 字段，实得: %s", m.Result)
			}
			return
		case <-deadline:
			t.Fatal("未收到对 ask_user_question 的应答")
		}
	}
}

// TestStopDoesNotEmitFailedResult 钉住 Stop 与 onClosed 的竞态：Stop 主动关连接
// 会触发读循环退出→OnClosed→onClosed，后者不得产出「ACP 连接断开」的假失败结果
// （真实原因是用户主动停了，不是执行失败）。
func TestStopDoesNotEmitFailedResult(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string { return nil }, nil) // 永不回应
	h := newFakeHandler()
	a, r := grok.NewAdapterWithRunForTest("t1")
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	r.AttachClientForTest(cli)

	if err := a.Stop("t1"); err != nil {
		t.Fatalf("Stop 失败: %v", err)
	}

	// Stop 关闭了事件通道：收集直到关闭，期间不得出现 OK=false 的 result
	deadline := time.After(3 * time.Second)
	for {
		select {
		case ev, ok := <-r.EventsForTest():
			if !ok {
				return
			}
			if ev.Type == "result" && ev.Result != nil && !ev.Result.OK {
				t.Fatalf("Stop 之后出现假的失败 result: %+v", ev.Result)
			}
		case <-deadline:
			t.Fatal("事件通道未在时限内关闭")
		}
	}
}
