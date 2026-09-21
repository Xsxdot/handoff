// 本文件声明 B233.20 非执行能力面的消费收窄接口。
//
// 职责：事件镜像、PTY、项目、回收、gc、纯查询命令、bundle 同步这些非执行面
// 各自按「该面实际调用的能力」声明窄接口（方法集逐字取自今日消费代码），
// 消费方持接口注入使用；*Client 作为兼容聚合门面逐个编译期断言实现。
// 先例沿 execution.go 的 ExecutionClient（B233.16）。
//
// 边界：
//   - 不改任何方法体、不发网络请求、不新增 HTTP 路径
//   - 一个面一个接口，禁止把多面能力并进一个「大接口」重新聚合
//   - 带「一跳封顶」语义的接口（ForwardedStreamEventsOnce / agentd 侧 clientFor*
//     收窄入口的返回值）由提供方在返回前完成 MarkForwarded，消费方不感知标记
package client

import (
	"context"
	"io"

	"github.com/Xsxdot/handoff/internal/proto"
)

// EventStreamClient 是账本事件镜像的订阅缝（B233.20，internal/ledgermirror 消费）。
//
// 方法是 StreamEventsOnce 的一跳封顶变体：订阅请求必须带 X-Handoff-Forwarded: 1，
// 让对端不再向外扇出（一跳封顶，A→B→A 不成环）。镜像身份判据（池对同一 target
// 返回同一实例）由 Machines.For 的生产适配层保证，本接口不承载身份语义。
type EventStreamClient interface {
	ForwardedStreamEventsOnce(ctx context.Context, taskID string, fromSeq int64,
		onEvent func(proto.Event) error) error
}

// ForwardedStreamEventsOnce 以一跳封顶语义建立一次事件流订阅。
//
// 与 c.MarkForwarded().StreamEventsOnce(...) 逐字等价（同一副本、同一请求头、
// 同一条 WS 路径）；收窄后的订阅缝经本方法拿到带标记的能力，不再引用聚合类型。
// 每次调用现做标记副本——与镜像源逐次重订的既有节奏一致。
func (c *Client) ForwardedStreamEventsOnce(ctx context.Context, taskID string, fromSeq int64,
	onEvent func(proto.Event) error) error {
	return c.MarkForwarded().StreamEventsOnce(ctx, taskID, fromSeq, onEvent)
}

// PtySessionClient 是 PTY 会话面的能力子集（B233.20，internal/agentd 的
// coordinator PTY 与终端会话扇出路径消费）。
//
// 生产返回值经 agentd 的 clientFor* 收窄入口给出，**已带一跳封顶标记**——
// 消费方不做 MarkForwarded，也不再引用聚合类型。
type PtySessionClient interface {
	PtySessions(ctx context.Context) (*proto.PtySessionsResp, error)
	CreatePtySession(ctx context.Context, req proto.CreatePtySessionReq) (*proto.PtySession, error)
	DeletePtySession(ctx context.Context, id string) error
}

// ProjectListClient 是项目清单读取面的能力子集（B233.20，internal/agentd 的
// coordinator PTY 工作目录解析路径消费）。
//
// 生产返回值经 agentd 的 clientFor* 收窄入口给出，**已带一跳封顶标记**。
type ProjectListClient interface {
	ProjectList(ctx context.Context) ([]proto.ProjectLocation, error)
}

// ReclaimClient 是 worktree 回收面的能力子集（B233.20，cmd reclaim 命令消费）。
type ReclaimClient interface {
	ReclaimList(ctx context.Context) (*proto.ReclaimListResp, error)
	Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error)
}

// GCClient 是终态缓存/工作树清理面的能力子集（B233.20，cmd gc 命令消费）。
type GCClient interface {
	GC(ctx context.Context, force bool) (*proto.GCResp, error)
	GCPreview(ctx context.Context, force bool) (*proto.GCResp, error)
}

// AttachClient 是终端实况面的能力子集（B233.20，cmd attach 命令消费：
// 实况流跟读 + 无参形态的任务列表）。
type AttachClient interface {
	ListTasks(ctx context.Context) ([]proto.TaskView, error)
	RenderStream(ctx context.Context, taskID string,
		offset, tail int64, follow bool) (io.ReadCloser, int64, error)
}

// FramesClient 是结构化回合帧读取面的能力子集（B233.20，cmd frames 命令消费）。
type FramesClient interface {
	FramesStream(ctx context.Context, taskID string,
		offset, tail int64, follow bool) (io.ReadCloser, int64, error)
}

// BundleClient 是任务分支 bundle 下载面的能力子集（B233.20，cmd pull 的
// bundle 同步路径消费）。
type BundleClient interface {
	Bundle(ctx context.Context, taskID, have string) (io.ReadCloser, error)
}

// 编译期锚：聚合门面必须逐个实现各面能力接口（沿 execution.go 先例）。
var (
	_ EventStreamClient = (*Client)(nil)
	_ PtySessionClient  = (*Client)(nil)
	_ ProjectListClient = (*Client)(nil)
	_ ReclaimClient     = (*Client)(nil)
	_ GCClient          = (*Client)(nil)
	_ AttachClient      = (*Client)(nil)
	_ FramesClient      = (*Client)(nil)
	_ BundleClient      = (*Client)(nil)
)
