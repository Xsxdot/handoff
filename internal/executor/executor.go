// Package executor 定义 handoff 的 executor 挂载契约：Adapter 接口与事件/结果类型。
//
// 职责：
//   - 定义「五动作」Adapter 契约：Start / Events / Send / RespondPermission / Stop
//   - 定义 AdapterEvent（permission/question/progress/result 四类）与 Result 数据结构
//   - 为不同 executor（opencode、Claude Code、grok）预留统一挂载点，实现方只实现本契约
//
// 边界：
//   - 实现方（adapter）不得直接碰 store——所有任务状态与工单都由 manager 写入，
//     这条边界防止 adapter 与 manager 双写打架：状态机只有一个写入者（manager），
//     adapter 只负责「执行、产事件、收指令」，任何持久化诉求（如记录会话 id）
//     都以事件或返回值形式交给 manager 落库
//   - 本包为纯契约：无 I/O、无实现、无外部依赖（仅引用 proto 的数据结构）
//
// 幂等约定：
//   - AdapterEvent.PermissionID 由实现方生成并保证稳定：同一权限请求（如 SSE
//     断线重连后的重放）携带相同 PermissionID。manager 把它按任务命名空间化为
//     ticket id（taskID:permID）——同任务重放时 CreateTicket 按 id 幂等，天然
//     去重；跨任务 permID 碰撞也不会互相吞工单。回传 executor 时还原裸 permID
package executor

import (
	"context"

	"github.com/xushixin/handoff/internal/proto"
)

// StartReq 是 Adapter.Start 的入参：任务上下文 + 计划内容 + 任务工作目录。
//
// 字段说明：
//   - Task: 任务快照（ID/仓库/目标等），adapter 可据此命名 tmux 会话、写日志等
//   - PlanContent: 解码后的计划原文（dispatch 时从 plan_b64 还原），adapter 负责
//     把它加工成 executor 的启动 prompt
//   - TaskDir: 任务专属目录（agentd 在 DataDir/tasks/<id> 下创建），adapter 可把
//     配置、日志、渲染文件等任务物料写在这里
type StartReq struct {
	Task        proto.Task
	PlanContent string
	TaskDir     string
}

// Result 是一次执行回合的终态结果（OK 或 FailReason 二选一）。
type Result struct {
	Branch     string // executor 工作分支名（如 handoff/T1）
	CommitHash string // 回合收尾 commit 的哈希
	SessionID  string // executor 会话标识（如 opencode session id），供续接与归档
	Summary    string // 执行摘要（给审核者看的完成说明）
	OK         bool   // true=正常完成；false=失败（见 FailReason）
	FailReason string // OK=false 时的失败原因/日志尾部
}

// AdapterEvent 是 executor 侧产出的单向事件，由 manager 中介循环消费。
//
// Type 取值："permission" | "question" | "progress" | "result"，语义：
//   - permission: 权限门请求，PermissionID 有效（manager 按其派生命名空间化的
//     ticket id，见包级幂等约定），Text 为权限描述（如 "Bash: rm -rf node_modules"）；
//     等待人工裁决
//   - question:   提问，Text 为问题原文；等待人工回答
//   - progress:   进度播报（可选心跳），Text 为进度文本；只入库不阻塞
//   - result:     回合终态，Result 有效；之后是否续接由审核者决定
//
// SessionID 可携带于任意事件（含 progress）：manager 收到非空 SessionID 时落
// task.ExecutorSession（空则忽略，向后兼容）。progress 带它是「会话就绪」信号
// ——审核主路径常以 question 收尾、result 永不出现，progress 是会话 id 到达
// manager 的可靠通道；result 携带它是双保险（见 adapter 的会话就绪 emit）。
type AdapterEvent struct {
	Type         string  // "permission" | "question" | "progress" | "result"
	PermissionID string  // Type=permission 时有效（manager 按其派生 ticket id，天然幂等）
	SessionID    string  // 可选：executor 会话标识，manager 落 task.ExecutorSession；空则忽略
	Text         string  // permission 描述 / question 原文 / progress 文本
	Result       *Result // Type=result 时有效
}

// Adapter 是 executor 挂载契约，实现方与 manager 的交互面就是这五个动作。
//
//   - Start: 异步启动执行，立即返回；事件通过 Events 通道持续产出
//   - Events: 该任务的事件流（Start 后可用），通道关闭表示执行终结
//   - Send: 回答提问 / 回发修改指令（同一会话续接，上下文完整保留）
//   - RespondPermission: 应答权限门，decision 取 "once"（批准本次）或 "reject"（拒绝）
//   - Stop: 终止执行并回收资源（事件通道随之关闭）
//
// 边界：
//   - 实现方不写 store、不做审批判断（见包级边界说明），「批不批」由 manager
//     根据审核者（人）的应答决定后经本接口回传
type Adapter interface {
	// Start 异步启动任务执行并立即返回。
	//
	// 参数：
	//   - ctx: 控制启动阶段的超时/取消；不代表执行生命周期（执行延续到 Stop）
	//   - req: 任务快照、计划原文与任务工作目录
	//
	// 返回：
	//   - 启动失败（如 executor 不可达）时返回错误，此时任务应标记 failed
	//
	// 注意：
	//   - 启动后 executor 侧产生的事件经 Events 通道持续送达，与返回时机无关
	Start(ctx context.Context, req StartReq) error

	// Events 返回该任务的事件流通道（Start 后可用）。
	//
	// 注意：
	//   - 通道关闭表示执行终结（Stop 或内部异常退出），manager 据此结束中介循环
	Events(taskID string) <-chan AdapterEvent

	// Send 回答提问 / 回发修改指令，对同一会话续接执行。
	//
	// 参数：
	//   - taskID: 目标任务
	//   - text: 回答原文或续接指令，必须原样透传，不得加工
	Send(ctx context.Context, taskID, text string) error

	// RespondPermission 应答 executor 的权限请求。
	//
	// 参数：
	//   - taskID: 目标任务
	//   - permID: 权限请求 id（与事件中的 PermissionID 一致，即 ticket id）
	//   - decision: "once"（批准本次）或 "reject"（拒绝）
	RespondPermission(ctx context.Context, taskID, permID, decision string) error

	// Stop 终止任务执行并回收 executor 侧资源，事件通道随即关闭。
	Stop(taskID string) error
}
