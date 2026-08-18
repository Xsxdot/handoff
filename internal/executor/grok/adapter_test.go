package grok_test

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/grok"
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

	if err := a.RespondPermission(ctx, "t1", "c1", "reject", ""); err != nil {
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

// TestAskQuestionReplyContainsOutcome 钉住 OnAskQuestion 的应答形态。
//
// 真相取自 grok 自己的 serve.log（2026-08-09 真机实测）：
//
//	tool_error: execution_failure tool_name="ask_user_question"
//	error_message=Client returned an invalid response to user question:
//	  invalid type: map, expected variant identifier at line 1 column 11
//
// 那是 serde 对**内部标签枚举** AskUserQuestionExtResponse 的报错——它按名字取到了
// 标签字段 `outcome`，却发现值是 map 而非变体名。所以 outcome 必须是**字符串**，
// 合法取值只有 accepted / skip_interview / chat_about_this（从 grok 二进制符号表读出）。
//
// 这条断言因此钉两件事：outcome 存在，且它是字符串而不是嵌套对象。曾经回过
// `{}`（缺字段）和 `{"outcome":{"outcome":"cancelled"}}`（照抄 request_permission
// 的内嵌形态），两次都被 grok 判为工具执行失败报回模型。
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
			outcomeRaw, ok := result["outcome"]
			if !ok {
				t.Fatalf("应答 result 必须含 outcome 字段，实得: %s", m.Result)
			}
			var outcome string
			if err := json.Unmarshal(outcomeRaw, &outcome); err != nil {
				t.Fatalf("outcome 必须是变体名字符串，不能是嵌套对象"+
					"（grok 会报 invalid type: map, expected variant identifier），实得: %s", outcomeRaw)
			}
			// 只认 grok 二进制里存在的变体名，防止拼错后又要走一轮真机才发现
			switch outcome {
			case "skip_interview", "chat_about_this", "accepted":
			default:
				t.Fatalf("outcome 取值不在 grok 合法变体内"+
					"（accepted/skip_interview/chat_about_this），实得: %q", outcome)
			}
			return
		case <-deadline:
			t.Fatal("未收到对 ask_user_question 的应答")
		}
	}
}

// TestToolAskSuppressesNoTrailerFallbackQuestion 钉住「一次提问只给协调者一张工单」。
//
// 真机复现（2026-08-09，任务 47c36ab9）：模型调了原生 ask_user_question，适配器把问题
// 转交协调者（工单一）；模型随后结束回合、没输出收尾协议 JSON，收尾兜底又把整段回合
// 叙述文本当成提问交上去（工单二，内容是「已调用一次提问工具；本回合结束。」）。
// 协调者因此看到两张工单，其中一张根本不是问题——回答它等于把废话灌回模型。
//
// 兜底本身要留（它保证回合不会静默结束），但本回合已经通过工具通道给过协调者一个
// 问题时，「不让回合静默」这个诉求已经满足，再补一张就是纯噪声。
func TestToolAskSuppressesNoTrailerFallbackQuestion(t *testing.T) {
	a, r := grok.NewAdapterWithRunForTest("t-dup")
	r.SetTaskDirForTest(t.TempDir())
	grok.NoteAskedViaToolForTest(r)

	// 无收尾协议 + 无新提交 → 走 default 兜底分支
	grok.FinishTurnForTest(a, r, "end_turn", "已调用一次提问工具；本回合结束。")

	select {
	case ev := <-r.EventsForTest():
		t.Fatalf("工具已提过问，兜底不应再产出事件，实得 type=%s text=%q", ev.Type, ev.Text)
	case <-time.After(200 * time.Millisecond):
	}
}

// TestNoTrailerFallbackStillAsksWhenToolDidNot 钉住上面的抑制不能矫枉过正：本回合
// 没走过工具提问时，兜底必须照旧把回合文本交协调者，否则回合会静默结束、任务卡死。
func TestNoTrailerFallbackStillAsksWhenToolDidNot(t *testing.T) {
	a, r := grok.NewAdapterWithRunForTest("t-nodup")
	r.SetTaskDirForTest(t.TempDir())

	grok.FinishTurnForTest(a, r, "end_turn", "我卡住了，不知道下一步该干啥。")

	select {
	case ev := <-r.EventsForTest():
		if ev.Type != "question" {
			t.Fatalf("兜底应产出 question 事件，实得 type=%s", ev.Type)
		}
		if !strings.Contains(ev.Text, "我卡住了") {
			t.Fatalf("兜底问题应含回合文本，实得: %q", ev.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("兜底未产出任何事件，回合静默结束会让任务卡死")
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
