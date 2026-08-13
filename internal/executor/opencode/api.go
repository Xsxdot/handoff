// Package opencode 提供 opencode server 的最小 HTTP/SSE 客户端与 serve 进程管理。
//
// api.go —— opencode server 的 HTTP 客户端与 SSE 事件订阅。
//
// 职责：
//   - 覆盖 MVP 所需的四个端点：POST /session、POST /session/{id}/prompt_async、
//     POST /session/{id}/permissions/{permID}、GET /event（SSE 事件流）
//   - 统一的 basic auth（用户名固定 opencode，密码来自 OPENCODE_SERVER_PASSWORD）
//   - SSE 断流自动指数退避重连（1s→2s→…→30s），直到 ctx 取消
//   - 两个 http.Client 分工：sseClient 无 Timeout 供 SSE 长连接，httpClient 带
//     Timeout=30s 供一元调用（半死 server 不永久挂起，why 见 NewAPI 注释）
//
// 边界：
//   - 不理解事件语义：事件类型字段含义、回合结束判定等语义归 adapter（Task 9），
//     本层只保证「事件 JSON 原样送达 onEvent」与「垃圾行不中断订阅」
//   - 不管理进程生命周期（见 proc.go）
//
// 为什么 SSE 手写解析而不引第三方依赖：SSE 协议只有 data:/event:/空行三种形态，
// 手写按行解析约 40 行即可完全掌控。事件 payload 可能携带大段文本（plan、diff、
// 用户问答），必须能按需配置 scanner buffer（本实现 1MB）并按行处理超长 token；
// 第三方库的 buffer 上限与截断行为不可控，且我们需要的「未知行 Debug 跳过、绝不
// 中断」宽容语义由自己实现最精确，不依赖库的严格/宽松差异。
package opencode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/executor/rawtap"
	"github.com/Xsxdot/handoff/internal/executor/turn"
)

// 常量说明：
//   - sessionTitle：建会话时写入的标题，便于用户在 opencode TUI
//     里区分会话归属
//   - sseInitialBackoff/sseMaxBackoff：断流重连的指数退避区间
//   - sseScanBuffer：scanner 单行上限。事件可能携带大段文本，必须给足空间；
//     超过该上限的行按流异常处理（走重连），而不是崩掉订阅
const (
	sessionTitle      = "handoff"
	sseInitialBackoff = 1 * time.Second
	sseMaxBackoff     = 30 * time.Second
	sseScanBuffer     = 1 << 20 // 1MB
	// sseStableAfter 是「这次连接算健康」的存活时长门槛（退避复位条件，A-8）。
	// 为什么 5s：正常的 /event 长连接一挂就是分钟到小时级，而半死 server 的
	// 「200 后立刻关流」在毫秒级——5s 把两者分得很开，且不会因一次正常的
	// 短暂网络抖动就误判为健康
	sseStableAfter = 5 * time.Second
	// unaryTimeout 是一元调用（建会话/发 prompt/权限应答）的超时上限。
	// 为什么 30s：这些调用对应「一次人工应答的最长合理等待」——opencode 若半死
	// （TCP 通但不响应），30s 内拿不到响应就按失败处理，不让 handoff reply 回程
	// 在协调者终端永久挂起。SSE 长连接不适用此值（见 NewAPI 的 sseClient 注释）。
	unaryTimeout = 30 * time.Second
	// ownershipTimeout 是子会话归属判定（GetSession）的超时上限。
	// 为什么不用 unaryTimeout（30s）：本调用在 SSE 事件回调里同步执行，会阻塞
	// 本任务的事件流。30s 是按「一次人工应答的最长合理等待」定的，用在热路径上
	// 太长；5s 足够一次本机 HTTP 往返（serve 就在 127.0.0.1 上），且此刻任务
	// 本来就在等这个审批，短暂阻塞不额外损失什么。
	ownershipTimeout = 5 * time.Second
)

// API 是 opencode server 的最小 HTTP 客户端，持有 baseURL 与鉴权密码。
//
// 并发安全：字段构造后只读，可被多个 goroutine 同时使用。
type API struct {
	baseURL  string
	password string
	// sseClient 服务 SSE 长连接（SubscribeEvents），不设 Timeout——连接生命周期
	// 由 ctx 控制，设 Timeout 会把正常的长时间事件流误杀
	sseClient *http.Client
	// httpClient 服务一元调用（CreateSession/PromptAsync/RespondPermission），
	// Timeout=unaryTimeout：半死 server（TCP 通但不响应）在此上限内必然失败，
	// 不会让调用方永久挂起
	httpClient *http.Client
	// sseInitialBackoff/sseMaxBackoff 是 SSE 断流重连的退避区间（P1-10a：
	// 每次成功连接后复位到初始值，防止退避爬到 30s 封顶后终身不降；
	// 测试经 NewAPIWithSSEBackoff 注入毫秒级退避）
	sseInitialBackoff time.Duration
	sseMaxBackoff     time.Duration
	// sseStableAfter 是「这次连接算健康」的存活时长门槛：连接活够这么久才
	// 复位退避（A-8）。半死 server 的连接寿命远低于它，因此照常退避
	sseStableAfter time.Duration
}

// NewAPI 创建 opencode server 客户端。
//
// 参数：
//   - baseURL: opencode serve 的地址（如 http://127.0.0.1:4345），尾斜杠会被剥掉
//   - password: OPENCODE_SERVER_PASSWORD 的值，与用户名 opencode 拼成 basic auth
func NewAPI(baseURL, password string) *API {
	return NewAPIWithUnaryTimeout(baseURL, password, unaryTimeout)
}

// NewAPIWithUnaryTimeout 是 NewAPI 的超时可注入变体：测试注入毫秒级短超时验证
// 「半死 server 不永久挂起」；生产代码一律走 NewAPI 的 30s 默认值。
//
// 参数：
//   - timeout: 一元调用（httpClient）的超时；SSE 长连接（sseClient）不受影响
func NewAPIWithUnaryTimeout(baseURL, password string, timeout time.Duration) *API {
	return newAPI(baseURL, password, timeout, sseInitialBackoff, sseMaxBackoff, sseStableAfter)
}

// NewAPIWithSSEBackoff 是 NewAPI 的 SSE 退避可注入变体：测试注入毫秒级退避，
// 让「成功连接后复位」的时间敏感断言不依赖真实 1s..30s 节奏；生产代码一律走
// NewAPI 的默认退避。
//
// 参数：
//   - initial/max: SSE 断流重连的初始/封顶退避（见 SubscribeEvents）
func NewAPIWithSSEBackoff(baseURL, password string, initial, max time.Duration) *API {
	return NewAPIWithSSETiming(baseURL, password, initial, max, sseStableAfter)
}

// NewAPIWithSSETiming 在退避区间之外再注入「连接算健康」的存活门槛，
// 供「退避复位按连接寿命而非按 200 响应」（A-8）的断言把门槛压到毫秒级。
//
// 参数：
//   - stableAfter: 连接存活多久才算健康、才复位退避（生产默认 sseStableAfter）
func NewAPIWithSSETiming(baseURL, password string, initial, max, stableAfter time.Duration) *API {
	return newAPI(baseURL, password, unaryTimeout, initial, max, stableAfter)
}

// newAPI 是全部构造器的公共骨架。
func newAPI(baseURL, password string, unaryTimeout, sseInitial, sseMax, stableAfter time.Duration) *API {
	// 两个 client 各持一个 Transport：拨号超时统一 10s（对端不可达不挂死），
	// 一元与 SSE 的差异只在 client 级 Timeout
	dialer := (&net.Dialer{Timeout: 10 * time.Second}).DialContext
	return &API{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		sseClient: &http.Client{
			Transport: &http.Transport{DialContext: dialer},
		},
		httpClient: &http.Client{
			Timeout:   unaryTimeout,
			Transport: &http.Transport{DialContext: dialer},
		},
		sseInitialBackoff: sseInitial,
		sseMaxBackoff:     sseMax,
		sseStableAfter:    stableAfter,
	}
}

// log 返回运行时 slog.Default()。
//
// 为什么不用包级 var：logx.Setup 在 main 里才执行 slog.SetDefault，包级 var 在
// init 时求值会锁死默认 logger，导致本模块日志绕开 logx 的「JSON 文件 + stderr
// 双路」输出（与 Task 5 对 store/config 的同款修正）。
func (a *API) log() *slog.Logger {
	return slog.Default()
}

// do 发送带 basic auth 的 JSON 请求，返回响应（调用方负责关闭 resp.Body）。
//
// body 为 nil 时请求体为空；非 nil 时序列化为 JSON 并带 Content-Type。
func (a *API) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体 %s: %w", path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.baseURL+path, rd)
	if err != nil {
		return nil, fmt.Errorf("构造请求 %s: %w", path, err)
	}
	req.Header.Set("Authorization", "Basic "+basicAuth("opencode", a.password))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return a.httpClient.Do(req)
}

// basicAuth 构造 HTTP basic auth 的 Base64 编码值（用户名:密码）。
func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// httpError 把非 2xx 响应转为带状态码与响应体（截断 200 字符）的错误，
// 并打 Error 日志——响应体是「服务端为什么拒绝」的第一手线索。
func (a *API) httpError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	trunc := turn.TruncateRunes(string(body), 200)
	a.log().Error("opencode 请求失败", "op", op, "status", resp.StatusCode, "body", trunc)
	return fmt.Errorf("%s: 状态码 %d: %s", op, resp.StatusCode, trunc)
}

// sessionCreateRequest 是 POST /session 的请求体（title 可选，对齐 opencode 官方契约）。
type sessionCreateRequest struct {
	Title string `json:"title"`
}

// sessionResponse 是 POST /session 的响应体（Session 对象的 id 字段）。
type sessionResponse struct {
	ID string `json:"id"`
}

// sessionListItem 是 GET /session 列表里每个会话的最小形状（冷恢复在场校验只用 id）。
type sessionListItem struct {
	ID string `json:"id"`
}

// sessionDetail 是 GET /session/{id} 的响应体形状。
//
// 只取三个字段：id 供自校验，parentID 是把子会话归属回父任务的唯一依据，
// title 供工单标注「这条审批来自哪个子 agent」。响应体里还有 directory /
// agent / permission 等字段，本层用不上就不入结构——多解析一个字段就多一处
// 会随 opencode 版本漂移的耦合面。
type sessionDetail struct {
	ID       string `json:"id"`
	ParentID string `json:"parentID"`
	Title    string `json:"title"`
}

// GetSession 取单个会话的详情，用于把子会话归属回父任务。
//
// 参数：
//   - ctx: 上下文；调用方负责叠加 ownershipTimeout
//   - sessionID: 目标会话 id
//
// 返回：
//   - sessionDetail: 会话详情
//   - err: sessionID 为空、请求失败、非 2xx、响应解析失败时非 nil，此时详情为零值
//
// 注意：
//   - sessionID 为空直接返回错误，不触达服务端：拿空 id 拼出的 "/session/" 只会
//     换来一个 404，白白占掉一次超时预算
//   - 本方法在 SSE 事件回调里同步调用（见 adapter.resolveChildSession），
//     阻塞的是本任务的事件流——超时必须用 ownershipTimeout 而非 unaryTimeout
func (a *API) GetSession(ctx context.Context, sessionID string) (d sessionDetail, err error) {
	if sessionID == "" {
		return sessionDetail{}, fmt.Errorf("查询会话详情：会话 id 为空")
	}
	start := time.Now()
	path := "/session/" + sessionID
	a.log().Info("opencode 查询会话详情", "path", path, "session", sessionID)
	defer func() {
		if err != nil {
			a.log().Error("opencode 查询会话详情失败", "path", path, "session", sessionID, "cause", err)
		} else {
			a.log().Info("opencode 会话详情已取得", "path", path, "session", sessionID,
				"parent", d.ParentID, "elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return sessionDetail{}, fmt.Errorf("查询会话详情请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return sessionDetail{}, a.httpError("查询会话详情", resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return sessionDetail{}, fmt.Errorf("解析会话详情: %w", err)
	}
	return d, nil
}

// HasSession 检查指定会话 id 是否仍存在于 serve 的会话列表里。
//
// 返回：
//   - ok: 会话是否在场；GET /session 失败或列表解析失败时返回 (false, err)
//
// 注意：
//   - 本方法只用于冷恢复的会话在场校验（spec §5.5.2）：会话存在全局 sqlite，
//     进程重起不影响它，但要确认它真的还在——不能默认。不在就降级新会话
func (a *API) HasSession(ctx context.Context, sessionID string) (ok bool, err error) {
	start := time.Now()
	const path = "/session"
	a.log().Info("opencode 查询会话列表", "path", path)
	defer func() {
		if err != nil {
			a.log().Error("opencode 查询会话列表失败", "path", path, "cause", err)
		} else {
			a.log().Info("opencode 会话在场校验完成", "path", path, "session", sessionID,
				"ok", ok, "elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, fmt.Errorf("查询会话列表请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, a.httpError("查询会话列表", resp)
	}
	var list []sessionListItem
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return false, fmt.Errorf("解析会话列表: %w", err)
	}
	for _, s := range list {
		if s.ID == sessionID {
			return true, nil
		}
	}
	return false, nil
}

// SessionMessage 是会话里一条消息的最小形状（对账只需要这几个字段）。
//
// 字段说明：
//   - ID: 消息 id，同时是对账水位的载体
//   - Role: "assistant" | "user"
//   - CompletedMS: 完结时刻（毫秒 epoch）；**0 表示尚未完结**（消息未 finalize，
//     在飞或冻结），对账据此判「回合还在跑」
//   - ErrorText: 非空表示该消息以错误告终（info.error 的原始 JSON）
//   - Text: 该消息全部文本 part 的拼接结果，交给 turn.ParseTrailer 分类
//   - Finish: 该消息的完结方式（info.finish），取值实测：""（缺席）/ "tool-calls" /
//     "stop" / "unknown"。**只当正向结束标记用**：finish=="stop" 一定意味着回合
//     结束，但 finish=="tool-calls" 既可能是「中间工具消息、回合继续」也可能是
//     「被拒/工具报错而终」——不能反过来当「未结束」判据，须看 ToolStatus 消歧
//   - ErrorName: 消息级错误的类型名（info.error.name）；实测 "MessageAbortedError"
//     表示会话被 abort 而终（finish 缺席）
//   - ToolStatus: 最后一条 tool part 的 state.status（取值实测 "running"/
//     "completed"/"error"）。用于把「finish=tool-calls 的回合终态」与「真·回合
//     中途冻结」区分开：error=被拒/报错而终（补发），completed=中途冻结（不补发）
type SessionMessage struct {
	ID          string
	Role        string
	CompletedMS int64
	ErrorText   string
	Text        string
	Finish      string
	ErrorName   string
	ToolStatus  string
}

// sessionMessageEnvelope 是 GET /session/{id}/message 列表里每一项的形状。
//
// 注意：字段路径按真实抓包确定（testdata/session_messages.json），不是按
// schema 名字推断的。改动前请先重新抓包核对。
type sessionMessageEnvelope struct {
	Info struct {
		ID   string `json:"id"`
		Role string `json:"role"`
		Time struct {
			Completed int64 `json:"completed"`
		} `json:"time"`
		Finish string          `json:"finish"`
		Error  json.RawMessage `json:"error"`
	} `json:"info"`
	Parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
		// State 是 tool part 的运行时状态（state.status 区分 running/completed/error）；
		// 非 tool part 无此字段
		State struct {
			Status string `json:"status"`
		} `json:"state"`
	} `json:"parts"`
}

// LastAssistantMessage 取会话里最后一条 assistant 消息（对账的数据源，B38）。
//
// 参数：
//   - ctx: 控制单次请求超时
//   - sessionID: 目标会话
//
// 返回：
//   - (*SessionMessage, nil): 找到了。CompletedMS==0 表示该回合仍在进行
//   - (nil, nil): 会话里还没有任何 assistant 消息——**合法状态，不是错误**
//   - (nil, err): 请求或解析失败
//
// 注意：
//   - 只看最后一条 assistant 消息就够，依据是「一个断连窗口内至多跨越一个回合
//     边界」（spec §2.2）；不需要全量拉取比对
//   - 权限请求**查不回来**：本端点的 tool part 只有 callID 没有权限 id，而
//     RespondPermission 要求真实 id、伪造即 404（更早的 spike 结论，见
//     adapter.go 的 onReconnect 降级告警）。故本方法不尝试提取权限
func (a *API) LastAssistantMessage(ctx context.Context, sessionID string) (msg *SessionMessage, err error) {
	start := time.Now()
	path := "/session/" + sessionID + "/message"
	a.log().Info("opencode 查会话尾部", "path", path, "session", sessionID)
	defer func() {
		switch {
		case err != nil:
			a.log().Error("opencode 查会话尾部失败", "path", path, "session", sessionID, "cause", err)
		case msg == nil:
			a.log().Info("opencode 会话尾部无 assistant 消息", "path", path,
				"session", sessionID, "elapsed_ms", time.Since(start).Milliseconds())
		default:
			a.log().Info("opencode 会话尾部已取得", "path", path, "session", sessionID,
				"msg", msg.ID, "completed_ms", msg.CompletedMS, "has_error", msg.ErrorText != "",
				"text_runes", len([]rune(msg.Text)), "elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("查会话消息请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, a.httpError("查会话消息", resp)
	}
	var list []sessionMessageEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, fmt.Errorf("解析会话消息: %w", err)
	}
	// 从尾部往前找第一条 assistant：列表按时间正序，最后一条 assistant 才是
	// 「当前回合」的载体；user 消息夹在中间（reply 的应答就是一条 user 消息）
	for i := len(list) - 1; i >= 0; i-- {
		e := list[i]
		if e.Info.Role != "assistant" {
			continue
		}
		out := &SessionMessage{
			ID:          e.Info.ID,
			Role:        e.Info.Role,
			CompletedMS: e.Info.Time.Completed,
			Finish:      e.Info.Finish,
		}
		// error 字段的形态在不同版本里可能是 null / 字符串 / 对象（本次真机抓包
		// 的会话里该字段完全缺席），统一按原始 JSON 处理：非 null 即视为出错，
		// 原文进 ErrorText 供协调者看；对象形态时取 name 进 ErrorName（对账判
		// "MessageAbortedError" 用）
		if s := strings.TrimSpace(string(e.Info.Error)); s != "" && s != "null" {
			out.ErrorText = s
			var errObj struct {
				Name string `json:"name"`
			}
			if json.Unmarshal(e.Info.Error, &errObj) == nil {
				out.ErrorName = errObj.Name
			}
		}
		var sb strings.Builder
		for _, p := range e.Parts {
			if p.Type == "text" && p.Text != "" {
				sb.WriteString(p.Text)
			}
			if p.Type == "tool" && p.State.Status != "" {
				out.ToolStatus = p.State.Status // 最后一条 tool part 的 status
			}
		}
		out.Text = sb.String()
		return out, nil
	}
	return nil, nil
}

// CreateSession 在 opencode server 上创建会话。
//
// 返回：
//   - sessionID: 新建会话的 id，后续 PromptAsync / RespondPermission 都需要它
//   - err: 请求或解析失败
func (a *API) CreateSession(ctx context.Context) (sessionID string, err error) {
	start := time.Now()
	const path = "/session"
	a.log().Info("opencode 创建会话", "path", path)
	defer func() {
		if err != nil {
			a.log().Error("opencode 创建会话失败", "path", path, "cause", err)
		} else {
			a.log().Info("opencode 会话已创建", "path", path, "session", sessionID,
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodPost, path, sessionCreateRequest{Title: sessionTitle})
	if err != nil {
		return "", fmt.Errorf("创建会话请求: %w", err)
	}
	defer resp.Body.Close()
	// 接受整个 2xx 区间：真实 opencode server 对建会话可能回 201/202 而非 200，
	// 只认 200 会让合法的创建成功被当成失败
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", a.httpError("创建会话", resp)
	}
	var out sessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("解析创建会话响应: %w", err)
	}
	if out.ID == "" {
		return "", fmt.Errorf("创建会话响应缺少 id 字段")
	}
	return out.ID, nil
}

// promptPart 是 prompt_async 请求中一个 text part（对齐 opencode 官方契约）。
type promptPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// promptAsyncRequest 是 POST /session/{id}/prompt_async 的请求体。
type promptAsyncRequest struct {
	Parts []promptPart `json:"parts"`
}

// PromptAsync 向会话发送一条 prompt，opencode 随即开始执行，函数立即返回。
//
// 参数：
//   - sessionID: CreateSession 返回的会话 id
//   - text: prompt 文本（计划内容、用户指令等）
//
// 注意：
//   - 本调用不等待执行结果，执行事件通过 SubscribeEvents 消费
func (a *API) PromptAsync(ctx context.Context, sessionID, text string) (err error) {
	start := time.Now()
	path := "/session/" + sessionID + "/prompt_async"
	a.log().Info("opencode 发送 prompt", "path", path, "session", sessionID,
		"prompt_len", len(text))
	defer func() {
		if err != nil {
			a.log().Error("opencode 发送 prompt 失败", "path", path, "session", sessionID,
				"cause", err)
		} else {
			a.log().Info("opencode prompt 已发送", "path", path, "session", sessionID,
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodPost, path, promptAsyncRequest{
		Parts: []promptPart{{Type: "text", Text: text}},
	})
	if err != nil {
		return fmt.Errorf("发送 prompt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.httpError("发送 prompt", resp)
	}
	return nil
}

// RespondPermission 应答 opencode 的权限请求。
//
// 参数：
//   - sessionID: 权限所属会话
//   - permID: 权限请求 id（来自 SSE 事件的 permissionID 字段）
//   - response: "once"（批准本次）或 "reject"（拒绝）
//
// 注意：
//   - 非法 response 值直接返回错误，不触达服务端
func (a *API) RespondPermission(ctx context.Context, sessionID, permID, response string) (err error) {
	if response != "once" && response != "reject" {
		return fmt.Errorf("非法权限应答 %q，仅支持 once/reject", response)
	}
	start := time.Now()
	path := "/session/" + sessionID + "/permissions/" + permID
	a.log().Info("opencode 应答权限", "path", path, "session", sessionID,
		"permission", permID, "response", response)
	defer func() {
		if err != nil {
			a.log().Error("opencode 应答权限失败", "path", path, "session", sessionID,
				"permission", permID, "cause", err)
		} else {
			a.log().Info("opencode 权限应答完成", "path", path, "session", sessionID,
				"permission", permID, "elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodPost, path, map[string]string{"response": response})
	if err != nil {
		return fmt.Errorf("应答权限: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.httpError("应答权限", resp)
	}
	return nil
}

// ErrCustomAnswerRejected 表示 opencode 拒绝了本次 reply 携带的答案——最可能的
// 原因是该问不接受自定义答案（服务端按选项 label 白名单校验）。
//
// 为什么要一个专门的哨兵：协调者填了一个不在选项里的答案时，调用方要把它
// 降级成「重问」而不是报一个语焉不详的 HTTP 错误。只有 4xx 归入本哨兵，
// 5xx 是服务端故障，与答案内容无关（见 ReplyQuestion）。
var ErrCustomAnswerRejected = errors.New("opencode 拒绝了自定义答案")

// QuestionOption 是一个问题的一个候选项。
type QuestionOption struct {
	Label       string `json:"label"`
	Description string `json:"description"`
}

// QuestionInfo 是 opencode question 工具的单个问题。
//
// Multiple 为真表示该问可多选，Custom 为真表示该问接受选项之外的自定义答案。
type QuestionInfo struct {
	Question string           `json:"question"`
	Header   string           `json:"header"`
	Options  []QuestionOption `json:"options"`
	Multiple bool             `json:"multiple"`
	Custom   bool             `json:"custom"`
}

// PendingQuestion 是一条挂起的 question 请求（一次可含多道问题）。
type PendingQuestion struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionID"`
	Questions []QuestionInfo `json:"questions"`
}

// questionReplyRequest 是 POST /question/{id}/reply 的请求体。
type questionReplyRequest struct {
	// Answers 按问题顺序排列，每项是该问选中的 label 数组（多选时多元素）
	Answers [][]string `json:"answers"`
}

// ReplyQuestion 把协调者的答案回填给 opencode 的 question 工具，工具随即返回、
// 回合继续。
//
// 参数：
//   - requestID: question.asked 事件里的 properties.id
//   - answers: 按问题顺序排列，每项是该问选中的 label 数组
//
// 返回：
//   - 4xx 时返回可 errors.Is 命中 ErrCustomAnswerRejected 的错误（答案不被接受）
//   - 其余失败返回普通错误
func (a *API) ReplyQuestion(ctx context.Context, requestID string, answers [][]string) (err error) {
	if requestID == "" {
		return fmt.Errorf("应答提问：请求 id 为空")
	}
	start := time.Now()
	path := "/question/" + requestID + "/reply"
	a.log().Info("opencode 应答提问", "path", path, "request", requestID,
		"answer_count", len(answers))
	defer func() {
		if err != nil {
			a.log().Error("opencode 应答提问失败", "path", path, "request", requestID,
				"cause", err)
		} else {
			a.log().Info("opencode 提问应答完成", "path", path, "request", requestID,
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodPost, path, questionReplyRequest{Answers: answers})
	if err != nil {
		return fmt.Errorf("应答提问: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 && resp.StatusCode < 500 {
		// 4xx：请求本身被拒。答案内容是这次请求里唯一由协调者决定的部分，
		// 因此归因到「答案不被接受」，由调用方降级重问。5xx 不能这样归因
		return fmt.Errorf("%w: %v", ErrCustomAnswerRejected, a.httpError("应答提问", resp))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.httpError("应答提问", resp)
	}
	return nil
}

// RejectQuestion 拒绝一条挂起的提问，解除 question 工具的阻塞。
//
// 参数：requestID 为 question.asked 事件里的 properties.id
//
// 注意：
//   - 用于「任务要停了但提问还挂着」的兜底解阻塞，不是协调者的正常答复通道
func (a *API) RejectQuestion(ctx context.Context, requestID string) (err error) {
	if requestID == "" {
		return fmt.Errorf("拒绝提问：请求 id 为空")
	}
	start := time.Now()
	path := "/question/" + requestID + "/reject"
	a.log().Info("opencode 拒绝提问", "path", path, "request", requestID)
	defer func() {
		if err != nil {
			a.log().Error("opencode 拒绝提问失败", "path", path, "request", requestID,
				"cause", err)
		} else {
			a.log().Info("opencode 提问已拒绝", "path", path, "request", requestID,
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return fmt.Errorf("拒绝提问: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return a.httpError("拒绝提问", resp)
	}
	return nil
}

// ListPendingQuestions 拉取当前全部挂起的提问请求（跨会话）。
//
// 返回：挂起请求列表；请求失败或解析失败时返回错误，列表为 nil
//
// 注意：
//   - 返回的是**全部会话**的挂起请求，调用方必须按 SessionID 过滤出自己的
//   - agentd 重启后重新发现挂起提问的唯一途径：SSE 无重放语义，重启窗口里
//     发生的 question.asked 永远收不到
func (a *API) ListPendingQuestions(ctx context.Context) (out []PendingQuestion, err error) {
	start := time.Now()
	const path = "/question"
	a.log().Info("opencode 查询挂起提问", "path", path)
	defer func() {
		if err != nil {
			a.log().Error("opencode 查询挂起提问失败", "path", path, "cause", err)
		} else {
			a.log().Info("opencode 挂起提问已取得", "path", path, "count", len(out),
				"elapsed_ms", time.Since(start).Milliseconds())
		}
	}()

	resp, err := a.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("查询挂起提问请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, a.httpError("查询挂起提问", resp)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析挂起提问: %w", err)
	}
	return out, nil
}

// SubscribeEvents 订阅 GET /event（SSE），每收到一条事件就同步调用
// onEvent(raw)，直到 ctx 取消。
//
// 断流处理：连接意外断开（EOF/网络错误/服务端未就绪）后指数退避重连，
// 1s→2s→4s→…→30s 封顶，无限重试；ctx 取消时立即返回 nil。
//
// 参数：
//   - onEvent: 每条事件的回调（同步调用：顺序有保证，但回调阻塞会暂停消费）
//   - onReconnect: 断连后成功重连的回调（首次建连不触发）；调用方借此把
//     「断连间隙可能丢事件」的告警交到业务层（P1-10b，可为 nil）
//
// 返回：
//   - ctx 取消时返回**最后一次连接的失败原因**（最后一次连接健康则返回 nil）：
//     调用方拿它填 FailReason（A-7）。恒返回 nil 会让「看门狗判死 → 事件流退出」
//     的失败现场只剩 "<nil>"，零信息
//   - 解析器遇到无法恢复的流异常（如单行超过 1MB 上限）时返回错误
//
// 注意：
//   - 未知/解析失败的行（event: 行、注释、非 JSON data）Debug 跳过，绝不中断订阅
func (a *API) SubscribeEvents(ctx context.Context, onEvent func(json.RawMessage), onReconnect func()) error {
	backoff := a.sseInitialBackoff
	var lastErr error
	for attempt := 1; ; attempt++ {
		// onEstablished：连接建立（HTTP 200）时回调。断连恢复信号挂在「建立」
		// 而非「流结束」的时点上——流可能长时间不结束，告警要尽早送达
		onEstablished := func() {
			if attempt > 1 && onReconnect != nil {
				// 断连恢复：首次连接不是「重连」，只有断过再连上才算；
				// 让业务层知道刚才发生过断连（间隙内的事件可能已丢失）
				onReconnect()
			}
		}
		start := time.Now()
		err := a.streamOnce(ctx, onEvent, onEstablished)
		lived := time.Since(start)
		lastErr = err
		if ctx.Err() != nil {
			// 退出：把最后一次连接的失败原因交给调用方（A-7）。
			// 「因为 ctx 被取消而中断」不是失败原因，那是正常关停路径
			return dropCtxCause(lastErr, ctx)
		}
		if err != nil {
			a.log().Info("SSE 连接失败，等待后重连", "attempt", attempt,
				"backoff_seconds", int(backoff.Seconds()), "lived_ms", lived.Milliseconds(),
				"cause", err)
		} else {
			a.log().Info("SSE 流结束，等待后重连", "attempt", attempt,
				"backoff_seconds", int(backoff.Seconds()), "lived_ms", lived.Milliseconds())
		}

		// 先按本次连接的寿命定下下一次的退避，再去等——顺序反了的话，
		// 复位只会在「下下次」生效，本次仍按旧退避空等
		//
		// 退避复位挂在「连接活够了 sseStableAfter」上，而不是「拿到 200 响应头」
		// 上（A-8）：半死的 opencode 会接受连接、回 200、立刻关流，按 200 复位
		// 等于永不退避——每秒一次重连 + 每次一行 Info 日志，永远升不到上限。
		// 连接真的活了一段时间才说明服务端恢复了，此时才该回到最快节奏（P1-10a）
		if lived >= a.sseStableAfter {
			backoff = a.sseInitialBackoff
		}
		select {
		case <-ctx.Done():
			return dropCtxCause(lastErr, ctx)
		case <-time.After(backoff):
		}
		if lived < a.sseStableAfter {
			backoff *= 2
			if backoff > a.sseMaxBackoff {
				backoff = a.sseMaxBackoff
			}
		}
	}
}

// dropCtxCause 在 ctx 已取消时滤掉「由取消本身派生」的错误。
//
// 为什么需要它：正常关停（Stop/看门狗判死）走的就是取消 ctx，此时在途的
// 连接会返回 context canceled。把它当失败原因上报，FailReason 里就会出现
// 一句与真实故障无关的噪音；而真正的失败原因（连不上、500）必须原样透出（A-7）。
func dropCtxCause(err error, ctx context.Context) error {
	if err == nil || errors.Is(err, ctx.Err()) {
		return nil
	}
	return err
}

// streamOnce 建立一次 SSE 连接并消费到流结束，期间每条事件同步回调 onEvent。
//
// 返回：
//   - 流正常结束（服务端关闭）时返回 nil
//   - 连接失败、非 200、扫描器异常时返回对应错误
//
// 参数：
//   - onEstablished: 连接建立（HTTP 200）后立即回调（可为 nil）；供上层做
//     「成功连接」的退避复位与断连恢复通知（见 SubscribeEvents）
func (a *API) streamOnce(ctx context.Context, onEvent func(json.RawMessage), onEstablished func()) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/event", nil)
	if err != nil {
		return fmt.Errorf("构造 SSE 请求: %w", err)
	}
	req.Header.Set("Authorization", "Basic "+basicAuth("opencode", a.password))
	// 走 sseClient：不设 Timeout，长连接生命周期由 ctx 控制（与一元调用分离的 why
	// 见 NewAPI 注释；若复用带 Timeout 的 httpClient，事件流空闲稍久就被误杀）
	resp, err := a.sseClient.Do(req)
	if err != nil {
		return fmt.Errorf("SSE 连接: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return a.httpError("订阅事件流", resp)
	}
	if onEstablished != nil {
		onEstablished()
	}

	a.log().Info("SSE 连接建立", "path", "/event")
	defer a.log().Info("SSE 连接关闭", "path", "/event")

	// 按行读：SSE 事件以空行分隔；data: 行聚合为事件体，其余行（event:/id:/注释）
	// 一律 Debug 跳过。Buffer 上限 1MB，见文件头 why 注释
	//
	// 原始字节旁路：必须在剥 data: 前缀与聚合之前写，空行也要写——空行就是
	// SSE 的分帧信号，剥掉它样本就不能回放。taskID 传空串：API 是跨任务复用的
	// 最小客户端，无任务标识；文件名因此退化为 opencode-.jsonl，探针一次只跑
	// 一个任务，可接受
	rawTap := rawtap.Open("opencode", "", a.log())
	defer rawTap.Close()

	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), sseScanBuffer)
	var data []string
	for sc.Scan() {
		rawTap.Write(sc.Bytes())
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			// 对齐官方 SDK 语义：剥掉 "data:" 前缀并去掉紧随的空格
			data = append(data, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		case line == "":
			// 空行 = 事件结束；只有 data 内容非空且是合法 JSON 才回调
			a.dispatch(data, onEvent)
			data = nil
		default:
			a.log().Debug("SSE 忽略未知行", "line", turn.TruncateRunes(line, 80))
		}
	}
	if err := sc.Err(); err != nil {
		// EOF 不是错误（bufio 对 EOF 返回 nil）；此处只剩真实异常（如超长行）。
		// ctx 取消导致的读取中断属正常关停路径，不按异常告警，静默交给上层退出
		if ctx.Err() == nil {
			a.log().Warn("SSE 流读取异常", "cause", err)
			return fmt.Errorf("SSE 流读取: %w", err)
		}
		return err
	}
	return nil
}

// dispatch 把聚合的 data 行拼成事件体，合法 JSON 才回调 onEvent，其余 Debug 跳过。
//
// 多条 data 行按 SSE 规范用换行连接（对齐官方 SDK 的 dataLines.join("\n")）。
func (a *API) dispatch(data []string, onEvent func(json.RawMessage)) {
	if len(data) == 0 || (len(data) == 1 && data[0] == "") {
		return
	}
	raw := json.RawMessage(strings.Join(data, "\n"))
	if !json.Valid(raw) {
		a.log().Debug("SSE 事件非合法 JSON，跳过", "data", turn.TruncateRunes(string(raw), 120))
		return
	}
	onEvent(raw)
}
