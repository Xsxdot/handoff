// orchestration_client.go —— 任务编排的出站 client 契约（B233.13 Ticket 0 立面，
// B233.26 反转 D1 后归位提供方侧编排包）。
//
// 职责：定义 gateway 包生产字段持有的编排接口，方法集覆盖
// handler 对 s.mgr 的生产调用；实现是本包的 *Manager，由 cmd 组装点注入。
// 接口放提供方侧沿 internal/client/capabilities.go（B233.20）先例：方法名、
// 参数语义、返回形状冻结在 contract。
//
// 边界：不放业务实现、不新增 HTTP/CLI/事件类型；*Manager 只在组装点 new。
package orchestration

import (
	"context"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// TerminalOutcome 是终态动作（Done/Stop）的结果。
//
// Claimed 是本卡冻界的核心凭证：只有本次调用真正完成终态迁移的 CAS 时才为 true；
// 调用方（handleDone/handleStop）据此释放载体占用。任务已在终态时按幂等成功返回
// Claimed=false，不再释放占用。骨架期无实现，判定逻辑见 contract 冻结清单。
type TerminalOutcome struct {
	// Claimed 表示本次调用赢得了进入终态的 CAS，是唯一有资格 Release 的凭证。
	Claimed bool
	// WorktreeRemoved 是 Stop 的兼容字段；成功 Stop 恒为 false（现场留存，
	// 显式 reclaim/gc 才清理）。
	WorktreeRemoved bool
}

// OrchestrationClient 是 gateway 生产路径消费任务编排能力的唯一接口。
//
// B233.26 反转 D1 后接口随提供方归本包（沿 capabilities.go 先例）；gateway
// 以字段类型持有，不再在使用方定义。新增方法先回 contract 节点。
type OrchestrationClient interface {
	// —— 任务生命周期 ——
	Dispatch(ctx context.Context, req DispatchReq) (*proto.Task, error)
	Continue(ctx context.Context, taskID, instructions string) error
	Done(ctx context.Context, taskID, note string) (TerminalOutcome, error)
	Stop(ctx context.Context, taskID string) (TerminalOutcome, error)
	RelayAnswer(taskID, ticketID, answer string) error
	NoteDeliveryFailed(taskID, ticketID string, cause error)
	RecoverStuck(taskID string, force bool) (*RecoverReport, error)

	// —— 任务查询与回收 ——
	Status() (*proto.StatusResp, error)
	FootprintAll() (*proto.FootprintResp, error)
	ReclaimList() (*proto.ReclaimListResp, error)
	Reclaim(ctx context.Context, taskID string, force bool) (*proto.ReclaimResp, error)
	GC(ctx context.Context, force, execute bool) (*proto.GCResp, error)

	// —— 任务查询与应答（B233.28：gateway 不再直打 store 的这一面）——
	ListTasks() ([]proto.Task, error)
	GetTask(taskID string) (*proto.Task, error)
	PendingTickets(taskID string) ([]proto.Ticket, error)
	EventsFrom(taskID string, fromSeq int64, limit int) ([]proto.Event, error)
	EventsFromAsc(taskID string, fromSeq int64, limit int) ([]proto.Event, error)
	LatestEvent(taskID string) (*proto.Event, error)
	CountEvents(taskID string, afterSeq, throughSeq int64) (int, error)
	ListMirrorTasks() ([]store.MirrorTask, error)
	MirrorTaskTarget(taskID string) (string, bool, error)
	MirrorEventsFrom(taskID string, fromSeq int64, limit int) ([]proto.Event, error)
	AnswerTicket(ctx context.Context, taskID, ticketID, answer string) (applied bool, err error)
	ResumeIfIdle(ctx context.Context, taskID string)

	// —— 项目位置 ——
	RegisterProject(ctx context.Context, req workspace.RegisterProjectReq) (proto.ProjectLocation, error)
	ListProjects(ctx context.Context) ([]proto.ProjectLocation, error)
	ListProjectLocations() ([]proto.ProjectLocation, error)
	GetProjectLocationByName(name string) (proto.ProjectLocation, error)
	UpdateProjectLocation(name, newName, newPath string) (proto.ProjectLocation, error)
	UnregisterProject(ctx context.Context, name string) error

	// —— 执行者名单 ——
	ExecutorNames() []string

	// —— 工作区能力只读透传（本卡不迁工作区，仅收口 handler 对 s.mgr 的调用）——
	Workspace() workspace.Capability
	RequireWorkspace() error
	AssembleResultRef(ctx context.Context, task *proto.Task, repo, headRev string) (workspace.ResultRef, error)
	InspectRepoDir(ctx context.Context, dir string) (root, origin string, err error)
}
