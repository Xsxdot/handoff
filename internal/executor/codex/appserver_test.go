package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/executor/codex"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type fakeHandler struct {
	notes  chan [2]string
	reqs   chan [3]string
	closed chan error
	known  map[string]bool
}

func newFakeHandler(known ...string) *fakeHandler {
	f := &fakeHandler{
		notes:  make(chan [2]string, 16),
		reqs:   make(chan [3]string, 16),
		closed: make(chan error, 4),
		known:  map[string]bool{},
	}
	for _, k := range known {
		f.known[k] = true
	}
	return f
}

func (f *fakeHandler) OnNotify(method string, params json.RawMessage) {
	f.notes <- [2]string{method, string(params)}
}

func (f *fakeHandler) OnServerRequest(reqID json.RawMessage, method string, params json.RawMessage) bool {
	if !f.known[method] {
		return false
	}
	f.reqs <- [3]string{string(reqID), method, string(params)}
	return true
}

func (f *fakeHandler) OnClosed(err error) { f.closed <- err }

// startFakeServer 起一个 WS 服务端：把客户端发来的每条消息喂给 script，
// script 返回要回发的报文列表；另外把 replies 里的报文原样转发出去。
//
// 登记的连接由 closeFakeConns 按需关闭（模拟服务端非预期死亡）。
// 为什么不能用 srv.CloseClientConnections()：httptest 在连接被 websocket.Accept
// 劫持时（StateHijacked）把它从内部跟踪表移除，CloseClientConnections 永远碰不到
// 这条连接——用它会得到一个「服务端死了客户端却毫无察觉」的假死测试。
func startFakeServer(t *testing.T, script func(in string) []string) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		registerFakeConn(srv, conn)
		defer conn.Close(websocket.StatusNormalClosure, "")
		ctx := r.Context()
		for {
			_, data, err := conn.Read(ctx)
			if err != nil {
				return
			}
			for _, out := range script(string(data)) {
				if err := conn.Write(ctx, websocket.MessageText, []byte(out)); err != nil {
					return
				}
			}
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fakeConnsMu 保护 fakeConns 登记表：登记发生在 handler goroutine
// （websocket.Accept 之后），关闭发生在测试 goroutine，没有锁会在 -race 下翻车。
var (
	fakeConnsMu sync.Mutex
	fakeConns   = map[*httptest.Server][]*websocket.Conn{}
)

// registerFakeConn 登记一条已 accept 的连接，供 closeFakeConns 关闭。
func registerFakeConn(srv *httptest.Server, conn *websocket.Conn) {
	fakeConnsMu.Lock()
	defer fakeConnsMu.Unlock()
	fakeConns[srv] = append(fakeConns[srv], conn)
}

// closeFakeConns 用**异常状态码**关闭一个假服务端的全部连接，模拟服务端非预期死亡。
//
// 为什么不用 StatusNormalClosure：本辅助函数服务的测试验的是「连接意外死掉时
// 挂起请求必须以错误终结、OnClosed 必须收到非 nil err」——正常关闭会把它退化
// 成一个优雅停机测试，两条路径（主动 Close 传 nil / 被动断线传 err）就分不开了。
func closeFakeConns(srv *httptest.Server) {
	fakeConnsMu.Lock()
	conns := append([]*websocket.Conn(nil), fakeConns[srv]...)
	fakeConnsMu.Unlock()
	for _, c := range conns {
		_ = c.Close(websocket.StatusInternalError, "fake server died")
	}
}

func wsURL(s *httptest.Server) string { return "ws" + s.URL[len("http"):] }

func TestCallMatchesResponseByID(t *testing.T) {
	srv := startFakeServer(t, func(in string) []string {
		var m struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &m)
		if m.Method != "initialize" {
			return nil
		}
		return []string{`{"jsonrpc":"2.0","id":` + itoa(m.ID) + `,"result":{"ok":true}}`}
	})

	h := newFakeHandler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := codex.Dial(ctx, wsURL(srv), h, quiet())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	res, err := cli.Call(ctx, "initialize", map[string]any{"x": 1})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if string(res) != `{"ok":true}` {
		t.Fatalf("result = %s", res)
	}
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

// 未识别的服务端请求必须收到 -32601，而不是被静默丢弃（丢弃 = codex 侧永久挂起）
func TestUnknownServerRequestGetsMethodNotFound(t *testing.T) {
	replies := make(chan string, 4)
	srv := startFakeServer(t, func(in string) []string {
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Error  *struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(in), &m)
		if m.Error != nil {
			replies <- in
			return nil
		}
		if m.Method == "initialize" {
			// 先回 initialize，再发一条本端不认识的服务端请求
			return []string{
				`{"jsonrpc":"2.0","id":1,"result":{}}`,
				`{"jsonrpc":"2.0","id":7,"method":"totally/unknown","params":{}}`,
			}
		}
		return nil
	})

	h := newFakeHandler("item/commandExecution/requestApproval")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := codex.Dial(ctx, wsURL(srv), h, quiet())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()
	if _, err := cli.Call(ctx, "initialize", nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	select {
	case got := <-replies:
		var m struct {
			ID    int `json:"id"`
			Error struct {
				Code int `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal([]byte(got), &m)
		if m.ID != 7 || m.Error.Code != -32601 {
			t.Fatalf("期望 id=7 code=-32601，实得 %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未识别的服务端请求没有收到任何应答（会让 codex 永久挂起）")
	}
}

// 已识别的服务端请求交给 handler，且应答可以延迟任意久后回发
func TestKnownServerRequestIsHandedToHandlerAndReplyIsDeferred(t *testing.T) {
	replies := make(chan string, 4)
	srv := startFakeServer(t, func(in string) []string {
		var m struct {
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
		}
		_ = json.Unmarshal([]byte(in), &m)
		if m.Method == "initialize" {
			return []string{
				`{"jsonrpc":"2.0","id":1,"result":{}}`,
				`{"jsonrpc":"2.0","id":0,"method":"item/commandExecution/requestApproval","params":{"itemId":"exec-1"}}`,
			}
		}
		if m.Method == "" && m.Result != nil {
			replies <- in
		}
		return nil
	})

	h := newFakeHandler("item/commandExecution/requestApproval")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := codex.Dial(ctx, wsURL(srv), h, quiet())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()
	if _, err := cli.Call(ctx, "initialize", nil); err != nil {
		t.Fatalf("initialize: %v", err)
	}

	var reqID string
	select {
	case got := <-h.reqs:
		reqID = got[0]
		if got[1] != "item/commandExecution/requestApproval" {
			t.Fatalf("method = %s", got[1])
		}
	case <-time.After(3 * time.Second):
		t.Fatal("服务端请求没有上抛到 handler")
	}

	// 模拟审核者慢裁决
	time.Sleep(200 * time.Millisecond)
	if err := cli.Reply(json.RawMessage(reqID), map[string]any{"decision": "accept"}); err != nil {
		t.Fatalf("reply: %v", err)
	}
	select {
	case got := <-replies:
		if !json.Valid([]byte(got)) || !contains(got, `"decision":"accept"`) {
			t.Fatalf("延迟应答内容不对: %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("延迟应答没有发出")
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && stringsIndex(s, sub) >= 0 }

func stringsIndex(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// 对端请求 id 与本端 id 空间重叠时不得串号
func TestOverlappingRequestIDsDoNotCollide(t *testing.T) {
	srv := startFakeServer(t, func(in string) []string {
		var m struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		_ = json.Unmarshal([]byte(in), &m)
		if m.Method != "initialize" {
			return nil
		}
		return []string{
			// id=1 的服务端请求，与本端第一条请求同号
			`{"jsonrpc":"2.0","id":1,"method":"item/commandExecution/requestApproval","params":{"itemId":"exec-x"}}`,
			`{"jsonrpc":"2.0","id":1,"result":{"ok":true}}`,
		}
	})

	h := newFakeHandler("item/commandExecution/requestApproval")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := codex.Dial(ctx, wsURL(srv), h, quiet())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cli.Close()

	res, err := cli.Call(ctx, "initialize", nil)
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if string(res) != `{"ok":true}` {
		t.Fatalf("本端响应被对端同号请求顶掉了: %s", res)
	}
	select {
	case <-h.reqs:
	case <-time.After(time.Second):
		t.Fatal("同号的服务端请求被当成响应吞掉了")
	}
}

// 连接死亡时挂起请求必须以错误终结，不能让调用方永久等待。
// 本测试走的是「服务端非预期死亡」路径：客户端没有主动 Close，OnClosed 必须
// 收到非 nil err（与主动 Close 传 nil 的那条分支区分开）。
func TestPendingCallsFailWhenConnectionDies(t *testing.T) {
	srv := startFakeServer(t, func(in string) []string { return nil })
	h := newFakeHandler()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := codex.Dial(ctx, wsURL(srv), h, quiet())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	ch, err := cli.CallAsync("turn/start", nil)
	if err != nil {
		t.Fatalf("call async: %v", err)
	}
	closeFakeConns(srv)

	select {
	case r := <-ch:
		if r.Err == nil {
			t.Fatal("连接死亡后挂起请求必须以错误终结")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("挂起请求永久悬挂")
	}
	select {
	case e := <-h.closed:
		// 服务端把连接弄死：本端没有 activelyClosed，err 必须非 nil
		if e == nil {
			t.Fatal("非主动关闭时 OnClosed 必须收到非 nil err（nil 只留给主动 Close 分支）")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("OnClosed 未触发")
	}
}
