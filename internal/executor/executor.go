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
	"errors"
	"strings"

	"github.com/xushixin/handoff/internal/proto"
)

// ErrTaskNotRunning 表示任务在本 adapter 里没有运行态：executor 已终结、
// 从未启动，或运行态已随 Stop 注销。
//
// 实现方在 Send / RespondPermission / Stop 遇到这种情况时必须包装本哨兵错误
// （fmt.Errorf("...: %w", executor.ErrTaskNotRunning)），调用方据此区分
// 「executor 已经不在」与「executor 还在但这次调用失败了」——两者的处置完全
// 不同：前者应把任务交审核者裁决，后者应保持可重试。上层禁止靠错误文本判别。
var ErrTaskNotRunning = errors.New("任务不在运行中")

// TruncationMarker 是文本截断的显式标记，追加在截断文本末尾（如权限描述里的
// bash 命令超限时）。
//
// 契约：executor 侧截断时必须以本常量收尾（opencode 的 truncateMarked），
// manager 侧据此 fail-closed——权限文本含本标记说明「审核/裁决者看到的是截断
// 后的不完整命令」，危险片段可能落在截断之外，黑名单与廉价模型都不可信，必须
// 升级人工审核者。
//
// 注意（B6 起）：权限描述在 executor 侧只受 64KB 防失控硬上限约束，常规长度
// 不再触发本标记——只有真的超了 64KB 才会出现它。事件 payload 的短展示由
// manager 侧另行截断（同样以本常量收尾）。
const TruncationMarker = "…（已截断）"

// StartReq 是 Adapter.Start 的入参：任务上下文 + 计划内容 + 任务工作目录。
//
// 字段说明：
//   - Task: 任务快照（ID/仓库/目标等），adapter 可据此写日志与恢复凭据
//   - PlanContent: 解码后的计划原文（dispatch 时从 plan_b64 还原），adapter 负责
//     把它加工成 executor 的启动 prompt
//   - TaskDir: 任务专属目录（agentd 在 DataDir/tasks/<id> 下创建），adapter 可把
//     配置、日志、渲染文件等任务物料写在这里
//   - Env: 启动 executor 进程时额外注入的环境变量（形如 KEY=VALUE，已解析已展开）。
//     由 manager 按 task.Executor 从 env 文件解析后填入；nil/空表示不注入。
//     实现方必须把它注入到自己拉起的进程环境中——这是 B19 对所有 adapter 的统一要求，
//     放在契约上而非各 adapter 的构造参数上，是为了让后续 adapter（Claude Code、grok）
//     不必各写一份注入逻辑
type StartReq struct {
	Task        proto.Task
	PlanContent string
	TaskDir     string
	Env         []string
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

// 归一化工具名：各 adapter 的原始工具名折算到这一组常量，permgate 只认它们。
//
// 为什么要归一化：三个 executor 对同一件事的叫法不同（claude 的 "Write"、
// opencode 的 "edit"、grok 的工具名见 Task 1 探针），而判据只有一份。
const (
	PermToolBash     = "bash"
	PermToolWrite    = "write"
	PermToolEdit     = "edit"
	PermToolWebFetch = "webfetch"
	PermToolOther    = "other" // 未识别的工具：判据退回按描述全文处理
)

// PermRequest 是权限请求的结构化形态。
//
// 它与 AdapterEvent.Text 不是重复：Text 是给人看的全文（工单与展示的唯一
// 真相源），PermRequest 是给判据看的字段。拍平成字符串会丢掉工具名与路径，
// 那正是黑名单只能对整串做正则、于是既误判又漏判的根因。
//
// 边界：
//   - adapter 提取不出结构时**不要伪造**：整个 Perm 置 nil，manager 会
//     fail-closed 升级人工。填一个空壳会让判据误以为拿到了结构
type PermRequest struct {
	Tool    string   // 归一化工具名，取上面的 PermTool* 常量
	Command string   // Tool=bash 时的完整命令串（不截断）
	Paths   []string // Tool=write|edit 时的目标路径（可为相对路径）
}

// NormalizePermTool 把 executor 的原始工具名折算为归一化名。
//
// 参数：raw 为 executor 侧的原始工具名，允许带空白、大小写任意
// 返回：PermTool* 之一；未识别时返回 PermToolOther
//
// 注意：本表只收本项目**实测见过**的名字。新增 executor 时先取真实样本再
// 补表，不要按想象加别名——猜错的代价是判据静默走错路由。
func NormalizePermTool(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "bash":
		return PermToolBash
	case "write":
		return PermToolWrite
	case "edit":
		return PermToolEdit
	case "webfetch":
		return PermToolWebFetch
	default:
		return PermToolOther
	}
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
	Type         string // "permission" | "question" | "progress" | "result"
	PermissionID string // Type=permission 时有效（manager 按其派生 ticket id，天然幂等）
	SessionID    string // 可选：executor 会话标识，manager 落 task.ExecutorSession；空则忽略
	Text         string // permission 描述 / question 原文 / progress 文本
	// Perm 是 Type=permission 时的结构化载荷；nil 表示 adapter 提取不出结构，
	// manager 据此 fail-closed 升级人工（看不懂的请求交给人）。
	Perm   *PermRequest
	Result *Result // Type=result 时有效
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
	//   - permID: 权限请求 id（与事件中的 PermissionID 一致；manager 的 ticket id
	//     经 taskID:permID 命名空间化，此处传裸 permID，不得传命名空间化 id）
	//   - decision: "once"（批准本次）或 "reject"（拒绝）
	RespondPermission(ctx context.Context, taskID, permID, decision string) error

	// Stop 终止任务执行并回收 executor 侧资源，事件通道随即关闭。
	Stop(taskID string) error
}
