// approval_client.go —— 编排侧 ApprovalClient 实现（B233.1）。
//
// 职责：限定到单个 Task.ID 的政策快照、判定、升级、复用与 Ack。
// 边界：不 import opencode；Consult 不得出站；失败/超时/截断不得 allow。
package agentd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

type taskApprovalClient struct {
	m      *Manager
	taskID string
	snap   executor.PolicySnapshot
}

func (m *Manager) bindApproval(taskID string, snap executor.PolicySnapshot) executor.ApprovalClient {
	if snap.TaskID == "" {
		snap.TaskID = taskID
	}
	return &taskApprovalClient{m: m, taskID: taskID, snap: snap}
}

func (c *taskApprovalClient) PolicySnapshot(context.Context) (executor.PolicySnapshot, error) {
	return c.snap, nil
}

func (c *taskApprovalClient) Request(ctx context.Context, req executor.ApprovalRequest) (executor.ApprovalResult, error) {
	c.m.log.Info("审批 Request", "task", c.taskID, "native", req.NativeID,
		"version", c.snap.Version, "truncated", req.Truncated)
	if req.NativeID == "" {
		return executor.ApprovalResult{}, fmt.Errorf("审批请求缺 NativeID")
	}
	ev := executor.AdapterEvent{PermissionID: req.NativeID, Text: req.Text, Perm: req.Perm}
	if req.Truncated || strings.Contains(req.Text, executor.TruncationMarker) {
		return c.escalate(ctx, ev, "描述截断，fail-closed")
	}
	verdict := c.m.judgePermission(c.taskID, ev)
	switch verdict.Action {
	case permgate.AutoAllow:
		ref := executor.ApprovalRef{ID: executor.NamespacedTicketID(c.taskID, req.NativeID)}
		c.recordAutoAllow(ev, verdict)
		return executor.ApprovalResult{Ref: ref, Decision: executor.ApprovalDecision{
			Status: executor.ApprovalAllow, Rule: verdict.Rule, Reason: verdict.Reason,
		}}, nil
	case permgate.Consult:
		if c.m.shouldConsultApprover(c.taskID) {
			return c.consult(ctx, ev)
		}
		return c.escalate(ctx, ev, verdict.Reason)
	default:
		return c.escalate(ctx, ev, verdict.Reason)
	}
}

func (c *taskApprovalClient) Await(ctx context.Context, ref executor.ApprovalRef) (executor.ApprovalDecision, error) {
	c.m.log.Info("审批 Await", "task", c.taskID, "ref", ref.ID)
	if d, ok, err := c.decisionFromStore(ref.ID); err != nil {
		return executor.ApprovalDecision{}, err
	} else if ok {
		return d, nil
	}
	ans, err := c.m.hub.WaitAnswer(ctx, ref.ID)
	if err != nil {
		if d, ok, serr := c.decisionFromStore(ref.ID); serr != nil {
			return executor.ApprovalDecision{}, serr
		} else if ok {
			return d, nil
		}
		c.m.log.Error("Await 取消且 store 无终态", "task", c.taskID, "ref", ref.ID, "cause", err)
		return executor.ApprovalDecision{}, err
	}
	return mapGateAnswer(ans), nil
}

func (c *taskApprovalClient) Acknowledge(ctx context.Context, ack executor.ApprovalAck) error {
	c.m.log.Info("审批 Acknowledge", "task", c.taskID, "ref", ack.Ref.ID,
		"stage", ack.Stage, "native", ack.NativeID)
	switch ack.Stage {
	case executor.AckFormed:
		return nil
	case executor.AckDelivered:
		if err := c.m.st.MarkTicketDelivered(ack.Ref.ID); err != nil {
			c.m.log.Error("Ack delivered 失败", "task", c.taskID, "ref", ack.Ref.ID, "cause", err)
			return err
		}
		return nil
	case executor.AckExecuted:
		return nil
	default:
		return fmt.Errorf("未知 AckStage %q", ack.Stage)
	}
}

func (c *taskApprovalClient) consult(ctx context.Context, ev executor.AdapterEvent) (executor.ApprovalResult, error) {
	if c.m.approver == nil {
		return c.escalate(ctx, ev, "审批者不可用")
	}
	dec := c.m.approver.Decide(ctx, ev.Text, "")
	if dec.Err != nil || !dec.Approve {
		c.m.log.Info("审批模型未放行，升级", "task", c.taskID, "perm", ev.PermissionID, "cause", dec.Err)
		return c.escalate(ctx, ev, "审批模型未形成允许")
	}
	ref := executor.ApprovalRef{ID: executor.NamespacedTicketID(c.taskID, ev.PermissionID)}
	return executor.ApprovalResult{Ref: ref, Decision: executor.ApprovalDecision{
		Status: executor.ApprovalAllow, Rule: "approver", Reason: dec.Reason,
	}}, nil
}

func (c *taskApprovalClient) escalate(ctx context.Context, ev executor.AdapterEvent, reason string) (executor.ApprovalResult, error) {
	ticketID := executor.NamespacedTicketID(c.taskID, ev.PermissionID)
	fp := executor.ReuseFingerprint(c.snap.Version, executor.PermFingerprint(ev))
	if prior, err := c.m.st.FindReusableGrant(c.taskID, fp); err != nil {
		c.m.log.Warn("复用查询失败，升级人工", "task", c.taskID, "cause", err)
	} else if prior != nil {
		if _, err := c.m.st.AppendEvent(c.taskID, proto.EventTypePermissionReuse, permissionReusePayload{
			TicketID: ticketID, PriorTicketID: prior.ID,
			Fingerprint: fp[:8], Permission: permEventText(ev.Text),
		}); err != nil {
			c.m.log.Error("追加 permission_reuse 失败", "task", c.taskID, "cause", err)
		}
		return executor.ApprovalResult{
			Ref:      executor.ApprovalRef{ID: ticketID},
			Decision: executor.ApprovalDecision{Status: executor.ApprovalAllow, Rule: "reuse", Reason: "复用工单 " + prior.ID},
		}, nil
	}
	c.m.transitBestEffort(c.taskID, proto.TaskStateWaitingAnswer, "permission_request")
	reqJSON, _ := json.Marshal(ticketRequest{Kind: "gate", Permission: ev.Text})
	if _, err := c.m.st.CreateTicket(&proto.Ticket{
		ID: ticketID, TaskID: c.taskID, Kind: "gate",
		Request: reqJSON, CreatedAt: time.Now().UTC(), Fingerprint: fp,
	}); err != nil {
		c.m.transitBestEffort(c.taskID, proto.TaskStateRunning, "权限工单创建失败回滚")
		return executor.ApprovalResult{}, err
	}
	evt, err := c.m.st.AppendEvent(c.taskID, proto.EventTypePermissionRequest, permissionPayload{
		TicketID: ticketID, Permission: permEventText(ev.Text), Kind: "gate",
	})
	if err != nil {
		c.m.log.Error("追加 permission_request 失败", "task", c.taskID, "cause", err)
		return executor.ApprovalResult{Ref: executor.ApprovalRef{ID: ticketID}, Decision: executor.ApprovalDecision{Status: executor.ApprovalPending}}, nil
	}
	c.m.hub.Publish(evt)
	return executor.ApprovalResult{
		Ref:      executor.ApprovalRef{ID: ticketID},
		Decision: executor.ApprovalDecision{Status: executor.ApprovalPending, Reason: reason},
	}, nil
}

func (c *taskApprovalClient) recordAutoAllow(ev executor.AdapterEvent, v permgate.Verdict) {
	c.m.autoAllowPermission(c.taskID, ev, v)
}

func (c *taskApprovalClient) decisionFromStore(ticketID string) (executor.ApprovalDecision, bool, error) {
	tk, err := c.m.st.GetTicket(ticketID)
	if err != nil {
		return executor.ApprovalDecision{}, false, nil
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

// HashPolicyVersion 对政策内容做稳定哈希。字段集：黑名单、审批者启用、安全命令表、范围三根。
func HashPolicyVersion(blacklist []string, approverOn bool, safeIDs []string, scope executor.ApprovalScope) string {
	bl := append([]string(nil), blacklist...)
	sort.Strings(bl)
	ids := append([]string(nil), safeIDs...)
	sort.Strings(ids)
	type payload struct {
		Blacklist  []string `json:"blacklist"`
		ApproverOn bool     `json:"approver_on"`
		Safe       []string `json:"safe"`
		Workdir    string   `json:"workdir"`
		TaskDir    string   `json:"task_dir"`
		TaskTmp    string   `json:"task_tmp"`
	}
	b, _ := json.Marshal(payload{Blacklist: bl, ApproverOn: approverOn, Safe: ids,
		Workdir: scope.Workdir, TaskDir: scope.TaskDir, TaskTmp: scope.TaskTmpDir})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
