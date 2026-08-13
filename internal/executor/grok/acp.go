// acp.go —— ACP（Agent Client Protocol）的 WebSocket JSON-RPC 双向客户端。
//
// 职责：
//   - 维护一条到 grok agent serve 的 WS 连接，跑 JSON-RPC 2.0 双向消息
//   - 我方请求（initialize/session.*）按 id 匹配响应；session/prompt 用 CallAsync
//     异步等待（它要跑完一整个回合才响应）
//   - 对方通知（session/update 及 _x.ai/* 私有通知）经 OnNotify 分发
//   - 对方请求（session/request_permission）经 OnPermission 上抛，应答可延迟
//     任意久后经 Reply 回发——协调者可能过夜才裁决（spec §5.1 实测 20min 无超时）
//
// 边界：
//   - 不认识 ACP 的业务语义（不知道什么是权限、什么是回合），只做协议管道；
//     语义翻译在 adapter.go
//   - 不重连：重连策略属 adapter 的生命周期决策，本层只在连接死亡时 OnClosed 通知
package grok

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"

	"github.com/coder/websocket"

	"github.com/xushixin/handoff/internal/executor/rawtap"
)

// ACPResult 是一次异步调用的终局（二选一）。
type ACPResult struct {
	Result json.RawMessage
	Err    error
}

// ACPHandler 是 adapter 侧的回调面。实现方必须假定回调在读循环 goroutine 上
// 触发：**不得在回调里做阻塞操作**，否则会卡住整条连接的消息消费。
type ACPHandler interface {
	// OnNotify 收到对方通知（无 id 的消息）
	OnNotify(method string, params json.RawMessage)
	// OnAskQuestion 收到 _x.ai/ask_user_question（对方请求，**必须应答**）。
	// 不应答会让 session/prompt 永不返回、任务永久静止（spec §4.2.3 / §5.3(c) 实测）。
	OnAskQuestion(reqID json.RawMessage, params json.RawMessage)
	// OnPermission 收到 session/request_permission（对方请求，需应答）。
	// reqID 原样保存，裁决回来后经 Reply 回发。
	OnPermission(reqID json.RawMessage, params json.RawMessage)
	// OnClosed 连接终止（err 为终止原因，正常关闭时为 nil）
	OnClosed(err error)
}

// ACPClient 是一条 ACP 连接。并发安全：nextID/pending 由 mu 保护；
// 写连接由 writeMu 串行化（websocket 不允许并发写）。
type ACPClient struct {
	conn   *websocket.Conn
	log    *slog.Logger
	cancel context.CancelFunc
	// rawTap 是本连接的上游原始字节旁路（DialACP 开启，readLoop 退出时 Close）。
	// 缺省关闭时为 nil，Write 是空操作，不影响任何既有行为。
	rawTap *rawtap.Tap

	writeMu sync.Mutex

	mu             sync.Mutex
	nextID         int
	pending        map[int]chan ACPResult
	closed         bool
	activelyClosed bool // Close() 置位：读循环据此以 nil 通知 OnClosed
}

// DialACP 连接 ACP 端点并启动读循环。
//
// 参数：
//   - ctx: 仅控制握手阶段；连接生命周期延续到 Close
//   - wsURL: 形如 ws://127.0.0.1:<port>/ws?server-key=<secret>
//   - h: 回调面（不得为 nil）
//   - log: 日志入口（nil 退回 slog.Default()）
//
// 注意：wsURL 含 secret，**日志里绝不能打印它**（本函数只记录 host 与 path）。
func DialACP(ctx context.Context, wsURL string, h ACPHandler, log *slog.Logger) (*ACPClient, error) {
	if log == nil {
		log = slog.Default()
	}
	log.Info("ACP 连接中")
	conn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		log.Error("ACP 连接失败", "cause", err)
		return nil, fmt.Errorf("连接 ACP 端点: %w", err)
	}
	// 单条消息上限放宽：initialize 响应含完整模型/命令清单，实测数 KB～数十 KB
	conn.SetReadLimit(8 << 20)

	runCtx, cancel := context.WithCancel(context.Background())
	c := &ACPClient{conn: conn, log: log, cancel: cancel, pending: map[int]chan ACPResult{}}
	// 原始字节旁路：随建连开启，readLoop 退出时 Close。taskID 传空串——
	// DialACP 不持有任务标识，按协议不为此重构构造链路；文件名退化为
	// grok-.jsonl，探针一次只跑一个任务，可接受
	c.rawTap = rawtap.Open("grok", "", log)
	go c.readLoop(runCtx, h)
	log.Info("ACP 连接就绪")
	return c, nil
}

// Call 发起请求并阻塞等待响应。
func (c *ACPClient) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
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
//
// 为什么需要它：session/prompt 要跑完一整个回合（可能几十分钟）才响应，
// Start 必须立即返回，不能阻塞在这上面。
func (c *ACPClient) CallAsync(method string, params any) (<-chan ACPResult, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("ACP 连接已关闭")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan ACPResult, 1)
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
		c.log.Error("ACP 请求发送失败", "method", method, "cause", err)
		return nil, err
	}
	c.log.Debug("ACP 请求已发出", "method", method, "id", id)
	return ch, nil
}

// Reply 应答对方请求（用于权限裁决回发）。reqID 必须是 OnPermission 收到的原值。
func (c *ACPClient) Reply(reqID json.RawMessage, result any) error {
	if err := c.write(map[string]any{
		"jsonrpc": "2.0", "id": json.RawMessage(reqID), "result": result,
	}); err != nil {
		c.log.Error("ACP 应答发送失败", "req_id", string(reqID), "cause", err)
		return err
	}
	c.log.Info("ACP 应答已发出", "req_id", string(reqID))
	return nil
}

// Notify 发送通知（无需应答，用于 session/cancel）。
func (c *ACPClient) Notify(method string, params any) error {
	msg := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		msg["params"] = params
	}
	return c.write(msg)
}

// Close 关闭连接，所有挂起的请求以错误终结。
func (c *ACPClient) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.activelyClosed = true
	c.mu.Unlock()
	c.cancel()
	c.log.Info("ACP 连接关闭")
	return c.conn.Close(websocket.StatusNormalClosure, "")
}

func (c *ACPClient) write(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("序列化 ACP 消息: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(context.Background(), websocket.MessageText, b)
}

// readLoop 消费连接上的全部消息直到出错，并在退出时终结所有挂起请求。
func (c *ACPClient) readLoop(ctx context.Context, h ACPHandler) {
	var exitErr error
	defer func() {
		c.rawTap.Close() // 连接终止即收尾旁路；nil 接收者安全
		c.mu.Lock()
		c.closed = true
		active := c.activelyClosed
		pend := c.pending
		c.pending = map[int]chan ACPResult{}
		c.mu.Unlock()
		// 挂起请求全部以错误终结，避免调用方永久等待
		for id, ch := range pend {
			c.log.Warn("ACP 连接终止，挂起请求作废", "id", id)
			ch <- ACPResult{Err: fmt.Errorf("ACP 连接终止: %w", exitErr)}
		}
		c.log.Info("ACP 读循环退出", "cause", exitErr)
		// 主动 Close（Close 置位 activelyClosed）时 OnClosed 传 nil：
		// 此时「连接终止」是调用方主动行为而非故障，注释承诺「正常关闭时为 nil」
		// 必须成为事实，adapter 的 onClosed 依此跳过失败处置
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
				Data    any    `json:"data"`
			} `json:"error"`
		}
		if err := json.Unmarshal(data, &msg); err != nil {
			// 宽容：对端输出不可信，坏消息跳过不中断连接
			c.log.Warn("ACP 消息解析失败，跳过", "cause", err)
			continue
		}

		switch {
		case msg.Method != "" && len(msg.ID) > 0:
			// 对方请求。注意先判 Method 再判 ID：agent 侧请求 id 从 0 自增，与本端
			// 请求 id 空间**重叠**（spec §5.3(d) 实测），只看 id 会把对方的请求
			// 误认成自己请求的响应。
			// 未识别的请求一律回 -32601——静默丢弃有 id 的请求 = 让对方永久等待。
			switch msg.Method {
			case "session/request_permission":
				c.log.Info("ACP 收到权限请求", "req_id", string(msg.ID))
				h.OnPermission(append(json.RawMessage(nil), msg.ID...), msg.Params)
				continue
			case "_x.ai/ask_user_question":
				c.log.Info("ACP 收到提问请求", "req_id", string(msg.ID))
				h.OnAskQuestion(append(json.RawMessage(nil), msg.ID...), msg.Params)
				continue
			}
			c.log.Debug("ACP 未处理的对方请求，回 -32601", "method", msg.Method)
			_ = c.write(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(msg.ID),
				"error": map[string]any{"code": -32601, "message": "unhandled"}})
		case msg.Method != "":
			h.OnNotify(msg.Method, msg.Params)
		case len(msg.ID) > 0:
			var id int
			if err := json.Unmarshal(msg.ID, &id); err != nil {
				c.log.Warn("ACP 响应 id 非数字，跳过", "id", string(msg.ID))
				continue
			}
			c.mu.Lock()
			ch, ok := c.pending[id]
			delete(c.pending, id)
			c.mu.Unlock()
			if !ok {
				c.log.Warn("ACP 响应无对应请求，丢弃", "id", id)
				continue
			}
			if msg.Error != nil {
				ch <- ACPResult{Err: fmt.Errorf("ACP 错误 %d: %s", msg.Error.Code, msg.Error.Message)}
				continue
			}
			ch <- ACPResult{Result: msg.Result}
		default:
			c.log.Debug("ACP 无法归类的消息，跳过")
		}
	}
}
