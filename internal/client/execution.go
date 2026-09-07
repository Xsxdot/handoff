// 本文件声明跨机执行能力 client 与共用传输的编译期接缝。
//
// 职责：把「调用哪种能力」从「怎么连上目标机」拆成两个接口；*Client 作为兼容
// 聚合门面同时实现二者（spec 允许薄聚合，禁止新路径再取裸 HTTP 乱拼）。
//
// 边界：
//   - 不改任何方法体、不发网络请求、不把 in-process ApprovalClient 暴露成新 HTTP 面
//   - 工作区 / 卡 / 机器 / PTY 方法不进 ExecutionClient（工作区归 B233.4）
//   - HTTPClient 只出现在 Transport 上，供网关原样搬运；能力 client 接口不含它
package client

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// Transport 是直连与 relay 共用的字节通道：基址、认证传输、超时由调用方经 context 施加。
//
// 网关原样搬运（任意方法/路径）可取 HTTPClient；能力 client 必须走类型化方法，
// 不得经此拼执行/审批/事件请求。
type Transport interface {
	BaseURL() string
	HTTPClient() *http.Client
}

// ExecutionClient 是 233.1 执行能力在跨机调用面上的类型化入口。
//
// 方法与本机 *Client 同名同签名，语义与本机相同；差异只允许出现在失败诊断
// （ErrUnreachable / ErrTunnelDisconnected）。Ask 回程与审批回程都走 Reply，
// 不另开审批 HTTP 面。
type ExecutionClient interface {
	Dispatch(ctx context.Context, opts DispatchOpts) (*proto.Task, error)
	Reply(ctx context.Context, taskID, ticketID, answer string) error
	Continue(ctx context.Context, taskID, instructions string) error
	Stop(ctx context.Context, taskID string) (worktreeRemoved bool, err error)
	WaitEvent(ctx context.Context, taskID string, all bool) (*proto.Event, error)
	FollowEvents(ctx context.Context, taskID string, all bool, idle time.Duration, onEvent func(*proto.Event) error, onBacklog func(*BacklogSummary) error) error
}

// ErrTunnelDisconnected 表示 relay 隧道在请求途中断开。
//
// Ticket 0 只立哨兵：生产路径的隧道失败仍包装为 ErrUnreachable。实现节点接线后，
// 调用方用 errors.Is 区分「目标不可达」与「隧道断开」，不得把二者都吞成泛网络错误。
var ErrTunnelDisconnected = errors.New("relay 隧道断开")

var (
	_ Transport       = (*Client)(nil)
	_ ExecutionClient = (*Client)(nil)
)
