// Package client 是 handoff 协调者侧对 agentd 的唯一拨号方：任务列表、attach 现场恢复、
// ticket 应答（reply）、wait 事件等待（WS + cursor 断线续拉）与审阅命令（diff/fetch/run）。
//
// 职责：
//   - 封装 agentd 的全部 HTTP API 与 WS 事件流的调用（Bearer token 鉴权）
//   - WaitEvent 按 task 自存 cursor（<游标根>/cursors/<agentd>/<task>，见 cursordir.go）
//     实现「事件不丢不重」：重连时携带最后交付事件的 seq，从服务端补拉断线期间产生的事件
//   - 断线指数退避重连（1s→2s→…→60s），覆盖本机 agentd 重启、网络抖动等场景
//
// 边界：
//   - 无业务判断：不解析事件 payload 语义、不做审批决策——「答什么」由协调者（人/上层）
//     决定后经 Reply 原样透传，审批策略在协调者脑中，本包只保证传输可靠与语义透明
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
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/relay"
	"github.com/coder/websocket"
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

// httpStatusError 保留 agentd 非 2xx 响应的状态码，让长驻调用能区分
// “重试可能恢复”的 5xx 与“请求本身不会自愈”的 4xx。
type httpStatusError struct {
	op   string
	code int
	body string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("%s: 状态码 %d: %s", e.op, e.code, e.body)
}

// isPermanent 判定错误是否属于「重试无意义」的永久性失败。
//
// 判定来源：
//   - waitOnce 显式构造的 permanentError（握手状态码 400/401/403 等配置类错误）
//   - WS 对端以 StatusPolicyViolation（1008）关闭——服务端「任务不存在」的约定
//     close code（打错 task-id 的永久配置错误，见 server.go handleEvents）
//   - httpStatusError 携带 4xx 状态码（长驻等待的 Attach 快照被 401/403/404 拒绝）
//
// 为什么正常关闭（1000）/GoingAway（1001）不在此列：agentd 重启、主动断开等
// 瞬时场景都会先关连接，重连即可恢复；只有对端明示「你的请求本身非法」才该退出。
func isPermanent(err error) bool {
	var pe *permanentError
	if errors.As(err, &pe) {
		return true
	}
	var he *httpStatusError
	if errors.As(err, &he) && isPermanentStatus(he.code) {
		return true
	}
	var ce websocket.CloseError
	return errors.As(err, &ce) && ce.Code == websocket.StatusPolicyViolation
}

// isDeliverable 判定一个事件类型是否该唤醒协调者。
//
// 可交付 = 全部类型 − {progress, approver_decision, approver_disabled, tickets_voided}。
//
// 为什么后两类也要挡：它们在服务端是「只入库不 Publish」（见 manager.go 追加
// approver_decision 处的注释），**实时流本就见不到**——所以客户端不过滤长期
// 没有症状。但 WS 重放读的是 store（EventsFromAsc），会把它们一并推来，于是
// 「重连交付的东西比实时流更多」，多出来的全是审计噪音；审批链裁决越密，
// 重连时的唤醒风暴越大。handoff skill 早已写明这三类不唤醒 wait，这里是让
// 代码追上契约。
//
// tickets_voided（B63）加入的理由与前两类略有不同：它同样只入库不 Publish，但它
// 的产生时刻**恰好压在 completed/failed 上**——终态迁移的同一次调用里。可交付就
// 意味着一次性 wait 有机会拿它收手，协调者看到的是「作废了 1 张单」而不是任务成败。
//
// 注意：all=true 时调用方不使用本谓词，全量交付——排障需要看到审计事件。
func isDeliverable(t proto.EventType) bool {
	switch t {
	case proto.EventTypeProgress,
		proto.EventTypeApproverDecision,
		proto.EventTypeApproverDisabled,
		proto.EventTypeTicketsVoided:
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
// 与 agentd GET /api/tasks/{id} 的响应线格式一一对应，协调者恢复现场的关键数据源。
type AttachInfo struct {
	Task           proto.TaskView `json:"task"`
	PendingTickets []proto.Ticket `json:"pending_tickets"`
	RecentEvents   []proto.Event  `json:"recent_events"`
}

// Client 是 agentd 的 HTTP/WS 客户端，持有服务地址与 Bearer 令牌。
//
// 并发安全：baseURL/token/hc 与 WS 节奏字段构造后只读；游标根由
// cursorRootOnce 保护，首次调用解析、后续读缓存，可被多个 goroutine 同时使用。
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
	// 游标根解析结果的缓存（见 cursordir.go）。缓存错误与缓存成功同等重要：
	// 不缓存错误的话，两处都不可写时每写一次游标都要重跑两次文件系统探测。
	cursorRootOnce sync.Once
	cursorRoot     string
	cursorRootErr  error
	// initErr 非空表示这个 client 从构造起就不可用（地址不含主机名）。
	//
	// 为什么毒化而不是让 New 返回 error：New 有二十多个调用点，加返回值的波及
	// 远大于收益。而不管它的代价是实打实的——空地址会被归一化成
	// baseURL="http:"，请求 URL 退化成 http:/api/status，报出来的是
	// "no Host in request URL"，把「这台机器是 relay 形态、压根没有 addr」这个
	// 配置事实伪装成了网络故障。
	initErr error
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

// NewRelay creates a client backed by a relay Dialer. The fixed baseURL is an
// HTTP URL placeholder used to build request paths and WS URLs; routing is done
// by the Dialer. token remains the agentd Bearer credential as a defense-in-depth
// layer after the relay's E2E channel is established.
//
// baseURL 的 host 必须用 loopback 名（localhost）：relay 投递的请求经隧道直达
// 对端 agentd.Handler，会先过 agentd 的 hostGuard（Host 白名单，防 DNS rebinding）。
// loopback 三件套恒在白名单内，而任意占位名（如 "relay"）会被 403 拒。语义上
// relay 投递等价于对端本地投递，用 localhost 正合适。
func NewRelay(d *relay.Dialer, token string) *Client {
	return &Client{
		baseURL: "http://localhost",
		token:   token,
		hc: &http.Client{
			Transport: d.Transport(),
		},
		wsInitialBackoff: wsInitialBackoff,
		wsMaxBackoff:     wsMaxBackoff,
		wsStableAfter:    wsStableAfter,
	}
}

// NewWithWSTiming 是 New 的 WS 重连节奏可注入变体：测试注入毫秒级退避与
// 健康门槛，让「连接活够了才复位退避」的断言不必真等 1s..60s；生产一律走 New。
//
// 参数：
//   - initial/max: 断线重连的初始/封顶退避
//   - stableAfter: 连接存活多久才算健康、才复位退避（见 WaitEvent）
func NewWithWSTiming(addr, token string, initial, max, stableAfter time.Duration) *Client {
	raw := addr
	if !strings.Contains(addr, "://") {
		addr = "http://" + addr
	}
	base := strings.TrimRight(addr, "/")
	c := &Client{
		baseURL: base,
		token:   token,
		hc: &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: dialTimeout}).DialContext,
				// Proxy 显式留空：协调者↔agentd 永不经代理（纪律见
				// internal/proxycfg 包头——那是 LAN/loopback 地址，代理化轻则
				// 多绕一跳、重则 socks5 解析不了 100.x.y.z 直接断链）。
				//
				// 零值本来就是 nil，写出来是因为**上一次它只是隐式成立**：
				// WS 拨号那条路没交出这个 Transport，退回了带
				// ProxyFromEnvironment 的 DefaultTransport，于是被代理回 503
				// 且不报错（B161）。让纪律在代码里看得见，比省一行重要。
				Proxy: nil,
			},
		},
		wsInitialBackoff: initial,
		wsMaxBackoff:     max,
		wsStableAfter:    stableAfter,
	}
	// 地址缺 host 时当场毒化：raw 为空会一路变成 base="http:"（TrimRight 把两个
	// 斜杠一起削掉），后续每个请求都报 no Host——那个文案指不出真正的原因。
	if u, err := url.Parse(base); err != nil || u.Host == "" {
		c.initErr = fmt.Errorf("agentd 地址不含主机名（原始地址 %q）：relay 形态的机器没有 addr，应经 targetclient 选路而不是直连构造", raw)
		slog.Default().Error("client 构造时地址不含主机名，该实例已毒化", "raw_addr", raw, "base_url", base)
	}
	return c
}

// checkInit 在发请求前查毒化标记。
//
// 返回：initErr 原样返回；未毒化时 nil。
func (c *Client) checkInit() error { return c.initErr }

// BaseURL 返回这个 client 的基址。
//
// 用途：调用方判定「选路选对了没有」——relay 形态恒为 http://localhost（经隧道
// 直达对端），直连形态是 http://<addr>。只读，不暴露 token。
func (c *Client) BaseURL() string { return c.baseURL }

// HTTPClient 返回本 client 的底层 http.Client，供 agentd 的转发基座做原样搬运。
//
// 为什么要暴露它：跨机转发（REST/WS 反代）搬的是任意方法与路径的原始报文，
// 走不了本包的类型化方法；而选路（relay 隧道还是直连）恰恰长在这个 http.Client
// 的 Transport 里——转发层自己 new 一个就等于绕开选路，relay 机器会退化成
// "no Host in request URL"。
//
// 注意：
//   - 返回的是共享实例，调用方**不得**改它的字段（Timeout/Transport 等）
//   - Timeout 恒为 0（超时由调用方经 context 施加），同时满足 coder/websocket
//     对 HTTPClient.Timeout 必须为零的硬要求
//   - 不暴露 token：转发层的 Authorization 由它自己按 target 配置设置
func (c *Client) HTTPClient() *http.Client { return c.hc }

// wsDialOptions 组装 WS 拨号选项：本 Client 自己的 http.Client + Bearer 头。
//
// **HTTPClient 必须显式给出，这是本函数存在的全部理由。** 不给时
// coder/websocket 退回 http.DefaultClient，而 DefaultTransport 的 Proxy 是
// http.ProxyFromEnvironment——WS 拨号会被送进 HTTP_PROXY/HTTPS_PROXY。这破的是
// proxycfg 包头写死的那条纪律：「协调者↔agentd 那条链路永不走代理」。
//
// 实测症状（B161）：本机设了 HTTP_PROXY 且 NO_PROXY 不含执行机 IP 时，每次拨号
// 都被代理回 503，follow 长连接永远建不起来，只剩每 6s 一次的 HTTP 对账兜底
// ——**而且不报错**，看起来和「executor 正在长考」一模一样，极难发现。当时
// curl 拨同一个地址是 101，因为 curl 对 http:// URL 只读小写 http_proxy。
//
// 顺带两点：c.hc 的 DialContext 已带 dialTimeout，不必再靠 DefaultClient；
// c.hc.Timeout 恒为 0（只设 Transport），满足 coder/websocket 对
// HTTPClient.Timeout 必须为零的硬要求。
//
// 单独抽成函数是为了可测：拿 HTTP_PROXY 环境变量做判据不可靠——
// net/http 对它有 sync.Once 缓存，t.Setenv 很可能不生效，测试会因为
// 「代理压根没被解析」而变绿，是典型的为错误理由通过。
func (c *Client) wsDialOptions() *websocket.DialOptions {
	opts := &websocket.DialOptions{HTTPClient: c.hc}
	if c.token != "" {
		opts.HTTPHeader = http.Header{"Authorization": []string{"Bearer " + c.token}}
	}
	for k, v := range c.extraHeaders {
		if opts.HTTPHeader == nil {
			opts.HTTPHeader = http.Header{}
		}
		opts.HTTPHeader.Set(k, v)
	}
	return opts
}

// MarkForwarded 返回一个副本，其后续请求都带上 X-Handoff-Forwarded: 1。
//
// 用途：agentd 扇出到别的 agentd 时必须带这个标记，让对端不再向外扇出——
// 一跳封顶，A→B→A 不可能成环。审核者 CLI **不要**用它。
func (c *Client) MarkForwarded() *Client {
	// 合并 B102：main 侧给 Client 加了 cursorRootOnce（sync.Once），w4 侧的
	// MarkForwarded 是整体拷贝——整体拷贝会把 lock 一起复制，go vet 的
	// copylocks 会拒。这里改为逐字段构造并重置 Once：镜像连接是独立实例，
	// 游标根缓存让它自己解析一次即可。
	cp := &Client{
		baseURL:          c.baseURL,
		token:            c.token,
		hc:               c.hc,
		initErr:          c.initErr,
		extraHeaders:     map[string]string{"X-Handoff-Forwarded": "1"},
		wsInitialBackoff: c.wsInitialBackoff,
		wsMaxBackoff:     c.wsMaxBackoff,
		wsStableAfter:    c.wsStableAfter,
	}
	return cp
}

// NoRedirect 返回一个副本，其 HTTP 客户端不跟随重定向。
//
// 用途：可达性探测必须只认给定的地址（计划约束：探测请求不跟随任何跳转，
// 否则恶意/被劫持的对端可用 302 把带 Authorization 的请求引到别处）。
// 只影响本副本，不触碰共享的 hc——forward/fanout 等既有路径行为不变。
//
// 为什么逐字段构造而不是整体拷贝：整体拷贝会把 cursorRootOnce 一起复制，
// go vet 的 copylocks 会拒（与 MarkForwarded 同款修正）。本副本是独立实例，
// 游标缓存让它自己解析一次即可。
func (c *Client) NoRedirect() *Client {
	cp := &Client{
		baseURL: c.baseURL,
		token:   c.token,
		initErr: c.initErr,
		hc: &http.Client{
			Transport: c.hc.Transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		extraHeaders:     c.extraHeaders,
		wsInitialBackoff: c.wsInitialBackoff,
		wsMaxBackoff:     c.wsMaxBackoff,
		wsStableAfter:    c.wsStableAfter,
	}
	return cp
}

// log 返回运行时 slog.Default()。
//
// 为什么不用包级 var：cli 命令在 RunE 里才 logx.Setup + slog.SetDefault，包级 var
// 在 init 时求值会锁死默认 logger，导致本包日志绕开 logx 的 stderr 文本输出
// （与 agentd 侧 store/config 的同款修正）。
func (c *Client) log() *slog.Logger { return slog.Default() }

// do 发送带 Bearer token 的请求，返回响应（调用方负责关闭 resp.Body）。
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	if err := c.checkInit(); err != nil {
		return nil, err
	}
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
	resp, err := c.hc.Do(req)
	if err != nil {
		// ctx 取消/超时不算「够不着」：它们同样从 hc.Do 的错误返回出来，但含义是
		// 「人按了 Ctrl-C」或「主动限时到了」，不是「那台机器不在」。混进
		// ErrUnreachable 会让调用方的降级分支在用户中断之后继续往下走。
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		// Debug 而非 Warn：status 轮询这类热路径会连续撞这里（upgrade 换版后每秒一次），
		// 在这一层打 Warn 会把真正需要注意的失败淹掉。这次够不着是致命还是可降级，
		// 只有调用方知道——由它决定要不要升级成 Warn（见 cmd/project.go 的降级点）。
		c.log().Debug("agentd 请求未拿到响应",
			"method", method, "path", path, "url", c.baseURL, "cause", err)
		return nil, fmt.Errorf("%w: %s %s: %w", ErrUnreachable, method, path, err)
	}
	return resp, nil
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
	body := strings.TrimSpace(string(b))
	return &httpStatusError{op: op, code: resp.StatusCode, body: body}
}

// ErrStatusUnsupported 表示对端 agentd 不认识 /api/status（版本早于该端点引入）。
//
// why（必须是可判别的哨兵）：这是唯一一个「HTTP 失败但结论是成功」的分支——
// 能收到 404 说明 TCP 通、HTTP 正常、Bearer 已经通过，三件事都被证明了。
// CLI 据此输出降级结论并退 0，而不是把一台完全能用的机器判成失败。
var ErrStatusUnsupported = errors.New("对端 agentd 不支持 /api/status")

// ErrUnreachable 表示这次请求**一个 HTTP 响应都没拿到**——TCP 拨不通、连接被拒、
// DNS 解析失败或读写中断，对端在不在都无从判断。
//
// why（必须是可判别的哨兵）：调用方要区分「对端不在」与「对端拒绝了这次请求」。
// 后者（400/409/500）拿到了响应，说明 agentd 在、Bearer 通过、语义上真的冲突了，
// 绝不能当成「机器不在」咽下去——那是往登记表里写脏数据。这个区分只有 client
// 知道；让调用方去 grep 错误文本里的 "connection refused" 是把 Go 的错误措辞与
// 平台差异变成契约。同 ErrStatusUnsupported 的理由。
//
// **不包含 ctx 取消与超时**（见 do 里的注释）。
var ErrUnreachable = errors.New("对端 agentd 够不着")

// ErrFootprintUnsupported 表示对端 agentd 太旧，没有 /api/footprint。
//
// 与 ErrStatusUnsupported 分开而不复用：调用方要给出的处置建议不同
// （那条说「升级后才能看状态」，这条说「升级后才能看进程足迹，眼下只能上机器 ps」）
var ErrFootprintUnsupported = errors.New("对端 agentd 不支持足迹体检")

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

// Footprint 拉取对端全部任务的进程足迹体检结果。
//
// 返回：
//   - 体检结果；404（对端 agentd 过旧、没有这个端点）返回 ErrFootprintUnsupported
//   - 请求失败或响应非法时返回错误
//
// 注意：这是慢命令——对端要遍历全部历史任务目录，调用方应给足超时。
func (c *Client) Footprint(ctx context.Context) (*proto.FootprintResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/footprint", nil)
	if err != nil {
		return nil, fmt.Errorf("足迹体检请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// 与 Status 的 404 同款处理：这是**预期结论**不是异常，用 Debug 而非
		// Info——调用方会把它渲染成人读的一句话，库层再打 Info 就是重复
		c.log().Debug("对端 agentd 不支持 /api/footprint，按版本过旧处理")
		return nil, ErrFootprintUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("足迹体检", resp)
	}
	var out proto.FootprintResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析足迹体检响应: %w", err)
	}
	return &out, nil
}

// ErrReclaimUnsupported 表示对端 agentd 太旧，没有 worktree 回收端点。
//
// 与 ErrStatusUnsupported / ErrFootprintUnsupported 分开：处置建议不同
// （这条说「升级后才能远程回收，眼下只能上机器 git worktree remove」）
var ErrReclaimUnsupported = errors.New("对端 agentd 不支持 worktree 回收")

// ReclaimRejected 是一次被拒的回收，带机器码与（脏树时的）改动清单。
//
// 为什么不做成一堆哨兵：四种拒绝共用 409，调用方要的是「哪一种 + 细节」，
// 一个带 Reason 字段的类型比四个哨兵加一次类型断言更直白
type ReclaimRejected struct {
	Reason proto.ReclaimReason
	Msg    string
	Dirty  []proto.DirtyFile
}

func (e *ReclaimRejected) Error() string { return e.Msg }

// ReclaimList 拉取对端全部终态任务的 worktree 残留体检结果。
//
// 返回：
//   - 体检结果；404（对端过旧）返回 ErrReclaimUnsupported
//   - 请求失败或响应非法时返回错误
func (c *Client) ReclaimList(ctx context.Context) (*proto.ReclaimListResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/reclaim", nil)
	if err != nil {
		return nil, fmt.Errorf("残留体检请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		// 与 Status/Footprint 的 404 同款：这是预期结论不是异常，用 Debug
		c.log().Debug("对端 agentd 不支持 /api/reclaim，按版本过旧处理")
		return nil, ErrReclaimUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("残留体检", resp)
	}
	var out proto.ReclaimListResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析残留体检响应: %w", err)
	}
	return &out, nil
}

// Reclaim 回收指定终态任务残留的 managed worktree。
//
// 参数：
//   - taskID: 目标任务
//   - force: 对脏工作树强删（丢弃未提交改动）
//
// 返回：
//   - 回收结果
//   - ErrReclaimUnsupported: 对端过旧
//   - *ReclaimRejected: 被拒（409），带机器码与改动清单
//   - 其余错误：连不上、401、5xx、任务不存在
//
// 注意（404 消歧）：老 agentd 没有这条路由，POST 打过去也是 404——与「任务
// 不存在」撞码。照直翻译会对着一台好机器报「任务不存在」，把人引向完全错误
// 的方向。因此收到 404 时补打一次 GET /api/reclaim：它也 404 才是老 agentd，
// 它 200 说明任务是真不存在。只在错误路径上多一次往返，换一个不靠猜的结论
func (c *Client) Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error) {
	// 为什么传 map 而不是预编码的 bytes.NewReader：c.do 会对 body 再走一次
	// json.Marshal——bytes.Reader 没有导出字段，会序列化成 {}，force 悄悄变 false。
	// 真机烟测照出的缺陷：curl 直打 force=true 有效、CLI 的 --force 却永远被拒。
	// 与 Reply 等既有方法保持一致，传可序列化的 map。
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks/"+taskID+"/reclaim",
		map[string]bool{"force": force})
	if err != nil {
		return nil, fmt.Errorf("回收 worktree 请求: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		if _, lerr := c.ReclaimList(ctx); errors.Is(lerr, ErrReclaimUnsupported) {
			c.log().Debug("对端两条 reclaim 路由皆 404，按版本过旧处理", "task", taskID)
			return nil, ErrReclaimUnsupported
		}
		return nil, c.httpError("回收 worktree", resp)
	}
	if resp.StatusCode == http.StatusConflict {
		var re proto.ReclaimError
		if derr := json.NewDecoder(resp.Body).Decode(&re); derr != nil {
			return nil, c.httpError("回收 worktree", resp)
		}
		c.log().Warn("回收被拒", "task", taskID, "reason", re.Reason, "dirty", len(re.Dirty))
		return nil, &ReclaimRejected{Reason: re.Reason, Msg: re.Error, Dirty: re.Dirty}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("回收 worktree", resp)
	}
	var out proto.ReclaimResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析回收响应: %w", err)
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
// 是协调者恢复会话现场（pending_tickets）的数据源。
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
//     语义由上层（协调者/manager）决定，本包不做解释
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
	// BaseCommit 是协调者本地 HEAD 的提交号，随请求上送让 agentd 校验任务仓库
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
// 形态由 Path / 路径是否存在 / OriginURL 是否非空共同决定：
//   - Path 空 + OriginURL 有 → 让 agentd 自己 clone 到 repo_root/<Name>
//   - Path 有且目录存在 → 登记已有仓（OriginURL 可省，省则 agentd 现读）
//   - Path 有且目录不存在 + OriginURL 有 → clone 到该 Path
//   - 其余非法组合 → 400
//
// OriginURL 是条件必填：仅当 Path 指向一个已存在的仓库时可省。
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
//
// 空字段不进请求体，远端看到的是缺省而非 ""。
func (c *Client) ProjectAdd(ctx context.Context, opts ProjectAddOpts) (*proto.ProjectLocation, error) {
	body := map[string]any{}
	if opts.OriginURL != "" {
		body["origin_url"] = opts.OriginURL
	}
	if opts.Name != "" {
		body["name"] = opts.Name
	}
	if opts.Path != "" {
		body["path"] = opts.Path
	}
	resp, err := c.do(ctx, http.MethodPost, "/api/projects", body)
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

// ProjectTreeAll 取跨机汇总的项目树（GET /api/projects/tree?scope=all）。
//
// 注意：本方法不带转发标记——那是 agentd 之间的标记，CLI 用了会让本机拒绝扇出。
func (c *Client) ProjectTreeAll(ctx context.Context) (*proto.ProjectTreeResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/projects/tree?scope=all", nil)
	if err != nil {
		return nil, fmt.Errorf("请求项目树(全机器): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("项目树(全机器)", resp)
	}
	var out proto.ProjectTreeResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析项目树(全机器)响应: %w", err)
	}
	return &out, nil
}

// Machines 列出本机视角的全部机器与探活结果（GET /api/machines）。
func (c *Client) Machines(ctx context.Context) (*proto.MachinesResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/machines", nil)
	if err != nil {
		return nil, fmt.Errorf("机器列表请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("机器列表", resp)
	}
	var out proto.MachinesResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析机器列表响应: %w", err)
	}
	return &out, nil
}

// ListTasksAll 取跨机汇总的任务列表（GET /api/tasks?scope=all），
// 远端任务读镜像快照、带 machine 名，机器应答情况在信封的 machines 栏。
//
// 注意：本方法不带转发标记——那是 agentd 之间的标记，CLI 用了会让本机拒绝汇总。
func (c *Client) ListTasksAll(ctx context.Context) (*proto.TasksResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/tasks?scope=all", nil)
	if err != nil {
		return nil, fmt.Errorf("任务汇总请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("任务汇总", resp)
	}
	var out proto.TasksResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析任务汇总响应: %w", err)
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

// PatchProject 改一条项目位置的引用名与/或路径。
//
// 参数：
//   - name: 当前引用名（URL 路径定位那条登记）
//   - newName: 新引用名（空串=不改名）
//   - path: 新路径（空串=不改路径）
//
// 返回：
//   - 更新后的位置记录
//   - newName 与 path 都为空返回错误（服务端会拒，本地先拦）
//   - 登记不存在返回 404 错误；新名字非法或新路径是另一个项目返回 400 错误；
//     新名字已被别的登记占用返回 409 错误
func (c *Client) PatchProject(ctx context.Context, name, newName, path string) (proto.ProjectLocation, error) {
	// 只带非空字段：空串字段不带，服务端以「缺字段=不改这个字段」判定改动面
	body := map[string]any{}
	if newName != "" {
		body["new_name"] = newName
	}
	if path != "" {
		body["path"] = path
	}
	resp, err := c.do(ctx, http.MethodPatch, "/api/projects/"+url.PathEscape(name), body)
	if err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("project edit 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return proto.ProjectLocation{}, c.httpError("project edit", resp)
	}
	var loc proto.ProjectLocation
	if err := json.NewDecoder(resp.Body).Decode(&loc); err != nil {
		return proto.ProjectLocation{}, fmt.Errorf("解析 project edit 响应: %w", err)
	}
	return loc, nil
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

// Done 归档任务，可携带一句完成说明。
//
// 参数：
//   - taskID: 待归档任务 ID
//   - note: 完成说明；空串=不留说明（服务端照常归档并照常发 archived 事件）
//
// 返回：
//   - noteSaved: 响应体 note_saved 如实回传——true=说明已落库；false=本次没带
//     说明，**或对端是不支持该字段的旧版 agentd**。响应体缺字段按 false 处理，
//     与 Stop 的 worktree_removed 同一模式：宁可多告警一次，也不让「说明悄悄
//     丢了」变成哑失败。调用方据此决定是否提示，不猜
func (c *Client) Done(ctx context.Context, taskID, note string) (bool, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/tasks/"+taskID+"/done",
		map[string]string{"note": note})
	if err != nil {
		return false, fmt.Errorf("done 请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, c.httpError("done", resp)
	}
	// 缺字段按 false：旧 agentd 只回 {"ok":true}，零值恰好是保守的那一侧
	var out struct {
		NoteSaved bool `json:"note_saved"`
	}
	if derr := json.NewDecoder(resp.Body).Decode(&out); derr != nil {
		c.log().Debug("done 响应体解析失败，按说明未保存处理", "task", taskID, "cause", derr)
		return false, nil
	}
	return out.NoteSaved, nil
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
//     原样输出给协调者
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

// Fetch 读取任务仓库内相对路径文件的内容（协调者取上下文用）。
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
	if err := c.checkInit(); err != nil {
		return nil, err
	}
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

// FramesStream 打开任务的结构化回合帧（frames.jsonl）流式读取。
//
// 参数：
//   - taskID: 目标任务
//   - offset: 起始字节偏移；>0 时优先于 tail（用于断线续传）
//   - tail:   从尾部回溯的字节数（offset<=0 时生效；两者都为 0 时由服务端取默认值）
//   - follow: 是否在到达文件尾后继续等待增量
//
// 返回：
//   - 流（调用方负责 Close，每行一个 proto.Frame 的 JSON）、响应开始时的文件
//     字节数、错误
//
// 注意：
//   - 与 RenderStream 一样**不设读超时**：follow 模式下长时间无输出是正常的
//   - 服务端保证只在完整行边界切，但调用方仍应按行缓冲——中间设备可能在
//     任意字节处切包
func (c *Client) FramesStream(ctx context.Context, taskID string,
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
	path := "/api/tasks/" + taskID + "/frames"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	resp, err := c.doStream(ctx, http.MethodGet, path)
	if err != nil {
		return nil, 0, fmt.Errorf("frames 流请求: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, 0, c.httpError("frames 流", resp)
	}
	size, _ := strconv.ParseInt(resp.Header.Get("X-Handoff-Frames-Size"), 10, 64)
	return resp.Body, size, nil
}

// WaitEvent 阻塞等待任务的下一个事件：跳过 progress（除非 all=true），
// 拿到首个可动作事件即返回并把 cursor 写盘；断线指数退避 1s→2s→…→60s
// 无限重连，ctx 取消才退出。
//
// cursor 语义（事件不丢不重的根基）：
//   - 每次调用开始时从 <游标根>/cursors/<agentd>/<task> 读取上次交付事件的 seq，
//     连接 WS 时以 from_seq=cursor 补拉断线期间产生的事件
//   - 返回首个可动作事件时把 cursor 原子写盘为该事件的 seq；被跳过的 progress
//     事件不推进 cursor（下次调用会重新收到并再次跳过，重复跳过无副作用）
//   - 因此每条可动作事件恰好交付一次（不重），cursor 之后的事件断线后一条不丢（不丢）
//
// 为什么 progress 不唤醒：progress 是高频、无需人工动作的状态播报（如「正在运行」），
// 若用它唤醒，wait 会在每次进度变化时把协调者叫醒做无意义的一次「看-忽略」；
// 协调者只需在真正需要决策的事件（question/permission_request/completed/failed/stalled）
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
			// 任务归档后游标再无用处：立刻回收，不等 TTL。放在返回前而非
			// 调用方，是因为两个消费端（wait / follow）都要这个行为
			if ev.Type == proto.EventTypeArchived {
				c.DropCursor(taskID)
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
	if err := c.checkInit(); err != nil {
		return err
	}
	// 拨号段：与原 waitOnce 完全一致（scheme 换算、Bearer 头、dialTimeout、
	// 永久状态码判定、连接建立/关闭日志），照搬不改
	wsScheme := "ws"
	if strings.HasPrefix(c.baseURL, "https://") {
		wsScheme = "wss"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(c.baseURL, "http://"), "https://")
	wsURL := wsScheme + "://" + host + "/ws/events?task=" + taskID +
		"&from_seq=" + strconv.FormatInt(fromSeq, 10)
	// 附加头（agentd→agentd 的防环标记）由 wsDialOptions 一并带上：streamOnce
	// 不走 do，若不补，MarkForwarded 的镜像连接从拨号起就丢了标记。
	opts := c.wsDialOptions()
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
	c.log().Debug("WS 连接建立", "addr", c.baseURL, "task", taskID, "from_seq", fromSeq)
	defer func() {
		conn.CloseNow()
		c.log().Debug("WS 连接关闭", "addr", c.baseURL, "task", taskID)
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
// ~/.handoff/cursors/<agentd 地址>/<task>，那是**审核者本机**的状态。agentd 做事件镜像时
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
//   - cursor 仍只在**交付**事件时推进，但「交付」不再等价于「协调者看过了」
//     ——事件可能在协调者正忙时流入。此刻会话若崩溃，该事件不会再重放
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
				// cursor 写失败不吞事件：先把事件交给协调者（宁可下次重投，不可这次丢）
				c.log().Warn("cursor 写盘失败", "task", taskID, "seq", ev.Seq, "cause", werr)
			}
			// 任务归档后游标再无用处：立刻回收，不等 TTL。与 WaitEvent 同一条
			// 规则，两个消费端的行为保持一致
			if ev.Type == proto.EventTypeArchived {
				c.DropCursor(taskID)
			}
			c.log().Info("follow 事件交付", "task", taskID, "seq", ev.Seq, "type", ev.Type)
			if err := onEvent(&ev); err != nil {
				return err
			}
			if ev.Type == proto.EventTypeFailed {
				// 只有**任务终结**才收流。回合失败走 turn_failed，它与 completed
				// 是同一个状态迁移（都进 waiting_review），所以行为也必须与
				// completed 一致——投递、不收流。
				//
				// 旧实现的回合失败落的是 failed 类型，于是 follow 在这里把它当
				// 任务终结收了流，还打「任务已失败」并以 0 退出，而任务其实好端端
				// 等着审（B100）。更糟的是它与 completed 行为相反，两个后果完全
				// 相同的事件走了两条路。
				c.log().Info("follow 结束：任务已终结", "task", taskID, "seq", ev.Seq)
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

// cursorPath 返回任务 cursor 文件路径（<游标根>/<agentd 命名空间>/<taskID>）。
//
// 为什么放用户主目录而非配置 DataDir：cursor 是协调者侧的本地状态，
// 与配置/数据库文件位置解耦；即使 DataDir 被移动，协调者已看过的进度也不重投。
// 该决策保留，cursordir.go 只是让这个根在不可写时可以降级。
//
// 为什么要 agentd 这一层：文件名只按 taskID 时，两台 agentd 上碰巧同 ID 的
// 任务会共用一个游标文件，互相把对方的进度顶掉。
func (c *Client) cursorPath(taskID string) (string, error) {
	root, err := c.cursorRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, cursorNamespace(c.baseURL), taskID), nil
}

// readCursor 读取任务 cursor；任何读不出来的情形都返回 0（从头开始）。
func (c *Client) readCursor(taskID string) int64 {
	seq, _ := c.readCursorWithDiag(taskID)
	return seq
}

// readCursorWithDiag 是 readCursor 的可诊断变体。
//
// 返回：
//   - seq: 游标值；读不出来时为 0
//   - reported: 是否属于「游标存在但用不了」并已告警（供测试断言，生产不用）
//
// 为什么要把「文件不存在」与其它错误分开：文件不存在是每个任务第一次 wait 的
// 常态，报它等于每次都喊狼来了；而权限被拒与内容损坏意味着游标存在却用不了，
// 后果是静默从 0 重放全部历史事件——协调者会看到一串早就处理过的旧事件，
// 却没有任何一条信息指向真正的原因。这是 B75 现场的成因。
func (c *Client) readCursorWithDiag(taskID string) (seq int64, reported bool) {
	p, err := c.cursorPath(taskID)
	if err != nil {
		c.log().Warn("游标路径不可用，本次从头开始", "task", taskID, "cause", err)
		return 0, true
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			c.log().Debug("cursor 文件不存在，从头开始", "task", taskID, "path", p)
			return 0, false
		}
		c.log().Warn("cursor 存在但读不了，本次将从头重放事件",
			"task", taskID, "path", p, "cause", err)
		return 0, true
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	if err != nil || n < 0 {
		c.log().Warn("cursor 内容损坏，本次将从头重放事件",
			"task", taskID, "path", p, "content", turnTailForLog(string(b)))
		return 0, true
	}
	c.log().Debug("cursor 读取", "task", taskID, "path", p, "seq", n)
	return n, false
}

// turnTailForLog 把可能很长的损坏内容截到可入日志的长度。
//
// 为什么截断：损坏的 cursor 文件可能是任意内容（磁盘故障写进了别的东西），
// 原样入日志会把一行日志撑成几 MB。
func turnTailForLog(s string) string {
	const max = 64
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

// writeCursor 把 seq 原子写入 cursor 文件（临时文件 + rename）。
//
// 为什么先写临时文件再 rename：直接写目标文件在写盘中途崩溃会留下截断内容，
// 下一次 wait 会把截断文本解析成 0（从头重投全部事件）；rename 保证读到的一定是
// 完整内容——要么旧值要么新值，不存在中间态。
//
// 为什么临时文件必须唯一（L-3）：两个 wait 进程/goroutine 并发写同一任务的
// 游标文件时，固定后缀的 <path>.tmp 会被两边同时打开/截断——先写完者
// rename 掉的是对方可能还没写完的共享文件，目标文件会短暂出现半截内容，对端
// 恰好读到即「读到一半的 tmp」。CreateTemp 同目录生成 O_EXCL 唯一名，rename
// 的始终是「自己写完整并关闭的文件」，并发读保证只看到完整旧值或完整新值。
func (c *Client) writeCursor(taskID string, seq int64) error {
	p, err := c.cursorPath(taskID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("创建 cursor 目录: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(p), filepath.Base(p)+"-*.tmp")
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

// 合并 B102：w4 侧在此处新增的 cursorTempTTL / sweepStaleCursorTemps 与 main 侧
// cursorgc.go 里同名实现重复（main 侧 glob 用 taskID+"-*.tmp"，与 writeCursor
// 的 CreateTemp(filepath.Base(p)+"-*.tmp") 命名一致，w4 侧误写成 "cursor-"+taskID），
// 这里取 main 侧的实现并删掉 w4 侧重复定义；PtySessions 是 w4 侧独有的纯新增，保留。

// PtySessions 取对端的**单机**终端会话列表（GET /api/pty/sessions）。
//
// 供本机 agentd 的 ?scope=all 扇出使用，调用方应先 MarkForwarded()——
// 否则对端会再扇出一轮，一跳封顶的约定就破了。
func (c *Client) PtySessions(ctx context.Context) (*proto.PtySessionsResp, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/pty/sessions", nil)
	if err != nil {
		return nil, fmt.Errorf("请求终端会话列表: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("终端会话列表", resp)
	}
	var out proto.PtySessionsResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析终端会话列表响应: %w", err)
	}
	return &out, nil
}
