// 本文件实现 handoff 的状态机中枢与 adapter 事件中介（系统的心脏）。
//
// 职责：
//   - 任务生命周期的唯一状态写入者：dispatch（pending→running）、permission/question
//     中介（→waiting_answer→running）、result（→waiting_review）、continue/done（→running/completed）
//   - 把 executor 的 AdapterEvent 中介为「ticket + 事件 + 状态迁移」三件套：
//     permission/question 落 ticket 并挂到 hub.WaitAnswer 等审核者应答，
//     progress 只入库，result 落 completed/failed 事件进 waiting_review
//   - 审核者应答经 reply 回程（server）→ NotifyAnswer 唤醒等待 goroutine → 回传 executor
//
// 边界：
//   - 不做审批判断：「allow 之外一律 reject」「回答原样透传」是仅有的两条翻译规则，
//     批不批、答什么由审核者（人/上层）决定
//   - 不直接接触 executor 进程/会话细节，一切经由 executor.Adapter 契约
//   - 不负责 git 工作区（Task 9）、看门狗（Task 12）等横向能力
//
// 「失败也进 waiting_review」的 why：
//
//	失败的 result 同样进 waiting_review 而非自动重试或直接 failed——让审核者看到
//	失败现场（failed 事件携带原因）决定重试话术；自动重试会在同一坑里反复烧
//	token 且无人知情，failed 终态又让任务失去续接入口。人工裁决是唯一解。
//
// 状态迁移并发安全：
//   - 全部迁移经 store.UpdateTaskState 的 CAS（WHERE state=旧值）落库，双写者
//     只有一个赢家；reply 回程的 resumeIfIdle 与应答 goroutine 的回迁 race 由
//     CAS + transitBestEffort（容忍 ErrBadTransit）吸收
package agentd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// eventType 常量：AdapterEvent.Type 的合法取值（与 proto.EventType 的映射见各 handler）。
const (
	adapterEventPermission = "permission"
	adapterEventQuestion   = "question"
	adapterEventProgress   = "progress"
	adapterEventResult     = "result"
)

// Manager 是任务状态机中枢与 adapter 事件中介。
//
// 并发安全：无共享可变字段（st/hub/ad/cfg/log 构造后只读），
// 每个任务的中介 goroutine 与应答 goroutine 通过 store CAS + hub 路由协作。
type Manager struct {
	st  *store.Store
	hub *Hub
	ad  executor.Adapter
	cfg *config.Config
	log *slog.Logger
}

// NewManager 创建任务管理器。
//
// 参数：
//   - st: 持久化存储（任务/事件/工单的唯一落库点）
//   - hub: 进程内实时路由（事件广播 + ticket 应答等待）
//   - ad: executor 挂载实现（opencode / fake）
//   - cfg: 配置（DataDir 用于派生任务目录）
//   - log: 本模块日志入口
//
// 注意：
//   - 调用方须保证 log 为统一配置后的 logger；st/hub 必须已就绪
func NewManager(st *store.Store, hub *Hub, ad executor.Adapter, cfg *config.Config, log *slog.Logger) *Manager {
	return &Manager{st: st, hub: hub, ad: ad, cfg: cfg, log: log}
}

// DispatchReq 是 Dispatch 的入参：任务仓库与 base64 编码的计划内容。
type DispatchReq struct {
	Repo     string // 任务仓库路径（executor 工作区）
	PlanB64  string // plan 内容，base64 编码（路由/CLI 层编码，此处解码）
	PlanName string // plan 文件名（归档展示用，写入 task 的 PlanPath 目录下）
	Target   string // 目标主机名（归档展示用，记入 task.Target）
}

// ticketRequest 是工单 request 列的通用载体，kind 区分 gate/ask。
type ticketRequest struct {
	Kind       string `json:"kind"`
	Permission string `json:"permission,omitempty"` // gate：权限描述
	Question   string `json:"question,omitempty"`   // ask：问题原文
}

// permissionPayload 是 permission_request 事件的 payload（含 ticket_id 供回复）。
type permissionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Kind       string `json:"kind"`
}

// questionPayload 是 question 事件的 payload（含 ticket_id 供回复）。
type questionPayload struct {
	TicketID string `json:"ticket_id"`
	Question string `json:"question"`
	Kind     string `json:"kind"`
}

// progressPayload 是 progress 事件的 payload。
type progressPayload struct {
	Text string `json:"text"`
}

// completedPayload 是 completed 事件的 payload。
type completedPayload struct {
	Branch     string `json:"branch"`
	CommitHash string `json:"commit"`
	Summary    string `json:"summary"`
}

// failedPayload 是 failed 事件的 payload。
type failedPayload struct {
	FailReason string `json:"fail_reason"`
}

// Dispatch 派发一个新任务：建任务 → 建 taskDir 写 plan → Adapter.Start → running →
// 启动中介 goroutine 消费事件流。
//
// 参数：
//   - req: 仓库路径与 base64 计划（字段说明见 DispatchReq）
//
// 返回：
//   - 已入库的任务（state 为 running）；Adapter.Start 失败时返回错误，
//     此时任务已落库并迁移为 failed（供审核者经 tasks 命令查看失败现场）
func (m *Manager) Dispatch(ctx context.Context, req DispatchReq) (task *proto.Task, err error) {
	m.log.Info("dispatch 进入", "repo", req.Repo, "plan_name", req.PlanName, "target", req.Target)
	defer func() {
		if err != nil {
			m.log.Error("dispatch 失败", "repo", req.Repo, "cause", err)
		} else {
			m.log.Info("dispatch 完成", "task", task.ID)
		}
	}()

	if req.Repo == "" || req.PlanB64 == "" {
		return nil, fmt.Errorf("dispatch 参数不完整: repo=%q plan_b64 长度=%d", req.Repo, len(req.PlanB64))
	}
	planContent, err := base64.StdEncoding.DecodeString(req.PlanB64)
	if err != nil {
		return nil, fmt.Errorf("解码 plan_b64: %w", err)
	}

	now := time.Now().UTC()
	taskID := uuid.NewString()

	// taskDir 是任务专属工作目录（计划文件与 executor 侧任务物料都放这里）
	taskDir := filepath.Join(m.cfg.DataDir, "tasks", taskID)
	if err := os.MkdirAll(taskDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建任务目录 %s: %w", taskDir, err)
	}
	planPath := filepath.Join(taskDir, req.PlanName)
	if err := os.WriteFile(planPath, planContent, 0o600); err != nil {
		return nil, fmt.Errorf("写计划文件 %s: %w", planPath, err)
	}

	task = &proto.Task{
		ID:       taskID,
		Target:   req.Target,
		RepoPath: req.Repo,
		// PlanPath 不在 SetTaskField 白名单，只能在创建时一并写入
		PlanPath:  planPath,
		State:     proto.TaskStatePending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := m.st.CreateTask(task); err != nil {
		return nil, err
	}

	if err := m.ad.Start(ctx, executor.StartReq{Task: *task, PlanContent: string(planContent), TaskDir: taskDir}); err != nil {
		m.log.Error("adapter 启动失败", "task", taskID, "cause", err)
		// pending→failed 合法；失败现场留在任务里，审核者可见
		m.transitBestEffort(taskID, proto.TaskStateFailed, "adapter start 失败")
		return nil, fmt.Errorf("启动 executor: %w", err)
	}
	if err := m.transit(taskID, proto.TaskStateRunning, "dispatch"); err != nil {
		return nil, err
	}
	// 返回最新快照：transit 已把状态改为 running（含 updated_at 刷新），
	// 不能让调用方拿到创建时的 pending 旧值
	task, err = m.st.GetTask(taskID)
	if err != nil {
		return nil, fmt.Errorf("读取派发后的任务: %w", err)
	}
	go m.mediate(taskID)
	return task, nil
}

// Continue 向任务续发修改指令：要求任务处于 waiting_review，先回迁 running 再
// 经 Adapter.Send 原样透传指令（同一会话续接，上下文完整保留）。
//
// 返回：
//   - 任务不存在返回 store.ErrNotFound；状态不允许续接返回 store.ErrBadTransit
//   - Send 失败时任务回迁 waiting_review（审核者可重试指令），并返回该错误
func (m *Manager) Continue(ctx context.Context, taskID, instructions string) (err error) {
	m.log.Info("continue 进入", "task", taskID)
	defer func() {
		if err != nil {
			m.log.Error("continue 失败", "task", taskID, "cause", err)
		} else {
			m.log.Info("continue 完成", "task", taskID)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State != proto.TaskStateWaitingReview {
		m.log.Warn("continue 状态不允许", "task", taskID, "state", cur.State)
		return fmt.Errorf("任务 %s 状态 %s 不允许续接（需 waiting_review）: %w", taskID, cur.State, store.ErrBadTransit)
	}
	if err := m.transit(taskID, proto.TaskStateRunning, "continue"); err != nil {
		return err
	}
	if err := m.ad.Send(ctx, taskID, instructions); err != nil {
		m.log.Error("续发指令失败", "task", taskID, "cause", err)
		// 回迁 waiting_review：指令没送达，回到审核者可重试的位置，不让任务死在 running
		m.transitBestEffort(taskID, proto.TaskStateWaitingReview, "continue 发送失败回迁")
		return fmt.Errorf("续发指令: %w", err)
	}
	return nil
}

// Done 归档任务：要求任务处于 waiting_review，迁移 completed 后调用 Adapter.Stop
// 回收 executor 侧资源。
//
// 返回：
//   - 任务不存在返回 store.ErrNotFound；状态不允许归档返回 store.ErrBadTransit
//
// 注意：
//   - Stop 失败仅打 Error 日志不影响归档：任务已完成，executor 残留交给人工兜底
func (m *Manager) Done(ctx context.Context, taskID string) (err error) {
	m.log.Info("done 进入", "task", taskID)
	defer func() {
		if err != nil {
			m.log.Error("done 失败", "task", taskID, "cause", err)
		} else {
			m.log.Info("done 完成", "task", taskID)
		}
	}()

	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State != proto.TaskStateWaitingReview {
		m.log.Warn("done 状态不允许", "task", taskID, "state", cur.State)
		return fmt.Errorf("任务 %s 状态 %s 不允许归档（需 waiting_review）: %w", taskID, cur.State, store.ErrBadTransit)
	}
	if err := m.transit(taskID, proto.TaskStateCompleted, "done"); err != nil {
		return err
	}
	if err := m.ad.Stop(taskID); err != nil {
		m.log.Error("停止 executor 失败", "task", taskID, "cause", err)
	}
	return nil
}

// mediate 是单任务的事件中介循环（每任务一个 goroutine，由 Dispatch 启动）：
// 消费 Adapter.Events 直到通道关闭（Stop），每类事件交给对应 handler。
func (m *Manager) mediate(taskID string) {
	m.log.Info("中介循环启动", "task", taskID)
	events := m.ad.Events(taskID)
	for ev := range events {
		m.handleEvent(taskID, ev)
	}
	m.log.Info("中介循环结束", "task", taskID)
}

// handleEvent 按事件类型分发中介处理，并打每类事件的入口日志。
func (m *Manager) handleEvent(taskID string, ev executor.AdapterEvent) {
	switch ev.Type {
	case adapterEventPermission:
		m.log.Info("权限请求事件", "task", taskID, "perm", ev.PermissionID,
			"text", truncateRunes(ev.Text, 80))
		m.handlePermission(taskID, ev)
	case adapterEventQuestion:
		m.log.Info("提问事件", "task", taskID, "text", truncateRunes(ev.Text, 80))
		m.handleQuestion(taskID, ev)
	case adapterEventProgress:
		m.log.Info("进度事件", "task", taskID, "text", truncateRunes(ev.Text, 80))
		m.handleProgress(taskID, ev)
	case adapterEventResult:
		if ev.Result != nil {
			m.log.Info("执行结果事件", "task", taskID, "ok", ev.Result.OK,
				"branch", ev.Result.Branch, "commit", ev.Result.CommitHash)
		} else {
			m.log.Warn("执行结果事件缺 Result", "task", taskID)
		}
		m.handleResult(taskID, ev)
	default:
		m.log.Warn("未知 adapter 事件", "task", taskID, "type", ev.Type)
	}
}

// handlePermission 中介权限请求：ticket(id=PermissionID, kind=gate) → 事件 →
// waiting_answer → goroutine 等审核者应答后回传 executor。
func (m *Manager) handlePermission(taskID string, ev executor.AdapterEvent) {
	if ev.PermissionID == "" {
		m.log.Error("权限事件缺 PermissionID", "task", taskID)
		return
	}
	// ticket id 复用 PermissionID：SSE 重连重放同一权限请求时 CreateTicket 幂等，
	// 不会产生重复工单（见 executor.go 包级幂等约定）
	req, _ := json.Marshal(ticketRequest{Kind: "gate", Permission: ev.Text})
	if _, err := m.st.CreateTicket(&proto.Ticket{
		ID: ev.PermissionID, TaskID: taskID, Kind: "gate",
		Request: req, CreatedAt: time.Now().UTC(),
	}); err != nil {
		m.log.Error("创建权限工单失败", "task", taskID, "perm", ev.PermissionID, "cause", err)
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypePermissionRequest, permissionPayload{
		TicketID: ev.PermissionID, Permission: ev.Text, Kind: "gate",
	})
	if err != nil {
		m.log.Error("追加 permission_request 事件失败", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
	m.transitBestEffort(taskID, proto.TaskStateWaitingAnswer, "permission_request")
	go m.waitPermission(taskID, ev.PermissionID)
}

// waitPermission 阻塞等待权限工单的审核者应答，按规则回传 executor 并回迁 running。
//
// 规则：应答 trim 后等于 "allow" → RespondPermission("once")，其余一律 "reject"
// （deny:原因 等拒绝表达全部归入 reject，原因已随应答落库可追溯）。
//
// 为什么先回迁 running 再回传 executor：respond 一发出 executor 立刻继续执行并可能
// 立即产出下一个事件（如 result），若回迁在 respond 之后，result 可能先到而状态还
// 卡在 waiting_answer，导致 waiting_answer→waiting_review 非法迁移被拒。先落状态
// 保证「executor 恢复执行时状态必为 running」。
func (m *Manager) waitPermission(taskID, permID string) {
	start := time.Now()
	ans, err := m.hub.WaitAnswer(context.Background(), permID)
	if err != nil {
		m.log.Warn("权限应答等待被取消", "task", taskID, "perm", permID, "cause", err)
		return
	}
	m.log.Info("权限应答已收到", "task", taskID, "perm", permID,
		"wait_ms", time.Since(start).Milliseconds(), "answer", truncateRunes(ans, 80))

	// 回迁失败（reply 回程的 resumeIfIdle 已抢先回迁）由 transitBestEffort 容忍
	m.transitBestEffort(taskID, proto.TaskStateRunning, "permission 已应答")
	decision := "reject"
	if strings.TrimSpace(ans) == "allow" {
		decision = "once"
	}
	if err := m.ad.RespondPermission(context.Background(), taskID, permID, decision); err != nil {
		// executor 侧可能已不在（进程被杀）：记录错误并保持现状，交由审核者裁决
		m.log.Error("回应权限失败", "task", taskID, "perm", permID, "decision", decision, "cause", err)
		return
	}
}

// handleQuestion 中介提问：ticket(uuid, kind=ask) → 事件 → waiting_answer →
// goroutine 等审核者回答后原样透传 executor。
func (m *Manager) handleQuestion(taskID string, ev executor.AdapterEvent) {
	// 提问工单 id 用 uuid：问题没有天然稳定 id，回答一次即终结
	ticketID := uuid.NewString()
	req, _ := json.Marshal(ticketRequest{Kind: "ask", Question: ev.Text})
	if _, err := m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: taskID, Kind: "ask",
		Request: req, CreatedAt: time.Now().UTC(),
	}); err != nil {
		m.log.Error("创建提问工单失败", "task", taskID, "ticket", ticketID, "cause", err)
	}
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeQuestion, questionPayload{
		TicketID: ticketID, Question: ev.Text, Kind: "ask",
	})
	if err != nil {
		m.log.Error("追加 question 事件失败", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
	m.transitBestEffort(taskID, proto.TaskStateWaitingAnswer, "question")
	go m.waitQuestion(taskID, ticketID)
}

// waitQuestion 阻塞等待提问工单的回答，把回答原文原样透传给 executor 并回迁 running。
//
// 为什么原文透传：回答语义（技术建议、需求取舍）只有审核者理解，manager 不做
// 任何加工——无损透传是「回答什么由审核者决定」承诺的执行保证。
//
// 为什么先回迁 running 再 Send：同 waitPermission——Send 一发出 executor 立刻恢复
// 执行并可能立即产出 result，先落状态保证 result 到达时状态必为 running。
func (m *Manager) waitQuestion(taskID, ticketID string) {
	start := time.Now()
	ans, err := m.hub.WaitAnswer(context.Background(), ticketID)
	if err != nil {
		m.log.Warn("提问应答等待被取消", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
	m.log.Info("提问应答已收到", "task", taskID, "ticket", ticketID,
		"wait_ms", time.Since(start).Milliseconds(), "answer", truncateRunes(ans, 80))

	m.transitBestEffort(taskID, proto.TaskStateRunning, "question 已应答")
	if err := m.ad.Send(context.Background(), taskID, ans); err != nil {
		m.log.Error("回发提问回答失败", "task", taskID, "ticket", ticketID, "cause", err)
		return
	}
}

// handleProgress 中介进度事件：只入库广播，不改任务状态。
func (m *Manager) handleProgress(taskID string, ev executor.AdapterEvent) {
	evt, err := m.st.AppendEvent(taskID, proto.EventTypeProgress, progressPayload{Text: ev.Text})
	if err != nil {
		m.log.Error("追加 progress 事件失败", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
}

// handleResult 中介回合结果：OK → completed 事件，!OK → failed 事件；两者都进
// waiting_review（why 见文件头）——失败也交审核者裁决，不自动重试烧 token。
//
// 顺序为什么是「追加事件 → 迁移状态 → 广播」而非广播在前：审核者（或脚本）收到
// completed 事件后可能立即执行 continue/done，若状态尚未回迁 waiting_review 会被
// 409 拒绝——先落状态再唤醒，保证「事件到达时状态已就绪」。迁移失败（如并发
// done 已抢先归档）则不广播，避免唤醒审核者去操作一个已终结的任务。
func (m *Manager) handleResult(taskID string, ev executor.AdapterEvent) {
	if ev.Result == nil {
		m.log.Error("result 事件缺 Result", "task", taskID)
		return
	}
	// 已归档（done 后）的杂散 result 直接丢弃，不重复追加事件
	cur, err := m.st.GetTask(taskID)
	if err != nil {
		m.log.Error("读取任务失败", "task", taskID, "cause", err)
		return
	}
	if cur.State == proto.TaskStateCompleted || cur.State == proto.TaskStateFailed {
		m.log.Debug("任务已终结，忽略 result 事件", "task", taskID, "state", cur.State)
		return
	}

	r := ev.Result
	var evt proto.Event
	if r.OK {
		evt, err = m.st.AppendEvent(taskID, proto.EventTypeCompleted, completedPayload{
			Branch: r.Branch, CommitHash: r.CommitHash, Summary: r.Summary,
		})
	} else {
		evt, err = m.st.AppendEvent(taskID, proto.EventTypeFailed, failedPayload{FailReason: r.FailReason})
	}
	if err != nil {
		m.log.Error("追加 result 事件失败", "task", taskID, "cause", err)
		return
	}
	if err := m.transitToReview(taskID); err != nil {
		m.log.Error("回迁 waiting_review 失败，不广播事件", "task", taskID, "cause", err)
		return
	}
	m.hub.Publish(evt)
}

// transitToReview 把任务迁入 waiting_review；若当前仍卡在 waiting_answer（回答-续跑
// 链路因异常时序尚未回迁 running 的防御场景），先经 running 两跳进入。
//
// 为什么两跳：waiting_answer→waiting_review 不在状态机迁移表里（必须先回到 running），
// 单跳直接会被 ErrBadTransit 拒绝并丢事件；两跳保证结果事件不因时序问题丢失。
func (m *Manager) transitToReview(taskID string) error {
	if err := m.transit(taskID, proto.TaskStateWaitingReview, "result"); err == nil {
		return nil
	}
	cur, gerr := m.st.GetTask(taskID)
	if gerr != nil || cur.State != proto.TaskStateWaitingAnswer {
		return errors.New("任务不在 waiting_answer，无法两跳进入 waiting_review")
	}
	if err := m.transit(taskID, proto.TaskStateRunning, "result 到达前回迁"); err != nil {
		return err
	}
	return m.transit(taskID, proto.TaskStateWaitingReview, "result")
}

// transit 迁移任务状态并记录 from→to 迁移日志；已在目标状态时幂等返回 nil。
func (m *Manager) transit(taskID string, to proto.TaskState, reason string) error {
	cur, err := m.st.GetTask(taskID)
	if err != nil {
		return err
	}
	if cur.State == to {
		return nil
	}
	if err := m.st.UpdateTaskState(taskID, to); err != nil {
		return err
	}
	m.log.Info("任务状态迁移", "task", taskID, "from", cur.State, "to", to, "reason", reason)
	return nil
}

// transitBestEffort 容忍 ErrBadTransit 的迁移。
//
// 为什么容忍：reply 回程的 resumeIfIdle 与应答 goroutine 的回迁并发竞争同一迁移，
// CAS 保证只有一个赢家；输家拿 ErrBadTransit 说明目标状态已达成（resumeIfIdle
// 已抢先回迁 running），无需重试也无需告警。其余错误照实记录。
func (m *Manager) transitBestEffort(taskID string, to proto.TaskState, reason string) {
	if err := m.transit(taskID, to, reason); err != nil && !errors.Is(err, store.ErrBadTransit) {
		m.log.Error("状态迁移失败", "task", taskID, "to", to, "reason", reason, "cause", err)
	}
}
