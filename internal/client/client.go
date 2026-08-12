// Package client 是 handoff 审核者侧对 agentd 的唯一拨号方：任务列表、attach 现场恢复、
// ticket 应答（reply）、wait 事件等待（WS + cursor 断线续拉）与审阅命令（diff/fetch/run）。
//
// 职责：
//   - 封装 agentd 的全部 HTTP API 与 WS 事件流的调用（Bearer token 鉴权）
//   - WaitEvent 按 task 自存 cursor（~/.handoff/cursor-<task>）实现「事件不丢不重」：
//     重连时携带最后交付事件的 seq，从服务端补拉断线期间产生的事件
//   - 断线指数退避重连（1s→2s→…→60s），覆盖本机 agentd 重启、网络抖动等场景
//
// 边界：
//   - 无业务判断：不解析事件 payload 语义、不做审批决策——「答什么」由审核者（人/上层）
//     决定后经 Reply 原样透传，审批策略在审核者脑中，本包只保证传输可靠与语义透明
//   - 不持久化除 cursor 外的任何状态：任务/事件/工单数据全部实时向 agentd 查询
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/proto"
)

// 常量说明：
//   - wsInitialBackoff/wsMaxBackoff：WS 断线重连的指数退避区间
const (
	wsInitialBackoff = 1 * time.Second
	wsMaxBackoff     = 60 * time.Second
	// wsStableAfter 是「这次 WS 连接算健康」的存活门槛：连接活够这么久，
	// 下次断线才从初始退避重来（见 WaitEvent 的退避复位 why）
	wsStableAfter = 5 * time.Second
)

// dialTimeout 是 WS 单次拨号（含 TCP 连接与握手）的超时上限。
//
// 为什么必须有它：websocket.Dial 默认用 http.DefaultClient（Transport 无拨号超时），
// 对黑洞对端（SYN 无响应）会挂 ~2min 才失败——每次重连都白等两分钟，指数退避形同
// 虚设。包级 var 而非 const，便于测试注入更短值（与 workspace.go RunCmdTimeout 同款）。
var dialTimeout = 10 * time.Second

// permanentError 标记永久性失败：配置错误（握手 400/401/403）或任务不存在
// （服务端 PolicyViolation close）。这类失败重试只会得到相同结果，WaitEvent
// 遇到它立即返回，不做指数退避——退避重连是为瞬时故障（断网/agentd 重启）设计的。
type permanentError struct {
	op    string
	code  int // HTTP 握手状态码或 WS close code；0 表示未设置
	cause error
}

func (e *permanentError) Error() string {
	return fmt.Sprintf("%s: 永久失败 code=%d: %v", e.op, e.code, e.cause)
}

// Unwrap 暴露 cause，保证 errors.As/Is 能穿透 permanentError 找到原始错误。
func (e *permanentError) Unwrap() error { return e.cause }

// isPermanentStatus 判定握手状态码是否属于「重试无意义」的永久性失败。
//
// 为什么 400/401/403/404 永久：它们都表示请求本身非法——400 是参数错误、
// 401/403 是 token 未同步/无权限、404 是路由不存在（handoff 路由改名后旧
// agentd 会一直命中 404）。token 不同步正是文档写明的手工配对步骤（最常见的
// 配置错误），退避重连只会无限循环，且与「还没有事件」的静默挂起无法区分
// （P0-2 根因）。其余状态（如 500、网关错误）可能是 agentd 瞬时故障，继续退避重连。
func isPermanentStatus(code int) bool {
	switch code {
	case http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden,
		http.StatusNotFound:
		return true
	}
	return false
}

// isPermanent 判定错误是否属于「重试无意义」的永久性失败。
//
// 判定来源：
//   - waitOnce 显式构造的 permanentError（握手状态码 400/401/403 等配置类错误）
//   - WS 对端以 StatusPolicyViolation（1008）关闭——服务端「任务不存在」的约定
//     close code（打错 task-id 的永久配置错误，见 server.go handleEvents）
//
// 为什么正常关闭（1000）/GoingAway（1001）不在此列：agentd 重启、主动断开等
// 瞬时场景都会先关连接，重连即可恢复；只有对端明示「你的请求本身非法」才该退出。
func isPermanent(err error) bool {
	var pe *permanentError
	if errors.As(err, &pe) {
		return true
	}
	var ce websocket.CloseError
	return errors.As(err, &ce) && ce.Code == websocket.StatusPolicyViolation
}

// isDeliverable 判定一个事件类型是否该唤醒审核者。
//
// 可交付 = 全部类型 − {progress, approver_decision, approver_disabled}。
//
// 为什么后两类也要挡：它们在服务端是「只入库不 Publish」（见 manager.go 追加
// approver_decision 处的注释），**实时流本就见不到**——所以客户端不过滤长期
// 没有症状。但 WS 重放读的是 store（EventsFromAsc），会把它们一并推来，于是
// 「重连交付的东西比实时流更多」，多出来的全是审计噪音；审批链裁决越密，
// 重连时的唤醒风暴越大。handoff skill 早已写明这三类不唤醒 wait，这里是让
// 代码追上契约。
//
// 注意：all=true 时调用方不使用本谓词，全量交付——排障需要看到审计事件。
func isDeliverable(t proto.EventType) bool {
	switch t {
	case proto.EventTypeProgress,
		proto.EventTypeApproverDecision,
		proto.EventTypeApproverDisabled:
		return false
	}
	return true
}

// ErrIdleTimeout 表示 follow 期间空闲超过约定时长——期间**一帧都没收到**
// （含被过滤掉的 progress）。
//
// 为什么它值得一个独立哨兵：它与「任务停滞」不是一回事。任务停滞由 agentd 的
// 看门狗诊断并作为 stalled 事件送达（带 last_seq 与 idle 时长）；本错误只说明
// 连接侧一片死寂，第一嫌疑是 agentd 失联而不是任务卡住。
var ErrIdleTimeout = errors.New("空闲超时：期间未收到任何帧")

// errStopStream 是 streamOnce 的内部哨兵：onFrame 返回它表示「本次连接的使命
// 已完成」，按正常结束处理而非错误（一次性 wait 用它在首个可动作事件后收手）。
var errStopStream = errors.New("stream stopped by callback")

// errArchived 是内部哨兵：对端以 StatusNormalClosure 关闭，表示任务已归档。
var errArchived = errors.New("任务已归档")

// AttachInfo 是 attach 命令的完整现场快照：任务 + 待办工单 + 最近事件。
// 与 agentd GET /api/tasks/{id} 的响应线格式一一对应，审核者恢复现场的关键数据源。
type AttachInfo struct {
	Task           proto.TaskView `json:"task"`
	PendingTickets []proto.Ticket `json:"pending_tickets"`
	RecentEvents   []proto.Event  `json:"recent_events"`
}

// Client 是 agentd 的 HTTP/WS 客户端，持有服务地址与 Bearer 令牌。
//
// 并发安全：字段构造后只读，可被多个 goroutine 同时使用。
type Client struct {
	baseURL string
	token   string
	hc      *http.Client
	// extraHeaders 是每个请求都要带的附加头（目前只有 agentd→agentd 的防环标记）。
	// nil 表示没有附加头，生产上的审核者客户端恒为 nil。
	extraHeaders map[string]string
	// WS 断线重连的退避区间与「这次连接算健康」的存活门槛（见 WaitEvent）。
	// 测试经 NewWithWSTiming 注入毫秒级值，生产一律用包级默认。
	wsInitialBackoff time.Duration
	wsMaxBackoff     time.Duration
	wsStableAfter    time.Duration
}

// New 创建 agentd 客户端。
//
// 参数：
//   - addr: agentd 地址（如 http://127.0.0.1:7777）；缺少 scheme 时自动补 http://
//   - token: Bearer 访问令牌；为空时请求不带 Authorization 头
//
// 注意：
//   - 仅做地址归一化，不做任何网络请求，连接在首次调用时建立
func New(addr, token string) *Client {
	return NewWithWSTiming(addr, token, wsInitialBackoff, wsMaxBackoff, wsStableAfter)
}

// NewWithWSTiming 是 New 的 WS 重连节奏可注入变体：测试注入毫秒级退避与
// 健康门槛，让「连接活够了才复位退避」的断言不必真等 1s..60s；生产一律走 New。
//
// 参数：
//   - initial/max: 断线重连的初始/封顶退避
//   - stableAfter: 连接存活多久才算健康、才复位退避（见 WaitEvent）
func NewWithWSTiming(addr, token string, initial, max, stableAfter time.Duration) *Client {
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	return &Client{
		baseURL: strings.TrimRight(addr, "/"),
		token:   token,
		hc: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: dialTimeout}).DialContext,
			},
		},
		wsInitialBackoff: initial,
		wsMaxBackoff:     max,
		wsStableAfter:    stableAfter,
	}
}

// MarkForwarded 返回一个副本，其后续请求都带上 X-Handoff-Forwarded: 1。
//
// 用途：agentd 扇出到别的 agentd 时必须带这个标记，让对端不再向外扇出——
// 一跳封顶，A→B→A 不可能成环。审核者 CLI **不要**用它。
func (c *Client) MarkForwarded() *Client {
	cp := *c
	cp.extraHeaders = map[string]string{"X-Handoff-Forwarded": "1"}
	return &cp
}

// log 返回运行时 slog.Default()。
//
// 为什么不用包级 var：cli 命令在 RunE 里才 logx.Setup + slog.SetDefault，包级 var
// 在 init 时求值会锁死默认 logger，导致本包日志绕开 logx 的 stderr 文本输出
// （与 agentd 侧 store/config 的同款修正）。
func (c *Client) log() *slog.Logger { return slog.Default() }

// do 发送带 Bearer token 的请求，返回响应（调用方负责关闭 resp.Body）。
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("序列化请求体 %s: %w", path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rd)
	if err != nil {
		return nil, fmt.Errorf("构造请求 %s: %w", path, err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	for k, v := range c.extraHeaders {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.hc.Do(req)
}

// httpError 把非 2xx 响应转成错误，并按状态码分级记录日志。
//
// 为什么按状态码分级：4xx 是预期内的客户端错误（任务不存在、状态不允许），
// 在 attach 列表、pull 等常规路径上会正常出现——一律打 ERROR 会刷出假告警，
// 把真正需要注意的服务端故障（5xx）淹没在噪音里。
func (c *Client) httpError(op string, resp *http.Response) error {
	// 上限 4096 而不是 256（B42 修）：这条上限截的是服务端错误体，而中文一个字
	// 3 字节，256 字节只够 ~85 个汉字——B42 的 409 报文正是把「点名占用者 + 两条
	// 出路」放在后半句，被截掉等于这个功能在最后一寸断了。可诊断性是 B42/B45
	// 的目的本身，不是附带项。上限保留（防超大响应体），只是抬到一个中文报文
	// 放得下的量级。
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 500 {
		c.log().Error("agentd 请求失败", "op", op, "status", resp.StatusCode, "body", string(b))
	} else {
		c.log().Warn("agentd 请求被拒", "op", op, "status", resp.StatusCode, "body", string(b))
	}
	return fmt.Errorf("%s: 状态码 %d: %s", op, resp.StatusCode, strings.TrimSpace(string(b)))
}

// ErrStatusUnsupported 表示对端 agentd 不认识 /api/status（版本早于该端点引入）。
//
// why（必须是可判别的哨兵）：这是唯一一个「HTTP 失败但结论是成功」的分支——
// 能收到 404 说明 TCP 通、HTTP 正常、Bearer 已经通过，三件事都被证明了。
// CLI 据此输出降级结论并退 0，而不是把一台完全能用的机器判成失败。
var ErrStatusUnsupported = errors.New("对端 agentd 不支持 /api/status")

// Status 查询 agentd 的可用性与身份信息（handoff status 的数据源）。
//
// 返回：
//   - StatusResp：版本、监听地址、DataDir、执行者清单、任务计数、活跃任务
//   - ErrStatusUnsupported：对端是老 agentd（404），调用方应走降级输出
//   - 其余错误：连不上、401、5xx 等真失败
func (c *Client) Status(ctx context.Context) (*proto.StatusResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/status", nil)
	if err != nil {
		return nil, fmt.Errorf("状态查询请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// 不走 httpError：它会打 Warn 日志并造出一个普通错误，而这里的 404
		// 是一条有用的结论，不是异常。
		//
		// 为什么是 Debug（而不是 Info）：这是**预期结论**不是异常——调用方
		// （cmd/status.go）已经把它渲染成人读输出（「可用（版本过旧）」四行），
		// 库层再打 Info 就是重复，混进 stderr 看着像出了错。降级到 Debug
		// 保留排障时的可观测性，但默认不污染诊断命令的输出。
		c.log().Debug("对端 agentd 不支持 /api/status，按版本过旧处理")
		return nil, ErrStatusUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("状态查询", resp)
	}
	var out proto.StatusResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析状态响应: %w", err)
	}
	return &out, nil
}

// ListTasks 查询全部任务（created_at 降序）。
//
// 返回：
//   - 任务列表；服务端保证空库时返回空切片而非 nil
//   - 请求失败或响应非法时返回错误
func (c *Client) ListTasks(ctx context.Context) ([]proto.TaskView, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/tasks", nil)
	if err != nil {
		return nil, fmt.Errorf("任务列表请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("任务列表", resp)
	}
	var tasks []proto.TaskView
	if err := json.NewDecoder(resp.Body).Decode(&tasks); err != nil {
		return nil, fmt.Errorf("解析任务列表响应: %w", err)
	}
	return tasks, nil
}

// Attach 获取任务的完整现场快照（任务 + 待办工单 + 最近事件），
// 是审核者恢复会话现场（pending_tickets）的数据源。
//
// 参数：
//   - taskID: 任务 ID；任务不存在时返回 404 错误
func (c *Client) Attach(ctx context.Context, taskID string) (*AttachInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/tasks/"+taskID, nil)
	if err != nil {
		return nil, fmt.Errorf("任务详情请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("任务详情", resp)
	}
	var info AttachInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return nil, fmt.Errorf("解析任务详情响应: %w", err)
	}
	return &info, nil
}

// Reply 回答一个工单（权限门批准/拒绝、提问的答案）。
//
// 参数：
//   - taskID: 工单所属任务 ID
//   - ticketID: 待回答的工单 ID
//   - answer: 应答原文，原样透传给 agentd（如 "allow" / "deny: 原因" / 任意文本），
//     语义由上层（审核者/manager）决定，本包不做解释
//
// 注意：
//   - 工单不存在、已回答（不可重复回答）或不属于该任务时返回错误
func (c *Client) Reply(ctx context.Context, taskID, ticketID, answer string) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks/"+taskID+"/reply",
		map[string]string{"ticket_id": ticketID, "answer": answer})
	if err != nil {
		return fmt.Errorf("reply 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("reply", resp)
	}
	return nil
}

// DispatchOpts 是 Dispatch 的入参，与 agentd POST /api/tasks 的请求体键一一对应。
//
// PlanB64 与 Prompt 至少其一：PlanB64 是 base64 的 plan 文件内容（附 plan 名归档），
// Prompt 是直接指令（prompt-only 派发）；两者都传时 Prompt 作为附加指令拼接在
// plan 之后。Branch/NewBranch、Worktree/NewWorktree 各自二选一，空=自动分支/原地。
type DispatchOpts struct {
	// ProjectID 是项目身份，由 CLI 从 cwd 的 origin 离线算出；与 ProjectName 二选一。
	ProjectID string
	// ProjectName 是 --project <名字> 的取值，仅在 cwd 不是目标项目时使用。
	ProjectName string
	PlanB64     string
	PlanName    string
	Target      string
	Prompt      string
	Name        string
	Executor    string
	Model       string
	Branch      string
	NewBranch   string
	Base        string
	Worktree    string
	NewWorktree bool
	// BaseCommit 是审核者本地 HEAD 的提交号，随请求上送让 agentd 校验任务仓库
	// 不落后于本地（空=不校验）。
	BaseCommit string
}

// Dispatch 派发一个新任务到 agentd 执行。
//
// 参数：
//   - opts: 派发参数（仓库/计划/执行者/分支/工作区等，见 DispatchOpts）
//
// 返回：
//   - 创建后的任务（state=running）；服务端启动 executor 失败时返回错误
func (c *Client) Dispatch(ctx context.Context, opts DispatchOpts) (*proto.Task, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks", map[string]any{
		"project_id": opts.ProjectID, "project_name": opts.ProjectName,
		"plan_b64": opts.PlanB64, "plan_name": opts.PlanName, "target": opts.Target,
		"prompt": opts.Prompt, "name": opts.Name, "executor": opts.Executor, "model": opts.Model,
		"branch": opts.Branch, "new_branch": opts.NewBranch, "base": opts.Base,
		"worktree": opts.Worktree, "new_worktree": opts.NewWorktree, "base_commit": opts.BaseCommit,
	})
	if err != nil {
		return nil, fmt.Errorf("dispatch 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("dispatch", resp)
	}
	var task proto.Task
	if err := json.NewDecoder(resp.Body).Decode(&task); err != nil {
		return nil, fmt.Errorf("解析 dispatch 响应: %w", err)
	}
	return &task, nil
}

// ProjectAddOpts 是 ProjectAdd 的参数。
//
// 两种形态由 Path 是否为空决定：
//   - Path 非空：目标 agentd 所在机器上已经有一份代码，登记它（agentd 现读
//     它的 origin 校验一致）
//   - Path 为空：让 agentd 自己 clone 到 repo_root/<Name>
type ProjectAddOpts struct {
	OriginURL string
	Name      string
	Path      string
}

// ProjectAdd 在目标 agentd 上登记一个项目位置（必要时先克隆）。
//
// 注意：
//   - 路径不是 git 仓库/没有 origin/克隆失败返回 400 错误（报文含 git 原文）
//   - 路径上是另一个项目返回 400 错误（报文同时给出两边的 origin）
//   - 项目/名字/路径已被登记、克隆落点已存在返回 409 错误
func (c *Client) ProjectAdd(ctx context.Context, opts ProjectAddOpts) (*proto.ProjectLocation, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/projects", map[string]any{
		"origin_url": opts.OriginURL, "name": opts.Name, "path": opts.Path,
	})
	if err != nil {
		return nil, fmt.Errorf("project add 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("project add", resp)
	}
	var loc proto.ProjectLocation
	if err := json.NewDecoder(resp.Body).Decode(&loc); err != nil {
		return nil, fmt.Errorf("解析 project add 响应: %w", err)
	}
	return &loc, nil
}

// ProjectList 列出目标 agentd 上的全部项目位置（含实际状态）。
func (c *Client) ProjectList(ctx context.Context) ([]proto.ProjectLocation, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/projects", nil)
	if err != nil {
		return nil, fmt.Errorf("project list 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("project list", resp)
	}
	var locs []proto.ProjectLocation
	if err := json.NewDecoder(resp.Body).Decode(&locs); err != nil {
		return nil, fmt.Errorf("解析 project list 响应: %w", err)
	}
	return locs, nil
}

// ProjectTree 取项目树（GET /api/projects/tree）。
//
// 注意：本方法只取**单机**树。跨机汇总是 agentd 侧的事（它对每台取单机树再合并），
// 客户端拿汇总请打 ?scope=all 的那条路径，由 agentd 负责扇出。
func (c *Client) ProjectTree(ctx context.Context) (*proto.ProjectTreeResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/projects/tree", nil)
	if err != nil {
		return nil, fmt.Errorf("请求项目树: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("项目树", resp)
	}
	var out proto.ProjectTreeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析项目树响应: %w", err)
	}
	return &out, nil
}

// ProjectRemove 注销一条项目位置。
//
// 注意：
//   - 只删登记，**不删磁盘上的代码**
//   - 登记不存在返回 404 错误；项目仓库仍被活跃任务占用返回 409 错误
func (c *Client) ProjectRemove(ctx context.Context, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/projects/"+name, nil)
	if err != nil {
		return fmt.Errorf("project remove 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("project remove", resp)
	}
	return nil
}

// Continue 向任务续发修改指令（要求任务处于 waiting_review，指令原样透传 executor）。
//
// 注意：
//   - 任务不存在返回 404 错误；状态不允许续接返回 409 错误
func (c *Client) Continue(ctx context.Context, taskID, instructions string) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks/"+taskID+"/continue",
		map[string]string{"instructions": instructions})
	if err != nil {
		return fmt.Errorf("continue 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("continue", resp)
	}
	return nil
}

// Done 归档任务（要求任务处于 waiting_review）：置 completed 并回收 executor。
//
// 注意：
//   - 任务不存在返回 404 错误；状态不允许归档返回 409 错误
func (c *Client) Done(ctx context.Context, taskID string) error {
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks/"+taskID+"/done", nil)
	if err != nil {
		return fmt.Errorf("done 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("done", resp)
	}
	return nil
}

// Stop 主动中止任务：停 executor、作废挂起工单、任务落 failed。
//
// 参数：
//   - taskID: 待中止的任务 ID
//
// 返回：
//   - worktreeRemoved: 响应体 worktree_removed 如实回传——true=本次删除了
//     managed worktree，false=用户自带 worktree / 原地模式（没删）；响应体缺字段
//     （旧版 agentd）按 false 处理。CLI 据此打印与行为一致的提示，不猜
//   - 任务不存在（404）或已是终态（409）时返回错误
func (c *Client) Stop(ctx context.Context, taskID string) (worktreeRemoved bool, err error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks/"+taskID+"/stop", nil)
	if err != nil {
		return false, fmt.Errorf("中止任务请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, c.httpError("中止任务", resp)
	}
	var body struct {
		WorktreeRemoved bool `json:"worktree_removed"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	return body.WorktreeRemoved, nil
}

// Resume 显式恢复卡死的任务：让 agentd 重投「已落库但未送达 executor」的应答，
// 并（B38）对断连窗口内丢失的回合终态做会话对账。
//
// 参数：
//   - taskID: 任务 ID
//   - force: 为真时即使对账判不出（executor 不支持对账 / 回合确实还在忙 /
//     查询失败）仍把任务强制收口到 waiting_review，使 continue/done 可用；
//     收口保住 executor 会话，与 stop 不同（stop 会杀会话并落 failed）
//
// 返回：
//   - 恢复结果 JSON 原文（重投条数、对账结果、executor 是否已不在、收尾状态与结论），
//     原样输出给审核者
//   - executor 仍不可用（502）或任务已终结（409）等情况返回错误；502 时响应体
//     里仍带着本次已重投成功的条数，错误信息中包含它
func (c *Client) Resume(ctx context.Context, taskID string, force bool) (string, error) {
	path := "/api/tasks/" + taskID + "/resume"
	if force {
		path += "?force=true"
	}
	resp, err := c.do(ctx, http.MethodPost, path, nil)
	if err != nil {
		return "", fmt.Errorf("resume 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", c.httpError("resume", resp)
	}
	// 报告是固定几个字段的小对象，1MiB 上限纯属防御（与其余读体路径一致）
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("读取 resume 响应: %w", err)
	}
	return strings.TrimSpace(string(body)), nil
}

// Diff 获取任务分支相对基准分支的审阅素材（git diff + 提交列表）。
//
// 参数：
//   - base: 基准分支名；传空串时由 agentd 按仓库默认分支推导（origin/HEAD → main → master）
func (c *Client) Diff(ctx context.Context, taskID, base string) (string, error) {
	path := "/api/tasks/" + taskID + "/diff"
	if base != "" {
		path += "?base=" + url.QueryEscape(base)
	}
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", fmt.Errorf("diff 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", c.httpError("diff", resp)
	}
	var out struct {
		Diff string `json:"diff"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("解析 diff 响应: %w", err)
	}
	return out.Diff, nil
}

// Fetch 读取任务仓库内相对路径文件的内容（审核者取上下文用）。
//
// 注意：
//   - 路径逃出仓库（如 ../ 前缀或绝对路径）返回错误；文件不存在返回 404 错误
func (c *Client) Fetch(ctx context.Context, taskID, relPath string) (string, error) {
	path := "/api/tasks/" + taskID + "/file?path=" + url.QueryEscape(relPath)
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", fmt.Errorf("fetch 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", c.httpError("fetch", resp)
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("解析 fetch 响应: %w", err)
	}
	return out.Content, nil
}

// Run 在任务仓库执行一条审阅命令（sh -c，10min 超时），返回合并输出与退出码。
//
// 注意：
//   - 命令非零退出不返回错误，退出码经 exitCode 表达；超时被杀时 exitCode=124
//   - 只有执行未发生（启动失败/超时/请求失败）才返回错误
func (c *Client) Run(ctx context.Context, taskID, cmd string) (stdout string, exitCode int, err error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks/"+taskID+"/run",
		map[string]string{"cmd": cmd})
	if err != nil {
		return "", 0, fmt.Errorf("run 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, c.httpError("run", resp)
	}
	var out struct {
		Stdout   string `json:"stdout"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, fmt.Errorf("解析 run 响应: %w", err)
	}
	return out.Stdout, out.ExitCode, nil
}

// IssueAuthTicket 请求 agentd 签发一次性 ticket，返回可直接打开的兑换 URL。
//
// 参数：
//   - deviceName: 设备展示名，纯展示，服务端会净化控制字符
//
// 返回：
//   - 兑换 URL 与过期时刻
//   - 连不上 agentd 时返回带诊断提示的错误（不退化成一句裸的 dial 失败）
func (c *Client) IssueAuthTicket(ctx context.Context, deviceName string) (*proto.AuthTicketResp, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/auth/tickets",
		map[string]string{"device_name": deviceName})
	if err != nil {
		return nil, fmt.Errorf("连接 agentd %s 失败（它在运行吗？可先执行 handoff status 确认）: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("签发 ticket", resp)
	}
	var out proto.AuthTicketResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析签发响应: %w", err)
	}
	return &out, nil
}

// ListSessions 列出 agentd 上的全部浏览器会话（含已吊销）。
func (c *Client) ListSessions(ctx context.Context) ([]proto.SessionInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/auth/sessions", nil)
	if err != nil {
		return nil, fmt.Errorf("连接 agentd %s 失败（它在运行吗？可先执行 handoff status 确认）: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("列出会话", resp)
	}
	var out []proto.SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析会话列表: %w", err)
	}
	return out, nil
}

// RevokeSession 吊销指定会话。
//
// 返回：
//   - 404 时错误里含服务端原文「会话不存在或已吊销」
func (c *Client) RevokeSession(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/auth/sessions/"+url.PathEscape(id), nil)
	if err != nil {
		return fmt.Errorf("连接 agentd %s 失败（它在运行吗？可先执行 handoff status 确认）: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("吊销会话", resp)
	}
	return nil
}

// doStream 发送带 Bearer token 的流式 GET 请求，返回不关闭 body 的响应。
//
// 为什么不能复用 do：do 不做任何读体/超时假设，但它没有专门的流式语义——
// 而 RenderStream 需要保证响应体不被整体超时掐断（follow 长连接可能挂很久）。
// c.hc 本就没有全局 Timeout（只有拨号超时），直接复用即可；本函数与 do 的
// 区别只在明确「调用方负责消费并 Close body」。
func (c *Client) doStream(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求 %s: %w", path, err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	return c.hc.Do(req)
}

// RenderStream 打开任务实况（render.log）的流式读取。
//
// 参数：
//   - taskID: 目标任务
//   - offset: 起始字节偏移；>0 时优先于 tail（用于断线续传）
//   - tail:   从尾部回溯的字节数（offset<=0 时生效；两者都为 0 时由服务端取默认值）
//   - follow: 是否在到达文件尾后继续等待增量
//
// 返回：
//   - 流（调用方负责 Close）、响应开始时的文件字节数、错误
//
// 注意：
//   - 本方法**不设读超时**：follow 模式下长时间无输出是正常的（模型在思考）。
//     取消靠 ctx——CLI 把 Ctrl+C 接到 ctx 上
//   - 非 200 一律转成错误并读走响应体，避免连接泄漏
func (c *Client) RenderStream(ctx context.Context, taskID string,
	offset, tail int64, follow bool) (io.ReadCloser, int64, error) {
	q := url.Values{}
	if offset > 0 {
		q.Set("offset", strconv.FormatInt(offset, 10))
	} else if tail > 0 {
		q.Set("tail", strconv.FormatInt(tail, 10))
	}
	if follow {
		q.Set("follow", "1")
	}
	path := "/api/tasks/" + taskID + "/render"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	resp, err := c.doStream(ctx, http.MethodGet, path)
	if err != nil {
		return nil, 0, fmt.Errorf("render 流请求: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, 0, c.httpError("render 流", resp)
	}
	size, _ := strconv.ParseInt(resp.Header.Get("X-Handoff-Render-Size"), 10, 64)
	return resp.Body, size, nil
}

// WaitEvent 阻塞等待任务的下一个事件：跳过 progress（除非 all=true），
// 拿到首个可动作事件即返回并把 cursor 写盘；断线指数退避 1s→2s→…→60s
// 无限重连，ctx 取消才退出。
//
// cursor 语义（事件不丢不重的根基）：
//   - 每次调用开始时从 ~/.handoff/cursor-<task> 读取上次交付事件的 seq，
//     连接 WS 时以 from_seq=cursor 补拉断线期间产生的事件
//   - 返回首个可动作事件时把 cursor 原子写盘为该事件的 seq；被跳过的 progress
//     事件不推进 cursor（下次调用会重新收到并再次跳过，重复跳过无副作用）
//   - 因此每条可动作事件恰好交付一次（不重），cursor 之后的事件断线后一条不丢（不丢）
//
// 为什么 progress 不唤醒：progress 是高频、无需人工动作的状态播报（如「正在运行」），
// 若用它唤醒，wait 会在每次进度变化时把审核者叫醒做无意义的一次「看-忽略」；
// 审核者只需在真正需要决策的事件（question/permission_request/completed/failed/stalled）
// 到达时被唤醒。需要全量事件流时显式传 all=true。
//
// 永久性失败（不重试）：握手 400/401/403（配置错误）与任务不存在（PolicyViolation
// close）立即返回错误——退避重连只为瞬时故障设计，见 isPermanent 的 why。
//
// 参数：
//   - taskID: 要等待的任务 ID
//   - all: true 时不做类型过滤，第一个到达的事件即返回
//
// 返回：
//   - 首个可动作事件；ctx 取消时返回 ctx.Err()（context.Canceled/DeadlineExceeded）；
//     永久性失败时返回对应的错误（不做退避）
func (c *Client) WaitEvent(ctx context.Context, taskID string, all bool) (*proto.Event, error) {
	fromSeq := c.readCursor(taskID)

	backoff := c.wsInitialBackoff
	for attempt := 1; ; attempt++ {
		start := time.Now()
		ev, err := c.waitOnce(ctx, taskID, fromSeq, all)
		lived := time.Since(start)
		if err == nil {
			if werr := c.writeCursor(taskID, ev.Seq); werr != nil {
				// cursor 写失败不吞事件：先把事件交还用户（宁可下次重投，不可这次挂住）
				c.log().Warn("cursor 写盘失败", "task", taskID, "seq", ev.Seq, "cause", werr)
			}
			return ev, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// 永久性失败立即退出：重试只会在相同结果上打转，且与「还没有事件」的
		// 静默挂起不可区分（P0-2）——必须带 status/cause 让命令层直接展示
		if isPermanent(err) {
			c.log().Error("wait 永久失败，不再重试", "addr", c.baseURL, "task", taskID, "cause", err)
			return nil, err
		}
		// 断网重连是「为什么没唤醒」的唯一线索点，必须带 addr、第 n 次与下次退避秒数
		c.log().Info("WS 连接断开，等待后重连", "addr", c.baseURL, "task", taskID,
			"attempt", attempt, "next_backoff_seconds", int(backoff.Seconds()), "cause", err)

		// 先按本次连接的寿命定下下一次退避，再去等（顺序反了，复位要到下下次才生效）。
		//
		// 复位判据是「连接活够了 wsStableAfter」而不是「连上过」：断网重连的
		// 退避一路翻倍到 60s 封顶后，若不复位，即便对端早已恢复，余下整个 wait
		// 期间每次断线都要再空等 60s（A-9）。而按「连上过」复位则走向另一个极端
		// ——半死的对端（接受连接后立刻断）会被无限快速重连
		if lived >= c.wsStableAfter {
			backoff = c.wsInitialBackoff
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if lived < c.wsStableAfter {
			backoff *= 2
			if backoff > c.wsMaxBackoff {
				backoff = c.wsMaxBackoff
			}
		}
	}
}

// streamOnce 建立一次 WS 连接并把收到的每一帧交给 onFrame，直到 onFrame 收手或连接结束。
//
// 参数：
//   - fromSeq: 断线续拉起点（服务端据此补发历史事件）
//   - readDeadline: 每次 Read 前调用一次，返回本次读取的**绝对**截止时刻；
//     返回零值表示不设。为什么是绝对时刻而不是时长：空闲要跨重连累计，
//     每次连接都从头计时等于让一个反复断连的对端永远超不了时
//   - onFrame: 每收到一帧调用一次（**含 progress**，过滤由调用方做）
//
// 返回：
//   - nil: onFrame 返回 errStopStream
//   - errArchived: 对端以 StatusNormalClosure 关闭（任务已归档）
//   - ErrIdleTimeout: 读取超过 readDeadline 且外层 ctx 未取消
//   - permanentError / 其他: 拨号或读取失败，由调用方决定是否重连
func (c *Client) streamOnce(ctx context.Context, taskID string, fromSeq int64,
	readDeadline func() time.Time, onFrame func(proto.Event) error) error {
	// 拨号段：与原 waitOnce 完全一致（scheme 换算、Bearer 头、dialTimeout、
	// 永久状态码判定、连接建立/关闭日志），照搬不改
	wsScheme := "ws"
	if strings.HasPrefix(c.baseURL, "https://") {
		wsScheme = "wss"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(c.baseURL, "http://"), "https://")
	wsURL := wsScheme + "://" + host + "/ws/events?task=" + taskID +
		"&from_seq=" + strconv.FormatInt(fromSeq, 10)
	opts := &websocket.DialOptions{}
	if c.token != "" {
		opts.HTTPHeader = http.Header{"Authorization": []string{"Bearer " + c.token}}
	}
	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	conn, resp, err := websocket.Dial(dialCtx, wsURL, opts)
	dialCancel()
	if err != nil {
		if resp != nil && isPermanentStatus(resp.StatusCode) {
			return &permanentError{op: "WS 拨号", code: resp.StatusCode, cause: err}
		}
		if resp != nil {
			return fmt.Errorf("WS 拨号失败 status=%d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("WS 拨号失败: %w", err)
	}
	c.log().Info("WS 连接建立", "addr", c.baseURL, "task", taskID, "from_seq", fromSeq)
	defer func() {
		conn.CloseNow()
		c.log().Info("WS 连接关闭", "addr", c.baseURL, "task", taskID)
	}()

	for {
		readCtx, cancelRead := ctx, context.CancelFunc(func() {})
		if readDeadline != nil {
			if dl := readDeadline(); !dl.IsZero() {
				readCtx, cancelRead = context.WithDeadline(ctx, dl)
			}
		}
		_, b, err := conn.Read(readCtx)
		// 顺序要紧：先分辨「是我们自己设的空闲期限到了」（外层 ctx 仍活着），
		// 再分辨归档，最后才当普通断线交给外层重连。
		//
		// 为什么必须在 cancelRead() 之前读取 readCtx.Err()：cancel 之后它的 Err()
		// 恒为 context.Canceled。若先 cancel 再判，idle>0 下任何读错误（归档的
		// StatusNormalClosure、普通断线、对端重启）都会被误判成 ErrIdleTimeout——
		// errArchived 与重连分支永远到不了，124 会掩盖一切。判据必须收紧到
		// DeadlineExceeded 本身，而不是「非 nil」。
		idleExpired := ctx.Err() == nil && errors.Is(readCtx.Err(), context.DeadlineExceeded)
		cancelRead()
		if err != nil {
			if idleExpired {
				return ErrIdleTimeout
			}
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure {
				return errArchived
			}
			return fmt.Errorf("WS 读取: %w", err)
		}
		var ev proto.Event
		if err := json.Unmarshal(b, &ev); err != nil {
			// 服务端推了非事件 JSON：按连接异常处理，交给外层重连（数据已由 store 兜底）
			return fmt.Errorf("WS 事件反序列化: %w", err)
		}
		if err := onFrame(ev); err != nil {
			if errors.Is(err, errStopStream) {
				return nil
			}
			return err
		}
	}
}

// StreamEventsOnce 建立一次事件流连接，把收到的每一帧交给 onEvent，直到连接
// 断开或 ctx 取消。**不读写任何 cursor 文件，不做重连**。
//
// 参数：
//   - taskID: 任务 id（必须是完整 UUID）
//   - fromSeq: 起始 seq（开区间）；调用方自己持有水位
//   - onEvent: 每帧回调；返回错误即中止本次连接
//
// 为什么必须有这个「无 cursor」变体：FollowEvents / WaitEvent 把水位存在
// ~/.handoff/cursor-<task>，那是**审核者本机**的状态。agentd 做事件镜像时
// 跑在同一台机器上，若复用带 cursor 的路径，agentd 的镜像与人手敲的
// handoff wait 会互相推进对方的水位——一方吃掉另一方的事件，且极难归因。
// 镜像的水位属于 mirror_events 表，不属于文件系统。
//
// 注意：单次连接、不重连。退避与重连策略由调用方（镜像订阅循环）决定，
// 它的节奏（300ms→×2→10s）与审核者 CLI 的（1s→60s）刻意不同。
func (c *Client) StreamEventsOnce(ctx context.Context, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error {
	c.log().Debug("镜像事件流建立", "addr", c.baseURL, "task", taskID, "from_seq", fromSeq)
	// readDeadline 返回零值 = 不设读超时：镜像是常驻订阅，长时间无事件是正常态
	return c.streamOnce(ctx, taskID, fromSeq, func() time.Time { return time.Time{} }, onEvent)
}

// waitOnce 建立一次 WS 连接并消费事件，直到返回首个可动作事件或连接失败。
//
// 返回：
//   - 可动作事件；连接失败/断流时返回错误（由 WaitEvent 决定是否重连）
//
// 注意：
//   - 返回错误时调用方凭 fromSeq 重连补拉，已消费的事件不会重复交付（见 WaitEvent doc）
//   - 永久性失败以 permanentError 返回（握手 400/401/403），或原样透传对端的
//     StatusPolicyViolation close（任务不存在）——两者均被 isPermanent 识别，不再重连
//   - 本函数现在是 streamOnce 的薄封装，外部行为与重构前逐字节一致；
//     正常关闭码在 streamOnce 里被识别为 errArchived，它不是永久失败，
//     WaitEvent 仍按断线重连处理（ws_backoff_test 钉住了这条等价性）
func (c *Client) waitOnce(ctx context.Context, taskID string, fromSeq int64, all bool) (*proto.Event, error) {
	var got *proto.Event
	err := c.streamOnce(ctx, taskID, fromSeq, nil, func(ev proto.Event) error {
		if !all && !isDeliverable(ev.Type) {
			return nil // 审计类与 progress 不唤醒；口径与 FollowEvents 共用 isDeliverable，避免两处漂移
		}
		c.log().Info("wait 事件返回", "task", taskID, "seq", ev.Seq, "type", ev.Type)
		got = &ev
		return errStopStream
	})
	if err != nil {
		return nil, err
	}
	return got, nil
}

// reconcileBacklog 在建立 WS 连接前对账：拿一次权威快照，判断本机 cursor 之后
// 是否积压了未读事件；有则吐一行摘要并把 cursor 直接推到当前水位。
//
// 参数：
//   - taskID: 完整 UUID
//   - fromSeq: 本机 cursor 当前停在哪
//   - onBacklog: 摘要的消费者（cmd 层写 stdout）；返回非 nil 立即上抛
//
// 返回：
//   - next: WS 应当使用的 from_seq——有积压时是当前水位，否则原样是 fromSeq
//   - terminal: 快照显示任务已 failed，调用方应吐完摘要后正常收尾（返回 nil）
//   - err: 仅当 onBacklog 报错；**网络/HTTP 失败一律不报错**（见下）
//
// 为什么积压事件不拉取而是直接跳过：摘要用的是权威快照，比逐条重放**信息更全**
// ——重放里混着已被审批链答掉的历史工单（补 reply 会 404），而 PendingTickets
// 只含真正还欠的，且每张带完整 Request 原文。
//
// 为什么 Attach 失败是降级而不是报错：摘要是优化不是正确性，对账失败就该退回
// 改动前的逐条重放，绝不能因此中断 follow。永久性（401/404）也不在这里判——
// Client.Attach 的错误是普通 fmt.Errorf 而非 permanentError，isPermanent 认不出
// 它；紧随其后的 WS 握手会用既有的、已被测试覆盖的路径判出同一个结论。判定
// 只留一处，不复制。
func (c *Client) reconcileBacklog(ctx context.Context, taskID string, fromSeq int64,
	onBacklog func(*BacklogSummary) error) (int64, bool, error) {
	c.log().Debug("follow 积压对账开始", "task", taskID, "from_seq", fromSeq)

	snap, err := c.Attach(ctx, taskID)
	if err != nil {
		c.log().Warn("follow 积压对账失败，退回逐条重放", "task", taskID,
			"from_seq", fromSeq, "cause", err)
		return fromSeq, false, nil
	}

	sum := computeBacklog(taskID, fromSeq, snap)
	if sum == nil {
		c.log().Debug("follow 积压对账完成：无积压", "task", taskID, "from_seq", fromSeq)
		return fromSeq, false, nil
	}

	// 有积压是「你离开期间发生了事」，是 Info 不是 Debug：它是排查「我错过了什么」
	// 的唯一线索行，必须带齐全部计数
	c.log().Info("follow 积压对账：有积压", "task", taskID,
		"from_seq", sum.FromSeq, "to_seq", sum.ToSeq, "missed", sum.Missed,
		"stale", sum.Stale, "actionable", len(sum.Actionable),
		"truncated", sum.MissedTruncated, "state", string(sum.State))

	if berr := onBacklog(sum); berr != nil {
		return fromSeq, false, berr
	}
	if werr := c.writeCursor(taskID, sum.ToSeq); werr != nil {
		// 不因写盘失败中止：下次对账会重新吐同一行摘要，重复一行无害；
		// 吞掉才危险
		c.log().Warn("对账后 cursor 写盘失败", "task", taskID, "seq", sum.ToSeq, "cause", werr)
	}
	return sum.ToSeq, sum.State == proto.TaskStateFailed, nil
}

// FollowEvents 持续订阅任务事件流，逐条交给 onEvent，直到任务终结或出错。
//
// 与 WaitEvent 的区别只有一条：不在首个事件后返回。这条区别是本设计的全部理由
// ——一事件一退出意味着每两个事件之间必然有一段无人订阅的真空，而「回合结束后
// 记得重挂」是需要每轮重做的人工动作，漏一次即永久断链。
//
// 参数：
//   - all: false 时过滤 progress（与 WaitEvent 同义）
//   - idle: 空闲上限，0 表示不设。**空闲以「收到任何帧」为准，包含被过滤掉的
//     progress**——一个健康的长跑任务可以数小时只有 progress，用可交付事件计时
//     会让它周期性无故超时。这个计时跨重连累计
//   - onEvent: 每条**可交付**事件调用一次；返回非 nil 立即终止跟随并原样返回该错误
//   - onBacklog: 每次建连前对账出的积压摘要的消费者。**传 nil 表示完全跳过对账**，
//     行为与改动前逐字一致——不能只是丢弃摘要却照样跳过积压，那会让事件无声消失
//
// 返回：
//   - nil: 任务终结（收到 failed 事件，或对端归档关闭连接）
//   - ErrIdleTimeout: 空闲超过 idle
//   - ctx.Err() / 永久失败（401、任务不存在）: 原样返回
//
// cursor 语义（与 WaitEvent 的差别，取舍已在 spec §2.4 记录并接受）：
//   - cursor 仍只在**交付**事件时推进，但「交付」不再等价于「审核者看过了」
//     ——事件可能在审核者正忙时流入。此刻会话若崩溃，该事件不会再重放
//   - 接受这个回退的理由：事件流本就不是权威，工单在 agentd 侧持久，
//     pending_tickets 才是权威清单。醒来先 show 这条纪律因此从建议变成必须
//   - 断线续拉起点（fromSeq）则按**任何帧**推进：已经收到的帧没有再补发的必要，
//     它与 cursor 的分叉是有意的，且分叉方向安全（cursor 永远更保守）
func (c *Client) FollowEvents(ctx context.Context, taskID string, all bool,
	idle time.Duration, onEvent func(*proto.Event) error,
	onBacklog func(*BacklogSummary) error) error {
	fromSeq := c.readCursor(taskID)
	lastFrame := time.Now()
	// readDeadline 与 onFrame 都只在 streamOnce 的读循环里被同一个 goroutine 调用，
	// lastFrame 无需加锁
	readDeadline := func() time.Time {
		if idle <= 0 {
			return time.Time{}
		}
		return lastFrame.Add(idle)
	}

	c.log().Info("follow 开始", "addr", c.baseURL, "task", taskID,
		"from_seq", fromSeq, "idle", idle.String(), "all", all)

	backoff := c.wsInitialBackoff
	for attempt := 1; ; attempt++ {
		// 每次建连前对账——首连与重连同一条路径。断网重连与「忘挂后补挂」是
		// 同一个问题的两种入口，不该有两套代码
		if onBacklog != nil {
			next, terminal, rerr := c.reconcileBacklog(ctx, taskID, fromSeq, onBacklog)
			if rerr != nil {
				return rerr
			}
			fromSeq = next
			if terminal {
				c.log().Info("follow 结束：对账时快照已是 failed", "task", taskID, "from_seq", fromSeq)
				return nil
			}
		}
		start := time.Now()
		err := c.streamOnce(ctx, taskID, fromSeq, readDeadline, func(ev proto.Event) error {
			lastFrame = time.Now()
			fromSeq = ev.Seq
			if !all && !isDeliverable(ev.Type) {
				return nil // 审计类与 progress 不交付；口径与 waitOnce 共用 isDeliverable，避免两处漂移
			}
			if werr := c.writeCursor(taskID, ev.Seq); werr != nil {
				// cursor 写失败不吞事件：先把事件交给审核者（宁可下次重投，不可这次丢）
				c.log().Warn("cursor 写盘失败", "task", taskID, "seq", ev.Seq, "cause", werr)
			}
			c.log().Info("follow 事件交付", "task", taskID, "seq", ev.Seq, "type", ev.Type)
			if err := onEvent(&ev); err != nil {
				return err
			}
			if ev.Type == proto.EventTypeFailed {
				// failed 是任务终态；completed 不是——那只是一轮结束，continue 之后还有事件
				c.log().Info("follow 结束：任务已失败", "task", taskID, "seq", ev.Seq)
				return errStopStream
			}
			return nil
		})
		lived := time.Since(start)

		switch {
		case err == nil:
			return nil // onEvent 侧收手（failed）
		case errors.Is(err, errArchived):
			c.log().Info("follow 结束：任务已归档", "task", taskID)
			return nil
		case errors.Is(err, ErrIdleTimeout):
			c.log().Error("follow 空闲超时", "addr", c.baseURL, "task", taskID,
				"idle", idle.String(), "last_frame", lastFrame.Format(time.RFC3339))
			return err
		case ctx.Err() != nil:
			return ctx.Err()
		case isPermanent(err):
			c.log().Error("follow 永久失败，不再重试", "addr", c.baseURL, "task", taskID, "cause", err)
			return err
		}

		// 断线：与 WaitEvent 同一套退避（复位判据是「连接活够 wsStableAfter」，why 见那里）
		c.log().Info("follow 连接断开，等待后重连", "addr", c.baseURL, "task", taskID,
			"attempt", attempt, "next_backoff_seconds", int(backoff.Seconds()), "cause", err)
		if idle > 0 && time.Since(lastFrame) >= idle {
			// 重连期间同样在空闲：不检查这里，一个反复拒连的对端会让 follow 永远超不了时
			c.log().Error("follow 空闲超时（重连期间）", "task", taskID, "idle", idle.String())
			return ErrIdleTimeout
		}
		if lived >= c.wsStableAfter {
			backoff = c.wsInitialBackoff
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		if lived < c.wsStableAfter {
			backoff *= 2
			if backoff > c.wsMaxBackoff {
				backoff = c.wsMaxBackoff
			}
		}
	}
}

// cursorPath 返回任务 cursor 文件路径（~/.handoff/cursor-<task>）。
//
// 为什么放用户主目录而非配置 DataDir：cursor 是审核者侧的本地状态，
// 与配置/数据库文件位置解耦；即使 DataDir 被移动，审核者已看过的进度也不重投。
func cursorPath(taskID string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("读取用户主目录: %w", err)
	}
	return filepath.Join(home, ".handoff", "cursor-"+taskID), nil
}

// readCursor 读取任务 cursor；文件不存在、内容非法或主目录不可用时返回 0（从头开始）。
func (c *Client) readCursor(taskID string) int64 {
	p, err := cursorPath(taskID)
	if err != nil {
		c.log().Debug("cursor 路径不可用，从头开始", "task", taskID, "cause", err)
		return 0
	}
	b, err := os.ReadFile(p)
	if err != nil {
		c.log().Debug("cursor 文件不存在，从头开始", "task", taskID, "path", p)
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		c.log().Debug("cursor 内容非法，从头开始", "task", taskID, "path", p, "content", string(b))
		return 0
	}
	c.log().Debug("cursor 读取", "task", taskID, "path", p, "seq", n)
	return n
}

// writeCursor 把 seq 原子写入 cursor 文件（临时文件 + rename）。
//
// 为什么先写临时文件再 rename：直接写目标文件在写盘中途崩溃会留下截断内容，
// 下一次 wait 会把截断文本解析成 0（从头重投全部事件）；rename 保证读到的一定是
// 完整内容——要么旧值要么新值，不存在中间态。
//
// 为什么临时文件必须唯一（L-3）：两个 wait 进程/goroutine 并发写同一
// cursor-<task> 时，固定后缀的 <path>.tmp 会被两边同时打开/截断——先写完者
// rename 掉的是对方可能还没写完的共享文件，目标文件会短暂出现半截内容，对端
// 恰好读到即「读到一半的 tmp」。CreateTemp 同目录生成 O_EXCL 唯一名，rename
// 的始终是「自己写完整并关闭的文件」，并发读保证只看到完整旧值或完整新值。
func (c *Client) writeCursor(taskID string, seq int64) error {
	p, err := cursorPath(taskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("创建 cursor 目录: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(p), "cursor-"+taskID+"-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时 cursor 文件: %w", err)
	}
	tmp := f.Name()
	if _, err := f.WriteString(strconv.FormatInt(seq, 10)); err != nil {
		f.Close()
		os.Remove(tmp) // 清理半写临时文件，避免残留
		return fmt.Errorf("写临时 cursor 文件: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("关闭临时 cursor 文件: %w", err)
	}
	if err := os.Rename(tmp, p); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("cursor 落盘: %w", err)
	}
	c.log().Debug("cursor 写入", "task", taskID, "path", p, "seq", seq)
	c.sweepStaleCursorTemps(filepath.Dir(p), taskID)
	return nil
}

// cursorTempTTL 是 cursor 临时文件被判定为「遗留垃圾」的年龄阈值。
//
// 为什么按年龄而不是一律清空：同一任务可能有并发的 wait 进程正在写各自的
// 临时文件，无差别删除会掐掉别人在途的 Rename。而任何一次正常写入都在毫秒级
// 完成，1 小时的阈值把「在途」与「遗留」分得足够开。
const cursorTempTTL = time.Hour

// sweepStaleCursorTemps 清理该任务遗留的 cursor 临时文件。
//
// 为什么需要它：writeCursor 用 CreateTemp + Rename 保证原子写，进程若在两步
// 之间被杀（Ctrl+C、机器重启、oom kill）就会留下一个 .tmp，而此后没有任何
// 代码会再碰它——~/.handoff 里的 .tmp 只增不减。
//
// 清理失败一律只记 Debug：这是顺带的卫生工作，绝不能影响 cursor 写入的成败。
func (c *Client) sweepStaleCursorTemps(dir, taskID string) {
	matches, err := filepath.Glob(filepath.Join(dir, "cursor-"+taskID+"-*.tmp"))
	if err != nil {
		c.log().Debug("扫描遗留 cursor 临时文件失败", "task", taskID, "cause", err)
		return
	}
	for _, m := range matches {
		fi, err := os.Stat(m)
		if err != nil || time.Since(fi.ModTime()) < cursorTempTTL {
			continue // 取不到状态或还在途：交给下一次写入再看
		}
		if rerr := os.Remove(m); rerr != nil {
			c.log().Debug("清理遗留 cursor 临时文件失败", "path", m, "cause", rerr)
			continue
		}
		c.log().Debug("已清理遗留 cursor 临时文件", "task", taskID, "path", m)
	}
}
