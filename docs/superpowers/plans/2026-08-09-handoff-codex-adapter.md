# codex adapter 实现计划（B28）

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **同时必须遵守 `instrumenting-code`**：每个实现类 task 都带「加关键节点日志」与「加注释」两个 step，它们是交付物的一部分，不是可选润色。

**Goal:** 让 `handoff dispatch --executor codex` 与 opencode / claude / grok 对等——五动作全链路、分级审批链、`handoff attach` 同形终端、agentd 重启后恢复运行态。

**Architecture:** 新增 `internal/executor/codex/` 包，形态对齐 `internal/executor/grok/`：tmux 托管 `codex app-server --listen ws://127.0.0.1:PORT`，WS 上跑 JSON-RPC 2.0 双向协议；adapter 把 codex 的 ServerNotification / ServerRequest 翻译成 `executor.AdapterEvent` 的四类事件。与 grok 的最大结构差异有三处：(1) **不做任务级 home**，复用用户级 `~/.codex`，因此没有 `authsync`/`EnsureAuthLink`/凭据巡检那一整层；(2) 安全档位**全部协议级下发**且每回合重钉，不写任何 config 文件；(3) 权限报文里 fileChange 不带路径，需要一张 `itemId → ThreadItem` 索引（新增 `items.go`）。

**Tech Stack:** Go 1.2x；`github.com/coder/websocket`（与 grok 同源）；`internal/executor/turn`（提示词渲染、trailer 解析、git 回合探测、render 落盘）原样复用不改；tmux；`codex-cli 0.144.1` 的 app-server 协议。

## Global Constraints

以下逐条来自 spec（`docs/superpowers/specs/2026-08-09-handoff-codex-adapter-design.md`），值必须一字不差地落进代码，每个 task 的要求隐含包含本节。

- **home**：不设 `CODEX_HOME`，复用用户级 `~/.codex`（spec §1.3 / §2）。env 文件里出现 `CODEX_HOME` 一律**丢弃并 WARN**。
- **沙箱**：`{"type":"workspaceWrite","networkAccess":true,"excludeSlashTmp":true,"excludeTmpdirEnvVar":true,"writableRoots":[]}`（spec §2 / §2.2）。
- **审批档**：`approvalPolicy: "on-request"` + `approvalsReviewer: "user"`（spec §2.1）。
- **每回合重钉四参**：每次 `turn/start` 同时带 `sandboxPolicy` / `approvalPolicy` / `approvalsReviewer` / `cwd`；`thread/resume` 重传 `cwd` / `approvalPolicy` / `approvalsReviewer`（spec §5.1 步骤 6 / §5.6）。
- **裁决映射**：`once → {"decision":"accept"}`，其余一律 `{"decision":"decline"}`（fail-closed）。**禁止**使用 `cancel` / `acceptForSession` / `acceptWithExecpolicyAmendment` / `applyNetworkPolicyAmendment`（spec §5.4）。
- **`item/permissions/requestApproval` 一律 fail-closed**：回一份空 `GrantedPermissionProfile`，落 render.log + progress，**不做成可批准的权限门**（spec §5.4）。
- **`account/chatgptAuthTokens/refresh` 不实现**：收到即回 JSON-RPC 错误，并把任务判失败，失败文案为「codex 登录态失效，请在 executor 机重新 `codex login`」（spec §4 / §8）。
- **会话标识**：`threadId`（== sessionId）落 `task.ExecutorSession`（spec §2）。
- **权限文本不为安全而截断**：只有超 64KB 硬上限才截（B6 教训），常量 `permTextHardLimit = 64 << 10`。
- **运行态不存在**：`Send` / `RespondPermission` / `Stop` 一律包装 `executor.ErrTaskNotRunning` 哨兵；**禁止**靠错误文本判别（spec §8）。
- **日志**：一律 `log/slog`，**禁止 `fmt.Printf`**；env 注入只打 key 名不打值（B19）。
- **不动的东西**：`internal/executor/turn` 不改；`internal/executor/grok` 不动；`internal/executor/oneshot.go` 不动（codex 不登记为审批者执行者，spec §9）。

## File Structure

| 文件 | 职责 |
|------|------|
| `internal/executor/codex/appserver.go` | WS JSON-RPC 2.0 双向传输管道。只搬消息，不认业务语义 |
| `internal/executor/codex/taskenv.go` | 生成 `run_codex.sh`（B19 env 注入、丢弃 `CODEX_HOME`）；包内文件名常量 |
| `internal/executor/codex/proc.go` | tmux 进程生命周期：起 app-server、TCP 探活等就绪、render tail 窗口、`serve.json` 读写、回收 |
| `internal/executor/codex/items.go` | `ThreadItem` 结构与有界 `itemId → item` 索引（fileChange 权限报文无路径，路径只能从这里取） |
| `internal/executor/codex/perm.go` | 权限挂起表、裁决映射、被拒清单、`RespondPermission`、`PermissionsVolatile` |
| `internal/executor/codex/adapter.go` | 五动作与事件翻译：Start / Events / Send / Stop、回合边界、render.log 渲染 |
| `internal/executor/codex/resume.go` | agentd 重启恢复（四级阶梯）+ 看门狗 |
| `internal/executor/codex/preflight.go` | `--executor=codex` 的启动预检（PATH / 登录态 / 污染源 WARN） |
| `internal/executor/codex/reap.go` | 兜底回收（运行态丢失时按确定性会话名回收，B20） |
| `internal/executor/codex/export_test.go` | 内部测试缝 |
| `cmd/agentd.go` | `defaultAdapters` 注册 codex；`--executor` 文案；预检接线 |
| `README.md` | 架构图 / 前置条件 / 示例 / executor 差异表补 codex |
| `docs/superpowers/backlog.md` | B28 状态推进 |

---

### Task 1: WS JSON-RPC 传输管道（`appserver.go`）

**Files:**
- Create: `internal/executor/codex/appserver.go`
- Test: `internal/executor/codex/appserver_test.go`

**Interfaces:**
- Consumes: 无（本包第一个文件）
- Produces:
  - `type Result struct { Result json.RawMessage; Err error }`
  - `type Handler interface { OnNotify(method string, params json.RawMessage); OnServerRequest(reqID json.RawMessage, method string, params json.RawMessage) bool; OnClosed(err error) }`
  - `func Dial(ctx context.Context, wsURL string, h Handler, log *slog.Logger) (*Client, error)`
  - `func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error)`
  - `func (c *Client) CallAsync(method string, params any) (<-chan Result, error)`
  - `func (c *Client) Reply(reqID json.RawMessage, result any) error`
  - `func (c *Client) ReplyError(reqID json.RawMessage, code int, message string) error`
  - `func (c *Client) Notify(method string, params any) error`
  - `func (c *Client) Close() error`

**为什么 `OnServerRequest` 返回 bool 而不是像 grok 那样一个方法一个回调**：codex 侧要处理的 ServerRequest 有五种（两类审批 + permissions + userInput + authTokens），且还会长出新的。「**每一条带 id 的入站消息都必须有应答**」这条铁律必须只在一个地方实现——handler 返回 `false` 表示本端不认识，传输层随即回 `-32601`。分散成五个回调时，将来新增一种就多一次「忘了回复 → 对端永久挂起」的机会。

- [ ] **Step 1: 写失败测试——按 id 匹配响应**

创建 `internal/executor/codex/appserver_test.go`：

```go
package codex_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
func startFakeServer(t *testing.T, script func(in string) []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run TestCallMatchesResponseByID -v`
Expected: FAIL —— `no required module provides package .../internal/executor/codex`（包还不存在）

- [ ] **Step 3: 实现传输管道**

创建 `internal/executor/codex/appserver.go`：

```go
// appserver.go —— codex app-server 的 WebSocket JSON-RPC 2.0 双向客户端。
//
// 职责：
//   - 维护一条到 `codex app-server --listen ws://…` 的 WS 连接
//   - 我方请求（initialize / thread.* / turn.*）按 id 匹配响应；turn/start 用
//     CallAsync 拿异步通道（它虽然立即返回，但仍不能阻塞 Start 的启动路径）
//   - 对方通知经 OnNotify 分发；对方请求经 OnServerRequest 上抛，应答可延迟任意久
//     后经 Reply 回发——审核者可能过夜才裁决
//
// 边界：
//   - 不认识 codex 的业务语义（不知道什么是回合、什么是权限），只做协议管道；
//     语义翻译在 adapter.go
//   - 不重连：重连属 adapter 的生命周期决策，本层只在连接死亡时 OnClosed 通知
//
// 铁律：**每一条带 id 的入站消息都必须有应答**。Handler 认不出的方法由本层统一
// 回 -32601——静默丢弃有 id 的请求等于让 codex 侧永久等待，回合从此挂死。
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/coder/websocket"
)

// Result 是一次异步调用的终局（二选一）。
type Result struct {
	Result json.RawMessage
	Err    error
}

// Handler 是 adapter 侧的回调面。实现方必须假定回调在读循环 goroutine 上触发：
// **不得在回调里做阻塞操作**，否则会卡住整条连接的消息消费。
type Handler interface {
	// OnNotify 收到对方通知（无 id 的消息）。
	OnNotify(method string, params json.RawMessage)
	// OnServerRequest 收到对方请求（有 id，必须应答）。
	//
	// 返回 false 表示本端不认识该方法，传输层随即代为回 -32601；返回 true 表示
	// 本端接管，实现方**必须**在此后某个时刻调用 Reply 或 ReplyError。
	OnServerRequest(reqID json.RawMessage, method string, params json.RawMessage) bool
	// OnClosed 连接终止（err 为终止原因，主动 Close 时为 nil）。
	OnClosed(err error)
}

// Client 是一条 app-server 连接。并发安全：nextID/pending 由 mu 保护；
// 写连接由 writeMu 串行化（websocket 不允许并发写）。
type Client struct {
	conn   *websocket.Conn
	log    *slog.Logger
	cancel context.CancelFunc

	writeMu sync.Mutex

	mu             sync.Mutex
	nextID         int
	pending        map[int]chan Result
	closed         bool
	activelyClosed bool // Close() 置位：读循环据此以 nil 通知 OnClosed
}

// Dial 连接 app-server 端点并启动读循环。
//
// 参数：
//   - ctx: 仅控制握手阶段；连接生命周期延续到 Close
//   - wsURL: 形如 ws://127.0.0.1:<port>
//   - h: 回调面（不得为 nil）
//   - log: 日志入口（nil 退回 slog.Default()）
//
// 返回：已就绪的连接；握手失败时返回错误
func Dial(ctx context.Context, wsURL string, h Handler, log *slog.Logger) (*Client, error) {
	if log == nil {
		log = slog.Default()
	}
	log.Info("codex app-server 连接中", "url", wsURL)
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		log.Error("codex app-server 连接失败", "url", wsURL, "cause", err)
		return nil, fmt.Errorf("连接 codex app-server: %w", err)
	}
	// 单条消息上限放宽：initialize 响应与 item 报文可达数十 KB
	conn.SetReadLimit(8 << 20)

	runCtx, cancel := context.WithCancel(context.Background())
	c := &Client{conn: conn, log: log, cancel: cancel, pending: map[int]chan Result{}}
	go c.readLoop(runCtx, h)
	log.Info("codex app-server 连接就绪")
	return c, nil
}

// Call 发起请求并阻塞等待响应。
func (c *Client) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	ch, err := c.CallAsync(method, params)
	if err != nil {
		return nil, err
	}
	select {
	case r := <-ch:
		return r.Result, r.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CallAsync 发起请求并立即返回结果通道。
func (c *Client) CallAsync(method string, params any) (<-chan Result, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("codex app-server 连接已关闭")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan Result, 1)
	c.pending[id] = ch
	c.mu.Unlock()

	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	if err := c.write(msg); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		c.log.Error("codex 请求发送失败", "method", method, "cause", err)
		return nil, err
	}
	c.log.Debug("codex 请求已发出", "method", method, "id", id)
	return ch, nil
}

// Reply 应答对方请求。reqID 必须是 OnServerRequest 收到的原值。
func (c *Client) Reply(reqID json.RawMessage, result any) error {
	if err := c.write(map[string]any{
		"jsonrpc": "2.0", "id": json.RawMessage(reqID), "result": result,
	}); err != nil {
		c.log.Error("codex 应答发送失败", "req_id", string(reqID), "cause", err)
		return err
	}
	c.log.Info("codex 应答已发出", "req_id", string(reqID))
	return nil
}

// ReplyError 以 JSON-RPC 错误应答对方请求（用于不实现的方法，如令牌刷新）。
func (c *Client) ReplyError(reqID json.RawMessage, code int, message string) error {
	if err := c.write(map[string]any{
		"jsonrpc": "2.0", "id": json.RawMessage(reqID),
		"error": map[string]any{"code": code, "message": message},
	}); err != nil {
		c.log.Error("codex 错误应答发送失败", "req_id", string(reqID), "cause", err)
		return err
	}
	c.log.Info("codex 错误应答已发出", "req_id", string(reqID), "code", code, "message", message)
	return nil
}

// Notify 发送通知（无需应答，用于 initialized）。
func (c *Client) Notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.write(msg)
}

// Close 关闭连接，所有挂起的请求以错误终结。
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.activelyClosed = true
	c.mu.Unlock()
	c.cancel()
	c.log.Info("codex app-server 连接关闭")
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *Client) write(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化 codex 消息: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(context.Background(), websocket.MessageText, b)
}

// readLoop 消费连接上的全部消息直到出错，并在退出时终结所有挂起请求。
func (c *Client) readLoop(ctx context.Context, h Handler) {
	var exitErr error
	defer func() {
		c.mu.Lock()
		c.closed = true
		active := c.activelyClosed
		pend := c.pending
		c.pending = map[int]chan Result{}
		c.mu.Unlock()
		for id, ch := range pend {
			c.log.Warn("codex 连接终止，挂起请求作废", "id", id)
			ch <- Result{Err: fmt.Errorf("codex 连接终止: %w", exitErr)}
		}
		c.log.Info("codex 读循环退出", "cause", exitErr)
		// 主动 Close 时 OnClosed 传 nil：此时「连接终止」是调用方主动行为而非故障，
		// adapter 的 onClosed 依此跳过失败处置（对齐 grok 的单一处置路径）
		if active {
			h.OnClosed(nil)
		} else {
			h.OnClosed(exitErr)
		}
	}()

	for {
		_, data, err := c.conn.Read(ctx)
		if err != nil {
			exitErr = err
			return
		}
		var msg struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			// 宽容：对端输出不可信，坏消息跳过不中断连接
			c.log.Warn("codex 消息解析失败，跳过", "cause", err)
			continue
		}

		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			// 对方请求。**先判 Method 再判 ID**：codex 侧请求 id 从 0 自增，与本端
			// 请求 id 空间重叠，只看 id 会把对方的请求误认成自己请求的响应。
			c.log.Info("codex 收到服务端请求", "method", msg.Method, "req_id", string(msg.ID))
			if h.OnServerRequest(append(json.RawMessage(nil), msg.ID...), msg.Method, msg.Params) {
				continue
			}
			c.log.Warn("codex 未处理的服务端请求，回 -32601", "method", msg.Method)
			_ = c.ReplyError(msg.ID, -32601, "unhandled method: "+msg.Method)
		case msg.Method != "":
			h.OnNotify(msg.Method, msg.Params)
		case len(msg.ID) > 0:
			var id int
			if err := json.Unmarshal(msg.ID, &id); err != nil {
				c.log.Warn("codex 响应 id 非数字，跳过", "id", string(msg.ID))
				continue
			}
			c.mu.Lock()
			ch, ok := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if !ok {
				c.log.Warn("codex 响应无对应请求，丢弃", "id", id)
				continue
			}
			if msg.Error != nil {
				ch <- Result{Err: fmt.Errorf("codex 错误 %d: %s", msg.Error.Code, msg.Error.Message)}
				continue
			}
			ch <- Result{Result: msg.Result}
		default:
			c.log.Debug("codex 无法归类的消息，跳过")
		}
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/codex/ -run TestCallMatchesResponseByID -v`
Expected: PASS

- [ ] **Step 5: 补齐四条边界测试**

追加到 `internal/executor/codex/appserver_test.go`：

```go
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
			Method string `json:"method"`
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

// 连接死亡时挂起请求必须以错误终结，不能让调用方永久等待
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
	srv.CloseClientConnections()

	select {
	case r := <-ch:
		if r.Err == nil {
			t.Fatal("连接死亡后挂起请求必须以错误终结")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("挂起请求永久悬挂")
	}
	select {
	case <-h.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("OnClosed 未触发")
	}
}
```

- [ ] **Step 6: 运行全部测试**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS（5 个测试全绿）

- [ ] **Step 7: 加关键节点日志**

按 `instrumenting-code` 逐项核对 `appserver.go` 已有的日志点（上面的实现已内置，本步是**核对而非新写**，缺哪条补哪条）：

- 进入关键操作：`Dial` 前 `Info("codex app-server 连接中", "url", …)`
- 外部调用前后：`Dial` 成功 `Info("codex app-server 连接就绪")`；失败 `Error(..., "cause", err)`
- 每个错误分支带上下文：请求发送失败带 `method`；应答发送失败带 `req_id`；消息解析失败带 `cause`
- 状态变更：收到服务端请求 `Info(..., "method", …, "req_id", …)`；回 -32601 `Warn(..., "method", …)`
- 成功路径不静默：`Reply` / `ReplyError` 成功都打 `Info`
- 退出带结局：`readLoop` 退出 `Info("codex 读循环退出", "cause", exitErr)`；挂起请求作废逐条 `Warn`
- 高频路径降级 Debug：请求已发出、无法归类的消息 → `Debug`

用 `log/slog`，**禁止 `fmt.Printf`**。

- [ ] **Step 8: 加注释**

- 文件头：职责 + 边界（不认业务语义、不重连），并写明「每条带 id 的入站消息都必须有应答」这条铁律
- 导出符号：`Result` / `Handler`（含「不得在回调里阻塞」的警告与 `OnServerRequest` 返回值语义）/ `Client` / `Dial` / `Call` / `CallAsync` / `Reply` / `ReplyError` / `Notify` / `Close` 全部有 doc 注释
- 「为什么」型行内注释三处：`readLoop` 里**先判 Method 再判 ID**（id 空间重叠）、解析失败**跳过而非断连**（对端输出不可信）、`activelyClosed` 时 `OnClosed(nil)`（主动关闭不是故障）

- [ ] **Step 9: 提交**

```bash
git add internal/executor/codex/appserver.go internal/executor/codex/appserver_test.go && git commit -m "feat(codex): app-server 的 WS JSON-RPC 双向传输管道"
```

- [ ] **Step 10: 真机验 V-5（`ws://` 握手与断线行为）**

spec §6 的 V-5：spike 走的是 stdio，`ws://` 的握手与断线行为必须实测，**在这一步就验**。

```bash
codex app-server --listen 'ws://127.0.0.1:47777' &
```

写一个一次性探针 `/private/tmp/claude-501/.../scratchpad/v5_ws.go`（不入库），用本 task 的 `codex.Dial` 连上去，依次确认：

1. `ws://127.0.0.1:47777` 这个**确切 URL 形态**能握手成功；若失败，逐个试 `ws://127.0.0.1:47777/`、`/ws`，把实际可用的形态记下来
2. `initialize` + `initialized` 后能收到响应
3. `kill` 掉 app-server 后 `OnClosed` 在 5s 内触发且 err 非 nil
4. 客户端 `Close()` 后 `OnClosed` 收到的是 `nil`

把结论回写 spec §6 的 V-5 行（改成「已验：<结论>」），**尤其是最终 WS URL 形态**——Task 3 的 `Proc.WSURL()` 直接依赖它。若 URL 形态与 `ws://127.0.0.1:<port>` 不同，Task 3 按实测形态实现。

```bash
git add docs/superpowers/specs/2026-08-09-handoff-codex-adapter-design.md && git commit -m "docs(spec): B28 V-5 已验——ws 传输握手与断线行为"
```

---

### Task 2: 任务启动脚本（`taskenv.go`）

**Files:**
- Create: `internal/executor/codex/taskenv.go`
- Test: `internal/executor/codex/taskenv_test.go`

**Interfaces:**
- Consumes: 无
- Produces:
  - 常量 `serveScriptName = "run_codex.sh"` / `serveLogName = "serve.log"` / `renderLogName = "render.log"` / `serveInfoName = "serve.json"`
  - `func WriteServeScript(taskDir string, port int, env []string) (string, error)` —— 返回脚本绝对路径

**与 grok 的关键差异**：grok 的 `protectedEnvKeys` 靠「handoff 的 export 排在用户行之后所以胜出」来兜底；codex **自己从不 export `CODEX_HOME`**（这是本设计的核心——复用用户级 home），排序兜不住，所以 `CODEX_HOME` 必须**直接丢弃并 WARN**。这条不是洁癖：env 文件里一行 `CODEX_HOME=/tmp/x` 会让 executor 换一个空 home 跑，凭据、插件、sessions 全部落空，任务以「未登录」形态失败，且原因极难追。

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/codex/taskenv_test.go`：

```go
package codex_test

import (
	"os"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
)

func TestWriteServeScriptShape(t *testing.T) {
	dir := t.TempDir()
	p, err := codex.WriteServeScript(dir, 47777, nil)
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "exec codex app-server --listen 'ws://127.0.0.1:47777'") {
		t.Fatalf("启动命令形态不对:\n%s", s)
	}
	if !strings.Contains(s, "serve.log") {
		t.Fatalf("未重定向到 serve.log:\n%s", s)
	}
	if strings.Contains(s, "CODEX_HOME") {
		t.Fatalf("脚本不得设置 CODEX_HOME（本设计复用用户级 ~/.codex）:\n%s", s)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("权限 = %v，应为 0600", fi.Mode().Perm())
	}
}

// B19：env 注入的值必须单引号包裹（Go 侧已展开一次，不能让 shell 再展开）
func TestWriteServeScriptQuotesEnvValues(t *testing.T) {
	dir := t.TempDir()
	p, err := codex.WriteServeScript(dir, 1234, []string{
		"API_BASE=https://a.example.com",
		"WEIRD=$HOME/x y",
		"MALFORMED_NO_EQUALS",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if !strings.Contains(s, "export API_BASE='https://a.example.com'") {
		t.Fatalf("普通值未正确导出:\n%s", s)
	}
	if !strings.Contains(s, "export WEIRD='$HOME/x y'") {
		t.Fatalf("含 $ 的值必须单引号包裹防二次展开:\n%s", s)
	}
	if strings.Contains(s, "MALFORMED_NO_EQUALS") {
		t.Fatalf("非 KEY=VALUE 条目必须跳过，不得污染脚本语法:\n%s", s)
	}
}

// CODEX_HOME 必须被丢弃：它一旦生效会把 executor 换到空 home，凭据/插件/sessions 全落空
func TestWriteServeScriptDropsCodexHome(t *testing.T) {
	dir := t.TempDir()
	p, err := codex.WriteServeScript(dir, 1234, []string{
		"CODEX_HOME=/tmp/hijack",
		"KEEP=1",
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	b, _ := os.ReadFile(p)
	s := string(b)
	if strings.Contains(s, "/tmp/hijack") || strings.Contains(s, "CODEX_HOME") {
		t.Fatalf("CODEX_HOME 必须被丢弃:\n%s", s)
	}
	if !strings.Contains(s, "export KEEP='1'") {
		t.Fatalf("丢弃 CODEX_HOME 不得牵连其他变量:\n%s", s)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run TestWriteServeScript -v`
Expected: FAIL —— `undefined: codex.WriteServeScript`

- [ ] **Step 3: 实现**

创建 `internal/executor/codex/taskenv.go`：

```go
// taskenv.go —— codex 任务的启动物料：app-server 启动脚本与包内文件名常量。
//
// 职责：
//   - 生成 taskDir 下的 run_codex.sh（0600），把 B19 注入的 env 变量展开成 export 行
//   - 统一约定任务目录内的文件名（serve.log / render.log / serve.json）
//
// 边界：
//   - 不起进程（tmux 在 proc.go）、不碰协议（appserver.go）
//   - **刻意不生成任何 codex 配置文件**：本设计的安全档位全部协议级下发
//     （spec §2「配置下发：全部协议级，不碰任何 config 文件」），写配置文件会
//     让「代码钉死安全边界」这条保证多出一个可被绕过的入口
package codex

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/xushixin/handoff/internal/shellq"
)

const (
	serveScriptName = "run_codex.sh"
	serveLogName    = "serve.log"
	renderLogName   = "render.log"
	serveInfoName   = "serve.json"
)

// droppedEnvKeys 是 env 文件里出现即**丢弃**的变量。
//
// 为什么是丢弃而不是像 grok 那样靠 export 顺序覆盖：codex adapter 自身从不
// export CODEX_HOME（本设计刻意复用用户级 ~/.codex，spec §1.3），没有「后写的
// 那行」可以压过它。一旦生效，executor 会换到一个空 home 跑——凭据、插件、
// sessions 全部落空，任务以「未登录」形态失败且原因极难追。
var droppedEnvKeys = map[string]bool{
	"CODEX_HOME": true,
}

// WriteServeScript 在 taskDir 生成 codex app-server 的启动脚本。
//
// 参数：
//   - taskDir: 任务物料目录（须已存在，由调用方保证）
//   - port: app-server 的 WS 监听端口
//   - env: 注入到 app-server 进程的环境变量（形如 KEY=VALUE，已由 manager 展开）；
//     命中 droppedEnvKeys 的条目会被丢弃并打 WARN；非 KEY=VALUE 的条目直接跳过
//
// 返回：脚本绝对路径；写文件失败时返回错误
//
// 注意：
//   - 脚本权限 0600，重复调用幂等覆盖
//   - env 的值一律单引号包裹：Go 侧已展开过一次，不加引号会被 shell 再展开一次，
//     含 $ 的值会变成别的东西（B19）
func WriteServeScript(taskDir string, port int, env []string) (string, error) {
	log := slog.Default()
	serveLog := filepath.Join(taskDir, serveLogName)

	var envLines strings.Builder
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue // 形如 KEY=VALUE 之外的条目直接跳过，不让它污染脚本语法
		}
		if droppedEnvKeys[k] {
			log.Warn("env 文件定义了 handoff 禁止覆盖的变量，已丢弃",
				"key", k, "reason", "codex 复用用户级 ~/.codex，覆盖它会让凭据与插件全部落空")
			continue
		}
		envLines.WriteString("export " + k + "=" + shellq.Quote(v) + "\n")
	}

	script := fmt.Sprintf(`#!/bin/sh
# 由 agentd 生成：codex app-server 启动脚本（0600，勿外泄）。
# 刻意不设 CODEX_HOME——本设计复用用户级 ~/.codex（spec §1.3），凭据零副本。
%sexec codex app-server --listen 'ws://127.0.0.1:%d' >> %s 2>&1
`, envLines.String(), port, shellq.Quote(serveLog))

	p := filepath.Join(taskDir, serveScriptName)
	if err := os.WriteFile(p, []byte(script), 0o600); err != nil {
		log.Error("写 codex 启动脚本失败", "path", p, "cause", err)
		return "", fmt.Errorf("写 codex serve 启动脚本 %s: %w", p, err)
	}
	log.Info("codex serve 启动脚本已生成", "path", p, "port", port)
	return p, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

核对（实现已内置，缺则补）：

- 丢弃 `CODEX_HOME` → `Warn`，带 `key` 与**为什么被丢**的 reason（用户看到这条要能自己解决）
- 写脚本失败 → `Error`，带 `path` + `cause`
- 成功路径不静默 → `Info("codex serve 启动脚本已生成", "path", …, "port", …)`
- **禁止打 env 的值**：只打 key 名（值里可能带凭据，如 `http://user:pass@host`）

- [ ] **Step 6: 加注释**

- 文件头：职责 + 边界，明写「刻意不生成任何 codex 配置文件」及其理由
- `WriteServeScript` doc 注释：参数 / 返回 / 两条注意（0600 幂等、单引号防二次展开）
- `droppedEnvKeys` 上方写清「为什么是丢弃而不是靠顺序覆盖」——这是与 grok 同名机制的实质差异，将来读代码的人一定会问

- [ ] **Step 7: 提交**

```bash
git add internal/executor/codex/taskenv.go internal/executor/codex/taskenv_test.go && git commit -m "feat(codex): 生成 app-server 启动脚本，丢弃 CODEX_HOME 注入"
```

---

### Task 3: 进程生命周期（`proc.go`）

**Files:**
- Create: `internal/executor/codex/proc.go`
- Create: `internal/executor/codex/export_test.go`
- Test: `internal/executor/codex/proc_test.go`

**Interfaces:**
- Consumes: `WriteServeScript`（Task 2）、`serveLogName`/`renderLogName`/`serveInfoName`（Task 2）
- Produces:
  - `type Proc struct { Session string; TaskDir string; Port int }`（JSON tag：`session` / `task_dir` / `port`）
  - `func (p *Proc) WSURL() string`
  - `func (p *Proc) Alive() bool`
  - `func (p *Proc) Kill() error`
  - `func (p *Proc) LogTail() string`
  - `func StartServe(ctx context.Context, repoPath, taskID, taskDir string, env []string, log *slog.Logger) (*Proc, error)`
  - `func ReadServeInfo(taskDir string) (*Proc, error)`
  - 包内测试缝 `var tmuxKill`、`var tmuxHasSession`、`var startServe`
  - `export_test.go`：`func WriteServeInfoForTest(p *Proc) error`、`func SwapTmuxKillForTest(fn func(session string) error) func()`

**与 grok 的两处实质差异（必须在代码注释里写明）：**

1. **没有 Secret 字段**。codex app-server 的 `--listen ws://` 不带鉴权 secret，因此 `serve.json` 不含凭据、`LogTail` 也不需要脱敏替换。仍保持 0600——任务目录里的东西一律 0600，不为个案开口子。
2. **探活是 TCP 连通而不是 HTTP GET**。`--listen ws://` 起的是纯 WS 服务端，没有 HTTP 面可探；判据退化为 `net.DialTimeout("tcp", …)`。这比 grok 弱：端口 listen 住但协议层已死时会误判为活。**真正的健康信号是 WS 连接本身的死亡**（`OnClosed`），Alive 只用于「起没起来」与看门狗的粗判——这条限制要写进注释，避免后人误以为 Alive 是强判据。

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/codex/proc_test.go`：

```go
package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
)

func TestWSURLHasNoSecret(t *testing.T) {
	p := &codex.Proc{Session: "handoff-abc12345", TaskDir: t.TempDir(), Port: 47777}
	if got := p.WSURL(); got != "ws://127.0.0.1:47777" {
		t.Fatalf("WSURL = %s", got)
	}
}

func TestServeInfoRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := &codex.Proc{Session: "handoff-abc12345", TaskDir: dir, Port: 47777}
	if err := codex.WriteServeInfoForTest(p); err != nil {
		t.Fatalf("write serve info: %v", err)
	}
	fi, err := os.Stat(filepath.Join(dir, "serve.json"))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("serve.json 权限 = %v，应为 0600", fi.Mode().Perm())
	}
	got, err := codex.ReadServeInfo(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got.Session != p.Session || got.Port != p.Port || got.TaskDir != dir {
		t.Fatalf("回读不一致: %+v", got)
	}
}

func TestLogTailReadsServeLog(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "serve.log"),
		[]byte("boot line\nlisten failed: address already in use\n"), 0o600); err != nil {
		t.Fatalf("seed log: %v", err)
	}
	p := &codex.Proc{Session: "s", TaskDir: dir, Port: 1}
	if !strings.Contains(p.LogTail(), "address already in use") {
		t.Fatalf("LogTail 没带上可行动真因: %q", p.LogTail())
	}
}

func TestKillTreatsMissingSessionAsClean(t *testing.T) {
	restore := codex.SwapTmuxKillForTest(func(session string) error {
		return os.ErrNotExist // 模拟 tmux kill-session 失败
	})
	defer restore()
	p := &codex.Proc{Session: "handoff-nonexistent", TaskDir: t.TempDir(), Port: 1}
	// 会话本就不存在时，Kill 视为已清理，不报错（B20：回收要幂等）
	if err := p.Kill(); err != nil {
		t.Fatalf("Kill 应把「会话不存在」视为已清理，实得: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run 'TestWSURL|TestServeInfo|TestLogTail|TestKill' -v`
Expected: FAIL —— `undefined: codex.Proc`

- [ ] **Step 3: 实现 `proc.go`**

```go
// proc.go —— codex app-server 的进程生命周期：tmux 托管、探活、恢复凭据落盘。
//
// 职责：
//   - StartServe：选空闲端口、写启动脚本、tmux 起 app-server、开 render tail 窗口、
//     探活等就绪、落 serve.json
//   - Alive/Kill/LogTail：存活探测、回收、诊断尾部
//   - ReadServeInfo：从 serve.json 重建 Proc，供 agentd 重启后 Resume（B18）
//
// 边界：
//   - 不说协议、不解析事件：协议在 appserver.go，语义在 adapter.go
//   - 不做重试决策：探活失败只如实返回，重试与判死节奏归 adapter 的看门狗
//
// 为什么没有 Secret 字段（与 grok 不同）：`codex app-server --listen ws://` 不带
// 鉴权 secret，serve.json 里没有凭据，LogTail 也不需要脱敏。仍写 0600——任务目录
// 里的文件一律 0600，不为个案开口子。
//
// 为什么存活判据是 TCP 连通而不是 HTTP GET（与 grok 不同）：`--listen ws://` 起的
// 是纯 WebSocket 服务端，没有 HTTP 面可探。这条判据比 grok 弱——端口 listen 住但
// 协议层已死时会误判为活。**真正的健康信号是 WS 连接自身的死亡**（Handler.OnClosed），
// Alive 只用于「起没起来」和看门狗的粗判，不要把它当强判据用。
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/executor/turn"
	"github.com/xushixin/handoff/internal/shellq"
)

const (
	serveReadyTimeout  = 20 * time.Second // 大于 grok 的 15s：codex 冷启动要加载插件与 skills
	serveProbeInterval = 200 * time.Millisecond
	serveProbeDialTO   = 2 * time.Second
	serveLogTailBytes  = 4 << 10
	serveLogTailRunes  = 500
)

// Proc 是一个 codex app-server 实例的句柄与恢复凭据。
type Proc struct {
	Session string `json:"session"`  // tmux 会话名 handoff-<id8>
	TaskDir string `json:"task_dir"` // 任务目录
	Port    int    `json:"port"`
}

// WSURL 返回 app-server 的 WebSocket 端点。
//
// 注意：形态由 Task 1 的 V-5 探针实测确认；若实测形态带路径，改这里一处即可。
func (p *Proc) WSURL() string {
	return fmt.Sprintf("ws://127.0.0.1:%d", p.Port)
}

// Alive 探测 app-server 是否仍在监听（TCP 能连上即算活）。
//
// 注意：判据弱于 grok 的 HTTP 探活，见文件头说明——端口活着不等于协议层活着。
func (p *Proc) Alive() bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p.Port), serveProbeDialTO)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// tmuxKill 是 tmux kill-session 的测试缝：测试替换它断言回收的会话名，绕开真实 tmux。
var tmuxKill = func(session string) error {
	return exec.Command("tmux", "kill-session", "-t", session).Run()
}

// tmuxHasSession 是 tmux has-session 探活的测试缝。
var tmuxHasSession = func(session string) bool {
	return exec.Command("tmux", "has-session", "-t", session).Run() == nil
}

// Kill 杀掉 tmux 会话回收 app-server；会话已不存在视为已清理，不报错（B20：回收幂等）。
func (p *Proc) Kill() error {
	err := tmuxKill(p.Session)
	if err != nil {
		if !tmuxHasSession(p.Session) {
			slog.Default().Info("codex tmux 会话已不存在，视为已清理", "session", p.Session)
			return nil
		}
		slog.Default().Error("codex tmux 会话回收失败", "session", p.Session, "cause", err)
		return fmt.Errorf("kill tmux 会话 %s: %w (%s)", p.Session, err, tmuxKillErrTail(err))
	}
	slog.Default().Info("codex tmux 会话已回收", "session", p.Session)
	return nil
}

// tmuxKillErrTail 提取 kill 错误的 stderr 尾部，让失败原因可行动（B16）。
func tmuxKillErrTail(err error) string {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return strings.TrimSpace(string(exitErr.Stderr))
	}
	return err.Error()
}

// LogTail 返回 serve.log 尾部，供启动超时与死亡诊断（B16：失败要给可行动真因）。
func (p *Proc) LogTail() string {
	b, err := os.ReadFile(filepath.Join(p.TaskDir, serveLogName))
	if err != nil {
		return ""
	}
	if len(b) > serveLogTailBytes {
		b = b[len(b)-serveLogTailBytes:]
	}
	return turn.TailRunes(string(b), serveLogTailRunes)
}

// startServe 是 StartServe 的测试缝：冷恢复测试替换它断言「起 serve」是否被调用。
var startServe = StartServe

// StartServe 起一个任务专属的 codex app-server 并等其就绪。
//
// 参数：
//   - ctx: 控制启动阶段的超时/取消
//   - repoPath: 任务工作目录（tmux 会话的 cwd）
//   - taskID: 任务 ID（取前 8 字符作会话名后缀）
//   - taskDir: 任务物料目录
//   - env: 注入到 app-server 进程的环境变量（B19）
//   - log: 日志入口（nil 退回 slog.Default()）
//
// 返回：就绪的 Proc；任一步失败返回错误（错误携带 serve.log 尾部）
//
// 注意：**没有 model 参数**（与 grok 不同）——codex 的模型选择是协议级的
// （thread/start 的 model 字段），不经启动脚本。
func StartServe(ctx context.Context, repoPath, taskID, taskDir string, env []string, log *slog.Logger) (*Proc, error) {
	if log == nil {
		log = slog.Default()
	}
	start := time.Now()
	log.Info("codex app-server 启动中", "task", taskID, "repo", repoPath, "task_dir", taskDir)

	port, err := freePort()
	if err != nil {
		return nil, err
	}

	// env 注入（B19）：只打 key 名不打值——值里可能带凭据。
	if len(env) > 0 {
		keys := make([]string, 0, len(env))
		for _, kv := range env {
			if k, _, ok := strings.Cut(kv, "="); ok {
				keys = append(keys, k)
			}
		}
		log.Info("注入 env 变量到 codex app-server 进程", "task", taskID, "keys", keys, "count", len(keys))
	}

	scriptPath, err := WriteServeScript(taskDir, port, env)
	if err != nil {
		return nil, err
	}

	p := &Proc{Session: "handoff-" + id8(taskID), TaskDir: taskDir, Port: port}
	args := []string{"new-session", "-d", "-s", p.Session, "-c", repoPath,
		"sh " + shellq.Quote(scriptPath)}
	if out, err := exec.Command("tmux", args...).CombinedOutput(); err != nil {
		log.Error("codex tmux 启动失败", "task", taskID, "cause", err, "out", strings.TrimSpace(string(out)))
		return nil, fmt.Errorf("tmux 启动 codex app-server: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	startRenderTailWindow(p.Session, taskDir, log)

	deadline := time.Now().Add(serveReadyTimeout)
	for time.Now().Before(deadline) {
		if p.Alive() {
			if err := writeServeInfo(p); err != nil {
				log.Warn("写 serve.json 失败，Resume 将不可用", "task", taskID, "cause", err)
			}
			log.Info("codex app-server 就绪", "task", taskID, "port", port,
				"elapsed_ms", time.Since(start).Milliseconds())
			return p, nil
		}
		select {
		case <-ctx.Done():
			_ = p.Kill()
			return nil, ctx.Err()
		case <-time.After(serveProbeInterval):
		}
	}
	tail := p.LogTail()
	_ = p.Kill() // 清理残留，不留孤儿进程
	log.Error("codex app-server 就绪超时", "task", taskID, "timeout", serveReadyTimeout, "log_tail", tail)
	return nil, fmt.Errorf("codex app-server %s 内未就绪: %s", serveReadyTimeout, tail)
}

// startRenderTailWindow 在会话内开第二窗口 tail -f render.log（回合实况）。
//
// 稳健做法：先 touch render.log 再开窗口——tail -f 对不存在的文件会立即报错退出。
// 窗口启动失败只 Warn 不阻断：这是增强型可见性，不值得为它挂掉任务启动。
func startRenderTailWindow(session, taskDir string, log *slog.Logger) {
	p := filepath.Join(taskDir, renderLogName)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		log.Warn("创建 render.log 失败，tmux 第二窗口不可用", "session", session, "cause", err)
		return
	}
	f.Close()
	if err := exec.Command("tmux", "new-window", "-t", session,
		"tail -f "+shellq.Quote(p)).Run(); err != nil {
		log.Warn("tmux 第二窗口启动失败（tail render.log 不可用），不影响主流程",
			"session", session, "cause", err)
	}
}

// writeServeInfo 落恢复凭据（0600：与任务目录内其他文件同档）。
func writeServeInfo(p *Proc) error {
	b, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 serve.json: %w", err)
	}
	path := filepath.Join(p.TaskDir, serveInfoName)
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("写 %s: %w", path, err)
	}
	return nil
}

// ReadServeInfo 从任务目录读回 Proc，供 agentd 重启后 Resume（B18）。
func ReadServeInfo(taskDir string) (*Proc, error) {
	b, err := os.ReadFile(filepath.Join(taskDir, serveInfoName))
	if err != nil {
		return nil, fmt.Errorf("读 serve.json: %w", err)
	}
	var p Proc
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("解析 serve.json: %w", err)
	}
	p.TaskDir = taskDir // 目录可能被整体搬动，以实参为准
	return &p, nil
}

// freePort 让内核分配一个空闲回环端口。
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, fmt.Errorf("分配空闲端口: %w", err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// id8 取字符串前 8 字符作 tmux 会话名后缀（与另三个 adapter 同规则，attach 零改动）。
func id8(s string) string {
	if len(s) <= 8 {
		return s
	}
	return s[:8]
}
```

创建 `internal/executor/codex/export_test.go`：

```go
// export_test.go —— 包内实现细节的测试缝。
//
// 职责：把 unexported 的构造/替换点暴露给同包外的 _test 包，避免为测试改可见性。
// 边界：仅测试构建时编译（_test.go 后缀），不进生产二进制。
package codex

// WriteServeInfoForTest 暴露 writeServeInfo，供 serve.json 回环测试。
func WriteServeInfoForTest(p *Proc) error { return writeServeInfo(p) }

// SwapTmuxKillForTest 替换 tmux kill 测试缝，返回还原函数。
func SwapTmuxKillForTest(fn func(session string) error) func() {
	old := tmuxKill
	oldHas := tmuxHasSession
	tmuxKill = fn
	tmuxHasSession = func(string) bool { return false } // 配套：让「会话不存在」成立
	return func() { tmuxKill = old; tmuxHasSession = oldHas }
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

核对：

- 进入关键操作：`StartServe` 开头 `Info(..., "task", "repo", "task_dir")`
- 外部调用前后：tmux 启动失败 `Error` 带 `out`；就绪成功 `Info` 带 `port` + `elapsed_ms`；就绪超时 `Error` 带 `timeout` + `log_tail`
- 每个错误分支带上下文：`Kill` 失败 `Error` 带 `session` + `cause`；写 serve.json 失败 `Warn` 并**明说后果**（"Resume 将不可用"）
- 状态变更：会话回收成功 / 会话已不存在 → `Info`
- 成功路径不静默：`codex app-server 就绪` 必打
- env 注入只打 key 名，**绝不打值**

- [ ] **Step 6: 加注释**

- 文件头：职责 + 边界 + **两条「为什么与 grok 不同」**（无 Secret、TCP 探活弱判据）
- 导出符号 doc 注释：`Proc` / `WSURL`（注明形态由 V-5 实测确认）/ `Alive`（注明判据弱）/ `Kill` / `LogTail` / `StartServe`（含「没有 model 参数」的理由）/ `ReadServeInfo`
- 行内「为什么」：`startRenderTailWindow` 先 touch 再 tail、超时后 `Kill` 清残留、`ReadServeInfo` 以实参 taskDir 为准

- [ ] **Step 7: 提交**

```bash
git add internal/executor/codex/proc.go internal/executor/codex/proc_test.go internal/executor/codex/export_test.go && git commit -m "feat(codex): app-server 的 tmux 进程生命周期与就绪探活"
```

---

### Task 4: ThreadItem 结构与有界索引（`items.go`）

**Files:**
- Create: `internal/executor/codex/items.go`
- Test: `internal/executor/codex/items_test.go`

**Interfaces:**
- Consumes: 无
- Produces（均为 unexported，供同包使用；测试经 `export_test.go` 暴露）：
  - `type threadItem struct { Type, ID, Text, Command, Cwd, Status, AggregatedOutput string; ExitCode *int; CommandActions []commandAction; Changes []fileUpdateChange }`
  - `type commandAction struct { Type, Command, Path string }`
  - `type fileUpdateChange struct { Path string; Kind changeKind }` / `type changeKind struct { Type string }`
  - `func parseItemNotification(params json.RawMessage) (*threadItem, bool)`
  - `func newItemIndex(capacity int) *itemIndex`
  - `func (x *itemIndex) put(it *threadItem)` / `func (x *itemIndex) get(id string) (*threadItem, bool)`
  - `func (it *threadItem) renderLine() string`
  - 常量 `itemIndexCap = 512`
- `export_test.go` 追加：`func NewItemIndexForTest(n int) *ItemIndexHandle` 形态的薄封装（见 Step 3）

**这个文件为什么存在**：spec §5.4 实证 `item/fileChange/requestApproval` 的报文里**没有路径**（必填字段只有 `itemId`/`threadId`/`turnId`/`startedAtMs`）。路径在同 `itemId` 的 `item/started` 通知的 `item.changes[].path` 里。没有这张索引，写文件类权限门就只能交出一个没有路径的 `PermRequest`，B27 的路径判据直接失效。

**为什么有界**：索引条目由 codex 侧的 item 数量决定，一个长任务可以产出上万条。上限 512 条、超了淘汰最旧的——权限请求总是紧跟在对应 item 之后到达，512 条的窗口足够宽；无界会让长任务的内存随 item 数线性涨。

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/codex/items_test.go`：

```go
package codex_test

import (
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
)

func TestParseItemNotificationExtractsFileChangePaths(t *testing.T) {
	raw := []byte(`{"threadId":"t1","item":{
		"type":"fileChange","id":"patch-1",
		"changes":[{"path":"/w/a.go","kind":{"type":"update"}},
		           {"path":"/w/b.go","kind":{"type":"add"}}]}}`)
	it, ok := codex.ParseItemNotificationForTest(raw)
	if !ok {
		t.Fatal("应解析成功")
	}
	if it.Type != "fileChange" || it.ID != "patch-1" || len(it.Changes) != 2 {
		t.Fatalf("解析结果不对: %+v", it)
	}
	if it.Changes[0].Path != "/w/a.go" || it.Changes[0].Kind.Type != "update" {
		t.Fatalf("changes[0] = %+v", it.Changes[0])
	}
	if it.Changes[1].Kind.Type != "add" {
		t.Fatalf("changes[1].kind = %+v", it.Changes[1].Kind)
	}
}

func TestParseItemNotificationRejectsGarbage(t *testing.T) {
	for _, raw := range []string{``, `{}`, `{"item":{}}`, `not json`, `{"item":{"id":"x"}}`} {
		if it, ok := codex.ParseItemNotificationForTest([]byte(raw)); ok {
			t.Fatalf("垃圾输入 %q 不应解析成功，实得 %+v", raw, it)
		}
	}
}

func TestItemIndexEvictsOldestBeyondCap(t *testing.T) {
	idx := codex.NewItemIndexForTest(2)
	idx.PutForTest("a", "fileChange")
	idx.PutForTest("b", "fileChange")
	idx.PutForTest("c", "fileChange")
	if _, ok := idx.GetForTest("a"); ok {
		t.Fatal("超出上限后最旧条目应被淘汰")
	}
	if _, ok := idx.GetForTest("b"); !ok {
		t.Fatal("b 应还在")
	}
	if _, ok := idx.GetForTest("c"); !ok {
		t.Fatal("c 应还在")
	}
}

func TestItemIndexPutSameIDUpdatesInPlace(t *testing.T) {
	idx := codex.NewItemIndexForTest(2)
	idx.PutForTest("a", "commandExecution")
	idx.PutForTest("a", "commandExecution") // item/started → item/completed 会重复投递同一 id
	idx.PutForTest("b", "fileChange")
	if _, ok := idx.GetForTest("a"); !ok {
		t.Fatal("同 id 重复 put 不得占两个槽位把自己挤掉")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run TestItem -v`
Expected: FAIL —— `undefined: codex.ParseItemNotificationForTest`

- [ ] **Step 3: 实现 `items.go` 并补测试缝**

创建 `internal/executor/codex/items.go`：

```go
// items.go —— codex ThreadItem 的结构定义与 itemId → item 的有界索引。
//
// 职责：
//   - 解析 item/started 与 item/completed 通知里的 item 本体
//   - 维护 itemId → 最近一次 item 的有界索引
//   - 把 item 渲染成 render.log 的一行人读文本
//
// 边界：
//   - 不产 handoff 事件、不做权限判据：判据在 perm.go，事件在 adapter.go
//
// 为什么需要索引：`item/fileChange/requestApproval` 的报文**没有路径**（schema 的
// 必填字段只有 itemId/threadId/turnId/startedAtMs，spec §5.4），路径只在同 itemId 的
// item 通知的 changes[].path 里。没有这张索引，写文件类权限门交出的 PermRequest
// 就没有路径，B27 的路径判据直接失效。
//
// 为什么有界：item 数量由 codex 侧决定，长任务可产出上万条。权限请求总是紧跟在
// 对应 item 之后到达，512 条窗口足够宽；无界会让内存随 item 数线性增长。
package codex

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

// itemIndexCap 是索引容量上限，超出后淘汰最旧条目。
const itemIndexCap = 512

// changeKind 是 fileChange 的变更类型。
//
// 注意：schema 里它是**对象** {"type":"add"|"delete"|"update"} 而不是裸字符串，
// 按字符串解析会静默得到空值，进而让 Task 5 的工具分类全部退化成 write。
type changeKind struct {
	Type string `json:"type"`
}

// fileUpdateChange 是 fileChange item 里的一条文件变更。
type fileUpdateChange struct {
	Path string     `json:"path"`
	Kind changeKind `json:"kind"`
}

// commandAction 是 commandExecution 的结构化动作（codex 自己给出的动作类型与路径）。
//
// 注意：本期**不改权限判据**（spec §9），这里只做保留与展示；它是后续替换正则
// 判据的更可靠输入。
type commandAction struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Path    string `json:"path"`
}

// threadItem 是 codex 一条 ThreadItem 的宽松视图。
//
// 注意：字段取并集而非按 type 分结构——codex 的 item 类型会增长，宽松视图让
// 未知类型也能落进索引与 render.log，不至于整条丢弃。
type threadItem struct {
	Type             string             `json:"type"`
	ID               string             `json:"id"`
	Text             string             `json:"text"`
	Command          string             `json:"command"`
	Cwd              string             `json:"cwd"`
	Status           string             `json:"status"`
	ExitCode         *int               `json:"exitCode"`
	AggregatedOutput string             `json:"aggregatedOutput"`
	CommandActions   []commandAction    `json:"commandActions"`
	Changes          []fileUpdateChange `json:"changes"`
}

// parseItemNotification 从 item/started、item/completed 的 params 里取出 item 本体。
//
// 参数：
//   - params: 通知的 params 原文
//
// 返回：
//   - item 与 true；params 不是合法 item 通知（缺 item、缺 id 或缺 type）时返回 nil, false
//
// 注意：解析失败一律返回 false 而不是半成品——半成品会让索引里存进一条没有路径
// 的 fileChange，权限门据此交出空 Paths，比查不到更危险（查不到会 fail-closed）。
func parseItemNotification(params json.RawMessage) (*threadItem, bool) {
	var env struct {
		Item *threadItem `json:"item"`
	}
	if err := json.Unmarshal(params, &env); err != nil || env.Item == nil {
		return nil, false
	}
	if env.Item.ID == "" || env.Item.Type == "" {
		return nil, false
	}
	return env.Item, true
}

// itemIndex 是 itemId → 最近一次 item 的有界索引（FIFO 淘汰）。并发安全。
type itemIndex struct {
	mu    sync.Mutex
	cap   int
	order []string
	m     map[string]*threadItem
}

// newItemIndex 建一个容量为 capacity 的索引（capacity <= 0 时退回 itemIndexCap）。
func newItemIndex(capacity int) *itemIndex {
	if capacity <= 0 {
		capacity = itemIndexCap
	}
	return &itemIndex{cap: capacity, m: map[string]*threadItem{}}
}

// put 写入或更新一条 item。
//
// 注意：同 id 重复写入只更新内容、不占新槽位——item/started 与 item/completed 会
// 对同一个 id 各投递一次，占两个槽位等于把索引窗口砍半。
func (x *itemIndex) put(it *threadItem) {
	if it == nil || it.ID == "" {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, exists := x.m[it.ID]; !exists {
		x.order = append(x.order, it.ID)
		for len(x.order) > x.cap {
			oldest := x.order[0]
			x.order = x.order[1:]
			delete(x.m, oldest)
		}
	}
	x.m[it.ID] = it
}

// get 取一条 item；不存在时返回 nil, false（调用方据此 fail-closed）。
func (x *itemIndex) get(id string) (*threadItem, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	it, ok := x.m[id]
	return it, ok
}

// renderLine 把 item 渲染成 render.log 的一行人读文本。
//
// 注意：审核者 `handoff attach` 看到的就是这行，跨 executor 要同形——
// 命令带 cwd、文件变更带路径清单、模型消息取正文。
func (it *threadItem) renderLine() string {
	switch it.Type {
	case "commandExecution":
		s := "【命令】" + strings.TrimSpace(it.Command)
		if it.Cwd != "" {
			s += "  (cwd: " + it.Cwd + ")"
		}
		if it.ExitCode != nil {
			s += fmt.Sprintf("  → exit %d", *it.ExitCode)
		}
		return s
	case "fileChange":
		paths := make([]string, 0, len(it.Changes))
		for _, c := range it.Changes {
			paths = append(paths, c.Kind.Type+" "+c.Path)
		}
		return "【文件变更】" + strings.Join(paths, ", ")
	case "agentMessage":
		return strings.TrimSpace(it.Text)
	case "reasoning":
		return "【推理】" + strings.TrimSpace(it.Text)
	default:
		if s := strings.TrimSpace(it.Text); s != "" {
			return "【" + it.Type + "】" + s
		}
		return "【" + it.Type + "】"
	}
}
```

追加到 `internal/executor/codex/export_test.go`：

```go
// ParseItemNotificationForTest 暴露 item 通知解析。
func ParseItemNotificationForTest(raw []byte) (*ThreadItemView, bool) {
	it, ok := parseItemNotification(raw)
	if !ok {
		return nil, false
	}
	return &ThreadItemView{it}, true
}

// ThreadItemView 是 threadItem 的只读测试视图。
type ThreadItemView struct{ it *threadItem }

func (v *ThreadItemView) Type() string                 { return v.it.Type }
func (v *ThreadItemView) ID() string                   { return v.it.ID }
func (v *ThreadItemView) Changes() []fileUpdateChange  { return v.it.Changes }
func (v *ThreadItemView) RenderLine() string           { return v.it.renderLine() }

// ItemIndexHandle 是 itemIndex 的测试封装。
type ItemIndexHandle struct{ x *itemIndex }

// NewItemIndexForTest 建一个指定容量的索引。
func NewItemIndexForTest(n int) *ItemIndexHandle { return &ItemIndexHandle{newItemIndex(n)} }

func (h *ItemIndexHandle) PutForTest(id, typ string) {
	h.x.put(&threadItem{ID: id, Type: typ})
}

func (h *ItemIndexHandle) GetForTest(id string) (*ThreadItemView, bool) {
	it, ok := h.x.get(id)
	if !ok {
		return nil, false
	}
	return &ThreadItemView{it}, true
}
```

测试里 `it.Type` / `it.ID` / `it.Changes` 相应改为方法调用 `it.Type()` / `it.ID()` / `it.Changes()`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

本文件是纯数据结构，**不打常规日志**（每条 item 一行日志会淹掉 agentd.log）。唯一要打的是**异常静默点**——`parseItemNotification` 返回 false 时，由调用方（Task 6 的 handler）打一条 `Debug("codex item 通知解析失败，跳过", "params_len", len(params))`。在本 task 的 Step 5 里明确记下这条约定，Task 6 落实。

理由：`instrumenting-code` 要求「成功路径不静默」，但这里的成功路径是每秒数十次的热路径，规范本身允许降级到 Debug 或不打；真正需要可观测的是**解析失败**，因为它会导致后续 fileChange 权限门 fail-closed 升级人工，审核者需要能查到原因。

- [ ] **Step 6: 加注释**

- 文件头：职责 + 边界 + **索引存在的理由**（fileChange 报文无路径）+ **有界的理由**
- `changeKind` 上方写明「schema 里是对象不是字符串」——这是最容易踩的一处，踩了会静默退化
- `threadItem` 上方写明「字段取并集而非按 type 分结构」的理由
- `parseItemNotification` doc 注释含「失败一律返回 false 而不是半成品」的理由
- `put` doc 注释含「同 id 不占新槽位」的理由

- [ ] **Step 7: 提交**

```bash
git add internal/executor/codex/items.go internal/executor/codex/items_test.go internal/executor/codex/export_test.go && git commit -m "feat(codex): ThreadItem 结构与 itemId 有界索引"
```

---

### Task 5: 权限判据、挂起表与裁决映射（`perm.go`）

**Files:**
- Create: `internal/executor/codex/perm.go`
- Test: `internal/executor/codex/perm_test.go`
- Modify: `internal/executor/codex/export_test.go`

**Interfaces:**
- Consumes: `threadItem` / `fileUpdateChange` / `itemIndex`（Task 4）
- Produces:
  - `func decisionFor(decision string) string` —— `"once" → "accept"`，其余 → `"decline"`
  - `type commandApproval struct { ItemID, ThreadID, TurnID, Command, Cwd string; CommandActions []commandAction }`
  - `func parseCommandApproval(params json.RawMessage) (commandApproval, bool)`
  - `func permRequestFromCommand(a commandApproval) *executor.PermRequest`
  - `func permRequestFromFileChange(it *threadItem) *executor.PermRequest`
  - `func commandPermText(a commandApproval) string` / `func fileChangePermText(it *threadItem) string`
  - `type pendingPerm struct { reqID json.RawMessage; desc string }`
  - `type permTable struct{…}`，方法 `note(itemID string, reqID json.RawMessage, desc string)` / `take(itemID string) (pendingPerm, bool)` / `voidAll() int` / `noteRejected(desc string)` / `takeRejected() []string`
  - `func rejectedTurnQuestion(rejected []string) string`
  - 常量 `permTextHardLimit = 64 << 10`
- 供 Task 6：`runState` 内嵌 `*permTable`；`RespondPermission` 与 `PermissionsVolatile` 在 Task 6 追加进本文件

**判据规则（写死，不许自行发挥）：**

| 来源 | `Tool` | `Command` | `Paths` |
|------|--------|-----------|---------|
| `item/commandExecution/requestApproval` | `PermToolBash` | 报文的 `command` **全文不截断** | `commandActions[].path` 里非空的那些 |
| `item/fileChange/requestApproval`，索引命中且**全部** `kind.type == "update"` | `PermToolEdit` | 空 | `changes[].path` |
| `item/fileChange/requestApproval`，索引命中且有非 update | `PermToolWrite` | 空 | `changes[].path` |
| `item/fileChange/requestApproval`，**索引未命中** | —— | —— | 返回 `nil`（manager fail-closed 升级人工） |

**为什么非 update 一律判 write 而不是按具体 kind 细分**：`write` 的爆炸半径比 `edit` 大（新建/删除文件 vs 改已有文件），判据不确定时往大了判。这与 `decisionFor` 的 fail-closed 是同一条原则。

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/codex/perm_test.go`：

```go
package codex_test

import (
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/codex"
)

// 裁决映射：只有 once 放行，其余一律 decline（fail-closed）
func TestDecisionForIsFailClosed(t *testing.T) {
	if got := codex.DecisionForTest("once"); got != "accept" {
		t.Fatalf(`once → %q，应为 "accept"`, got)
	}
	for _, d := range []string{"reject", "", "always", "ONCE", "accept", "cancel"} {
		if got := codex.DecisionForTest(d); got != "decline" {
			t.Fatalf(`%q → %q，应为 "decline"`, d, got)
		}
	}
}

// cancel 会掐掉整个回合，handoff 的 reject 语义是「拒这一次、回合继续」，绝不能用它
func TestDecisionNeverEmitsCancel(t *testing.T) {
	for _, d := range []string{"once", "reject", "cancel", "acceptForSession"} {
		if got := codex.DecisionForTest(d); got == "cancel" {
			t.Fatalf("裁决 %q 映射出了 cancel，会掐掉整个回合", d)
		}
	}
}

func TestPermRequestFromCommandKeepsFullCommand(t *testing.T) {
	long := "echo " + strings.Repeat("x", 5000) + " && rm -rf build"
	raw := []byte(`{"itemId":"exec-1","threadId":"t","turnId":"u",
		"command":` + mustJSON(long) + `,"cwd":"/w",
		"commandActions":[{"type":"read","path":"/w/a.go"},{"type":"unknown","command":"rm -rf build"}]}`)
	a, ok := codex.ParseCommandApprovalForTest(raw)
	if !ok {
		t.Fatal("应解析成功")
	}
	p := codex.PermRequestFromCommandForTest(a)
	if p == nil {
		t.Fatal("命令类权限必须给出结构化 PermRequest")
	}
	if p.Tool != executor.PermToolBash {
		t.Fatalf("Tool = %s", p.Tool)
	}
	if p.Command != long {
		t.Fatalf("命令被改写或截断了（安全判据必须拿到全文），len=%d", len(p.Command))
	}
	if len(p.Paths) != 1 || p.Paths[0] != "/w/a.go" {
		t.Fatalf("Paths = %v，应只收 commandActions 里非空的 path", p.Paths)
	}
}

func TestPermRequestFromFileChangeClassifiesTool(t *testing.T) {
	allUpdate := codex.ThreadItemForTest("patch-1", "fileChange",
		[][2]string{{"/w/a.go", "update"}, {"/w/b.go", "update"}})
	p := codex.PermRequestFromFileChangeForTest(allUpdate)
	if p == nil || p.Tool != executor.PermToolEdit {
		t.Fatalf("全 update 应判 edit，实得 %+v", p)
	}
	if len(p.Paths) != 2 || p.Paths[1] != "/w/b.go" {
		t.Fatalf("Paths = %v", p.Paths)
	}

	mixed := codex.ThreadItemForTest("patch-2", "fileChange",
		[][2]string{{"/w/a.go", "update"}, {"/w/new.go", "add"}})
	p2 := codex.PermRequestFromFileChangeForTest(mixed)
	if p2 == nil || p2.Tool != executor.PermToolWrite {
		t.Fatalf("含非 update 应判 write（爆炸半径更大，往大了判），实得 %+v", p2)
	}
}

// 索引查不到时必须返回 nil，让 manager fail-closed 升级人工——绝不伪造空结构
func TestPermRequestFromFileChangeNilWhenUnknown(t *testing.T) {
	if p := codex.PermRequestFromFileChangeForTest(nil); p != nil {
		t.Fatalf("索引未命中必须返回 nil，实得 %+v", p)
	}
	empty := codex.ThreadItemForTest("patch-3", "fileChange", nil)
	if p := codex.PermRequestFromFileChangeForTest(empty); p != nil {
		t.Fatalf("没有任何 change 时必须返回 nil，实得 %+v", p)
	}
}

// B6：权限文本不为安全而截断，只有超 64KB 硬上限才截
func TestPermTextNotTruncatedForSecurity(t *testing.T) {
	long := strings.Repeat("y", 20000)
	a, _ := codex.ParseCommandApprovalForTest([]byte(
		`{"itemId":"exec-2","command":` + mustJSON(long) + `,"cwd":"/w"}`))
	text := codex.CommandPermTextForTest(a)
	if !strings.Contains(text, long) {
		t.Fatal("20KB 的命令不该被截断——安全门要看全文")
	}
	if strings.Contains(text, executor.TruncationMarker) {
		t.Fatal("未超硬上限不应出现截断标记")
	}

	huge := strings.Repeat("z", 70000)
	a2, _ := codex.ParseCommandApprovalForTest([]byte(
		`{"itemId":"exec-3","command":` + mustJSON(huge) + `,"cwd":"/w"}`))
	text2 := codex.CommandPermTextForTest(a2)
	if !strings.Contains(text2, executor.TruncationMarker) {
		t.Fatal("超 64KB 硬上限必须截断并留标记（防失控）")
	}
}

// 挂起表：取走即移除，作废返回数量
func TestPermTableTakeAndVoid(t *testing.T) {
	tb := codex.NewPermTableForTest()
	tb.NoteForTest("exec-1", []byte("1"), "运行 rm -rf build")
	if _, ok := tb.TakeForTest("exec-1"); !ok {
		t.Fatal("应能取到")
	}
	if _, ok := tb.TakeForTest("exec-1"); ok {
		t.Fatal("取走后不应还在")
	}
	tb.NoteForTest("exec-2", []byte("2"), "d2")
	tb.NoteForTest("exec-3", []byte("3"), "d3")
	if n := tb.VoidAllForTest(); n != 2 {
		t.Fatalf("作废数量 = %d，应为 2", n)
	}
}

// 被拒清单交给审核者的是描述而不是不透明 id
func TestRejectedTurnQuestionShowsDescription(t *testing.T) {
	tb := codex.NewPermTableForTest()
	tb.NoteRejectedForTest("运行 rm -rf /etc")
	got := tb.TakeRejectedForTest()
	if len(got) != 1 {
		t.Fatalf("被拒清单 = %v", got)
	}
	q := codex.RejectedTurnQuestionForTest(got)
	if !strings.Contains(q, "rm -rf /etc") {
		t.Fatalf("问题正文必须含权限描述，实得: %s", q)
	}
	if len(tb.TakeRejectedForTest()) != 0 {
		t.Fatal("取走后应清空，否则下回合会重复上报")
	}
}

func mustJSON(s string) string {
	b, _ := jsonMarshal(s)
	return b
}
```

在 `perm_test.go` 顶部补一个小工具（避免与其他测试文件重名）：

```go
func jsonMarshal(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}
```

并在 import 里加 `"encoding/json"`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run TestPerm -v`
Expected: FAIL —— `undefined: codex.DecisionForTest`

- [ ] **Step 3: 实现 `perm.go`**

```go
// perm.go —— codex 权限请求的判据、挂起表与裁决映射。
//
// 职责：
//   - 把 item/*/requestApproval 的报文翻译成 executor.PermRequest（安全判据的输入）
//   - 维护 itemId → 待裁决请求 的挂起表，供 RespondPermission 回发
//   - 记录本回合被拒清单，回合收尾时一并交代给审核者
//
// 边界：
//   - **不做审批判断**：批不批由 manager 依审核者应答决定（executor 契约的硬边界）
//   - 不写 store、不发事件
//
// 裁决映射只有两个出口（spec §5.4，依据官方 schema）：
//   - accept  —— 放行这一次
//   - decline —— 拒这一次，**回合继续**
//
// 绝不使用 cancel（会立刻掐掉整个回合，等于审核者点一次「拒绝」就杀掉任务，
// 与另三个 adapter 行为不对等），也绝不使用 acceptForSession /
// acceptWithExecpolicyAmendment / applyNetworkPolicyAmendment（都是「以后同类
// 不再问」，正是 B23 明确否掉的语义）。
package codex

import (
	"encoding/json"
	"strings"
	"sync"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

// permTextHardLimit 是权限描述的硬上限（64KB）。
//
// B6 的教训：权限文本**不为美观或安全而截断**——安全门与廉价模型审批者必须看到
// 全文。这个上限只防失控（比如模型生成一条几十 MB 的命令把事件库撑爆）。
const permTextHardLimit = 64 << 10

// decisionFor 把 handoff 的裁决翻译为 codex 的 decision 枚举。
//
// fail-closed：除 "once" 外一律 decline，绝不误放行——误拒的代价是审核者再来一轮，
// 误放的代价可能是不可逆的破坏性操作。
func decisionFor(decision string) string {
	if decision == "once" {
		return "accept"
	}
	return "decline"
}

// commandApproval 是 item/commandExecution/requestApproval 的报文视图。
type commandApproval struct {
	ItemID         string          `json:"itemId"`
	ThreadID       string          `json:"threadId"`
	TurnID         string          `json:"turnId"`
	Command        string          `json:"command"`
	Cwd            string          `json:"cwd"`
	CommandActions []commandAction `json:"commandActions"`
}

// parseCommandApproval 解析命令审批报文。
//
// 返回：报文与 true；JSON 非法或缺 itemId 时返回零值与 false（缺 itemId 就无法
// 回发裁决，登记进挂起表只会制造一张永远应答不了的工单）。
func parseCommandApproval(params json.RawMessage) (commandApproval, bool) {
	var a commandApproval
	if err := json.Unmarshal(params, &a); err != nil || a.ItemID == "" {
		return commandApproval{}, false
	}
	return a, true
}

// permRequestFromCommand 从命令审批报文构造结构化权限请求。
//
// 注意：Command 是**全文不截断**——B23/B27 的判据与廉价模型审批者都依赖它；
// 展示层的长度收口在 commandPermText 里做，两者刻意分离。
func permRequestFromCommand(a commandApproval) *executor.PermRequest {
	if strings.TrimSpace(a.Command) == "" {
		return nil
	}
	var paths []string
	for _, act := range a.CommandActions {
		if act.Path != "" {
			paths = append(paths, act.Path)
		}
	}
	return &executor.PermRequest{
		Tool:    executor.PermToolBash,
		Command: a.Command,
		Paths:   paths,
	}
}

// permRequestFromFileChange 从索引里的 fileChange item 构造结构化权限请求。
//
// 参数：
//   - it: itemId 索引命中的 item；**nil 表示索引未命中**
//
// 返回：
//   - 权限请求；it 为 nil 或没有任何 change 时返回 nil
//
// 注意：返回 nil 不是「无所谓」，而是**明确的 fail-closed 信号**——manager 拿到
// 没有 Perm 的权限事件会升级人工裁决。伪造一个空 Paths 的结构反而更危险：路径
// 判据会以为「没有越界路径」而自动放行（spec §5.4）。
//
// 工具分类：全部 kind.type == "update" 判 edit，只要有一个不是就判 write——
// write 的爆炸半径更大（新建/删除 vs 改已有），不确定时往大了判。
func permRequestFromFileChange(it *threadItem) *executor.PermRequest {
	if it == nil || len(it.Changes) == 0 {
		return nil
	}
	paths := make([]string, 0, len(it.Changes))
	allUpdate := true
	for _, c := range it.Changes {
		paths = append(paths, c.Path)
		if c.Kind.Type != "update" {
			allUpdate = false
		}
	}
	tool := executor.PermToolWrite
	if allUpdate {
		tool = executor.PermToolEdit
	}
	return &executor.PermRequest{Tool: tool, Paths: paths}
}

// commandPermText 渲染命令审批的人读描述（工单正文与被拒清单都用它）。
func commandPermText(a commandApproval) string {
	var b strings.Builder
	b.WriteString("运行命令：" + a.Command)
	if a.Cwd != "" {
		b.WriteString("\n工作目录：" + a.Cwd)
	}
	return turn.TruncateMarked(b.String(), permTextHardLimit)
}

// fileChangePermText 渲染文件变更审批的人读描述。
//
// 注意：it 为 nil（索引未命中）时也要给出可读文本——权限事件仍要发出去让审核者
// 知情，只是 Perm 为 nil 触发 fail-closed。
func fileChangePermText(it *threadItem) string {
	if it == nil {
		return "修改文件（codex 未提供变更清单，已按最保守方式升级人工裁决）"
	}
	var b strings.Builder
	b.WriteString("修改文件：\n")
	for _, c := range it.Changes {
		b.WriteString("  - " + c.Kind.Type + " " + c.Path + "\n")
	}
	return turn.TruncateMarked(b.String(), permTextHardLimit)
}

// pendingPerm 是一条待裁决的权限请求。
type pendingPerm struct {
	reqID json.RawMessage // JSON-RPC 请求 id，回发裁决必需
	desc  string          // 人读描述；被拒时记入被拒清单
}

// permTable 是挂起权限表与本回合被拒清单。并发安全。
type permTable struct {
	mu       sync.Mutex
	pending  map[string]pendingPerm
	rejected []string
}

// newPermTable 建一张空表。
func newPermTable() *permTable {
	return &permTable{pending: map[string]pendingPerm{}}
}

// note 登记一个待裁决的权限请求。
//
// 参数：
//   - itemID: codex 的 itemId，manager 经它应答（事件的 PermissionID 与之同名）
//   - reqID: JSON-RPC 请求 id，应答回发必需
//   - desc: 人读描述；拒绝时记入被拒清单，**不用 itemId**
func (t *permTable) note(itemID string, reqID json.RawMessage, desc string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[itemID] = pendingPerm{reqID: reqID, desc: desc}
}

// take 取出并移除挂起项。
func (t *permTable) take(itemID string) (pendingPerm, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pp, ok := t.pending[itemID]
	delete(t.pending, itemID)
	return pp, ok
}

// voidAll 作废全部挂起项，返回作废数量（连接死亡时调用）。
func (t *permTable) voidAll() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(t.pending)
	t.pending = map[string]pendingPerm{}
	return n
}

// noteRejected 记下本回合被拒的权限描述，回合收尾时一并交代给审核者。
func (t *permTable) noteRejected(desc string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rejected = append(t.rejected, desc)
}

// takeRejected 取走并清空本回合的被拒记录。
//
// 注意：必须取走而非读取——不清空会让下一回合重复上报同一批被拒项，
// 审核者会收到一张内容陈旧的工单。
func (t *permTable) takeRejected() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.rejected
	t.rejected = nil
	return out
}

// rejectedTurnQuestion 把被拒清单拼成交给审核者的问题。
//
// 注意：正文里放的是权限**描述**而不是 itemId——被拒清单存在的意义是让审核者
// 知道「模型刚才想干什么、被挡了」，一串不透明 id 等于没说。
func rejectedTurnQuestion(rejected []string) string {
	var b strings.Builder
	b.WriteString("本回合有权限请求被拒，模型可能改用其它做法或停下。被拒清单：\n")
	for _, d := range rejected {
		b.WriteString("  - " + d + "\n")
	}
	b.WriteString("请确认下一步该怎么做。")
	return b.String()
}
```

追加到 `export_test.go`：

```go
// DecisionForTest 暴露裁决映射。
func DecisionForTest(d string) string { return decisionFor(d) }

// ParseCommandApprovalForTest 暴露命令审批报文解析。
func ParseCommandApprovalForTest(raw []byte) (commandApproval, bool) {
	return parseCommandApproval(raw)
}

// PermRequestFromCommandForTest 暴露命令类权限判据。
func PermRequestFromCommandForTest(a commandApproval) *executor.PermRequest {
	return permRequestFromCommand(a)
}

// PermRequestFromFileChangeForTest 暴露文件变更类权限判据。
func PermRequestFromFileChangeForTest(v *ThreadItemView) *executor.PermRequest {
	if v == nil {
		return permRequestFromFileChange(nil)
	}
	return permRequestFromFileChange(v.it)
}

// CommandPermTextForTest 暴露命令审批的人读描述。
func CommandPermTextForTest(a commandApproval) string { return commandPermText(a) }

// ThreadItemForTest 造一个带 changes 的 fileChange item（每项形如 {path, kind}）。
func ThreadItemForTest(id, typ string, changes [][2]string) *ThreadItemView {
	it := &threadItem{ID: id, Type: typ}
	for _, c := range changes {
		it.Changes = append(it.Changes, fileUpdateChange{Path: c[0], Kind: changeKind{Type: c[1]}})
	}
	return &ThreadItemView{it}
}

// PermTableHandle 是 permTable 的测试封装。
type PermTableHandle struct{ t *permTable }

// NewPermTableForTest 建一张空的挂起表。
func NewPermTableForTest() *PermTableHandle { return &PermTableHandle{newPermTable()} }

func (h *PermTableHandle) NoteForTest(id string, reqID []byte, desc string) {
	h.t.note(id, reqID, desc)
}
func (h *PermTableHandle) TakeForTest(id string) (string, bool) {
	pp, ok := h.t.take(id)
	return pp.desc, ok
}
func (h *PermTableHandle) VoidAllForTest() int              { return h.t.voidAll() }
func (h *PermTableHandle) NoteRejectedForTest(desc string)  { h.t.noteRejected(desc) }
func (h *PermTableHandle) TakeRejectedForTest() []string    { return h.t.takeRejected() }

// RejectedTurnQuestionForTest 暴露被拒清单的问题渲染。
func RejectedTurnQuestionForTest(r []string) string { return rejectedTurnQuestion(r) }
```

`export_test.go` 顶部 import 里补 `"github.com/xushixin/handoff/internal/executor"`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS

- [ ] **Step 5: 加关键节点日志**

本文件是判据与数据结构，日志由调用方（Task 6 的 handler / RespondPermission）承担。本 step 记下 Task 6 必须落实的三条：

- 收到权限请求 → `Info("codex 权限请求", "task", …, "perm", itemID, "tool", p.Tool)`（**不打命令全文**，全文进的是工单正文；agentd.log 里打全文会把日志刷爆且可能带敏感串）
- `permRequestFromFileChange` 返回 nil → `Warn("codex fileChange 权限缺变更清单，已 fail-closed 升级人工", "task", …, "perm", itemID)`——这是审核者收到「没有路径的工单」时唯一能查到原因的地方
- 回发裁决前后 → `Info("回发权限裁决", …, "decision", …)` + `Info("权限裁决已送达 executor", …)`；失败 `Error` 带 `cause`

- [ ] **Step 6: 加注释**

- 文件头：职责 + 边界（**不做审批判断**）+ 裁决只有两个出口 + **为什么绝不用 cancel / acceptForSession**
- `permTextHardLimit` 上方写明 B6 的教训（不为安全而截断）
- `decisionFor` / `permRequestFromFileChange` 的 fail-closed 理由各一段
- `permRequestFromFileChange` 里写明「返回 nil 是明确信号，伪造空 Paths 更危险」
- `takeRejected` 写明「必须取走否则下回合重复上报」
- `rejectedTurnQuestion` 写明「放描述不放 id」

- [ ] **Step 7: 提交**

```bash
git add internal/executor/codex/perm.go internal/executor/codex/perm_test.go internal/executor/codex/export_test.go && git commit -m "feat(codex): 权限判据、挂起表与 fail-closed 裁决映射"
```

- [ ] **Step 8: 真机验 V-3（文件系统越界是否真产工单）**

spec §6 的 V-3 是 §2.1 安全论证的**唯一实证支点**（网络那条已实证不产工单，不能沿用）。**必须在这一步验，不能推迟**——若结论是「越界写也不产工单」，`on-request` 档就不成立，整个设计要回炉。

复用 spike 的 Python 客户端（`scratchpad/spike.py` + `s4.py` 的形态）写 `v3_fs.py`，用**用户级 home**：

1. `thread/start`：`cwd`=一个临时 repo、`sandbox`=workspace-write、`approvalPolicy`=on-request、`approvalsReviewer`=user
2. `turn/start` 带本设计钉死的四参（`networkAccess:true`、`excludeSlashTmp:true`、`excludeTmpdirEnvVar:true`、`writableRoots:[]`）
3. 指令：只做一件事——`touch /Users/<me>/handoff-v3-probe.txt`（工作区外、且不在 /tmp），然后原样报告输出或报错
4. 所有 `requestApproval` 一律回 `{"decision":"decline"}` 并计数
5. 回合结束后在宿主机 `ls` 确认该文件**不存在**（B27 探针的做法）

判据：

- `approval_count >= 1` **且** 文件不存在 → V-3 通过，§2.1 成立，继续按计划实现
- `approval_count == 0` 且文件**被创建** → 沙箱没拦住，**停止实现并回到 spec**：`on-request` 档不成立，需要重新讨论（候选：改 `untrusted`，代价是每条命令一次审批往返）
- `approval_count == 0` 且文件**没被创建** → 沙箱拦了但不产工单（与网络同形），也要**停止并回到 spec**：这属于「哑失败」，审核者全程不知情，与 §2.1 的论证不符

把结论（含 approval 计数与 `ls` 结果）回写 spec §6 的 V-3 行。

```bash
git add docs/superpowers/specs/2026-08-09-handoff-codex-adapter-design.md && git commit -m "docs(spec): B28 V-3 已验——文件系统越界的工单行为"
```

---

### Task 6: 五动作与事件翻译（`adapter.go`）

**Files:**
- Create: `internal/executor/codex/adapter.go`
- Modify: `internal/executor/codex/perm.go`（追加 reqID→itemID 反查、`RespondPermission`、`PermissionsVolatile`）
- Modify: `internal/executor/codex/appserver.go`（加 `replyHook` 测试缝）
- Modify: `internal/executor/codex/export_test.go`
- Test: `internal/executor/codex/adapter_test.go`

**Interfaces:**
- Consumes: `Dial`/`Client`/`Handler`/`Result`（Task 1）、`StartServe`/`Proc`/`startServe`（Task 3）、`parseItemNotification`/`itemIndex`/`renderLine`（Task 4）、`permTable`/`decisionFor`/`permRequestFrom*`/`*PermText`/`rejectedTurnQuestion`（Task 5）、`turn.RenderPrompt`/`turn.ParseTrailer`/`turn.GitTurnStatus`/`turn.AppendRender`/`turn.TruncateMarked`/`turn.ClampQuestion`
- Produces:
  - `type Adapter struct{…}` / `func New(log *slog.Logger) *Adapter`
  - 五动作：`Start` / `Events` / `Send` / `RespondPermission` / `Stop`（满足 `executor.Adapter`）
  - `func (a *Adapter) PermissionsVolatile() bool`（恒 true）
  - `type runState struct{…}`（内部，供 Task 8 的 Resume 复用）
  - `func (a *Adapter) startTurn(r *runState, text string) error`（内部，Send 与 Resume 复用）
  - `func (a *Adapter) lookup/drop/dropIf/emit/emitFailed`（内部，供 Task 7、8）
  - 协议方法名常量（见实现）

**三处必须写死的设计判断：**

1. **回合边界用 `turnInFlight` 标志，不用 turnId 匹配。** `turn/start` 是异步的（spec §1.1），`turn/completed` 通知**可能先于** `turn/start` 的响应到达；用 turnId 匹配时那一刻 `r.turnID` 还是空的，回合终局会被丢掉，任务永久静止。turnID 只单独留着给 `turn/interrupt` 用。
2. **回合正文从 `item/completed` 且 `type == "agentMessage"` 的 `text` 累积**，不从 delta 拼。每条 `agentMessage` 的 `item/completed` 带的是**完整正文**，delta 只喂 render.log。这样 trailer 解析拿到的永远是完整文本，不会因丢包或乱序拼错。
3. **`item/tool/requestUserInput` 在本 task 里返回 `false`（传输层回 -32601），Task 7 才真正实现。** 这是**刻意的中间态**：-32601 是明确的「不支持」，codex 侧会让那次工具调用失败而不是永久挂起——挂起才是致命的（grok 的教训）。Task 7 必须紧接着做，不允许停在这个中间态交付。

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/codex/adapter_test.go`：

```go
package codex_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/codex"
)

func drain(t *testing.T, ch <-chan executor.AdapterEvent, d time.Duration) []executor.AdapterEvent {
	t.Helper()
	var out []executor.AdapterEvent
	deadline := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
}

// finish trailer → result(OK)，且 branch/commit 以 trailer 为先
func TestFinishTrailerEmitsOKResult(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T1")
	body := "干完了。\n" + strings.Join([]string{
		"HANDOFF_STATUS: finish",
		"HANDOFF_BRANCH: handoff/T1",
		"HANDOFF_COMMIT: abc1234",
		"HANDOFF_SUMMARY: 加了 codex adapter",
	}, "\n")
	codex.FinishTurnForTest(a, r, "completed", "", body)

	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	if len(evs) == 0 {
		t.Fatal("没有产出任何事件")
	}
	last := evs[len(evs)-1]
	if last.Type != "result" || last.Result == nil || !last.Result.OK {
		t.Fatalf("应产出成功结果，实得 %+v", last)
	}
	if last.Result.Branch != "handoff/T1" || last.Result.CommitHash != "abc1234" {
		t.Fatalf("trailer 的 branch/commit 应优先，实得 %+v", last.Result)
	}
}

// 被拒清单优先于 trailer：模型被拒后可能悄悄绕路，人必须知情
func TestRejectedListTakesPriority(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T2")
	codex.NoteRejectedOnRunForTest(r, "运行命令：rm -rf /etc")
	codex.FinishTurnForTest(a, r, "completed", "",
		"HANDOFF_STATUS: finish\nHANDOFF_SUMMARY: 完成")

	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	if len(evs) != 1 || evs[0].Type != "question" {
		t.Fatalf("被拒时应只产出一条 question，实得 %+v", evs)
	}
	if !strings.Contains(evs[0].Text, "rm -rf /etc") {
		t.Fatalf("问题正文必须含被拒描述，实得: %s", evs[0].Text)
	}
}

// 回合 failed → 失败结果带上 codex 给的真因（B16：不许扁平化）
func TestTurnFailedCarriesCause(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T3")
	codex.FinishTurnForTest(a, r, "failed", "model stream aborted", "")

	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	if len(evs) != 1 || evs[0].Result == nil || evs[0].Result.OK {
		t.Fatalf("应产出失败结果，实得 %+v", evs)
	}
	if !strings.Contains(evs[0].Result.FailReason, "model stream aborted") {
		t.Fatalf("失败原因必须带 codex 给的真因，实得: %s", evs[0].Result.FailReason)
	}
}

// 主动停止时收到的 interrupted 不是失败
func TestInterruptedWhileStoppingIsNotFailure(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T4")
	codex.MarkStoppingForTest(r)
	codex.FinishTurnForTest(a, r, "interrupted", "", "")

	for _, ev := range drain(t, codex.EventsForTest(r), 300*time.Millisecond) {
		if ev.Type == "result" && ev.Result != nil && !ev.Result.OK {
			t.Fatalf("主动停止不得产出失败结果，实得 %+v", ev.Result)
		}
	}
}

// fileChange 权限：索引查不到时 Perm 必须为 nil（manager 据此 fail-closed）
func TestFileChangeApprovalFailsClosedWithoutIndexEntry(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T5")
	h := codex.NewHandlerForTest(a, r)
	ok := h.OnServerRequest(json.RawMessage("9"), "item/fileChange/requestApproval",
		json.RawMessage(`{"itemId":"patch-unknown","threadId":"t","turnId":"u"}`))
	if !ok {
		t.Fatal("fileChange 审批必须被本端接管，不能回 -32601")
	}
	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	if len(evs) != 1 || evs[0].Type != "permission" {
		t.Fatalf("应产出一条 permission 事件，实得 %+v", evs)
	}
	if evs[0].PermissionID != "patch-unknown" {
		t.Fatalf("PermissionID 应为 itemId，实得 %s", evs[0].PermissionID)
	}
	if evs[0].Perm != nil {
		t.Fatalf("索引未命中时 Perm 必须为 nil（fail-closed），实得 %+v", evs[0].Perm)
	}
}

// 索引里有 item 时，权限事件带上结构化路径
func TestFileChangeApprovalUsesIndexedPaths(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T6")
	h := codex.NewHandlerForTest(a, r)
	h.OnNotify("item/started", json.RawMessage(
		`{"item":{"type":"fileChange","id":"patch-1","changes":[{"path":"/w/a.go","kind":{"type":"update"}}]}}`))
	ok := h.OnServerRequest(json.RawMessage("3"), "item/fileChange/requestApproval",
		json.RawMessage(`{"itemId":"patch-1","threadId":"t","turnId":"u"}`))
	if !ok {
		t.Fatal("应被接管")
	}
	evs := drain(t, codex.EventsForTest(r), 500*time.Millisecond)
	var perm *executor.AdapterEvent
	for i := range evs {
		if evs[i].Type == "permission" {
			perm = &evs[i]
		}
	}
	if perm == nil || perm.Perm == nil {
		t.Fatalf("应产出带 Perm 的 permission 事件，实得 %+v", evs)
	}
	if perm.Perm.Tool != executor.PermToolEdit || len(perm.Perm.Paths) != 1 ||
		perm.Perm.Paths[0] != "/w/a.go" {
		t.Fatalf("Perm = %+v", perm.Perm)
	}
}

// permissions 升级申请一律 fail-closed：不产权限门，只产 progress
func TestPermissionsEscalationIsFailClosedNotAGate(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T7")
	codex.AttachFakeClientForTest(r) // 这条路径会回发应答，需要一条假连接
	h := codex.NewHandlerForTest(a, r)
	ok := h.OnServerRequest(json.RawMessage("4"), "item/permissions/requestApproval",
		json.RawMessage(`{"itemId":"perm-1","threadId":"t","turnId":"u"}`))
	if !ok {
		t.Fatal("应被接管（否则 codex 侧挂起）")
	}
	for _, ev := range drain(t, codex.EventsForTest(r), 300*time.Millisecond) {
		if ev.Type == "permission" {
			t.Fatalf("沙箱放宽申请绝不能做成可批准的权限门，实得 %+v", ev)
		}
	}
}

// 401 令牌刷新请求 → 任务失败并给出可操作指引
func TestAuthRefreshFailsTaskWithActionableMessage(t *testing.T) {
	a, r := codex.NewAdapterWithRunForTest("T8")
	codex.AttachFakeClientForTest(r) // 这条路径会回发错误应答，需要一条假连接
	h := codex.NewHandlerForTest(a, r)
	if ok := h.OnServerRequest(json.RawMessage("5"), "account/chatgptAuthTokens/refresh",
		json.RawMessage(`{"reason":"unauthorized"}`)); !ok {
		t.Fatal("应被接管（要回错误，不能静默）")
	}
	var failed *executor.Result
	for _, ev := range drain(t, codex.EventsForTest(r), 500*time.Millisecond) {
		if ev.Type == "result" && ev.Result != nil && !ev.Result.OK {
			failed = ev.Result
		}
	}
	if failed == nil {
		t.Fatal("登录态失效必须让任务失败，不能静默继续")
	}
	if !strings.Contains(failed.FailReason, "codex login") {
		t.Fatalf("失败文案必须给出可操作指引，实得: %s", failed.FailReason)
	}
}

// 运行态不存在时三个动作都必须带 ErrTaskNotRunning 哨兵
func TestActionsCarryNotRunningSentinel(t *testing.T) {
	a := codex.New(nil)
	ctx := t.Context()
	if err := a.Send(ctx, "nope", "hi"); !isNotRunning(err) {
		t.Fatalf("Send: %v", err)
	}
	if err := a.RespondPermission(ctx, "nope", "p", "once"); !isNotRunning(err) {
		t.Fatalf("RespondPermission: %v", err)
	}
	if err := a.Stop("nope"); !isNotRunning(err) {
		t.Fatalf("Stop: %v", err)
	}
}

func isNotRunning(err error) bool {
	return err != nil && errorsIs(err, executor.ErrTaskNotRunning)
}
```

在文件顶部 import `"errors"` 并加 `func errorsIs(err, target error) bool { return errors.Is(err, target) }`（保持测试可读，不与其他文件里的辅助函数重名）。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run 'TestFinishTrailer|TestRejectedList|TestTurnFailed|TestInterrupted|TestFileChange|TestPermissionsEscalation|TestAuthRefresh|TestActionsCarry' -v`
Expected: FAIL —— `undefined: codex.NewAdapterWithRunForTest`

- [ ] **Step 3: 实现 `adapter.go`**

```go
// adapter.go —— codex 的 executor.Adapter 实现：五动作与事件翻译。
//
// 职责：
//   - Start/Events/Send/RespondPermission/Stop 五动作
//   - 把 codex 的 ServerNotification / ServerRequest 翻译成 AdapterEvent 四类事件
//   - 回合边界判定与收尾分类（复用 internal/executor/turn 的 trailer 与 git 取证）
//   - 把事件流渲染进 render.log，供 handoff attach 的第二窗口实况显示
//
// 边界：
//   - 不写 store、不做审批判断、不做状态机迁移（executor 契约的硬边界）
//   - 不碰 codex 的配置文件：安全档位全部协议级下发且每回合重钉
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

// progressThrottle 与 opencode/grok 同值：防高频增量刷爆事件库。
const progressThrottle = 30 * time.Second

// 协议方法名。集中成常量，避免散落的字面量拼错后静默不生效。
const (
	methodInitialize    = "initialize"
	methodInitialized   = "initialized"
	methodThreadStart   = "thread/start"
	methodThreadResume  = "thread/resume"
	methodTurnStart     = "turn/start"
	methodTurnInterrupt = "turn/interrupt"

	ntfTurnCompleted     = "turn/completed"
	ntfItemStarted       = "item/started"
	ntfItemCompleted     = "item/completed"
	ntfThreadStatus      = "thread/status/changed"
	ntfRateLimits        = "account/rateLimits/updated"
	ntfServerReqResolved = "serverRequest/resolved"

	reqCommandApproval     = "item/commandExecution/requestApproval"
	reqFileChangeApproval  = "item/fileChange/requestApproval"
	reqPermissionsApproval = "item/permissions/requestApproval"
	reqUserInput           = "item/tool/requestUserInput"
	reqAuthRefresh         = "account/chatgptAuthTokens/refresh"
)

// deltaNotifications 是只喂 render.log、不产 handoff 事件的高频通知。
//
// 为什么不产事件：这些通知每秒可达数十条，逐条产事件会把事件库刷爆，
// 且 wait 的游标语义（B22）会被无意义的增量淹没。
var deltaNotifications = map[string]bool{
	"item/agentMessage/delta":            true,
	"item/reasoning/textDelta":           true,
	"item/reasoning/summaryTextDelta":    true,
	"item/commandExecution/outputDelta":  true,
}

// sandboxPolicy 是每回合显式钉死的沙箱策略（spec §2 / §2.2）。
//
// 为什么每回合都传而不是只在 thread/start 传一次：thread/start 钉过的值会被
// thread/resume 或任何一次带覆盖的 turn/start 改掉，而恢复路径正是最容易漏钉的
// 地方（B18 的教训）。每回合重钉是一次固定成本的幂等操作，换来「任何一个回合
// 都不可能跑在开发机 config 的档位上」。
//
// networkAccess 为 true 是 2026-08-09 用户的明确决定（spec §2.2）：executor 跑在
// 专用开发机上，网络面本来就敞着；反方向的代价是实的——关掉后装依赖会失败，
// 且实证拒网**不产工单**，属于审核者不知情的哑失败。
func sandboxPolicy() map[string]any {
	return map[string]any{
		"type":                "workspaceWrite",
		"networkAccess":       true,
		"excludeSlashTmp":     true,
		"excludeTmpdirEnvVar": true,
		"writableRoots":       []any{},
	}
}

// Adapter 是 codex 的 executor.Adapter 实现。
//
// 并发安全：runs 表由 mu 保护；每个任务的运行态只被该任务自己的回调路径访问。
type Adapter struct {
	log  *slog.Logger
	mu   sync.Mutex
	runs map[string]*runState
}

// New 创建 codex adapter。
//
// 参数：
//   - log: 本模块日志入口（nil 时退回 slog.Default()）
func New(log *slog.Logger) *Adapter {
	if log == nil {
		log = slog.Default()
	}
	return &Adapter{log: log, runs: make(map[string]*runState)}
}

// runState 是单任务运行的完整状态。
type runState struct {
	taskID      string
	taskDir     string
	repoPath    string
	threadID    string // == sessionId，落 task.ExecutorSession
	startCommit string

	proc *Proc
	cli  *Client

	*permTable
	items *itemIndex

	// stopping 是主动停止标记：Stop 先置位再关连接，onClosed 与回合收尾据此
	// 知道这是用户主动停止而非执行失败，不产出假的失败结果
	stopping bool

	evCh     chan executor.AdapterEvent
	emitMu   sync.Mutex
	evClosed bool

	turnMu       sync.Mutex
	turnInFlight bool
	turnID       string // 仅供 turn/interrupt 使用，不参与回合边界判定
	bodyBuf      strings.Builder
	renderBuf    strings.Builder
	lastProgress time.Time
	askedViaTool bool
}

// newRunState 建一条运行态。
func newRunState(taskID, taskDir, repoPath string) *runState {
	return &runState{
		taskID: taskID, taskDir: taskDir, repoPath: repoPath,
		evCh:      make(chan executor.AdapterEvent, 64),
		permTable: newPermTable(),
		items:     newItemIndex(itemIndexCap),
	}
}

// Start 异步启动执行并立即返回。
//
// 步骤：StartServe → Dial → initialize + initialized → thread/start →
// emit progress{SessionID}（会话就绪信号）→ turn/start（不等待）。
//
// 注意：turn/start 是异步的（立即返回 inProgress），回合终态在 turn/completed
// 通知里，因此回合边界由通知驱动而非响应驱动。
func (a *Adapter) Start(ctx context.Context, req executor.StartReq) (err error) {
	taskID := req.Task.ID
	start := time.Now()
	a.log.Info("codex 启动任务", "task", taskID, "repo", req.Task.Workdir(),
		"task_dir", req.TaskDir, "model", req.Task.Model)
	defer func() {
		if err != nil {
			a.log.Error("codex 启动任务失败", "task", taskID, "cause", err)
		}
	}()

	proc, err := startServe(ctx, req.Task.Workdir(), taskID, req.TaskDir, req.Env, a.log)
	if err != nil {
		return err
	}
	// 之后任一步失败都要回收进程，否则留下一个没人管的 tmux 会话
	defer func() {
		if err != nil {
			_ = proc.Kill()
		}
	}()

	r := newRunState(taskID, req.TaskDir, req.Task.Workdir())
	r.proc = proc
	// 回合起点 commit：兜底分类要靠「是否有新提交」这个事实裁决
	if _, c, _, gerr := turn.GitTurnStatus(req.Task.Workdir(), ""); gerr == nil {
		r.startCommit = c
	} else {
		a.log.Warn("读取回合起点 commit 失败，兜底裁决将退化", "task", taskID, "cause", gerr)
	}

	cli, err := Dial(ctx, proc.WSURL(), &handler{a: a, r: r}, a.log)
	if err != nil {
		return err
	}
	r.cli = cli
	defer func() {
		if err != nil {
			_ = cli.Close()
		}
	}()

	if err := a.openThread(ctx, r, req.Task.Workdir(), req.Task.Model); err != nil {
		return err
	}

	prompt, err := turn.RenderPrompt(taskID, req.PlanContent)
	if err != nil {
		return err
	}

	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()

	// 「会话就绪」信号：审核主路径常以 question 收尾、result 永不出现，
	// progress 是会话 id 到达 manager 的可靠通道。**必须排在 turn/start 之前**：
	// 回合可能在几秒内就产出权限工单，manager 那时必须已经知道 threadId。
	a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.threadID,
		Text: "codex 会话已就绪"})

	if err := a.startTurn(r, prompt); err != nil {
		return err
	}
	// 看门狗在 Task 8 接上（`go a.watchdog(r)`）；本 task 不引用它，避免前向依赖
	// 让包编译不过。

	a.log.Info("codex 任务已启动", "task", taskID, "thread", r.threadID,
		"port", proc.Port, "elapsed_ms", time.Since(start).Milliseconds())
	return nil
}

// openThread 完成握手与会话建立：initialize → initialized → thread/start。
//
// 单独抽出：登录态失效那条路径要能在不起进程的情况下被测到。
func (a *Adapter) openThread(ctx context.Context, r *runState, cwd, model string) error {
	a.log.Info("codex 会话建立中", "task", r.taskID, "cwd", cwd, "model", model)
	if _, err := r.cli.Call(ctx, methodInitialize, map[string]any{
		"clientInfo":   map[string]any{"name": "handoff", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		return fmt.Errorf("codex initialize: %w", err)
	}
	if err := r.cli.Notify(methodInitialized, nil); err != nil {
		return fmt.Errorf("codex initialized 通知: %w", err)
	}

	params := map[string]any{
		"cwd":               cwd,
		"sandbox":           "workspace-write",
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
	}
	if model != "" {
		params["model"] = model
	}
	res, err := r.cli.Call(ctx, methodThreadStart, params)
	if err != nil {
		// 凭据问题重试一万次也不会好，给可操作指引（spec §8）
		if strings.Contains(strings.ToLower(err.Error()), "unauthorized") {
			return fmt.Errorf("codex 登录态失效，请在 executor 机重新 `codex login`: %w", err)
		}
		return fmt.Errorf("codex thread/start: %w", err)
	}
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(res, &out); err != nil || out.Thread.ID == "" {
		return fmt.Errorf("codex thread/start 未返回 threadId: %s", res)
	}
	r.threadID = out.Thread.ID
	a.log.Info("codex 会话已建立", "task", r.taskID, "thread", r.threadID)
	return nil
}

// startTurn 发起一个回合。
//
// 参数：
//   - text: 回合输入原文（首轮是渲染后的 plan 提示词，续接时是审核者原话）
//
// 注意：**四个安全参数每回合重钉一遍**（spec §5.1 步骤 6）——安全姿态因此与
// thread 的历史状态和恢复路径完全无关。
func (a *Adapter) startTurn(r *runState, text string) error {
	r.turnMu.Lock()
	r.turnInFlight = true
	r.turnID = ""
	r.turnMu.Unlock()

	a.log.Info("codex 发起回合", "task", r.taskID, "thread", r.threadID, "input_len", len(text))
	ch, err := r.cli.CallAsync(methodTurnStart, map[string]any{
		"threadId":          r.threadID,
		"cwd":               r.repoPath,
		"sandboxPolicy":     sandboxPolicy(),
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
		"input":             []any{map[string]any{"type": "text", "text": text}},
	})
	if err != nil {
		r.turnMu.Lock()
		r.turnInFlight = false
		r.turnMu.Unlock()
		a.log.Error("codex 发起回合失败", "task", r.taskID, "cause", err)
		// CallAsync 只会因「连接已关闭 / 写失败」失败，两者都等于指令送不进
		// executor；必须带哨兵，否则 manager 的四级恢复阶梯整个不启动（B18/grok 教训）
		return fmt.Errorf("任务 %s 的 codex 连接不可用（%v）: %w", r.taskID, err, executor.ErrTaskNotRunning)
	}
	// turn/start 的响应只带 turnId 与 inProgress；回合终态在 turn/completed 通知里。
	// 这里只等它记 turnId（供 turn/interrupt），绝不把它当回合边界。
	go a.noteTurnID(r, ch)
	return nil
}

// noteTurnID 等 turn/start 的响应并记下 turnId；响应本身携带错误时判回合失败。
func (a *Adapter) noteTurnID(r *runState, ch <-chan Result) {
	res := <-ch
	if res.Err != nil {
		a.log.Error("codex turn/start 返回错误", "task", r.taskID, "cause", res.Err)
		a.finishTurn(r, "failed", res.Err.Error(), r.takeTurnText())
		return
	}
	var out struct {
		Turn struct {
			ID string `json:"id"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(res.Result, &out); err == nil && out.Turn.ID != "" {
		r.turnMu.Lock()
		r.turnID = out.Turn.ID
		r.turnMu.Unlock()
		a.log.Debug("codex 回合已受理", "task", r.taskID, "turn", out.Turn.ID)
	}
}

// Events 返回该任务的事件流通道（Start 后可用）。通道关闭表示执行终结。
func (a *Adapter) Events(taskID string) <-chan executor.AdapterEvent {
	r := a.lookup(taskID)
	if r == nil {
		return nil
	}
	return r.evCh
}

// Send 回答提问 / 回发修改指令，对同一会话续接执行。text 原样透传不加工。
func (a *Adapter) Send(ctx context.Context, taskID, text string) error {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s 无运行态: %w", taskID, executor.ErrTaskNotRunning)
	}
	a.log.Info("codex 续接回合", "task", taskID, "thread", r.threadID)
	return a.startTurn(r, text)
}

// Stop 终止执行并回收资源：置 stopping → turn/interrupt → 关连接 → kill tmux → 关事件通道。
func (a *Adapter) Stop(taskID string) error {
	r := a.lookup(taskID)
	if r == nil {
		return fmt.Errorf("任务 %s 无运行态: %w", taskID, executor.ErrTaskNotRunning)
	}
	a.log.Info("codex 停止任务", "task", taskID)
	// 先置 stopping 再动连接：让后续的 interrupted / onClosed 知道这是主动停止
	r.emitMu.Lock()
	r.stopping = true
	r.emitMu.Unlock()

	r.turnMu.Lock()
	turnID := r.turnID
	r.turnMu.Unlock()
	if r.cli != nil && turnID != "" {
		// 尽力而为：中断失败不阻断回收，反正连接和进程马上都要没了
		if err := r.cli.Notify(methodTurnInterrupt, map[string]any{
			"threadId": r.threadID, "turnId": turnID,
		}); err != nil {
			a.log.Warn("codex turn/interrupt 发送失败，继续回收", "task", taskID, "cause", err)
		}
	}
	if r.cli != nil {
		_ = r.cli.Close()
	}
	if r.proc != nil {
		if err := r.proc.Kill(); err != nil {
			// B20：回收失败要发事件而非静默
			a.log.Error("codex 进程回收失败", "task", taskID, "cause", err)
			a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.threadID,
				Text: "警告：codex tmux 会话回收失败，可能残留进程: " + err.Error()})
		}
	}
	r.closeEvents()
	a.drop(taskID)
	a.log.Info("codex 任务已停止", "task", taskID)
	return nil
}

// lookup 取任务运行态；不存在返回 nil。
func (a *Adapter) lookup(taskID string) *runState {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.runs[taskID]
}

// drop 注销任务运行态。
func (a *Adapter) drop(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.runs, taskID)
}

// dropIf 仅当 runs 表里那条仍是 r 时才摘除。
//
// 为什么不能用 drop：冷恢复会把新运行态换进 runs 表，而旧连接的 OnClosed 回调
// 可能在那之后才到（读循环退出有延迟）。无条件删会把刚恢复好的运行态删掉，
// 任务凭空失去运行态——比不摘更坏。
func (a *Adapter) dropIf(taskID string, r *runState) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cur, ok := a.runs[taskID]; ok && cur == r {
		delete(a.runs, taskID)
	}
}

// emit 向事件通道投递一个事件；通道已关闭或已满时丢弃并返回 false。
//
// 为什么要 emitMu + evClosed 而不是裸 send：事件可能来自读循环、看门狗、回合
// 终局三个 goroutine，而关闭权只有一处——没有这把锁会 send on closed channel。
func (a *Adapter) emit(r *runState, ev executor.AdapterEvent) bool {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		a.log.Debug("事件通道已关闭，丢弃事件", "task", r.taskID, "type", ev.Type)
		return false
	}
	select {
	case r.evCh <- ev:
		return true
	default:
		a.log.Warn("事件通道满，丢弃事件", "task", r.taskID, "type", ev.Type)
		return false
	}
}

// emitFailed 产出失败终局并关闭事件通道（一次性语义，后到者被丢弃）。
func (a *Adapter) emitFailed(r *runState, reason string) {
	a.log.Error("codex 任务失败", "task", r.taskID, "reason", reason)
	a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
		Result: &executor.Result{OK: false, SessionID: r.threadID, FailReason: reason}})
	r.closeEvents()
}

// closeEvents 关闭事件通道（幂等）。
func (r *runState) closeEvents() {
	r.emitMu.Lock()
	defer r.emitMu.Unlock()
	if r.evClosed {
		return
	}
	r.evClosed = true
	close(r.evCh)
}

// takeTurnInFlight 取走并清空「回合进行中」标志。
//
// 为什么用标志而不是 turnId 匹配：turn/start 是异步的，turn/completed **可能先于**
// turn/start 的响应到达——那一刻 r.turnID 还是空的，用 turnId 匹配会把回合终局
// 丢掉，任务从此永久静止。标志天然不受这个竞态影响。
func (r *runState) takeTurnInFlight() bool {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	in := r.turnInFlight
	r.turnInFlight = false
	return in
}

// appendBody 累积回合正文（只由 agentMessage 的 item/completed 调用）。
func (r *runState) appendBody(s string) {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	if r.bodyBuf.Len() > 0 {
		r.bodyBuf.WriteString("\n")
	}
	r.bodyBuf.WriteString(s)
}

// takeTurnText 取走本回合正文并清空，为下一回合做准备。
func (r *runState) takeTurnText() string {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	s := r.bodyBuf.String()
	r.bodyBuf.Reset()
	return s
}

// appendRenderDelta 累积 render.log 增量。
func (r *runState) appendRenderDelta(s string) {
	if s == "" {
		return
	}
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.renderBuf.WriteString(s)
	if !strings.HasSuffix(s, "\n") {
		r.renderBuf.WriteString("\n")
	}
}

// noteAskedViaTool 标记本回合已通过原生提问工具向审核者递过问题。
func (r *runState) noteAskedViaTool() {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	r.askedViaTool = true
}

// takeAskedViaTool 取走并清空「已走工具提问」标记。
//
// 取走式（而非只读）是刻意的：标记的生命周期就是一个回合，收尾读一次即失效，
// 否则下一回合的兜底会被上一回合的提问误抑制，真出现静默结束就没人兜了。
func (r *runState) takeAskedViaTool() bool {
	r.turnMu.Lock()
	defer r.turnMu.Unlock()
	asked := r.askedViaTool
	r.askedViaTool = false
	return asked
}

// flushRender 把累积的可见性增量落进 render.log，并按节流产出 progress。
//
// 失败只 Warn 不中断：可见性是增强能力，不值得为它挂掉回合。
func (a *Adapter) flushRender(r *runState) {
	r.turnMu.Lock()
	delta := r.renderBuf.String()
	r.renderBuf.Reset()
	due := time.Since(r.lastProgress) >= progressThrottle
	if due {
		r.lastProgress = time.Now()
	}
	r.turnMu.Unlock()

	if delta == "" {
		return
	}
	if err := turn.AppendRender(filepath.Join(r.taskDir, renderLogName), delta); err != nil {
		a.log.Warn("追加 render.log 失败，不影响回合", "task", r.taskID, "cause", err)
	}
	if due {
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.threadID,
			Text: turn.TruncateMarked(strings.TrimSpace(delta), 500)})
	}
}

// firstNonEmpty 返回第一个非空串（trailer 值优先于 git 实测值）。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// finishTurn 处理一个回合的终局：按 turn.status 与 trailer 分类产出事件。
//
// 参数：
//   - status: codex 的 turn.status（completed / interrupted / failed）
//   - errMsg: status=failed 时 codex 给的原因（B16：必须原样带出，不许扁平化）
//   - text: 本回合正文（已取走）
func (a *Adapter) finishTurn(r *runState, status, errMsg, text string) {
	a.flushRender(r)

	switch status {
	case "failed":
		a.emitFailed(r, "回合失败: "+firstNonEmpty(errMsg, "codex 未给出原因"))
		return
	case "interrupted":
		r.emitMu.Lock()
		stopping := r.stopping
		r.emitMu.Unlock()
		if stopping {
			a.log.Info("回合被主动中断，跳过失败处置", "task", r.taskID)
			return
		}
		a.emitFailed(r, "回合被中断（非 handoff 发起）: "+errMsg)
		return
	}

	// 本回合有被拒权限时优先交代：模型被拒后可能悄悄绕路，人不知情
	if rej := r.takeRejected(); len(rej) > 0 {
		a.emit(r, executor.AdapterEvent{Type: "question",
			Text: turn.ClampQuestion(rejectedTurnQuestion(rej))})
		return
	}

	askedViaTool := r.takeAskedViaTool()
	kind, tr := turn.ParseTrailer(text)
	branch, commit, hasNew, gerr := turn.GitTurnStatus(r.repoPath, r.startCommit)
	if gerr != nil {
		a.log.Warn("git 回合取证失败，降级只用 trailer", "task", r.taskID, "cause", gerr)
	}
	a.log.Info("codex 回合收尾", "task", r.taskID, "kind", kind,
		"has_new_commit", hasNew, "branch", branch, "asked_via_tool", askedViaTool)

	switch kind {
	case "ask":
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(tr.Question)})
	case "finish":
		a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
			Result: &executor.Result{OK: true, Branch: firstNonEmpty(tr.Branch, branch),
				CommitHash: firstNonEmpty(tr.Commit, commit), SessionID: r.threadID,
				Summary: tr.Summary}})
	default:
		// 兜底：模型没守收尾纪律。唯一可信的是 git 实况——有新提交才可能是「干完了」，
		// 没有就把整段回合文本当提问交审核者，绝不替模型宣布完成。
		if hasNew {
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
				Result: &executor.Result{OK: true, Branch: branch, CommitHash: commit,
					SessionID: r.threadID, Summary: "（模型未输出收尾协议，按 git 新提交判定完成）"}})
			return
		}
		// 本回合已走原生提问工具时兜底闭嘴：兜底的职责是「别让回合静默结束」，
		// 那个诉求已经满足了；再补一张工单等于把废话灌回模型（grok 真机教训）
		if askedViaTool {
			a.log.Info("回合无收尾协议，但本回合已走工具提问，兜底不再补工单", "task", r.taskID)
			return
		}
		// 空文本守卫：零文本是故障报告，不是问题
		if strings.TrimSpace(text) == "" {
			a.log.Warn("回合零文本且无新提交，转失败结果交审核者", "task", r.taskID)
			a.emit(r, executor.AdapterEvent{Type: "result", SessionID: r.threadID,
				Result: &executor.Result{OK: false, SessionID: r.threadID,
					FailReason: "回合结束但零文本产出；executor 仍在线，可 continue 续接重试"}})
			return
		}
		a.emit(r, executor.AdapterEvent{Type: "question", Text: turn.ClampQuestion(text)})
	}
}

// onClosed 是连接终止的唯一处置入口。
//
// 先判主动停止：Stop 置位 stopping 后才关连接，读循环随之退出并回调本函数，
// 此时必须**不**产出失败结果——审核者看到的失败原因是假的。
//
// 为什么挂起表非空就直接终结、不再尝试重连：按最保守路径实现（spec §8）——
// 假设未决权限在重连后不会重发。重连成功反而更危险：adapter 会以为一切正常，
// 而任务再也不会前进。
func (a *Adapter) onClosed(r *runState, cause error) {
	r.emitMu.Lock()
	stopping := r.stopping
	r.emitMu.Unlock()
	if stopping {
		a.log.Info("codex 连接已主动关闭，跳过失败处置", "task", r.taskID)
		return
	}
	// 连接断了这条运行态就永远不可用了，必须摘掉它——否则它以「陈运行态」的身份
	// 继续占着 runs 表：Send 会 lookup 到它、拿死连接去发指令；Resume 的冷恢复
	// 互斥以「runs 表里有条目」为判据，会把僵尸当成「恢复进行中」而拒绝恢复。
	defer a.dropIf(r.taskID, r)
	if n := r.voidAll(); n > 0 {
		a.log.Error("codex 连接断开且有未决权限，任务无法继续",
			"task", r.taskID, "voided", n, "cause", cause)
		a.emitFailed(r, fmt.Sprintf("权限应答通道中断（%d 个未决请求作废），需重新发起一轮", n))
		return
	}
	a.log.Warn("codex 连接断开，无未决权限", "task", r.taskID, "cause", cause)
	var logTail string
	if r.proc != nil {
		logTail = r.proc.LogTail()
	}
	a.emitFailed(r, fmt.Sprintf("codex 连接断开: %v；serve 日志尾部: %s", cause, logTail))
}

// handler 把传输层回调翻译成 handoff 语义。
//
// 注意：回调跑在读循环 goroutine 上，**不得阻塞**——所有耗时动作（git 取证、
// 落盘）都只在回合收尾这类低频路径上做。
type handler struct {
	a *Adapter
	r *runState
}

// OnNotify 分发服务端通知。
func (h *handler) OnNotify(method string, params json.RawMessage) {
	a, r := h.a, h.r
	switch {
	case deltaNotifications[method]:
		r.appendRenderDelta(deltaText(params))
		return
	case method == ntfItemStarted || method == ntfItemCompleted:
		it, ok := parseItemNotification(params)
		if !ok {
			// 解析失败会导致后续 fileChange 权限门 fail-closed 升级人工，
			// 审核者需要能查到原因（items.go 的约定）
			a.log.Debug("codex item 通知解析失败，跳过", "method", method, "params_len", len(params))
			return
		}
		r.items.put(it)
		r.appendRenderDelta(it.renderLine())
		// 回合正文只从 agentMessage 的 completed 取：它带的是**完整正文**，
		// 不必从 delta 拼，trailer 解析因此永远拿到完整文本
		if method == ntfItemCompleted && it.Type == "agentMessage" &&
			strings.TrimSpace(it.Text) != "" {
			r.appendBody(strings.TrimSpace(it.Text))
		}
		a.flushRender(r)
	case method == ntfTurnCompleted:
		if !r.takeTurnInFlight() {
			a.log.Debug("codex 收到无对应回合的 turn/completed，忽略", "task", r.taskID)
			return
		}
		status, errMsg := parseTurnCompleted(params)
		a.finishTurn(r, status, errMsg, r.takeTurnText())
	case method == ntfThreadStatus || method == ntfRateLimits:
		r.appendRenderDelta("【状态】" + method + " " + string(params))
		a.flushRender(r)
	case method == ntfServerReqResolved:
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if err := json.Unmarshal(params, &p); err == nil {
			if itemID, ok := r.dropByReqID(string(p.RequestID)); ok {
				a.log.Info("codex 权限请求已被别处了结，摘掉挂起项",
					"task", r.taskID, "perm", itemID)
			}
		}
	default:
		a.log.Debug("codex 未处理的通知", "method", method)
	}
}

// deltaText 从 delta 通知里取出可读文本（字段名随通知类型不同，逐个试）。
func deltaText(params json.RawMessage) string {
	var p struct {
		Delta string `json:"delta"`
		Text  string `json:"text"`
		Chunk string `json:"chunk"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return ""
	}
	return firstNonEmpty(p.Delta, p.Text, p.Chunk)
}

// parseTurnCompleted 取出回合终态与失败原因。
//
// 注意：解析失败时返回 "failed" 而不是 "completed"——把一个读不懂的终局当成
// 成功，会让 handoff 替模型宣布完成，是最不能接受的一种误判。
func parseTurnCompleted(params json.RawMessage) (status, errMsg string) {
	var p struct {
		Turn struct {
			Status string `json:"status"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		} `json:"turn"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Turn.Status == "" {
		return "failed", "无法解析 turn/completed 报文: " + string(params)
	}
	if p.Turn.Error != nil {
		errMsg = p.Turn.Error.Message
	}
	return p.Turn.Status, errMsg
}

// OnServerRequest 分发服务端请求。返回 false 时传输层代回 -32601。
func (h *handler) OnServerRequest(reqID json.RawMessage, method string, params json.RawMessage) bool {
	a, r := h.a, h.r
	switch method {
	case reqCommandApproval:
		ap, ok := parseCommandApproval(params)
		if !ok {
			a.log.Warn("codex 命令审批报文非法，回错误", "task", r.taskID)
			_ = r.cli.ReplyError(reqID, -32602, "invalid approval params")
			return true
		}
		desc := commandPermText(ap)
		perm := permRequestFromCommand(ap)
		r.note(ap.ItemID, reqID, desc)
		r.noteReqID(string(reqID), ap.ItemID)
		a.log.Info("codex 权限请求", "task", r.taskID, "perm", ap.ItemID, "tool", executor.PermToolBash)
		r.appendRenderDelta("【权限门】" + desc)
		a.flushRender(r)
		a.emit(r, executor.AdapterEvent{Type: "permission", PermissionID: ap.ItemID,
			SessionID: r.threadID, Text: desc, Perm: perm})
		return true

	case reqFileChangeApproval:
		var p struct {
			ItemID string `json:"itemId"`
		}
		if err := json.Unmarshal(params, &p); err != nil || p.ItemID == "" {
			a.log.Warn("codex 文件变更审批报文非法，回错误", "task", r.taskID)
			_ = r.cli.ReplyError(reqID, -32602, "invalid approval params")
			return true
		}
		it, found := r.items.get(p.ItemID)
		if !found {
			// 报文里没有路径，索引又查不到 → 不伪造结构，交 manager fail-closed
			a.log.Warn("codex fileChange 权限缺变更清单，已 fail-closed 升级人工",
				"task", r.taskID, "perm", p.ItemID)
			it = nil
		}
		desc := fileChangePermText(it)
		perm := permRequestFromFileChange(it)
		r.note(p.ItemID, reqID, desc)
		r.noteReqID(string(reqID), p.ItemID)
		a.log.Info("codex 权限请求", "task", r.taskID, "perm", p.ItemID, "indexed", found)
		r.appendRenderDelta("【权限门】" + desc)
		a.flushRender(r)
		a.emit(r, executor.AdapterEvent{Type: "permission", PermissionID: p.ItemID,
			SessionID: r.threadID, Text: desc, Perm: perm})
		return true

	case reqPermissionsApproval:
		// 一律 fail-closed（spec §5.4）：这是「模型申请把沙箱放宽一截」，等价于
		// acceptForSession。**绝不做成可批准的权限门**——能被批准的「放宽沙箱」
		// 正是 §2.1 安全论证赖以成立的那道边界。回一份空 profile，只让审核者知情。
		a.log.Warn("codex 申请放宽沙箱，已拒绝（fail-closed）", "task", r.taskID,
			"params", string(params))
		if err := r.cli.Reply(reqID, map[string]any{
			"profile": map[string]any{}, "scope": "turn",
		}); err != nil {
			a.log.Error("回发沙箱放宽拒绝失败", "task", r.taskID, "cause", err)
		}
		r.appendRenderDelta("【沙箱】模型申请放宽沙箱权限，已按最保守策略拒绝")
		a.flushRender(r)
		a.emit(r, executor.AdapterEvent{Type: "progress", SessionID: r.threadID,
			Text: "codex 申请放宽沙箱权限，已按最保守策略拒绝（不授予任何额外权限）"})
		return true

	case reqAuthRefresh:
		// 不实现（spec §4）：回错误让 codex 走它自己的刷新逻辑，同时把任务判失败
		// 并回显真因——登录态失效重试一万次也不会好。
		a.log.Error("codex 请求补令牌，登录态已失效", "task", r.taskID, "params", string(params))
		_ = r.cli.ReplyError(reqID, -32601, "handoff 不代管 codex 登录态")
		a.emitFailed(r, "codex 登录态失效，请在 executor 机重新 `codex login`")
		return true
	}
	// item/tool/requestUserInput 在 Task 7 实现；在此之前回 -32601 让那次工具调用
	// 失败——明确的「不支持」远好于永久挂起（grok 的教训）。
	return false
}

// OnClosed 连接终止。
func (h *handler) OnClosed(err error) { h.a.onClosed(h.r, err) }
```

- [ ] **Step 4: 补 `perm.go` 的 reqID 反查与两个动作**

追加到 `internal/executor/codex/perm.go`：

```go
// noteReqID 记下 JSON-RPC 请求 id 到 itemId 的反查关系。
//
// 为什么需要：serverRequest/resolved 通知带的是 **requestId**（JSON-RPC id），
// 而挂起表按 itemId 索引。没有这张反查表，「该请求已被别处了结」这个通知就无法
// 落到具体挂起项上，那张工单会一直挂着，审核者裁决时才发现回发失败。
func (t *permTable) noteReqID(reqID, itemID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byReq == nil {
		t.byReq = map[string]string{}
	}
	t.byReq[reqID] = itemID
}

// dropByReqID 按 JSON-RPC 请求 id 摘掉挂起项，返回对应 itemId。
func (t *permTable) dropByReqID(reqID string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	itemID, ok := t.byReq[reqID]
	if !ok {
		return "", false
	}
	delete(t.byReq, reqID)
	delete(t.pending, itemID)
	return itemID, true
}
```

同时给 `permTable` 加字段 `byReq map[string]string`，并在 `take` / `voidAll` 里一并清理对应的 `byReq` 条目（`take` 时按 itemID 反向找开销大，改为：`voidAll` 直接 `t.byReq = map[string]string{}`；`take` 不清 `byReq`，留下的孤儿条目在 `voidAll` 时统一清掉——一条 map entry 的代价远小于反向索引的复杂度，这条取舍写进注释）。

追加 `RespondPermission` 与 `PermissionsVolatile`：

```go
// PermissionsVolatile 表明本 adapter 的权限请求随连接消亡。
//
// manager 据此在 agentd 重启后拒绝恢复「尚有未决权限工单」的任务：按最保守路径
// 假设 thread/resume 不会重发未决授权请求（spec §8）。
func (a *Adapter) PermissionsVolatile() bool { return true }

// RespondPermission 应答 codex 的权限请求。
//
// 参数：
//   - taskID: 目标任务
//   - permID: 权限请求 id（即 codex 的 itemId，裸值不带命名空间前缀）
//   - decision: "once"（批准本次）或 "reject"（拒绝）
//
// 返回：
//   - 任务不在运行中、或挂起表查不到该 permID 时，包装 executor.ErrTaskNotRunning
//     ——两者都意味着「executor 侧那次请求已经不在了」，调用方据此转失败交审核者，
//     而不是当作可重试的瞬时错误
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision string) error {
	r := a.lookup(taskID)
	if r == nil {
		a.log.Warn("权限应答时任务不在运行中", "task", taskID, "perm", permID)
		return fmt.Errorf("任务 %s 无运行态: %w", taskID, executor.ErrTaskNotRunning)
	}
	pp, ok := r.take(permID)
	if !ok {
		a.log.Warn("权限应答找不到挂起请求（连接已重建或已作废）", "task", taskID, "perm", permID)
		return fmt.Errorf("权限请求 %s 已不在挂起表: %w", permID, executor.ErrTaskNotRunning)
	}

	d := decisionFor(decision)
	a.log.Info("回发权限裁决", "task", taskID, "perm", permID, "decision", decision, "mapped", d)
	if err := r.cli.Reply(pp.reqID, map[string]any{"decision": d}); err != nil {
		a.log.Error("回发权限裁决失败", "task", taskID, "perm", permID, "cause", err)
		return fmt.Errorf("回发权限裁决: %w", err)
	}
	if d == "decline" {
		// 记入被拒清单的是权限描述而非 permID：被拒清单存在的意义是让审核者知道
		// 「模型刚才想干什么、被挡了」，一串不透明 id 等于没说。长度收口在回合
		// 收尾的 turn.ClampQuestion 里做，不在本处截短。
		r.noteRejected(pp.desc)
	}
	r.appendRenderDelta("【裁决】" + decision + " → " + d + "：" + pp.desc)
	a.flushRender(r)
	a.log.Info("权限裁决已送达 executor", "task", taskID, "perm", permID)
	return nil
}
```

`perm.go` 的 import 里补 `"context"` 与 `"fmt"`。

- [ ] **Step 5: 补测试缝**

追加到 `export_test.go`：

```go
// NewAdapterWithRunForTest 造一个带运行态的 adapter（不起进程、不连 WS）。
func NewAdapterWithRunForTest(taskID string) (*Adapter, *runState) {
	a := New(quietTestLogger())
	r := newRunState(taskID, "", "")
	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	return a, r
}

// EventsForTest 返回运行态的事件通道。
func EventsForTest(r *runState) <-chan executor.AdapterEvent { return r.evCh }

// FinishTurnForTest 直接驱动回合收尾分类。
func FinishTurnForTest(a *Adapter, r *runState, status, errMsg, text string) {
	a.finishTurn(r, status, errMsg, text)
}

// NoteRejectedOnRunForTest 往运行态里塞一条被拒记录。
func NoteRejectedOnRunForTest(r *runState, desc string) { r.noteRejected(desc) }

// MarkStoppingForTest 置位主动停止标记。
func MarkStoppingForTest(r *runState) {
	r.emitMu.Lock()
	r.stopping = true
	r.emitMu.Unlock()
}

// NewHandlerForTest 造一个绑定到该运行态的通知/请求处理器。
func NewHandlerForTest(a *Adapter, r *runState) Handler { return &handler{a: a, r: r} }

// AttachFakeClientForTest 给运行态挂一条把应答吞掉的假连接。
//
// 为什么不给实现里的 r.cli.* 加 nil 守卫：那会把「连接已经没了却还在发裁决」
// 这种真 bug 一起吞掉。测试用假连接，实现里保持裸调用。
func AttachFakeClientForTest(r *runState) {
	r.cli = &Client{log: quietTestLogger(),
		replyHook: func(json.RawMessage, any) error { return nil }}
}

func quietTestLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
```

`export_test.go` import 补 `"io"`、`"log/slog"`、`"encoding/json"`。

同时给 `appserver.go` 的 `Client` 加一个 unexported 测试缝字段并在两个应答方法里让它生效：

```go
type Client struct {
	// …既有字段…

	// replyHook 是应答回发的测试缝：非 nil 时 Reply/ReplyError 走它而不碰真连接。
	// 生产路径恒为 nil。
	replyHook func(reqID json.RawMessage, payload any) error
}
```

`Reply` 与 `ReplyError` 各在开头加：

```go
	if c.replyHook != nil {
		return c.replyHook(reqID, result) // ReplyError 里传 map[string]any{"code": code, "message": message}
	}
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS（全部）

- [ ] **Step 7: 加关键节点日志**

核对：

- 进入关键操作：`Start`（带 task/repo/task_dir/model）、`openThread`（带 cwd/model）、`startTurn`（带 thread + input_len，**不打正文**）、`Send`、`Stop`
- 外部调用前后：`thread/start` 失败 → `Error` + 可操作指引；`turn/start` 失败 → `Error`；回发裁决前后各一条 `Info`
- 每个错误分支带上下文：连接断开带 `cause` + `voided` 数；进程回收失败 `Error` **并发 progress 事件**（B20：不许静默）
- 状态变更：会话已建立（带 thread）、回合收尾（带 kind / has_new_commit / branch / asked_via_tool）、权限请求（带 perm / tool / indexed）、沙箱放宽被拒 `Warn`
- 成功路径不静默：`codex 任务已启动`（带 port + elapsed_ms）、`权限裁决已送达 executor`
- 高频路径降级 Debug：未处理的通知、item 解析失败、事件通道已关闭丢弃、回合已受理
- **禁止 `fmt.Printf`**；**禁止打回合正文与命令全文**（正文进 render.log，命令全文进工单）

- [ ] **Step 8: 加注释**

- 文件头：职责 + 边界（不写 store / 不做审批判断 / 不碰配置文件）
- 导出符号：`Adapter` / `New` / 五动作 / `PermissionsVolatile` 全部有 doc 注释
- 三处「为什么」必须写全（本 task 开头列的三条设计判断）：`takeTurnInFlight` 上方写 turnId 匹配的竞态、`OnNotify` 的 agentMessage 分支写「completed 带完整正文」、`OnServerRequest` 结尾写 `requestUserInput` 的中间态与 Task 7 的承诺
- 另外三处：`sandboxPolicy` 上方写「为什么每回合重钉」+「networkAccess 是用户决定」、`dropIf` 写「为什么不能用 drop」、`onClosed` 写「为什么不重连」
- `parseTurnCompleted` 写「解析失败按 failed 处理」的理由

- [ ] **Step 9: 提交**

```bash
git add internal/executor/codex/ && git commit -m "feat(codex): 五动作与事件翻译，安全参数每回合协议级重钉"
```

---

### Task 7: 提问通道 `item/tool/requestUserInput`（`question.go`）

**Files:**
- Create: `internal/executor/codex/question.go`
- Modify: `internal/executor/codex/adapter.go`（`OnServerRequest` 增加 `reqUserInput` 分支）
- Modify: `internal/executor/codex/export_test.go`
- Test: `internal/executor/codex/question_test.go`

**Interfaces:**
- Consumes: `runState`（Task 6）、`turn.ClampQuestion`
- Produces:
  - `type userInputQuestion struct { ID, Header, Question string; Options []userInputOption; IsOther, IsSecret bool }`
  - `type userInputOption struct { Label, Description string }`
  - `func parseUserInput(params json.RawMessage) (itemID string, qs []userInputQuestion, ok bool)`
  - `func userInputText(qs []userInputQuestion) string`
  - `func userInputReply(qs []userInputQuestion) map[string]any`

**这条通道是整个 adapter 最容易翻车的地方**——grok 那边连翻两次：应答形态错被判成工具失败；兜底重复上报导致一次提问出两张工单。codex 这条还标着 EXPERIMENTAL（需 `capabilities.experimentalApi: true`，Task 6 的 `initialize` 已带）。

**处置策略（与 grok 同构，不自创）：**

1. **立即应答，不等审核者。** 回调跑在读循环上，等审核者会卡死整条连接；而不应答会让回合永久挂起。应答内容是一句「这个问题已转交人类审核者，请按收尾协议结束本回合并等待指示」。
2. **同时 emit 一条 question 事件**，正文是渲染后的问题全文（含选项），交 manager 建工单。
3. **置位 `askedViaTool`**，让回合收尾的兜底闭嘴——否则一次提问出两张工单。
4. **`isSecret` 的问题不进事件正文**：只给出「codex 索要一个机密值（问题标题：X），handoff 不代传机密」。凭据不经 handoff 的事件库中转。

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/codex/question_test.go`：

```go
package codex_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/codex"
)

const userInputParams = `{"itemId":"tool-1","threadId":"t","turnId":"u","questions":[
  {"id":"q1","header":"选择方案","question":"用 A 还是 B？",
   "options":[{"label":"A","description":"简单"},{"label":"B","description":"通用"}]}]}`

// 问题正文必须含问题与选项，审核者据此裁决
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
		t.Fatalf("应保留标题让审核者知情:\n%s", txt)
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
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run TestUserInput -v`
Expected: FAIL —— `undefined: codex.ParseUserInputForTest`

- [ ] **Step 3: 实现 `question.go`**

```go
// question.go —— codex 原生提问通道 item/tool/requestUserInput 的翻译。
//
// 职责：
//   - 解析提问报文，渲染成交给审核者的问题全文
//   - 构造必须立即回发的应答体
//
// 边界：
//   - 不决定「回合要不要结束」：那是 adapter 回合收尾的事
//   - **不代传机密**：isSecret 的问题正文不进事件库
//
// 为什么必须立即应答而不是等审核者：回调跑在读循环 goroutine 上，等审核者会卡死
// 整条连接；而不应答会让 codex 侧的回合永久挂起。grok 那边这条通道翻过两次车
// （应答形态错被判工具失败、兜底重复上报导致一次提问两张工单），此处逐条对症。
package codex

import (
	"encoding/json"
	"strings"
)

// handoffAnswerText 是回发给 codex 的固定答案。
//
// 内容必须是**对模型有效的指令**而不是占位符：告诉它问题已转交人类、按收尾协议
// 结束本回合。空答案或无意义答案会被 codex 判成工具失败（grok 的教训）。
const handoffAnswerText = "该问题已转交给人类审核者。请立即按 handoff 收尾协议结束本回合" +
	"（输出 HANDOFF_STATUS: ask 及问题正文），不要自行猜测答案继续执行。"

// userInputOption 是一个候选答案。
type userInputOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// userInputQuestion 是一条待答问题。
type userInputQuestion struct {
	ID       string            `json:"id"`
	Header   string            `json:"header"`
	Question string            `json:"question"`
	Options  []userInputOption `json:"options"`
	IsOther  bool              `json:"isOther"`
	IsSecret bool              `json:"isSecret"`
}

// parseUserInput 解析提问报文。
//
// 返回：itemId、问题列表与 true；报文非法或没有任何问题时返回 false
// （没有问题就没什么可转交的，但调用方仍须应答，见 adapter 的分支）。
func parseUserInput(params json.RawMessage) (string, []userInputQuestion, bool) {
	var p struct {
		ItemID    string              `json:"itemId"`
		Questions []userInputQuestion `json:"questions"`
	}
	if err := json.Unmarshal(params, &p); err != nil || len(p.Questions) == 0 {
		return "", nil, false
	}
	return p.ItemID, p.Questions, true
}

// userInputText 把问题列表渲染成交给审核者的正文。
//
// 注意：isSecret 的问题**只给标题不给正文**——凭据不经 handoff 的事件库中转，
// 事件是要落盘的。
func userInputText(qs []userInputQuestion) string {
	var b strings.Builder
	b.WriteString("【模型提问】\n")
	for _, q := range qs {
		if q.Header != "" {
			b.WriteString("■ " + q.Header + "\n")
		}
		if q.IsSecret {
			b.WriteString("  （codex 索要一个机密值，handoff 不代传机密；" +
				"若确需提供，请由人直接在 executor 机处理）\n")
			continue
		}
		if q.Question != "" {
			b.WriteString("  " + q.Question + "\n")
		}
		for _, o := range q.Options {
			line := "    - " + o.Label
			if o.Description != "" {
				line += "：" + o.Description
			}
			b.WriteString(line + "\n")
		}
		if q.IsOther {
			b.WriteString("    - （也可给出选项之外的答案）\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// userInputReply 构造应答体：{"answers":{"<qid>":{"answers":["…"]}}}。
//
// 注意：**每个问题都要有非空答案**——漏一个或给空串会被 codex 判成工具失败，
// 模型随后可能反复重试同一次提问。
func userInputReply(qs []userInputQuestion) map[string]any {
	answers := make(map[string]any, len(qs))
	for i, q := range qs {
		id := q.ID
		if id == "" {
			// 没有 id 的问题也要占一个键，否则应答与问题数量对不上
			id = "q" + itoaSmall(i)
		}
		answers[id] = map[string]any{"answers": []string{handoffAnswerText}}
	}
	return map[string]any{"answers": answers}
}

// itoaSmall 是小整数转字符串（避免为一个下标引入 strconv 的心智负担）。
func itoaSmall(i int) string {
	if i < 10 {
		return string(rune('0' + i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
```

- [ ] **Step 4: 接进 `adapter.go` 的 `OnServerRequest`**

把 Task 6 结尾那句「Task 7 实现」的注释替换成真正的分支，插在 `reqAuthRefresh` 之后：

```go
	case reqUserInput:
		itemID, qs, ok := parseUserInput(params)
		if !ok {
			// 报文读不懂也必须应答——不应答等于让回合永久挂起
			a.log.Warn("codex 提问报文非法，回空应答", "task", r.taskID, "params_len", len(params))
			_ = r.cli.Reply(reqID, map[string]any{"answers": map[string]any{}})
			return true
		}
		text := userInputText(qs)
		a.log.Info("codex 提问已转交审核者", "task", r.taskID, "item", itemID,
			"question_count", len(qs))
		// 立即应答：回调在读循环上，等审核者会卡死整条连接
		if err := r.cli.Reply(reqID, userInputReply(qs)); err != nil {
			a.log.Error("回发提问应答失败", "task", r.taskID, "item", itemID, "cause", err)
		}
		// 置位后回合收尾的兜底会闭嘴，避免一次提问出两张工单
		r.noteAskedViaTool()
		r.appendRenderDelta(text)
		a.flushRender(r)
		a.emit(r, executor.AdapterEvent{Type: "question", SessionID: r.threadID,
			Text: turn.ClampQuestion(text)})
		return true
```

并把函数末尾改为：

```go
	a.log.Debug("codex 未识别的服务端请求，交传输层回 -32601", "task", r.taskID, "method", method)
	return false
```

- [ ] **Step 5: 补测试缝**

追加到 `export_test.go`：

```go
// ParseUserInputForTest 暴露提问报文解析。
func ParseUserInputForTest(raw []byte) (string, []userInputQuestion, bool) {
	return parseUserInput(raw)
}

// UserInputTextForTest 暴露问题正文渲染。
func UserInputTextForTest(qs []userInputQuestion) string { return userInputText(qs) }

// UserInputReplyForTest 暴露应答体构造。
func UserInputReplyForTest(qs []userInputQuestion) map[string]any { return userInputReply(qs) }
```

（`AttachFakeClientForTest` 已在 Task 6 加过，此处直接复用。）

- [ ] **Step 6: 运行全部测试**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS

- [ ] **Step 7: 加关键节点日志**

- 提问到达 → `Info("codex 提问已转交审核者", "task", …, "item", …, "question_count", …)`（**不打问题正文**：可能含 isSecret 内容）
- 报文非法 → `Warn`，带 `params_len` 且明说「回空应答」
- 回发应答失败 → `Error` 带 `item` + `cause`——这条最关键，应答没发出去就意味着回合会挂死，审核者需要能查到
- 未识别的服务端请求 → `Debug`，说明交给传输层回 -32601

- [ ] **Step 8: 加注释**

- 文件头：职责 + 边界（不决定回合是否结束、不代传机密）+ **为什么必须立即应答** + grok 翻的两次车
- `handoffAnswerText` 上方写明「必须是对模型有效的指令而非占位符」及其后果
- `userInputText` 写明 isSecret 的处置理由
- `userInputReply` 写明「每个问题都要有非空答案」及其后果
- adapter 分支里的 `noteAskedViaTool` 上方写明「避免一次提问两张工单」

- [ ] **Step 9: 提交**

```bash
git add internal/executor/codex/ && git commit -m "feat(codex): 原生提问通道转交审核者，立即应答避免回合挂死"
```

- [ ] **Step 10: 真机验 V-1（提问通道能否触发、假答案会不会被判工具失败）**

spec §6 的 V-1。写探针 `v1_ask.py`（复用 spike 客户端，用户级 home，`capabilities.experimentalApi: true`）：

1. 指令：「你必须先用提问工具向用户确认『用方案 A 还是方案 B』，拿到答复前不要动手。」
2. 收到 `item/tool/requestUserInput` → 记下报文全文 → 按本 task 的 `userInputReply` 形态应答（答案文本用 `handoffAnswerText` 原文）
3. 观察：回合是否继续、是否出现「工具失败」类 item、模型是否照做输出了 `HANDOFF_STATUS: ask`

判据与处置：

- **能触发 + 应答被接受 + 模型照做** → V-1 通过，本 task 的实现有效
- **能触发但应答被判工具失败** → 改答案形态（先试：给每个问题回 `options[0].label` 而不是自由文本；再试：`{"answers":{"<qid>":{"answers":[…],"isOther":true}}}`），改完重跑探针，把最终有效形态回写 `handoffAnswerText` 附近的注释与 spec
- **压根不触发**（模型从不调这个工具）→ 记录为「通道存在但未观测到触发」，本 task 的实现作为**防御性代码**保留（它已经保证了「万一触发也不会挂死」），并在 spec §6 明记这条未获正面实证

把结论回写 spec §6 的 V-1 行。

```bash
git add docs/superpowers/specs/2026-08-09-handoff-codex-adapter-design.md && git commit -m "docs(spec): B28 V-1 已验——提问通道的触发与应答形态"
```

---

### Task 8: 重启恢复与看门狗（`resume.go`、`reap.go`）

**Files:**
- Create: `internal/executor/codex/resume.go`
- Create: `internal/executor/codex/reap.go`
- Modify: `internal/executor/codex/adapter.go`（`Start` 里接上 `go a.watchdog(r)`）
- Modify: `internal/executor/codex/export_test.go`
- Test: `internal/executor/codex/resume_test.go`

**Interfaces:**
- Consumes: `ReadServeInfo`/`Proc`/`startServe`（Task 3）、`runState`/`handler`/`emitFailed`/`dropIf`（Task 6）
- Produces:
  - `func (a *Adapter) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error)`
  - `func (a *Adapter) Reap(taskID, taskDir string) error`
  - `func (a *Adapter) watchdog(r *runState)`（内部）
  - `func resumeNote(mode, threadID string) string`（内部）

**恢复阶梯（B18，与 grok 同形）：**

| 级别 | 条件 | 动作 |
|------|------|------|
| 不可恢复 | 无 threadId 或无 serve.json | 直接返回 `Alive=false`，**不是错误** |
| reattach | 进程还活着 | 重连 WS + `initialize` + `thread/resume` |
| cold | 进程死了且 `req.Cold` | 回收旧会话 → 重起 app-server → 重连 → `thread/resume` |
| fresh | `thread/resume` 载不进且 `req.Cold` | `thread/start` 新开会话，`Mode=fresh` 让审核者知道上下文断了 |

**codex 独有的两个优势（写进注释）：** rollout 落在用户级 `~/.codex/sessions/**`，**agentd 重启、甚至 app-server 进程重启后 thread 都还在盘上**——比任务级 home 的方案更结实；且没有凭据软链要修，冷恢复路径比 grok 短一截。

**`thread/resume` 必须重传 `cwd` / `approvalPolicy` / `approvalsReviewer`**（spec §5.6）：恢复路径是最容易让安全档位悄悄退回开发机 config 的地方。恢复后的第一个 `turn/start` 会再钉一遍，两层都钉是刻意的。

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/codex/resume_test.go`：

```go
package codex_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/codex"
)

// 没有 threadId 就没法 resume——判不可恢复，且这不是错误
func TestResumeWithoutSessionIDIsNotAlive(t *testing.T) {
	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{TaskID: "T1", TaskDir: t.TempDir()})
	if err != nil {
		t.Fatalf("判不可恢复不应报错: %v", err)
	}
	if out.Alive {
		t.Fatal("无 threadId 时不应判活")
	}
}

// 没有 serve.json 同理
func TestResumeWithoutServeInfoIsNotAlive(t *testing.T) {
	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{
		TaskID: "T2", TaskDir: t.TempDir(), SessionID: "thread-1"})
	if err != nil {
		t.Fatalf("判不可恢复不应报错: %v", err)
	}
	if out.Alive {
		t.Fatal("无 serve.json 时不应判活")
	}
}

// 进程已死且不允许冷恢复 → 保持不可恢复，且旧 tmux 会话要先回收
func TestResumeColdDisallowedStaysDead(t *testing.T) {
	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	killed := make(chan string, 1)
	restore := codex.SwapTmuxKillForTest(func(s string) error { killed <- s; return nil })
	defer restore()

	a := codex.New(nil)
	out, err := a.Resume(executor.ResumeReq{
		TaskID: "T3", TaskDir: dir, RepoPath: dir, SessionID: "thread-1", Cold: false})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if out.Alive {
		t.Fatal("不允许冷恢复时不应判活")
	}
	if out.Note == "" {
		t.Fatal("必须给出判死原因，审核者要能看懂为什么任务没恢复")
	}
	select {
	case s := <-killed:
		if s == "" {
			t.Fatal("回收的会话名为空")
		}
	default:
		t.Fatal("冷恢复前必须先回收旧 tmux 会话，否则重起时会撞名")
	}
}

// 冷恢复时任务目录已被归档清理 → 判不可恢复，不越界重建
func TestResumeColdRefusesWhenTaskDirGone(t *testing.T) {
	dir := t.TempDir()
	writeDeadServeInfo(t, dir)
	gone := filepath.Join(dir, "not-exist")
	restore := codex.SwapTmuxKillForTest(func(string) error { return nil })
	defer restore()

	a := codex.New(nil)
	out, _ := a.Resume(executor.ResumeReq{
		TaskID: "T4", TaskDir: dir, RepoPath: gone, SessionID: "thread-1", Cold: true})
	if out.Alive {
		t.Fatal("工作区已不存在时不应判活（重建是 Dispatch 的职责）")
	}
}

// Reap：运行态丢失时也要能按确定性会话名兜底回收（B20）
func TestReapFallsBackToDeterministicName(t *testing.T) {
	killed := make(chan string, 1)
	restore := codex.SwapTmuxKillForTest(func(s string) error { killed <- s; return nil })
	defer restore()

	a := codex.New(nil)
	if err := a.Reap("abcdef1234", t.TempDir()); err != nil {
		t.Fatalf("Reap: %v", err)
	}
	select {
	case s := <-killed:
		if s != "handoff-abcdef12" {
			t.Fatalf("会话名 = %s，应为 handoff-<id8>", s)
		}
	default:
		t.Fatal("Reap 必须真的尝试回收")
	}
}

func writeDeadServeInfo(t *testing.T, dir string) {
	t.Helper()
	// 端口指向一个必然连不上的地址，让 Alive() 判死
	body := `{"session":"handoff-deadbeef","task_dir":"` + dir + `","port":1}`
	if err := os.WriteFile(filepath.Join(dir, "serve.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("seed serve.json: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run 'TestResume|TestReap' -v`
Expected: FAIL —— `a.Resume undefined`

- [ ] **Step 3: 实现 `resume.go`**

```go
// resume.go —— agentd 重启后的运行态恢复与连接看门狗。
//
// 职责：
//   - Resume：按四级阶梯尝试恢复（不可恢复 / reattach / cold / fresh）
//   - watchdog：探活 app-server，判死后走统一的失败处置
//
// 边界：
//   - 不重建 worktree：任务工作区可能已随归档清理，重建是 Dispatch 的职责，
//     越界重建会让归档过的任务诈尸
//   - 不改任务状态：Resume 只如实返回结论，状态迁移归 manager
//
// codex 的两个结构性优势（相对 grok）：
//   1. rollout 落在用户级 ~/.codex/sessions/**，agentd 重启、甚至 app-server
//      进程重启后 thread 都还在盘上，冷恢复不依赖任务目录里的会话数据
//   2. 没有凭据软链要修（复用用户级 home，凭据零副本），冷恢复路径短一截
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

const (
	watchdogFastInterval  = 200 * time.Millisecond
	watchdogSlowInterval  = 2 * time.Second
	watchdogFastProbes    = 10
	watchdogFailThreshold = 3
	resumeDialTimeout     = 20 * time.Second
)

// Resume 尝试恢复一个 agentd 重启前已在执行的任务。
//
// 参数：
//   - req: 恢复请求（TaskDir 是 serve.json 所在；RepoPath 是 thread/resume 的 cwd；
//     SessionID 是落库的 threadId；Cold 决定是否允许重起进程）
//
// 返回：
//   - Alive=true：进程存活或已重起、WS 已重连、thread 已载入、事件流已重建
//   - Alive=false：判不可恢复，调用方据此转 failed 交审核者。**这不是错误**，
//     err 恒为 nil 的路径很多，调用方不要靠 err 判别
func (a *Adapter) Resume(req executor.ResumeReq) (executor.ResumeOutcome, error) {
	taskID, taskDir, repoPath, threadID := req.TaskID, req.TaskDir, req.RepoPath, req.SessionID
	a.log.Info("codex 尝试恢复任务", "task", taskID, "task_dir", taskDir, "thread", threadID)

	if threadID == "" {
		a.log.Info("无 executor 会话 id，判不可恢复", "task", taskID)
		return executor.ResumeOutcome{}, nil
	}
	proc, err := ReadServeInfo(taskDir)
	if err != nil {
		a.log.Info("恢复凭据缺失，判不可恢复", "task", taskID, "cause", err)
		return executor.ResumeOutcome{}, nil
	}

	// 冷恢复互斥：先在 runs 上占位再拉进程，后到者直接返回「恢复进行中」。
	// 两个 app-server 抢同一个 thread 是数据损坏级别的后果。
	a.mu.Lock()
	if _, busy := a.runs[taskID]; busy {
		a.mu.Unlock()
		a.log.Info("该任务已有运行态或恢复进行中，跳过本次恢复", "task", taskID)
		return executor.ResumeOutcome{Alive: false, Note: "该任务的恢复正在进行中"}, nil
	}
	a.runs[taskID] = &runState{taskID: taskID} // 占位：evCh 为 nil 即占位标志
	a.mu.Unlock()
	defer func() {
		// 失败路径清掉占位，否则这个任务永远恢复不了
		a.mu.Lock()
		if cur, ok := a.runs[taskID]; ok && cur.evCh == nil {
			delete(a.runs, taskID)
		}
		a.mu.Unlock()
	}()

	mode := executor.ResumeModeReattach
	if !proc.Alive() {
		// 先回收旧会话：tmux 会话由窗口 1 的 tail -f 吊着，app-server 死了会话仍在，
		// 而冷恢复用的是同一个确定性会话名 handoff-<id8>，不回收就会撞名
		if kerr := proc.Kill(); kerr != nil {
			a.log.Warn("回收已死 app-server 的 tmux 会话失败", "task", taskID,
				"session", proc.Session, "cause", kerr)
		}
		if !req.Cold {
			a.log.Info("app-server 已不在且不允许冷恢复，判不可恢复", "task", taskID, "port", proc.Port)
			return executor.ResumeOutcome{Alive: false,
				Note: "codex app-server 进程已不在（本次只允许热重连）"}, nil
		}
		if _, serr := os.Stat(taskDir); serr != nil {
			a.log.Info("任务目录已不存在，判不可恢复", "task", taskID, "cause", serr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务目录已不存在（可能已归档清理），无法恢复"}, nil
		}
		if _, rerr := os.Stat(repoPath); rerr != nil {
			a.log.Info("任务工作区已不存在，判不可恢复", "task", taskID, "cause", rerr)
			return executor.ResumeOutcome{Alive: false,
				Note: "任务工作区已不存在（可能已归档清理），无法恢复"}, nil
		}
		a.log.Info("app-server 已不在，进入冷恢复", "task", taskID,
			"old_port", proc.Port, "thread", threadID)
		newProc, serr := startServe(context.Background(), repoPath, taskID, taskDir, req.Env, a.log)
		if serr != nil {
			// 起不来是可预期现场（未登录/端口占用），按不可恢复处理而非错误
			a.log.Warn("冷恢复重起 app-server 失败，判不可恢复", "task", taskID, "cause", serr)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("重起 codex app-server 失败：%v", serr)}, nil
		}
		proc = newProc
		mode = executor.ResumeModeCold
		a.log.Info("冷恢复新 app-server 就绪", "task", taskID, "new_port", proc.Port)
	}

	r := newRunState(taskID, taskDir, repoPath)
	r.proc = proc
	r.threadID = threadID
	if _, c, _, gerr := turn.GitTurnStatus(repoPath, ""); gerr == nil {
		r.startCommit = c
	}

	ctx, cancel := context.WithTimeout(context.Background(), resumeDialTimeout)
	defer cancel()
	cli, err := Dial(ctx, proc.WSURL(), &handler{a: a, r: r}, a.log)
	if err != nil {
		a.log.Warn("WS 重连失败，判不可恢复", "task", taskID, "cause", err)
		return executor.ResumeOutcome{Alive: false,
			Note: fmt.Sprintf("重连 codex app-server 失败：%v", err)}, nil
	}
	r.cli = cli
	if _, err := cli.Call(ctx, methodInitialize, map[string]any{
		"clientInfo":   map[string]any{"name": "handoff", "version": "1"},
		"capabilities": map[string]any{"experimentalApi": true},
	}); err != nil {
		_ = cli.Close()
		a.log.Warn("重连后 initialize 失败，判不可恢复", "task", taskID, "cause", err)
		return executor.ResumeOutcome{Alive: false,
			Note: fmt.Sprintf("重连后握手失败：%v", err)}, nil
	}
	if err := cli.Notify(methodInitialized, nil); err != nil {
		a.log.Warn("重连后 initialized 通知失败，继续尝试载入 thread", "task", taskID, "cause", err)
	}

	// thread/resume 必须把三个安全参数一起重传（spec §5.6）：恢复路径是最容易让
	// 安全档位悄悄退回开发机 config 的地方。恢复后的第一个 turn/start 会再钉一遍，
	// 两层都钉是刻意的。
	if _, err := cli.Call(ctx, methodThreadResume, map[string]any{
		"threadId":          threadID,
		"cwd":               repoPath,
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
	}); err != nil {
		if !req.Cold {
			_ = cli.Close()
			a.log.Warn("thread/resume 失败，判不可恢复", "task", taskID, "cause", err)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("载入原 thread 失败：%v", err)}, nil
		}
		// 第 4 级：原 thread 载不进，新开一个。上下文断了，manager 会据 Mode=fresh
		// 播报给审核者——这一条必须让人知道，它决定下一条指令要不要重述背景
		a.log.Warn("thread/resume 失败，降级新开会话", "task", taskID, "cause", err)
		if nerr := a.openThreadOnConn(ctx, r, repoPath, req.Model); nerr != nil {
			_ = cli.Close()
			a.log.Warn("降级新开会话也失败，判不可恢复", "task", taskID, "cause", nerr)
			return executor.ResumeOutcome{Alive: false,
				Note: fmt.Sprintf("原 thread 载不进且新建会话失败：%v", nerr)}, nil
		}
		threadID, mode = r.threadID, executor.ResumeModeFresh
	}
	r.threadID = threadID

	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	go a.watchdog(r)

	a.log.Info("codex 任务已恢复", "task", taskID, "thread", threadID,
		"mode", mode, "port", proc.Port)
	return executor.ResumeOutcome{Alive: true, Mode: mode, SessionID: threadID,
		Note: resumeNote(mode, threadID)}, nil
}

// openThreadOnConn 在既有连接上新开一个 thread（冷恢复降级第 4 级用）。
//
// 从 openThread 拆出「已握手之后 thread/start」那一段复用，不复制一份。
func (a *Adapter) openThreadOnConn(ctx context.Context, r *runState, cwd, model string) error {
	params := map[string]any{
		"cwd":               cwd,
		"sandbox":           "workspace-write",
		"approvalPolicy":    "on-request",
		"approvalsReviewer": "user",
	}
	if model != "" {
		params["model"] = model
	}
	res, err := r.cli.Call(ctx, methodThreadStart, params)
	if err != nil {
		return fmt.Errorf("codex thread/start: %w", err)
	}
	var out struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(res, &out); err != nil || out.Thread.ID == "" {
		return fmt.Errorf("codex thread/start 未返回 threadId: %s", res)
	}
	r.threadID = out.Thread.ID
	a.log.Info("codex 新 thread 已建立", "cwd", cwd, "thread", r.threadID)
	return nil
}

// resumeNote 拼恢复结果的一句话结论（进 ResumeOutcome.Note 供 manager 转播）。
func resumeNote(mode, threadID string) string {
	switch mode {
	case executor.ResumeModeReattach:
		return "codex app-server 仍存活，已重连事件流"
	case executor.ResumeModeCold:
		return "codex app-server 已重起并载入原会话（thread " + threadID + "），上下文完整"
	case executor.ResumeModeFresh:
		return "原会话载不进，已新开会话（thread " + threadID + "）；" +
			"上下文从下一条指令开始，回复时请重述必要背景"
	}
	return ""
}

// watchdog 探活 app-server，连续判死到阈值后走统一的失败处置。
//
// 为什么先快后慢：启动初期最容易死（未登录、端口占用），快探能让失败在几秒内
// 暴露；进入稳定期后降到 2s，避免为一个长时间跑的任务持续制造探活流量。
//
// 为什么要连续 watchdogFailThreshold 次才判死：Alive 是 TCP 连通判据（proc.go
// 里说明的弱判据），单次失败可能只是瞬时的连接抖动。
func (a *Adapter) watchdog(r *runState) {
	fails := 0
	for i := 0; ; i++ {
		interval := watchdogSlowInterval
		if i < watchdogFastProbes {
			interval = watchdogFastInterval
		}
		time.Sleep(interval)

		r.emitMu.Lock()
		stopping := r.stopping
		closed := r.evClosed
		r.emitMu.Unlock()
		if stopping || closed {
			a.log.Debug("看门狗退出（任务已停止或事件流已终结）", "task", r.taskID)
			return
		}
		if r.proc == nil {
			return
		}
		if r.proc.Alive() {
			fails = 0
			continue
		}
		fails++
		a.log.Warn("codex app-server 探活失败", "task", r.taskID,
			"port", r.proc.Port, "fails", fails)
		if fails < watchdogFailThreshold {
			continue
		}
		a.log.Error("codex app-server 已判死", "task", r.taskID, "port", r.proc.Port)
		a.dropIf(r.taskID, r)
		a.emitFailed(r, "codex app-server 进程已退出；serve 日志尾部: "+r.proc.LogTail())
		return
	}
}
```

- [ ] **Step 4: 实现 `reap.go`**

```go
// reap.go —— 运行态丢失时的兜底回收（B20）。
//
// 职责：按 serve.json 或确定性会话名回收 tmux 会话，不留孤儿进程。
// 边界：不删任务目录、不碰 worktree（那是归档与 B15 的职责）；
//       **不删 ~/.codex/sessions**——那是 codex 自己的会话历史，删了会破坏
//       用户本人的 `codex resume`（spec §5.5）。
package codex

import "log/slog"

// Reap 回收一个任务残留的 tmux 会话。
//
// 参数：
//   - taskID: 任务 ID（serve.json 读不到时按 handoff-<id8> 兜底）
//   - taskDir: 任务目录（用于读 serve.json）
//
// 返回：回收失败的错误；会话本就不存在时返回 nil（回收是幂等的）
func (a *Adapter) Reap(taskID, taskDir string) error {
	log := a.log
	if log == nil {
		log = slog.Default()
	}
	p, err := ReadServeInfo(taskDir)
	if err != nil {
		// serve.json 没了不代表进程没了——会话名是确定性的，按它兜底
		log.Info("codex serve.json 不可读，按确定性会话名兜底回收",
			"task", taskID, "cause", err)
		p = &Proc{Session: "handoff-" + id8(taskID), TaskDir: taskDir}
	}
	log.Info("codex 回收任务残留", "task", taskID, "session", p.Session)
	return p.Kill()
}
```

- [ ] **Step 5: 在 `Start` 里接上看门狗**

把 Task 6 里那段占位注释替换为：

```go
	go a.watchdog(r)
```

- [ ] **Step 6: 运行全部测试**

Run: `go test ./internal/executor/codex/ -v`
Expected: PASS

- [ ] **Step 7: 加关键节点日志**

- 进入关键操作：`Resume` 开头 `Info` 带 task/task_dir/thread
- 每一级判死都有 `Info`/`Warn` 且**带原因**（无会话 id / 凭据缺失 / 不允许冷恢复 / 任务目录已归档 / 工作区已归档 / 重起失败 / 重连失败 / 握手失败 / resume 失败）——审核者看到「任务没恢复」时，agentd.log 里必须能直接读出是哪一级卡住的
- 状态变更：进入冷恢复、新 app-server 就绪、降级新开会话、恢复成功（带 mode）
- 看门狗：探活失败 `Warn` 带 `fails` 计数；判死 `Error` 带 port；退出 `Debug`
- 成功路径不静默：`codex 任务已恢复` 必打，带 mode + port
- `Reap`：兜底路径 `Info` 明说「按确定性会话名兜底」

- [ ] **Step 8: 加注释**

- 两个文件头：职责 + 边界（不重建 worktree、不改状态、不删 sessions）+ **codex 的两个结构性优势**
- `Resume` doc 注释写明「Alive=false 不是错误，别靠 err 判别」
- 冷恢复互斥、先回收旧会话（撞名）、`thread/resume` 三参重传、fresh 模式要告诉审核者——各一段「为什么」
- `watchdog` 写明「先快后慢」与「为什么要连续三次才判死」

- [ ] **Step 9: 提交**

```bash
git add internal/executor/codex/ && git commit -m "feat(codex): 四级重启恢复阶梯、看门狗与兜底回收"
```

- [ ] **Step 10: 真机验 V-2（`thread/resume` 跨进程重启）**

spec §6 的 V-2。B18 是审核者亲身撞上的缺陷，不能再赌。

1. 起 app-server A，`thread/start` + 跑一个短回合（让模型记住一个只有它知道的事实，比如「记住暗号 pineapple-4417」）
2. `kill` 掉 A
3. 起 app-server B（**同一个用户级 home，新端口**），`initialize` + `thread/resume{threadId, cwd, approvalPolicy, approvalsReviewer}`
4. 新回合里问「刚才的暗号是什么」

判据：

- 答出 `pineapple-4417` → V-2 通过，cold 级恢复真的保留上下文
- `thread/resume` 报错或答不出 → 记录实际行为；若上下文丢失，`ResumeModeCold` 的 `resumeNote` 文案必须改成不承诺「上下文完整」（现文案会误导审核者），并在 spec §5.6 明记

顺带确认第 4 级（`thread/resume` 传一个不存在的 threadId）返回的错误形态，确保 `req.Cold` 时能正确降级到 fresh。

把结论回写 spec §6 的 V-2 行。

```bash
git add docs/superpowers/specs/2026-08-09-handoff-codex-adapter-design.md && git commit -m "docs(spec): B28 V-2 已验——thread/resume 跨进程重启行为"
```

---

### Task 9: 接线与启动预检（`preflight.go`、`cmd/agentd.go`）

**Files:**
- Create: `internal/executor/codex/preflight.go`
- Modify: `cmd/agentd.go:100-107`（注册表与 `--executor` 校验文案）、`cmd/agentd.go:135-142`（`defaultAdapters`）、`cmd/agentd.go:173`、`cmd/agentd.go:179`（flag 说明）
- Modify: `cmd/agentd_test.go:26`（注册表断言加 `codex`）
- Test: `internal/executor/codex/preflight_test.go`

**Interfaces:**
- Consumes: 无（预检只看文件系统与 PATH）
- Produces:
  - `func Preflight(home string, log *slog.Logger) error` —— `home` 为空时取 `os.UserHomeDir()` 下的 `.codex`
- `cmd/agentd.go` 的 `defaultAdapters` 新增一行 `"codex": codex.New(logger)`

**预检的两档区分（spec §3.3）：**

| 检查项 | 档位 | 理由 |
|--------|------|------|
| `codex` 在 PATH | **error** | 不在就一定起不来，早失败早止损（B16：给可行动真因） |
| `~/.codex/auth.json` 存在 | **error** | 未登录时任务必然失败，且失败点在回合中途，诊断成本高 |
| `~/.codex/AGENTS.md` 存在 | **WARN** | 会改变 executor 干活方式（实测模型会先花两个回合去读 skill），但不影响安全边界 |
| `~/.codex/hooks.json` 存在 | **WARN** | 同上；**没有协议级开关能关掉它**，只能靠清理——这是本方案已知且被接受的软肋 |
| `config.toml` 含 `[mcp_servers]` | **WARN** | executor 会多出一批工具 |

**只对 `--executor=codex` 做 error 档**：agentd 的注册表永远保留全部 adapter，若把「codex 未安装」做成 agentd 启动失败，一台只跑 opencode 的机器就起不来了。缺省执行者不是 codex 时，预检**只打 WARN 不阻断**。

- [ ] **Step 1: 写失败测试**

创建 `internal/executor/codex/preflight_test.go`：

```go
package codex_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xushixin/handoff/internal/executor/codex"
)

func TestPreflightFailsWithoutAuth(t *testing.T) {
	home := t.TempDir() // 空目录：没有 auth.json
	err := codex.Preflight(home, nil)
	if err == nil {
		t.Fatal("未登录必须报错——否则失败点会拖到回合中途")
	}
	if !strings.Contains(err.Error(), "codex login") {
		t.Fatalf("错误必须给出可行动指引，实得: %v", err)
	}
}

func TestPreflightPassesWithAuth(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// 污染源只 WARN 不阻断
	if err := os.WriteFile(filepath.Join(home, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"),
		[]byte("model = \"gpt-5.6-sol\"\n[mcp_servers.superdev]\ncommand = \"x\"\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := codex.Preflight(home, nil); err != nil {
		t.Fatalf("污染源只应 WARN 不应阻断，实得: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/executor/codex/ -run TestPreflight -v`
Expected: FAIL —— `undefined: codex.Preflight`

- [ ] **Step 3: 实现 `preflight.go`**

```go
// preflight.go —— agentd 以 codex 为缺省执行者启动时的环境预检。
//
// 职责：
//   - 硬前提（codex 在 PATH、已登录）不满足时给出可行动的错误，早失败早止损
//   - 软污染源（AGENTS.md / hooks.json / mcp_servers）存在时 WARN 并提示清理
//
// 边界：
//   - 不改任何文件：清理是人的决定，agentd 不替用户动他的 ~/.codex
//   - 不检查配置里的 model / sandbox_mode / approvals_reviewer 等项——它们全部
//     被 handoff 协议级压过（spec §1.1 实证），检查它们只会制造噪音
//
// 为什么区分 error 与 WARN：硬前提不满足时任务必然失败，且失败点在回合中途、
// 诊断成本高；软污染源只改变 executor 的干活方式，不影响安全边界（安全档位由
// 代码钉死，spec §1.3），值得提醒但不值得挡住启动。
package codex

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Preflight 检查 executor 机的 codex 环境。
//
// 参数：
//   - home: codex home 目录；空串时取 $HOME/.codex
//   - log: 日志入口（nil 退回 slog.Default()）
//
// 返回：
//   - 硬前提不满足时返回带可行动指引的错误；软污染源只打 WARN，返回 nil
func Preflight(home string, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("读取用户主目录失败，无法定位 ~/.codex: %w", err)
		}
		home = filepath.Join(h, ".codex")
	}
	log.Info("codex 环境预检", "home", home)

	if _, err := exec.LookPath("codex"); err != nil {
		return fmt.Errorf("executor 机上找不到 codex 可执行文件，请先安装 codex-cli 并确保它在 PATH 上: %w", err)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); err != nil {
		return fmt.Errorf("codex 未登录（%s/auth.json 不存在），请在 executor 机执行 `codex login`", home)
	}

	// 以下三条只 WARN：会改变 executor 的干活方式，但不影响安全边界
	if _, err := os.Stat(filepath.Join(home, "AGENTS.md")); err == nil {
		log.Warn("codex home 存在全局 AGENTS.md，executor 的干活方式会被它改变，建议移除",
			"path", filepath.Join(home, "AGENTS.md"))
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); err == nil {
		// 没有协议级开关能关掉 hooks，只能靠清理——本方案已知且被接受的软肋
		log.Warn("codex home 存在 hooks.json，且没有协议级开关能关掉它，强烈建议移除",
			"path", filepath.Join(home, "hooks.json"))
	}
	if b, err := os.ReadFile(filepath.Join(home, "config.toml")); err == nil &&
		strings.Contains(string(b), "[mcp_servers") {
		log.Warn("codex config.toml 配了 mcp_servers，executor 会多出一批工具，建议清空",
			"path", filepath.Join(home, "config.toml"))
	}
	log.Info("codex 环境预检通过", "home", home)
	return nil
}
```

- [ ] **Step 4: 改 `cmd/agentd.go`**

1. `defaultAdapters` 加一行（import 补 `"github.com/xushixin/handoff/internal/executor/codex"`）：

```go
func defaultAdapters(logger *slog.Logger) map[string]executor.Adapter {
	return map[string]executor.Adapter{
		"opencode": opencode.New(logger),
		"claude":   claudecode.New(logger),
		"grok":     grok.New(logger),
		"codex":    codex.New(logger),
		"fake":     fake.New(nil),
	}
}
```

2. 第 100 行的注释与第 105 行的错误文案：

```go
		// 五个执行者都注册：dispatch --executor 可按名选择；opencode/claude/grok/codex
		// 是真实执行，fake 用于演示/测试。缺省由 cfg.Executor.Default 决定（--executor flag 覆盖）
		ads := defaultAdapters(logger)
		if executorFlag != "" {
			if _, ok := ads[executorFlag]; !ok {
				return fmt.Errorf("未知 executor %q（支持 opencode/claude/grok/codex/fake）", executorFlag)
			}
			cfg.Executor.Default = executorFlag
		}
```

3. 在 `cfg.Executor.Default = executorFlag` 之后、`NewManager` 之前插入预检：

```go
		// 缺省执行者是 codex 时做硬预检：codex 复用用户级 ~/.codex（spec §1.3），
		// 未装/未登录会让每个任务都在回合中途失败，诊断成本远高于启动时挡一下。
		// 非缺省时不阻断——注册表保留全部 adapter，一台只跑 opencode 的机器不该
		// 因为没装 codex 就起不来。
		if cfg.Executor.Default == "codex" {
			if err := codex.Preflight("", logger); err != nil {
				return fmt.Errorf("codex 环境预检未通过: %w", err)
			}
		}
```

4. 第 173 行与第 179 行的 flag 文案：

```go
// executorFlag 覆盖 cfg.Executor.Default：opencode（默认，真实执行）| claude | grok | codex | fake（脚本演示）。
var executorFlag string

func init() {
	rootCmd.AddCommand(agentdCmd)
	agentdCmd.Flags().StringVar(&executorFlag, "executor", "",
		"覆盖缺省执行者：opencode（默认）| claude | grok | codex | fake（注册表保留全部，dispatch --executor 仍可按名选择）")
}
```

- [ ] **Step 5: 改 `cmd/agentd_test.go`**

第 26 行的断言列表加 `codex`：

```go
	for _, want := range []string{"opencode", "claude", "grok", "codex", "fake"} {
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./cmd/... ./internal/executor/codex/ -v`
Expected: PASS

Run: `go build ./... && go vet ./...`
Expected: 无输出

- [ ] **Step 7: 加关键节点日志**

- 预检开始 / 通过各一条 `Info`，带 `home`
- 三条软污染源各一条 `Warn`，**带具体路径**（用户要能直接照着删）
- 硬前提失败走 error 返回值（不是日志）——`cmd/agentd.go` 那层会包装成启动失败原因
- `cmd/agentd.go` 里预检失败的包装文案必须保留原因链（`%w`），不许扁平化成「启动失败」（B16）

- [ ] **Step 8: 加注释**

- `preflight.go` 文件头：职责 + 边界（不改文件、不检查被协议压过的配置项）+ **为什么区分 error 与 WARN**
- `Preflight` doc 注释：参数 / 返回 / 两档语义
- `hooks.json` 那条 WARN 上方写明「没有协议级开关能关掉它」——这是本方案已知软肋，代码里要留痕
- `cmd/agentd.go` 的预检块上方写明「为什么只在缺省执行者是 codex 时硬挡」

- [ ] **Step 9: 提交**

```bash
git add cmd/agentd.go cmd/agentd_test.go internal/executor/codex/preflight.go internal/executor/codex/preflight_test.go && git commit -m "feat(codex): 注册进 agentd 执行者表，补 --executor=codex 启动预检"
```

---

### Task 10: 文档与 backlog（`README.md`、`backlog.md`）

**Files:**
- Modify: `README.md`（架构图、executor 挂载、前置条件、agentd 示例、dispatch 示例、命令表、tmux 布局、已知边界、executor 差异表）
- Modify: `docs/superpowers/backlog.md`（B28 推进为 done）

**Interfaces:**
- Consumes: 前九个 task 的全部产出
- Produces: 无代码接口

**README 必须写清的三条 codex 独有事实**（不许含糊，它们是审核者选 executor 时的决策依据）：

1. **部署前置条件**：executor 机的 `~/.codex` 需已登录，且建议清理 `AGENTS.md` / `hooks.json` / `config.toml` 的 `[mcp_servers]`。**这是约定而非代码保证**——`hooks.json` 没有协议级开关可以关掉。
2. **权限行为差异**：codex 用 `on-request` + OS 沙箱，工作区内的操作（含 `rm -rf`）自动放行不进黑名单；**联网操作全程不经过任何人**（`curl … | sh`、`npm install` 零工单直接执行），而同样的命令在 claude/grok 上会走黑名单与三级审批链。
3. **凭据形态**：与用户本人共用同一份 `~/.codex` 登录态，令牌刷新由 codex 自己完成，**不存在 B26 那类任务目录里困住一份新凭据的窗口**；代价是 executor 继承开发机的 codex 全局环境。

- [ ] **Step 1: 读现状**

```bash
grep -n "grok" README.md
```

逐处判断该不该补 codex：架构图（约 15 行）、executor 挂载 bullet（约 24 行）、前置条件（约 29 行）、agentd 示例（约 37 行）、dispatch 示例（约 51 行）、命令表（约 69 行）、tmux 布局段（约 164 行）、已知边界（约 175-176 行）、executor 差异段（约 181-197 行）。

- [ ] **Step 2: 改 README**

按 Step 1 逐处补 codex，并在「executor 差异」一节新增 codex 行，把上面三条独有事实写进去。差异表至少覆盖：进程形态（tmux + app-server WS）、会话标识（threadId）、权限门粒度（沙箱越界才升级）、网络（放开）、凭据（用户级 home 共用）、恢复能力（rollout 在用户级 home，比另三个更结实）。

前置条件一节新增：

```markdown
- **codex**（`--executor codex`）：executor 机需安装 codex-cli 并已 `codex login`。
  建议清理 `~/.codex/AGENTS.md`、`~/.codex/hooks.json`、`~/.codex/config.toml` 的
  `[mcp_servers]`——它们会改变 executor 的干活方式（agentd 启动时会 WARN 提示）。
  这是**约定而非代码保证**：`hooks.json` 没有协议级开关可以关掉。
  `config.toml` 里的 `model` / `sandbox_mode` / `approvals_reviewer` /
  `[sandbox_workspace_write]` **不需要清理**——handoff 全部协议级钉死，压得过它们。
```

- [ ] **Step 3: 改 backlog**

把 B28 的状态推进为 done，备注里明记两条行为差异（§2.1 的权限差异、§2.2 的网络取舍）与真机验收结论（V-1 ~ V-6 各一句）。

- [ ] **Step 4: 自检文档与实现一致**

逐条核对 README 里新写的每一句是否与代码一致，尤其：

- 前置条件里的路径与 `preflight.go` 检查的路径一字不差
- 差异表里的「网络放开」与 `sandboxPolicy()` 的 `networkAccess: true` 一致
- 「工作区内 `rm -rf` 不进黑名单」与 `approvalPolicy: "on-request"` 一致

任一处对不上，**改代码或改文档，不许留着**——README 是审核者选 executor 的唯一依据，说错比不说更坏。

- [ ] **Step 5: 提交**

```bash
git add README.md docs/superpowers/backlog.md && git commit -m "docs(codex): README 补 codex executor 的前置条件、行为差异与凭据形态"
```

---

### Task 11: 真机端到端验收（V-4 / V-6）与 spec 回写

**Files:**
- Modify: `docs/superpowers/specs/2026-08-09-handoff-codex-adapter-design.md`（§6 全部 V 项收口）
- Modify: `docs/superpowers/backlog.md`（B28 收口结论）

**Interfaces:**
- Consumes: 全部实现
- Produces: 无代码接口

**为什么单独一个 task**：前面每个 task 的探针只验一条协议行为，这里验的是**串起来能不能干活**。B23/B25/B26 的教训都是「单点验过、串起来才暴露」。

- [ ] **Step 1: 起 agentd 并派发一个真任务**

```bash
handoff agentd --executor codex
```

先写一个**故意会触发权限门**的一次性验收 plan 到 `/tmp/codex-e2e-plan.md`：

```markdown
# codex 端到端验收

- [ ] 在工作区里新建 `probe.txt`，内容写一行 `hello from codex`
- [ ] 再往工作区**外**的 `~/handoff-e2e-probe.txt` 写一行（这一步预期会被沙箱拦下并升级审批）
- [ ] 提交工作区内的改动，按 handoff 收尾协议输出 HANDOFF_STATUS
```

另开一个会话派发它：

```bash
handoff dispatch --plan /tmp/codex-e2e-plan.md --executor codex
```

走完 `dispatch → wait → reply → diff → done` 全链路。

逐项确认：

- [ ] `handoff wait` 收到权限工单，工单正文含完整命令与 cwd
- [ ] `handoff reply` 批准后回合继续；拒绝后回合**继续跑完**（不是被掐掉——这是 `decline` 而非 `cancel` 的直接验证）
- [ ] `handoff attach` 能看到两个窗口，窗口 1 的 render.log 有命令、推理摘要、权限门、裁决结果
- [ ] `handoff diff` 能看到 executor 的提交
- [ ] `handoff done` 正常收口

- [ ] **Step 2: 验 agentd 重启恢复（B18 串联验证）**

任务跑到中途时 `kill` 掉 agentd 再起来，确认任务被 `RecoverOnStartup` 拉回，`handoff wait` 能继续收事件，`ResumeOutcome.Note` 的文案与实际情况相符。

- [ ] **Step 3: 验 V-4（并发任务共用 `~/.codex`）**

同时派发**三个** codex 任务，观察：

- 三个 tmux 会话都起得来（端口各自独立）
- `~/.codex/sessions/**` 下三份 rollout 各自独立、无覆盖
- `~/.codex/state_*.sqlite` 没有 `database is locked` 类报错（在三个任务的 `serve.log` 里 grep）
- 三个任务的事件流互不串扰（权限工单归属正确）

若出现锁竞争：把现象记进 spec §6 的 V-4 行，并在 README 的已知边界里写明「codex 并发任务数建议上限」。**不要为它临时加重试**——那是绕过问题，正确的做法是先如实记录，再单独立项。

- [ ] **Step 4: 验 V-6（清理过的 home 上 executor 行为是否干净）**

在清理过 `AGENTS.md` / `hooks.json` / `[mcp_servers]` 的 home 上跑一个任务，观察 render.log：

- 模型**没有**在开局花回合去读 superpowers 的 `SKILL.md` 之类的东西
- 没有 MCP 工具启动痕迹
- `thread/start` 回执的 `instructionSources` 为空

若仍不干净，把实际的残留来源记进 spec §1.3 与 README 的前置条件——那意味着前置条件清单不完整。

- [ ] **Step 5: 回写 spec §6 并收口**

把 V-1 ~ V-6 六行全部改成「已验：<结论>」或「未获正面实证：<说明>」。**不许留一行含糊**——spec §6 开头明写「未验之前不得声明对应能力可用」。

- [ ] **Step 6: 提交**

```bash
git add docs/superpowers/specs/2026-08-09-handoff-codex-adapter-design.md docs/superpowers/backlog.md && git commit -m "docs(spec): B28 真机端到端验收，V-1~V-6 全部收口"
```

- [ ] **Step 7: 最终代码审阅清单**

按全局 CLAUDE.md §5 逐项确认，任一未过必须修复后再提交审阅：

- [ ] 完成目标：spec §5 的五动作全部实现，§7 的改动清单每一项都落地
- [ ] 架构一致：包结构与 spec §7 一致，`turn` 未改、`grok` 未动、`oneshot.go` 未动
- [ ] 文件头注释：`codex/` 下每个新文件都有职责 + 边界
- [ ] 方法注释：每个导出方法有参数 / 返回 / 注意
- [ ] 中文注释：本计划点名的每一处「为什么」都在代码里
- [ ] 合理日志：按 `instrumenting-code` 清单自检——错误分支带上下文、外部调用前后有日志、成功路径不静默、无 `fmt.Printf`
- [ ] 无跨层调用：adapter 不写 store、不做审批判断、不做状态迁移
- [ ] 优先复用：`turn` 包全部复用，未复制一份
- [ ] 无硬编码：沙箱策略、方法名、超时、上限全部是常量或函数
- [ ] 安全档位：`sandboxPolicy()` 的五个字段、`approvalPolicy`、`approvalsReviewer` 与 spec §2 逐字一致；`decisionFor` 只产出 accept/decline








