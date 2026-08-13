package grok_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/grok"
	"github.com/coder/websocket"
)

// fakeHandler 收集回调，供断言。
type fakeHandler struct {
	notifies chan [2]string // method, params
	perms    chan json.RawMessage
	asks     chan json.RawMessage
	closed   chan error
}

func newFakeHandler() *fakeHandler {
	return &fakeHandler{
		notifies: make(chan [2]string, 16),
		perms:    make(chan json.RawMessage, 4),
		asks:     make(chan json.RawMessage, 4),
		closed:   make(chan error, 4),
	}
}

func (f *fakeHandler) OnNotify(method string, params json.RawMessage) {
	f.notifies <- [2]string{method, string(params)}
}
func (f *fakeHandler) OnPermission(reqID, params json.RawMessage)  { f.perms <- params }
func (f *fakeHandler) OnAskQuestion(reqID, params json.RawMessage) { f.asks <- params }
func (f *fakeHandler) OnClosed(err error)                          { f.closed <- err }

// startFakeAgent 起一个假 ACP agent：按脚本回消息。
// script 收到客户端每条消息后返回要发回的若干条消息（原样字符串）。
// replies 非 nil 时把客户端发出的每条消息原样投递过去（供断言应答内容），
// 传 nil 即退化为不记录。
func startFakeAgent(t *testing.T, script func(in string) []string, replies chan string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for {
			_, data, err := c.Read(ctx)
			if err != nil {
				return
			}
			if replies != nil {
				select {
				case replies <- string(data):
				default:
				}
			}
			for _, out := range script(string(data)) {
				if err := c.Write(ctx, websocket.MessageText, []byte(out)); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func wsURL(s *httptest.Server) string { return "ws" + s.URL[len("http"):] }

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// permReq 构造一条 session/request_permission 报文（id/toolCallId/command 可定制）。
// 注意 title 里的反引号是 ACP 实测原样（工具调用的呈现），不得改动。
func permReq(id, toolCallID, command string) string {
	return `{"jsonrpc":"2.0","id":` + id + `,"method":"session/request_permission","params":` +
		`{"sessionId":"s","toolCall":{"toolCallId":"` + toolCallID + `","title":"Execute ` + "`" + command + "`" + `",` +
		`"rawInput":{"command":"` + command + `"}},"options":[{"optionId":"allow-once","kind":"allow_once"},` +
		`{"optionId":"reject-once","kind":"reject_once"}]}}`
}

func TestCallMatchesResponseByID(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		if req.Method == "initialize" {
			// 先插一条无关通知，再回响应：验证不会把通知误当响应
			return []string{
				`{"jsonrpc":"2.0","method":"_x.ai/noise","params":{}}`,
				`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"protocolVersion":1}}`,
			}
		}
		return nil
	}, nil)
	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := cli.Call(ctx, "initialize", map[string]any{"protocolVersion": 1})
	if err != nil {
		t.Fatalf("Call 失败: %v", err)
	}
	var got struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if err := json.Unmarshal(res, &got); err != nil || got.ProtocolVersion != 1 {
		t.Fatalf("响应解析异常: %v %s", err, res)
	}
	select {
	case n := <-h.notifies:
		if n[0] != "_x.ai/noise" {
			t.Errorf("通知 method = %q", n[0])
		}
	case <-time.After(2 * time.Second):
		t.Error("未收到通知回调")
	}
}

func TestPermissionRequestCallbackAndDeferredReply(t *testing.T) {
	replies := make(chan string, 8)
	srv := startFakeAgent(t, func(in string) []string {
		var msg struct {
			ID     *int   `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &msg)
		switch {
		case msg.Method == "initialize":
			return []string{`{"jsonrpc":"2.0","id":` + itoa(*msg.ID) + `,"result":{}}`}
		case msg.Method == "trigger/perm":
			// agent 侧主动发权限请求（id=0，与我方 id 空间独立）
			return []string{permReq("0", "c1", "ls")}
		case msg.Method == "":
			// 客户端对我方请求的应答
			replies <- in
			return nil
		}
		return nil
	}, replies)
	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Call(ctx, "initialize", nil); err != nil {
		t.Fatalf("initialize 失败: %v", err)
	}

	// 用 Notify 触发 script 返回权限请求
	_ = cli.Notify("trigger/perm", nil)

	select {
	case p := <-h.perms:
		if !json.Valid(p) {
			t.Fatalf("权限参数非法 JSON: %s", p)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未收到权限回调")
	}
}

// TestAskQuestionRequestIsSurfacedNotDropped 钉住 spec §4.2.3：
// _x.ai/ask_user_question 是带 id 的请求，丢弃会让 session/prompt 永不返回。
func TestAskQuestionRequestIsSurfacedNotDropped(t *testing.T) {
	const askReq = `{"jsonrpc":"2.0","id":0,"method":"_x.ai/ask_user_question","params":` +
		`{"sessionId":"s","toolCallId":"c9","questions":[{"question":"用哪种语言？",` +
		`"options":[{"label":"Go","description":"用 Go"},{"label":"Rust","description":"用 Rust"}],` +
		`"multiSelect":null}],"mode":"default"}}`
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

	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Call(ctx, "initialize", map[string]any{}); err != nil {
		t.Fatalf("initialize 失败: %v", err)
	}
	_ = cli.Notify("trigger/ask", map[string]any{})

	select {
	case p := <-h.asks:
		var got struct {
			ToolCallID string `json:"toolCallId"`
			Questions  []struct {
				Question string `json:"question"`
				Options  []struct {
					Label string `json:"label"`
				} `json:"options"`
			} `json:"questions"`
		}
		if err := json.Unmarshal(p, &got); err != nil {
			t.Fatalf("提问参数解析失败: %v", err)
		}
		if got.ToolCallID != "c9" || len(got.Questions) != 1 ||
			got.Questions[0].Question != "用哪种语言？" || len(got.Questions[0].Options) != 2 {
			t.Fatalf("提问参数未原样上抛: %s", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("提问请求被丢弃 —— 真机上这会让回合永久挂死（spec §5.3(c)）")
	}
}

// TestUnknownAgentRequestGetsMethodNotFound 钉住：未识别的**有 id** 请求必须回错误，
// 不得静默丢弃——丢弃等于制造同款永久挂死。
func TestUnknownAgentRequestGetsMethodNotFound(t *testing.T) {
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
		case "trigger/unknown":
			return []string{`{"jsonrpc":"2.0","id":7,"method":"_x.ai/brand_new_thing","params":{}}`}
		}
		return nil
	}, replies)

	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := cli.Call(ctx, "initialize", map[string]any{}); err != nil {
		t.Fatalf("initialize 失败: %v", err)
	}
	_ = cli.Notify("trigger/unknown", map[string]any{})

	deadline := time.After(3 * time.Second)
	for {
		select {
		case raw := <-replies:
			var m struct {
				ID    json.RawMessage `json:"id"`
				Error *struct {
					Code int `json:"code"`
				} `json:"error"`
			}
			if json.Unmarshal([]byte(raw), &m) == nil && string(m.ID) == "7" {
				if m.Error == nil || m.Error.Code != -32601 {
					t.Fatalf("未知请求应回 -32601，实得: %s", raw)
				}
				return
			}
		case <-deadline:
			t.Fatal("未知请求未收到任何应答 —— 对方会永久等待")
		}
	}
}

// TestOverlappingRequestIDsDoNotCollide 钉住 spec §5.3(d)：
// agent 侧请求 id 从 0 自增，与本端 id 空间重叠。此处让 agent 主动发一个
// id=1 的请求，而本端第一个请求的 id 也是 1——两者必须各归各路。
func TestOverlappingRequestIDsDoNotCollide(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string {
		var req struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &req)
		if req.Method != "initialize" {
			return nil
		}
		// 先发一个与本端 id 撞号的 agent 请求，再回真正的响应
		return []string{
			permReq(itoa(req.ID), "cx", "ls"),
			`{"jsonrpc":"2.0","id":` + itoa(req.ID) + `,"result":{"protocolVersion":1}}`,
		}
	}, nil)
	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	defer cli.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	res, err := cli.Call(ctx, "initialize", map[string]any{})
	if err != nil {
		t.Fatalf("撞号的 agent 请求污染了响应匹配: %v", err)
	}
	var got struct {
		ProtocolVersion int `json:"protocolVersion"`
	}
	if json.Unmarshal(res, &got) != nil || got.ProtocolVersion != 1 {
		t.Fatalf("响应内容错误（可能拿到了 agent 请求）: %s", res)
	}
	select {
	case <-h.perms:
	case <-time.After(2 * time.Second):
		t.Fatal("撞号的 agent 请求被当成响应吃掉了")
	}
}

func TestPendingCallsFailWhenConnectionDies(t *testing.T) {
	srv := startFakeAgent(t, func(in string) []string { return nil }, nil) // 永不回应
	h := newFakeHandler()
	cli, err := grok.DialACP(context.Background(), wsURL(srv), h, nil)
	if err != nil {
		t.Fatalf("DialACP 失败: %v", err)
	}
	ch, err := cli.CallAsync("session/prompt", nil)
	if err != nil {
		t.Fatalf("CallAsync 失败: %v", err)
	}
	_ = cli.Close()
	select {
	case r := <-ch:
		if r.Err == nil {
			t.Error("连接终止后挂起请求必须以错误终结，不得永久等待")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("挂起请求未被终结——调用方会永久卡住")
	}
	select {
	case <-h.closed:
	case <-time.After(3 * time.Second):
		t.Error("未触发 OnClosed 回调")
	}
}
