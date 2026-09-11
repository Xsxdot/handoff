// Package approval owns the production ApprovalClient implementation.
//
// The client is task-scoped and reaches orchestration capabilities only
// through Hooks. Durable tickets and events remain authoritative in Store;
// AnswerHub is only the in-memory wake-up path.
package approval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// AnswerHub is the narrow runtime event seam used by ApprovalClient.
type AnswerHub interface {
	Publish(proto.Event)
	WaitAnswer(context.Context, string) (string, error)
}

// ConsultDecision is the agentd-independent result of an approver call.
//
// Err is retained as the original approver failure. An error or a non-approval
// is always fail-closed by Client.
type ConsultDecision struct {
	Approve   bool
	Reason    string
	ElapsedMS int64
	Err       error
}

// Hooks supplies the orchestration capabilities required by Client.
//
// Store owns durable ticket/event state; Hub only wakes an active waiter.
// Missing decision or persistence hooks are fail-closed and never allow.
type Hooks struct {
	Log                 *slog.Logger
	Store               *store.Store
	Hub                 AnswerHub
	JudgePermission     func(taskID string, ev executor.AdapterEvent) permgate.Verdict
	ShouldConsult       func(taskID string) bool
	Decide              func(context.Context, string, string) ConsultDecision
	CountConsultFailure func(taskID string)
	ResetConsultFailure func(taskID string)
	AutoAllow           func(taskID string, ev executor.AdapterEvent, verdict permgate.Verdict)
	TransitBestEffort   func(taskID string, to proto.TaskState, reason string)
	NoteDeliveryFailed  func(taskID, ticketID string, cause error)
}

// Client is the production ApprovalClient implementation for one task.
//
// It never imports or constructs an executor session. Approval requests are
// represented as Store tickets/events and may be woken through AnswerHub.
type Client struct {
	taskID string
	snap   executor.PolicySnapshot
	hooks  Hooks
}

// NewClient constructs an ApprovalClient for one task and policy snapshot.
//
// If snap.TaskID is empty it is filled from taskID. A non-empty snapshot
// identity is preserved so callers cannot silently rebind a snapshot.
func NewClient(taskID string, snap executor.PolicySnapshot, hooks Hooks) *Client {
	if snap.TaskID == "" {
		snap.TaskID = taskID
	}
	return &Client{taskID: taskID, snap: snap, hooks: hooks}
}

var _ executor.ApprovalClient = (*Client)(nil)

// PolicySnapshot returns the immutable policy snapshot bound at construction.
func (c *Client) PolicySnapshot(context.Context) (executor.PolicySnapshot, error) {
	if c == nil {
		return executor.PolicySnapshot{}, errors.New("approval client is nil")
	}
	c.logger().Info("审批 PolicySnapshot 成功", "task", c.taskID, "version", c.snap.Version)
	return c.snap, nil
}

// Request evaluates one native permission request.
//
// AutoAllow is the only immediate allow path. Other requests are reused only
// from a delivered same-task/same-version grant, consulted when configured,
// or escalated to a durable pending gate.
func (c *Client) Request(ctx context.Context, req executor.ApprovalRequest) (executor.ApprovalResult, error) {
	if c == nil {
		return executor.ApprovalResult{}, errors.New("approval client is nil")
	}
	log := c.logger()
	log.Info("审批 Request 进入", "task", c.taskID, "native", req.NativeID,
		"version", c.snap.Version, "truncated", req.Truncated)
	if req.NativeID == "" {
		err := errors.New("审批请求缺 NativeID")
		log.Error("审批 Request 拒绝", "task", c.taskID, "cause", err)
		return executor.ApprovalResult{}, err
	}
	ev := executor.AdapterEvent{PermissionID: req.NativeID, Text: req.Text, Perm: req.Perm}
	if req.Truncated || strings.Contains(req.Text, executor.TruncationMarker) {
		log.Warn("审批描述已截断，升级人工", "task", c.taskID, "native", req.NativeID)
		return c.escalate(ctx, ev, "描述截断，fail-closed")
	}
	if c.hooks.JudgePermission == nil {
		err := errors.New("权限判据不可用")
		log.Error("审批判据缺失，升级人工失败闭合", "task", c.taskID, "native", req.NativeID, "cause", err)
		return c.escalate(ctx, ev, err.Error())
	}
	verdict := c.hooks.JudgePermission(c.taskID, ev)
	log.Info("审批判据完成", "task", c.taskID, "native", req.NativeID,
		"action", verdict.Action.String(), "rule", verdict.Rule, "reason", verdict.Reason)
	if verdict.Action == permgate.AutoAllow {
		if c.hooks.AutoAllow == nil {
			err := errors.New("自动放行审计能力不可用")
			log.Error("自动放行缺少审计钩子，拒绝放行", "task", c.taskID, "native", req.NativeID, "cause", err)
			return c.escalate(ctx, ev, err.Error())
		}
		// 钩子只写审计与计数，不得做原生投递；OpenCode 的 RespondPermission
		// 只由 adapter 的 authorizeNativePermission 一次完成（冻结 #3/#16）。
		ref := executor.ApprovalRef{ID: executor.NamespacedTicketID(c.taskID, req.NativeID)}
		c.hooks.AutoAllow(c.taskID, ev, verdict)
		log.Info("审批自动放行成功", "task", c.taskID, "native", req.NativeID, "ref", ref.ID)
		return executor.ApprovalResult{Ref: ref, Decision: executor.ApprovalDecision{
			Status: executor.ApprovalAllow, Rule: verdict.Rule, Reason: verdict.Reason,
		}}, nil
	}

	ticketID := executor.NamespacedTicketID(c.taskID, req.NativeID)
	fp := executor.ReuseFingerprint(c.snap.Version, executor.PermFingerprint(ev))
	if res, ok := c.tryReuse(ev, ticketID, fp); ok {
		log.Info("审批复用已送达授权", "task", c.taskID, "native", req.NativeID, "ref", ticketID)
		return res, nil
	}

	if verdict.Action == permgate.Consult && c.hooks.ShouldConsult != nil && c.hooks.ShouldConsult(c.taskID) {
		log.Info("审批进入审批者", "task", c.taskID, "native", req.NativeID)
		return c.consult(ctx, ev)
	}
	if verdict.Action == permgate.Consult && c.hooks.ShouldConsult == nil {
		log.Error("审批者开关钩子缺失，升级人工", "task", c.taskID, "native", req.NativeID)
	}
	return c.escalate(ctx, ev, verdict.Reason)
}

// Await waits for a gate answer, preferring durable state before and after the
// in-memory wait. This second Store read closes the answer-vs-cancellation
// window where the answer is committed just as the waiter is cancelled.
func (c *Client) Await(ctx context.Context, ref executor.ApprovalRef) (executor.ApprovalDecision, error) {
	if c == nil {
		return executor.ApprovalDecision{}, errors.New("approval client is nil")
	}
	log := c.logger()
	log.Info("审批 Await 进入", "task", c.taskID, "ref", ref.ID)
	if d, ok, err := c.decisionFromStore(ref.ID); err != nil {
		log.Error("Await 初始读取 Store 失败", "task", c.taskID, "ref", ref.ID, "cause", err)
		return executor.ApprovalDecision{}, err
	} else if ok {
		log.Info("Await 从 Store 返回终态", "task", c.taskID, "ref", ref.ID, "status", d.Status)
		return d, nil
	}
	if c.hooks.Hub == nil {
		err := errors.New("审批应答 Hub 不可用")
		log.Error("Await 无法等待应答", "task", c.taskID, "ref", ref.ID, "cause", err)
		return executor.ApprovalDecision{}, err
	}
	log.Info("Await 调用 Hub.WaitAnswer", "task", c.taskID, "ref", ref.ID)
	ans, err := c.hooks.Hub.WaitAnswer(ctx, ref.ID)
	if err != nil {
		if d, ok, serr := c.decisionFromStore(ref.ID); serr != nil {
			log.Error("Await 取消后重读 Store 失败", "task", c.taskID, "ref", ref.ID, "cause", serr)
			return executor.ApprovalDecision{}, serr
		} else if ok {
			log.Info("Await 取消后从 Store 取到终态", "task", c.taskID, "ref", ref.ID, "status", d.Status)
			return d, nil
		}
		log.Error("Await 取消且 Store 无终态", "task", c.taskID, "ref", ref.ID, "cause", err)
		return executor.ApprovalDecision{}, err
	}
	d := mapGateAnswer(ans)
	log.Info("Await 从 Hub 返回终态", "task", c.taskID, "ref", ref.ID, "status", d.Status)
	return d, nil
}

// Acknowledge records only the durable delivered stage. Formed and Executed
// are runtime observations and must not alter the delivery timestamp.
func (c *Client) Acknowledge(ctx context.Context, ack executor.ApprovalAck) error {
	_ = ctx
	if c == nil {
		return errors.New("approval client is nil")
	}
	log := c.logger()
	log.Info("审批 Acknowledge 进入", "task", c.taskID, "ref", ack.Ref.ID,
		"stage", ack.Stage, "native", ack.NativeID)
	switch ack.Stage {
	case executor.AckFormed:
		log.Info("审批决定已形成", "task", c.taskID, "ref", ack.Ref.ID)
		return nil
	case executor.AckDelivered:
		if c.hooks.Store == nil {
			err := errors.New("审批 Store 不可用")
			log.Error("Ack delivered 失败", "task", c.taskID, "ref", ack.Ref.ID, "cause", err)
			return err
		}
		if err := c.hooks.Store.MarkTicketDelivered(ack.Ref.ID); err != nil {
			log.Error("Ack delivered 失败", "task", c.taskID, "ref", ack.Ref.ID, "cause", err)
			return err
		}
		log.Info("审批决定已送达", "task", c.taskID, "ref", ack.Ref.ID)
		return nil
	case executor.AckExecuted:
		log.Info("审批动作已执行", "task", c.taskID, "ref", ack.Ref.ID)
		return nil
	default:
		err := fmt.Errorf("未知 AckStage %q", ack.Stage)
		log.Error("Ack 阶段非法", "task", c.taskID, "ref", ack.Ref.ID,
			"stage", ack.Stage, "cause", err)
		return err
	}
}

// NoteDeliveryFailed reports a native permission delivery failure to the
// orchestration hook without adding a method to executor.ApprovalClient.
//
// Cross-task reports are rejected and never write a delivery_failed event.
func (c *Client) NoteDeliveryFailed(taskID, ticketID string, cause error) {
	if c == nil {
		slog.Default().Error("记录权限投递失败时 client 为空", "task", taskID, "ticket", ticketID, "cause", cause)
		return
	}
	log := c.logger()
	if taskID != c.taskID {
		log.Error("拒绝记录跨任务权限投递失败", "task", taskID,
			"bound_task", c.taskID, "ticket", ticketID, "cause", cause)
		return
	}
	if c.hooks.NoteDeliveryFailed == nil {
		log.Error("权限投递失败回调不可用", "task", taskID, "ticket", ticketID, "cause", cause)
		return
	}
	log.Info("记录权限投递失败", "task", taskID, "ticket", ticketID, "cause", cause)
	c.hooks.NoteDeliveryFailed(taskID, ticketID, cause)
	log.Info("权限投递失败已转发", "task", taskID, "ticket", ticketID)
}

func (c *Client) tryReuse(ev executor.AdapterEvent, ticketID, fp string) (executor.ApprovalResult, bool) {
	if c.hooks.Store == nil {
		c.logger().Error("复用查询跳过，Store 不可用", "task", c.taskID, "ticket", ticketID)
		return executor.ApprovalResult{}, false
	}
	prior, err := c.hooks.Store.FindReusableGrant(c.taskID, fp)
	if err != nil {
		c.logger().Warn("复用查询失败，升级人工", "task", c.taskID, "cause", err)
		return executor.ApprovalResult{}, false
	}
	if prior == nil {
		return executor.ApprovalResult{}, false
	}
	if _, err := c.hooks.Store.AppendEvent(c.taskID, proto.EventTypePermissionReuse, permissionReusePayload{
		TicketID:      ticketID,
		PriorTicketID: prior.ID,
		Fingerprint:   fp[:8],
		Permission:    permEventText(ev.Text),
	}); err != nil {
		c.logger().Error("追加 permission_reuse 失败", "task", c.taskID, "ticket", ticketID, "cause", err)
	}
	return executor.ApprovalResult{
		Ref:      executor.ApprovalRef{ID: ticketID},
		Decision: executor.ApprovalDecision{Status: executor.ApprovalAllow, Rule: "reuse", Reason: "复用工单 " + prior.ID},
	}, true
}

func (c *Client) consult(ctx context.Context, ev executor.AdapterEvent) (executor.ApprovalResult, error) {
	if c.hooks.Store == nil {
		err := errors.New("审批 Store 不可用")
		c.logger().Error("审批者路径无法持久化，升级人工", "task", c.taskID, "cause", err)
		return c.escalate(ctx, ev, err.Error())
	}
	if c.hooks.Decide == nil {
		err := errors.New("审批者决策钩子不可用")
		c.logger().Error("审批者决策钩子缺失，升级人工", "task", c.taskID, "cause", err)
		return c.escalate(ctx, ev, err.Error())
	}
	ticketID := executor.NamespacedTicketID(c.taskID, ev.PermissionID)
	fp := executor.ReuseFingerprint(c.snap.Version, executor.PermFingerprint(ev))

	summary := ""
	if task, err := c.hooks.Store.GetTask(c.taskID); err == nil {
		summary = task.PlanSummary
	} else {
		c.logger().Warn("读取任务摘要失败，审批者继续使用空摘要", "task", c.taskID, "cause", err)
	}
	c.logger().Info("调用审批者", "task", c.taskID, "ticket", ticketID)
	dec := c.hooks.Decide(ctx, ev.Text, summary)
	c.logger().Info("审批者返回", "task", c.taskID, "ticket", ticketID,
		"approve", dec.Approve, "elapsed_ms", dec.ElapsedMS, "error", dec.Err)

	decision := "error"
	switch {
	case dec.Approve:
		decision = "approve"
	case dec.Err == nil:
		decision = "escalate"
	}
	reason := dec.Reason
	if reason == "" && dec.Err != nil {
		reason = dec.Err.Error()
	}
	if _, err := c.hooks.Store.AppendEvent(c.taskID, proto.EventTypeApproverDecision, approverDecisionPayload{
		TicketID:   ticketID,
		Permission: permEventText(ev.Text),
		Decision:   decision,
		Reason:     reason,
		ElapsedMS:  dec.ElapsedMS,
	}); err != nil {
		c.logger().Error("追加 approver_decision 事件失败", "task", c.taskID, "ticket", ticketID, "cause", err)
	}

	if dec.Err != nil {
		if c.hooks.CountConsultFailure != nil {
			c.hooks.CountConsultFailure(c.taskID)
		}
		return c.escalate(ctx, ev, "审批者调用失败")
	}
	if !dec.Approve {
		if c.hooks.ResetConsultFailure != nil {
			c.hooks.ResetConsultFailure(c.taskID)
		}
		return c.escalate(ctx, ev, "审批模型未形成允许")
	}

	if c.hooks.ResetConsultFailure != nil {
		c.hooks.ResetConsultFailure(c.taskID)
	}

	reqJSON, err := jsonMarshal(ticketRequest{Kind: "gate", Permission: ev.Text})
	if err != nil {
		c.logger().Error("审批者批准：编码工单失败", "task", c.taskID, "ticket", ticketID, "cause", err)
		return c.escalate(ctx, ev, "审批者工单编码失败")
	}
	if _, err := c.hooks.Store.CreateTicket(&proto.Ticket{
		ID:          ticketID,
		TaskID:      c.taskID,
		Kind:        "gate",
		Request:     reqJSON,
		CreatedAt:   time.Now().UTC(),
		Fingerprint: fp,
	}); err != nil {
		c.logger().Error("审批者批准：创建工单失败", "task", c.taskID, "ticket", ticketID, "cause", err)
		if c.hooks.CountConsultFailure != nil {
			c.hooks.CountConsultFailure(c.taskID)
		}
		return c.escalate(ctx, ev, "审批者工单创建失败")
	}
	if err := c.hooks.Store.AnswerTicket(ticketID, "allow"); err != nil {
		c.logger().Error("审批者批准：应答工单失败", "task", c.taskID, "ticket", ticketID, "cause", err)
		if c.hooks.CountConsultFailure != nil {
			c.hooks.CountConsultFailure(c.taskID)
		}
		return c.escalate(ctx, ev, "审批者工单应答失败")
	}
	if _, err := c.hooks.Store.AppendEvent(c.taskID, proto.EventTypeTicketAnswered, ticketAnsweredPayload{
		TicketID: ticketID,
		Answer:   "allow",
	}); err != nil {
		c.logger().Warn("审批者批准：追加工单答复事件失败", "task", c.taskID, "ticket", ticketID, "cause", err)
	}
	// 送达时间戳归 Acknowledge(AckDelivered)（B233.7 第 58 条 / 冻结 #5、#7）；
	// 本函数只负责形成并持久化决定，绝不在此写 delivered_at。
	ref := executor.ApprovalRef{ID: ticketID}
	c.logger().Info("审批者批准决定已落库，尚未送达", "task", c.taskID, "ticket", ticketID)
	return executor.ApprovalResult{Ref: ref, Decision: executor.ApprovalDecision{
		Status: executor.ApprovalAllow, Rule: "approver", Reason: dec.Reason,
	}}, nil
}

func (c *Client) escalate(ctx context.Context, ev executor.AdapterEvent, reason string) (executor.ApprovalResult, error) {
	ticketID := executor.NamespacedTicketID(c.taskID, ev.PermissionID)
	fp := executor.ReuseFingerprint(c.snap.Version, executor.PermFingerprint(ev))
	if c.hooks.TransitBestEffort != nil {
		c.hooks.TransitBestEffort(c.taskID, proto.TaskStateWaitingAnswer, "permission_request")
	} else {
		c.logger().Error("升级人工缺少状态迁移钩子", "task", c.taskID, "ticket", ticketID)
	}
	if c.hooks.Store == nil {
		err := errors.New("审批 Store 不可用")
		c.logger().Error("创建权限工单失败", "task", c.taskID, "ticket", ticketID, "cause", err)
		return executor.ApprovalResult{}, err
	}
	reqJSON, err := jsonMarshal(ticketRequest{Kind: "gate", Permission: ev.Text})
	if err != nil {
		c.logger().Error("编码权限工单失败", "task", c.taskID, "ticket", ticketID, "cause", err)
		return executor.ApprovalResult{}, err
	}
	if _, err := c.hooks.Store.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: c.taskID, Kind: "gate",
		Request: reqJSON, CreatedAt: time.Now().UTC(), Fingerprint: fp,
	}); err != nil {
		if c.hooks.TransitBestEffort != nil {
			c.hooks.TransitBestEffort(c.taskID, proto.TaskStateRunning, "权限工单创建失败回滚")
		}
		c.logger().Error("创建权限工单失败", "task", c.taskID, "ticket", ticketID, "cause", err)
		return executor.ApprovalResult{}, err
	}
	evt, err := c.hooks.Store.AppendEvent(c.taskID, proto.EventTypePermissionRequest, permissionPayload{
		TicketID: ticketID, Permission: permEventText(ev.Text), Kind: "gate",
	})
	if err != nil {
		c.logger().Error("追加 permission_request 失败", "task", c.taskID, "ticket", ticketID, "cause", err)
		return executor.ApprovalResult{Ref: executor.ApprovalRef{ID: ticketID}, Decision: executor.ApprovalDecision{
			Status: executor.ApprovalPending, Reason: reason,
		}}, nil
	}
	if c.hooks.Hub == nil {
		c.logger().Error("权限工单已落库但 Hub 不可用", "task", c.taskID, "ticket", ticketID)
		return executor.ApprovalResult{Ref: executor.ApprovalRef{ID: ticketID}, Decision: executor.ApprovalDecision{
			Status: executor.ApprovalPending, Reason: reason,
		}}, nil
	}
	c.hooks.Hub.Publish(evt)
	c.logger().Info("权限工单已发布", "task", c.taskID, "ticket", ticketID)
	return executor.ApprovalResult{
		Ref:      executor.ApprovalRef{ID: ticketID},
		Decision: executor.ApprovalDecision{Status: executor.ApprovalPending, Reason: reason},
	}, nil
}

func (c *Client) decisionFromStore(ticketID string) (executor.ApprovalDecision, bool, error) {
	if c.hooks.Store == nil {
		return executor.ApprovalDecision{}, false, errors.New("审批 Store 不可用")
	}
	tk, err := c.hooks.Store.GetTicket(ticketID)
	if errors.Is(err, store.ErrNotFound) {
		return executor.ApprovalDecision{}, false, nil
	}
	if err != nil {
		return executor.ApprovalDecision{}, false, err
	}
	if tk.Answer == nil || *tk.Answer == store.VoidAnswer {
		return executor.ApprovalDecision{}, false, nil
	}
	return mapGateAnswer(*tk.Answer), true, nil
}

func mapGateAnswer(ans string) executor.ApprovalDecision {
	d, reason := gateDecision(ans)
	if d == "once" {
		return executor.ApprovalDecision{Status: executor.ApprovalAllow}
	}
	return executor.ApprovalDecision{Status: executor.ApprovalDeny, Reason: reason}
}

// gateDecision preserves the old manager-to-executor translation: only the
// exact answer "allow" grants once; all other answers reject, with a deny
// reason copied only from the explicit deny form.
func gateDecision(answer string) (decision, reason string) {
	trimmed := strings.TrimSpace(answer)
	if trimmed == "allow" {
		return "once", ""
	}
	rest := strings.TrimPrefix(trimmed, "deny")
	rest = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(rest), ":"))
	if rest == trimmed {
		return "reject", ""
	}
	return "reject", rest
}

// The payloads stay private to this package because they are the hand-written
// JSON projection of the existing wire shapes. Keeping them here avoids an
// agentd import while preserving manager's question/ticket payloads.
type ticketRequest struct {
	Kind       string `json:"kind"`
	Permission string `json:"permission,omitempty"`
}

type permissionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Kind       string `json:"kind"`
}

type permissionReusePayload struct {
	TicketID      string `json:"ticket_id"`
	PriorTicketID string `json:"prior_ticket_id"`
	Fingerprint   string `json:"fingerprint"`
	Permission    string `json:"permission"`
}

type approverDecisionPayload struct {
	TicketID   string `json:"ticket_id"`
	Permission string `json:"permission"`
	Decision   string `json:"decision"`
	Reason     string `json:"reason"`
	ElapsedMS  int64  `json:"elapsed_ms"`
}

type ticketAnsweredPayload struct {
	TicketID string `json:"ticket_id"`
	Answer   string `json:"answer"`
}

func permEventText(s string) string {
	const limit = 200
	if len([]rune(s)) <= limit {
		return s
	}
	r := []rune(s)
	return string(r[:limit]) + executor.TruncationMarker
}

func jsonMarshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

func (c *Client) logger() *slog.Logger {
	if c != nil && c.hooks.Log != nil {
		return c.hooks.Log
	}
	return slog.Default()
}
