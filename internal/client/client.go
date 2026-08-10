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

// AttachInfo 是 attach 命令的完整现场快照：任务 + 待办工单 + 最近事件。
// 与 agentd GET /api/tasks/{id} 的响应线格式一一对应，审核者恢复现场的关键数据源。
type AttachInfo struct {
	Task           proto.Task     `json:"task"`
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
func (c *Client) ListTasks(ctx context.Context) ([]proto.Task, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/tasks", nil)
	if err != nil {
		return nil, fmt.Errorf("任务列表请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("任务列表", resp)
	}
	var tasks []proto.Task
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
	Repo        string
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
		"repo": opts.Repo, "plan_b64": opts.PlanB64, "plan_name": opts.PlanName, "target": opts.Target,
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

// RepoAddOpts 是 RepoAdd 的参数。
//
// 两种形态：Clone=false 时 Path 必填（登记执行机上已有的克隆）；
// Clone=true 时 URL 必填（让 agentd 克隆一份），Path 为落点、可省。
type RepoAddOpts struct {
	Name  string
	Path  string
	URL   string
	Clone bool
}

// RepoAdd 在目标 agentd 上登记一个仓库（必要时先克隆）。
//
// 注意：
//   - 路径不是 git 仓库/没有 origin/克隆失败返回 400 错误（报文含 git 原文）
//   - 名字或路径已被登记、克隆落点已存在返回 409 错误
func (c *Client) RepoAdd(ctx context.Context, opts RepoAddOpts) (*proto.Repo, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/repos", map[string]any{
		"name": opts.Name, "path": opts.Path, "url": opts.URL, "clone": opts.Clone,
	})
	if err != nil {
		return nil, fmt.Errorf("repo add 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("repo add", resp)
	}
	var repo proto.Repo
	if err := json.NewDecoder(resp.Body).Decode(&repo); err != nil {
		return nil, fmt.Errorf("解析 repo add 响应: %w", err)
	}
	return &repo, nil
}

// RepoList 列出目标 agentd 上的全部仓库登记（含实际状态）。
func (c *Client) RepoList(ctx context.Context) ([]proto.Repo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/repos", nil)
	if err != nil {
		return nil, fmt.Errorf("repo list 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("repo list", resp)
	}
	var repos []proto.Repo
	if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
		return nil, fmt.Errorf("解析 repo list 响应: %w", err)
	}
	return repos, nil
}

// RepoRemove 注销一条仓库登记。
//
// 注意：
//   - 只删登记，**不删磁盘上的仓库**
//   - 登记不存在返回 404 错误；仓库仍被活跃任务占用返回 409 错误
func (c *Client) RepoRemove(ctx context.Context, name string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/repos/"+name, nil)
	if err != nil {
		return fmt.Errorf("repo remove 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("repo remove", resp)
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

// Resume 显式恢复卡死的任务：让 agentd 重投「已落库但未送达 executor」的应答。
//
// 参数：
//   - taskID: 任务 ID
//
// 返回：
//   - 恢复结果 JSON 原文（重投条数、executor 是否已不在、收尾状态与结论），
//     原样输出给审核者
//   - executor 仍不可用（502）或任务已终结（409）等情况返回错误；502 时响应体
//     里仍带着本次已重投成功的条数，错误信息中包含它
func (c *Client) Resume(ctx context.Context, taskID string) (string, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks/"+taskID+"/resume", nil)
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

// waitOnce 建立一次 WS 连接并消费事件，直到返回首个可动作事件或连接失败。
//
// 返回：
//   - 可动作事件；连接失败/断流时返回错误（由 WaitEvent 决定是否重连）
//
// 注意：
//   - 返回错误时调用方凭 fromSeq 重连补拉，已消费的事件不会重复交付（见 WaitEvent doc）
//   - 永久性失败以 permanentError 返回（握手 400/401/403），或原样透传对端的
//     StatusPolicyViolation close（任务不存在）——两者均被 isPermanent 识别，不再重连
func (c *Client) waitOnce(ctx context.Context, taskID string, fromSeq int64, all bool) (*proto.Event, error) {
	// http→ws / https→wss 的 scheme 换算；本项目只用 http，https 分支为完整性防御
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
	// 拨号套独立超时：握手（含 TCP 连接）必须在 dialTimeout 内完成，否则按连接
	// 失败交给外层退避重连——黑洞对端不再把每次重连拖到 ~2min 才失败
	dialCtx, dialCancel := context.WithTimeout(ctx, dialTimeout)
	conn, resp, err := websocket.Dial(dialCtx, wsURL, opts)
	dialCancel()
	if err != nil {
		if resp != nil && isPermanentStatus(resp.StatusCode) {
			// 配置类错误（400/401/403）以永久失败返回，由 WaitEvent 立即上报，
			// 不做退避——见 isPermanentStatus 的 why
			return nil, &permanentError{op: "WS 拨号", code: resp.StatusCode, cause: err}
		}
		if resp != nil {
			return nil, fmt.Errorf("WS 拨号失败 status=%d: %w", resp.StatusCode, err)
		}
		return nil, fmt.Errorf("WS 拨号失败: %w", err)
	}
	c.log().Info("WS 连接建立", "addr", c.baseURL, "task", taskID, "from_seq", fromSeq)
	defer func() {
		conn.CloseNow()
		c.log().Info("WS 连接关闭", "addr", c.baseURL, "task", taskID)
	}()

	for {
		_, b, err := conn.Read(ctx)
		if err != nil {
			return nil, fmt.Errorf("WS 读取: %w", err)
		}
		var ev proto.Event
		if err := json.Unmarshal(b, &ev); err != nil {
			// 服务端推了非事件 JSON：按连接异常处理，交给外层重连（数据已由 store 兜底）
			return nil, fmt.Errorf("WS 事件反序列化: %w", err)
		}
		if !all && ev.Type == proto.EventTypeProgress {
			continue // progress 不唤醒（why 见 WaitEvent doc 注释）
		}
		c.log().Info("wait 事件返回", "task", taskID, "seq", ev.Seq, "type", ev.Type)
		return &ev, nil
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
