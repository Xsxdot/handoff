// Package opencode 提供 opencode server 的最小 HTTP/SSE 客户端与 serve 进程管理。
//
// api.go —— opencode server 的 HTTP 客户端与 SSE 事件订阅。
//
// 职责：
//   - 覆盖 MVP 所需的四个端点：POST /session、POST /session/{id}/prompt_async、
//     POST /session/{id}/permissions/{permID}、GET /event（SSE 事件流）
//   - 统一的 basic auth（用户名固定 opencode，密码来自 OPENCODE_SERVER_PASSWORD）
//   - SSE 断流自动指数退避重连（1s→2s→…→30s），直到 ctx 取消
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
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"
)

// 常量说明：
//   - sessionTitle：建会话时写入的标题，便于用户在 tmux attach 后从 opencode TUI
//     里区分会话归属
//   - sseInitialBackoff/sseMaxBackoff：断流重连的指数退避区间
//   - sseScanBuffer：scanner 单行上限。事件可能携带大段文本，必须给足空间；
//     超过该上限的行按流异常处理（走重连），而不是崩掉订阅
const (
	sessionTitle      = "handoff"
	sseInitialBackoff = 1 * time.Second
	sseMaxBackoff     = 30 * time.Second
	sseScanBuffer     = 1 << 20 // 1MB
)

// API 是 opencode server 的最小 HTTP 客户端，持有 baseURL 与鉴权密码。
//
// 并发安全：字段构造后只读，可被多个 goroutine 同时使用。
type API struct {
	baseURL  string
	password string
	hc       *http.Client
}

// NewAPI 创建 opencode server 客户端。
//
// 参数：
//   - baseURL: opencode serve 的地址（如 http://127.0.0.1:4345），尾斜杠会被剥掉
//   - password: OPENCODE_SERVER_PASSWORD 的值，与用户名 opencode 拼成 basic auth
func NewAPI(baseURL, password string) *API {
	return &API{
		baseURL:  strings.TrimRight(baseURL, "/"),
		password: password,
		// 全局不设 Timeout：SSE 连接的生命周期由 ctx 控制（长连接）。
		// 仅限制 TCP 拨号时间，避免对端不可达时挂死
		hc: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
			},
		},
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
	return a.hc.Do(req)
}

// basicAuth 构造 HTTP basic auth 的 Base64 编码值（用户名:密码）。
func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// httpError 把非 2xx 响应转为带状态码与响应体（截断 200 字符）的错误，
// 并打 Error 日志——响应体是「服务端为什么拒绝」的第一手线索。
func (a *API) httpError(op string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
	trunc := truncateRunes(string(body), 200)
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
	if resp.StatusCode != http.StatusOK {
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

// SubscribeEvents 订阅 GET /event（SSE），每收到一条事件就同步调用
// onEvent(raw)，直到 ctx 取消。
//
// 断流处理：连接意外断开（EOF/网络错误/服务端未就绪）后指数退避重连，
// 1s→2s→4s→…→30s 封顶，无限重试；ctx 取消时立即返回 nil。
//
// 返回：
//   - ctx 取消（正常退出）时返回 nil
//   - 解析器遇到无法恢复的流异常（如单行超过 1MB 上限）时返回错误
//
// 注意：
//   - onEvent 同步调用：顺序有保证，但回调阻塞会暂停消费；阻塞行为由调用方承担
//   - 未知/解析失败的行（event: 行、注释、非 JSON data）Debug 跳过，绝不中断订阅
func (a *API) SubscribeEvents(ctx context.Context, onEvent func(json.RawMessage)) error {
	backoff := sseInitialBackoff
	for attempt := 1; ; attempt++ {
		if err := a.streamOnce(ctx, onEvent); err != nil {
			if ctx.Err() != nil {
				return nil // 正常退出：ctx 取消
			}
			a.log().Info("SSE 连接失败，等待后重连", "attempt", attempt,
				"backoff_seconds", int(backoff.Seconds()), "cause", err)
		} else if ctx.Err() != nil {
			return nil // 流自然结束后 ctx 恰好被取消
		} else {
			a.log().Info("SSE 流结束，等待后重连", "attempt", attempt,
				"backoff_seconds", int(backoff.Seconds()))
		}

		select {
		case <-ctx.Done():
			return nil
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > sseMaxBackoff {
			backoff = sseMaxBackoff
		}
	}
}

// streamOnce 建立一次 SSE 连接并消费到流结束，期间每条事件同步回调 onEvent。
//
// 返回：
//   - 流正常结束（服务端关闭）时返回 nil
//   - 连接失败、非 200、扫描器异常时返回对应错误
func (a *API) streamOnce(ctx context.Context, onEvent func(json.RawMessage)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+"/event", nil)
	if err != nil {
		return fmt.Errorf("构造 SSE 请求: %w", err)
	}
	req.Header.Set("Authorization", "Basic "+basicAuth("opencode", a.password))
	resp, err := a.hc.Do(req)
	if err != nil {
		return fmt.Errorf("SSE 连接: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return a.httpError("订阅事件流", resp)
	}

	a.log().Info("SSE 连接建立", "path", "/event")
	defer a.log().Info("SSE 连接关闭", "path", "/event")

	// 按行读：SSE 事件以空行分隔；data: 行聚合为事件体，其余行（event:/id:/注释）
	// 一律 Debug 跳过。Buffer 上限 1MB，见文件头 why 注释
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), sseScanBuffer)
	var data []string
	for sc.Scan() {
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
			a.log().Debug("SSE 忽略未知行", "line", truncateRunes(line, 80))
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
		a.log().Debug("SSE 事件非合法 JSON，跳过", "data", truncateRunes(string(raw), 120))
		return
	}
	onEvent(raw)
}

// truncateRunes 将字符串按 rune 截断为最多 n 个字符（避免切断多字节 UTF-8 字符）。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
