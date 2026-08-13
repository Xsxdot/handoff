// appserver.go —— codex app-server 的 WebSocket JSON-RPC 2.0 双向客户端。
//
// 职责：
//   - 维护一条到 `codex app-server --listen ws://…` 的 WS 连接
//   - 我方请求（initialize / thread.* / turn.*）按 id 匹配响应；turn/start 用
//     CallAsync 拿异步通道（它虽然立即返回，但仍不能阻塞 Start 的启动路径）
//   - 对方通知经 OnNotify 分发；对方请求经 OnServerRequest 上抛，应答可延迟任意久
//     后经 Reply 回发——协调者可能过夜才裁决
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

	"github.com/xushixin/handoff/internal/executor/rawtap"
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
	// rawTap 是本连接的上游原始字节旁路（Dial 开启，readLoop 退出时 Close）。
	// 缺省关闭时为 nil，Write 是空操作，不影响任何既有行为。
	rawTap *rawtap.Tap

	writeMu sync.Mutex

	mu             sync.Mutex
	nextID         int
	pending        map[int]chan Result
	closed         bool
	activelyClosed bool // Close() 置位：读循环据此以 nil 通知 OnClosed

	// replyHook 是应答回发的测试缝：非 nil 时 Reply/ReplyError 走它而不碰真连接。
	// 生产路径恒为 nil。
	replyHook func(reqID json.RawMessage, payload any) error
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
	// 原始字节旁路：随建连开启，readLoop 退出时 Close。taskID 传空串——
	// Dial 不持有任务标识，按协议不为此重构构造链路；文件名退化为
	// codex-.jsonl，探针一次只跑一个任务，可接受
	c.rawTap = rawtap.Open("codex", "", log)
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
	if c.replyHook != nil {
		return c.replyHook(reqID, result)
	}
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
	if c.replyHook != nil {
		return c.replyHook(reqID, map[string]any{"code": code, "message": message})
	}
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
		c.rawTap.Close() // 连接终止即收尾旁路；nil 接收者安全
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
		// 原始字节旁路：在 Unmarshal 之前写，解析失败被跳过的坏帧也要留样——
		// 被截断的工具调用极可能正是解析失败的那一帧，跳过它样本就不完整
		c.rawTap.Write(data)

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
