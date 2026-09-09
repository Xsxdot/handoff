// Package approval owns the production ApprovalClient implementation.
//
// Manager remains the assembly point. Hooks keeps the implementation
// independent from the agentd package while preserving its existing seams.
package approval

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/permgate"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

var errSkeleton = errors.New("approval: client skeleton")

// AnswerHub is the narrow runtime event seam used by ApprovalClient.
type AnswerHub interface {
	Publish(proto.Event)
	WaitAnswer(context.Context, string) (string, error)
}

// ConsultDecision is the agentd-independent result of an approver call.
type ConsultDecision struct {
	Approve   bool
	Reason    string
	ElapsedMS int64
	Err       error
}

// Hooks supplies the orchestration capabilities required by Client.
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

// Client is the production ApprovalClient implementation.
type Client struct {
	taskID string
	snap   executor.PolicySnapshot
	hooks  Hooks
}

// NewClient constructs an ApprovalClient for one task and policy snapshot.
func NewClient(taskID string, snap executor.PolicySnapshot, hooks Hooks) *Client {
	if snap.TaskID == "" {
		snap.TaskID = taskID
	}
	return &Client{taskID: taskID, snap: snap, hooks: hooks}
}

var _ executor.ApprovalClient = (*Client)(nil)

func (*Client) PolicySnapshot(context.Context) (executor.PolicySnapshot, error) {
	return executor.PolicySnapshot{}, errSkeleton
}

func (*Client) Request(context.Context, executor.ApprovalRequest) (executor.ApprovalResult, error) {
	return executor.ApprovalResult{}, errSkeleton
}

func (*Client) Await(context.Context, executor.ApprovalRef) (executor.ApprovalDecision, error) {
	return executor.ApprovalDecision{}, errSkeleton
}

func (*Client) Acknowledge(context.Context, executor.ApprovalAck) error {
	return errSkeleton
}
