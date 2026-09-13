// authority.go —— 非 OpenCode 权限决策权威（B233.21）。
//
// 职责：把 orchestration.Manager.handlePermission 内联的决策事实收口到
// internal/approval——重放幂等（IsReplay）、判据裁决（Judge）、决策复用
// （FindReuse）、AutoAllow 出口处置（AutoAllow）。与 OpenCode 面的 Client
// （Request/consult/escalate）同属一个审批权威包：permgate 判定、复用与
// 自动放行的政策事实在这里只有一份；OpenCode 面经 Hooks.JudgePermission 绑定
// 的也是 Judge 这份实现（编排侧委托）。
//
// 边界（B233.21 spec §目标）：工单、状态迁移、事件、waiter、Publish 与原生
// 回传是编排域账本事实，仍归 orchestration.Manager。本类型只产出决策，
// 自身不落任何账本写入——唯一的账本动作（AutoAllow 审计）经 Hooks 回调，
// 实现与 OpenCode 面 Hooks.AutoAllow 相同。
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// AuthorityHooks 绑定判据输入与 AutoAllow 出口的账本/投递回调。
type AuthorityHooks struct {
	Log     *slog.Logger
	Store   *store.Store
	Gate    *permgate.Gate
	DataDir string
	// AuditAutoAllow 写自动放行审计并累计计数。与 OpenCode 面 Hooks.AutoAllow
	// 绑定同一实现（orchestration.Manager.auditAutoAllowOnly）：只审计，
	// 不建工单、不发事件、不改状态。
	AuditAutoAllow func(taskID string, ev executor.AdapterEvent, verdict permgate.Verdict)
	// DeliverAutoAllow 把 AutoAllow 出口的 once 裁决原生回传 executor（解析
	// 执行者 + RespondPermission("once")）。原生投递归编排域（B233.21 spec：
	// Manager 收缩后仍保留「原生回传」一步），两类失败（解析执行者失败 /
	// RespondPermission 失败）的日志落在回调侧的第一现场。
	DeliverAutoAllow func(taskID, permID string)
}

// Authority 是非 OpenCode（claude/grok/codex/agy）权限请求的决策权威。
// 无内部可变状态，Manager 生命周期内共用一份，taskID 走参数。
type Authority struct {
	hooks AuthorityHooks
}

// NewAuthority 构造非 OpenCode 决策权威。
func NewAuthority(hooks AuthorityHooks) *Authority {
	return &Authority{hooks: hooks}
}

func (a *Authority) logger() *slog.Logger {
	if a != nil && a.hooks.Log != nil {
		return a.hooks.Log
	}
	return slog.Default()
}

// HasAutoAllow 判定某权限请求是否已有 safe-command AutoAllow 审计事件。
// （实现体迁自 Manager.hasPermissionAutoAllow，B233.21。）
//
// why 审计事件是幂等标记：Safe-command AutoAllow deliberately has no ticket,
// so its audit event is the durable idempotency marker for replayed permission
// notifications.
func (a *Authority) HasAutoAllow(taskID, permID string) bool {
	events, err := a.hooks.Store.EventsFrom(taskID, 0, 1000)
	if err != nil {
		a.logger().Error("查询权限自动放行审计事件失败", "task", taskID, "perm", permID, "cause", err)
		return false
	}
	for _, event := range events {
		if event.Type != proto.EventTypePermissionAutoAllow {
			continue
		}
		var payload struct {
			PermissionID string `json:"permission_id"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			a.logger().Error("解析权限自动放行审计事件失败", "task", taskID,
				"perm", permID, "seq", event.Seq, "cause", err)
			continue
		}
		if payload.PermissionID == permID {
			return true
		}
	}
	return false
}

// IsReplay 判定一次 permission 事件是否为「已完整中介过」的重放
// （SSE 断线重连 / agentd 重启后订阅重建都会重放同一权限请求）。
// （实现体迁自 Manager.isPermissionReplay，B233.21。）
//
// 参数：permID 是 executor 侧裸权限 id（仅用于日志），ticketID 是命名空间化的工单 id。
//
// 返回：true 表示应跳过全部中介动作。
//
// 判定规则（why 这里不能只看工单是否存在）：
//   - 工单不存在 → 新请求，正常中介
//   - 工单存在且已应答 → 真重放，跳过：再动一次就是重复交付
//   - 工单存在但通知事件缺失 → **不是**重放，而是崩溃恰好落在「建工单」与
//     「追加事件」之间留下的半截状态。仅凭工单存在就跳过，会让 permission_request
//     永不产生、状态停在 running、无等待者，协调者的 wait 永远不触发（N-4）——
//     此处放行以补发事件，CreateTicket 本身幂等，不会产生第二张工单
//   - 工单存在且事件也在 → 真重放，跳过（P1-7 的幂等承诺）
func (a *Authority) IsReplay(taskID, permID, ticketID string) bool {
	if a.HasAutoAllow(taskID, permID) {
		a.logger().Debug("权限自动放行请求重放，跳过中介", "task", taskID, "perm", permID)
		return true
	}
	tk, err := a.hooks.Store.GetTicket(ticketID)
	if errors.Is(err, store.ErrNotFound) {
		return false
	}
	if err != nil {
		a.logger().Error("读取权限工单失败", "task", taskID, "perm", permID, "ticket", ticketID, "cause", err)
		return true // 读不到就不动，宁可少一次唤醒也不重复中介
	}
	if tk.Answer != nil {
		a.logger().Debug("权限请求重放且已应答，跳过中介", "task", taskID, "perm", permID, "ticket", ticketID)
		return true
	}
	hasEvent, err := a.hooks.Store.TicketHasEvent(taskID, ticketID)
	if err != nil {
		a.logger().Error("查询权限工单通知事件失败", "task", taskID, "ticket", ticketID, "cause", err)
		return true
	}
	if hasEvent {
		a.logger().Debug("权限请求重放，跳过中介", "task", taskID, "perm", permID, "ticket", ticketID)
		return true
	}
	a.logger().Warn("权限工单存在但通知事件缺失，补发以自愈", "task", taskID, "perm", permID, "ticket", ticketID)
	return false
}

// Judge 把一次权限事件交给判据网关，返回裁决。
// （实现体迁自 Manager.judgePermission，B233.21。）
//
// 参数：
//   - taskID: 任务 id（用于取工作区范围）
//   - ev: 权限事件
//
// 返回：permgate.Verdict；一切无法判定的情形都返回 Escalate（fail-closed）
//
// 注意：
//   - ev.Perm 为 nil 表示 adapter 提取不出结构——看不懂的请求交给人，
//     绝不让廉价模型去猜
//   - 读任务失败时工作区范围不可知，同样升级人工：范围未知时判「路径在不在
//     范围内」是没有意义的
//   - Gate 为 nil 是构造契约被违反（装配方文档已写明不得为 nil），
//     但这里仍兜一手：不兜的话 Judge 会在权限处理 goroutine 里空指针 panic，
//     把整个 agentd 带走——那比升级人工严重得多，也违背「fail-closed 无例外」
func (a *Authority) Judge(taskID string, ev executor.AdapterEvent) permgate.Verdict {
	if a.hooks.Gate == nil {
		a.logger().Error("判据网关未装配，fail-closed 升级人工（NewManager 的 gate 不得为 nil）",
			"task", taskID, "perm", ev.PermissionID)
		return permgate.Verdict{Action: permgate.Escalate, Reason: "判据网关未装配"}
	}
	if ev.Perm == nil {
		a.logger().Warn("权限事件缺结构化载荷，fail-closed 升级人工",
			"task", taskID, "perm", ev.PermissionID,
			"text", turn.TruncateRunes(ev.Text, 120))
		return permgate.Verdict{Action: permgate.Escalate,
			Reason: "adapter 未提供结构化权限载荷"}
	}
	task, err := a.hooks.Store.GetTask(taskID)
	if err != nil {
		a.logger().Warn("读任务失败，工作区范围不可知，fail-closed 升级人工",
			"task", taskID, "perm", ev.PermissionID, "cause", err)
		return permgate.Verdict{Action: permgate.Escalate,
			Reason: "读任务失败，工作区范围不可知"}
	}
	scope := permgate.Scope{
		Workdir:    task.Workdir(),
		TaskDir:    filepath.Join(a.hooks.DataDir, "tasks", taskID),
		TaskTmpDir: executor.TaskTmpDir(a.hooks.DataDir, taskID),
	}
	v := a.hooks.Gate.Judge(permgate.Request{
		Tool:      ev.Perm.Tool,
		Text:      ev.Text,
		Command:   ev.Perm.Command,
		Paths:     ev.Perm.Paths,
		Truncated: strings.Contains(ev.Text, executor.TruncationMarker),
	}, scope)
	switch v.Action {
	case permgate.AutoAllow:
		a.logger().Debug("权限判定：自动放行", "task", taskID, "perm", ev.PermissionID,
			"tool", ev.Perm.Tool, "paths", ev.Perm.Paths, "reason", v.Reason)
	case permgate.Consult:
		a.logger().Info("权限判定：交审批者", "task", taskID, "perm", ev.PermissionID,
			"tool", ev.Perm.Tool, "reason", v.Reason, "rule", v.Rule)
	default:
		lvl := EscalateLogLevel(v.Rule)
		a.logger().Log(context.Background(), lvl, "权限判定：升级人工",
			"task", taskID, "perm", ev.PermissionID, "tool", ev.Perm.Tool,
			"paths", ev.Perm.Paths, "workdir", scope.Workdir, "task_dir", scope.TaskDir,
			"task_tmp_dir", scope.TaskTmpDir,
			"reason", v.Reason, "rule", v.Rule)
	}
	return v
}

// EscalateLogLevel 决定「权限判定：升级人工」这条日志的级别。
// （实现体迁自 orchestration.escalateLogLevel，B233.21；编排侧保留同名委托
// 供白盒测试沿用。）
//
// 参数：rule 为 Verdict.Rule（黑名单命中时是规则原文，自指令时是
// permgate.RuleSelfCommand，其余情形为空）
//
// 为什么不是一律 Info：Warn 这一档留给「本该被静默通过、现在被拦下」的事件，
// 那是每次收口改动的全部价值所在，必须在日志里一眼可见。今天有两类——
// 越界写与结构缺失（Rule 为空，B27 那一批）、自指令（B115）。黑名单命中
// 走 Info，因为它改动前后都会被拦，不是新增的信号。
func EscalateLogLevel(rule string) slog.Level {
	if rule == "" || rule == permgate.RuleSelfCommand {
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

// FindReuse 判定本次权限请求能否复用本任务内既有的人工批准（B57②）。
// （判定核心迁自 Manager.reuseDecision 的查询与 fail-closed 判定，B233.21；
// 命中后的 permission_reuse 审计事件与自动放行执行是账本事实，由 Manager 按
// 「命中」结论落。）
//
// 参数：ticketID 是命名空间化工单 id，仅用于日志定位。
//
// 返回：
//   - prior: 命中的已送达 allow 工单；未命中为 nil
//   - fp: 本次请求的裁决指纹（写入方与查询方必须同源——B91）
//   - hit: 是否命中
//
// 注意：
//   - 查询失败按未命中处理（fail-closed 到「照常问人」）——多问一次是噪音，
//     错误地复用是安全事故，两个方向的代价不对称
//   - 只复用 allow、只在同任务内复用：见 spec §3.3/§3.4
func (a *Authority) FindReuse(taskID, ticketID string, ev executor.AdapterEvent) (prior *proto.Ticket, fp string, hit bool) {
	fp = executor.PermFingerprint(ev)
	prior, err := a.hooks.Store.FindReusableGrant(taskID, fp)
	if err != nil {
		a.logger().Warn("查询可复用裁决失败，照常升级人工", "task", taskID,
			"ticket", ticketID, "fingerprint", fp[:8], "cause", err)
		return nil, fp, false
	}
	if prior == nil {
		a.logger().Debug("无可复用裁决，升级人工", "task", taskID,
			"ticket", ticketID, "fingerprint", fp[:8])
		return nil, fp, false
	}
	a.logger().Info("命中既有人工批准，自动放行不再叫醒协调者", "task", taskID,
		"ticket", ticketID, "prior_ticket", prior.ID, "fingerprint", fp[:8],
		"perm_chars", len([]rune(ev.Text)))
	return prior, fp, true
}

// AutoAllow 按 AutoAllow 出口处置一次权限请求。
// （实现体迁自 Manager.autoAllowPermission，B233.21。）流程：先审计、再恰好
// 一次原生回传 once。
//
// 三不（不建工单、不发事件、不改状态）由本方法不调用任何账本写入结构性保证；
// 审计经 AuditAutoAllow（与 OpenCode 面 Hooks.AutoAllow 同一实现；冻结 #14：
// 只有 handlePermission 的 AutoAllow 分支到达这里），回传经 DeliverAutoAllow。
//
// 注意：没有工单可失败，因此回传失败不产 delivery_failed 事件；最常见的
// 失败成因是订阅重放（同一权限请求被再次投递，而 executor 侧那次请求早已
// 应答完毕），由回调按既定级别记录。解析执行者失败意味着任务的运行态已没，
// executor 侧那次请求将无人应答——同样无工单可失败。两类失败的日志都在
// DeliverAutoAllow 回调侧（编排域第一现场）。
func (a *Authority) AutoAllow(taskID string, ev executor.AdapterEvent, verdict permgate.Verdict) {
	a.hooks.AuditAutoAllow(taskID, ev, verdict)
	a.hooks.DeliverAutoAllow(taskID, ev.PermissionID)
}
