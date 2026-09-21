package executor_test

import (
	"context"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

type fakeApprovalClient struct{}

func (fakeApprovalClient) PolicySnapshot(context.Context) (executor.PolicySnapshot, error) {
	return executor.PolicySnapshot{}, nil
}
func (fakeApprovalClient) Request(context.Context, executor.ApprovalRequest) (executor.ApprovalResult, error) {
	return executor.ApprovalResult{}, nil
}
func (fakeApprovalClient) Await(context.Context, executor.ApprovalRef) (executor.ApprovalDecision, error) {
	return executor.ApprovalDecision{}, nil
}
func (fakeApprovalClient) Acknowledge(context.Context, executor.ApprovalAck) error {
	return nil
}

var _ executor.ApprovalClient = fakeApprovalClient{}

func TestApprovalStatusLiterals(t *testing.T) {
	if executor.ApprovalAllow != "allow" {
		t.Fatalf("ApprovalAllow = %q, want allow", executor.ApprovalAllow)
	}
	if executor.ApprovalDeny != "deny" {
		t.Fatalf("ApprovalDeny = %q, want deny", executor.ApprovalDeny)
	}
	if executor.ApprovalPending != "pending" {
		t.Fatalf("ApprovalPending = %q, want pending", executor.ApprovalPending)
	}
	if executor.AckFormed != "formed" {
		t.Fatalf("AckFormed = %q, want formed", executor.AckFormed)
	}
	if executor.AckDelivered != "delivered" {
		t.Fatalf("AckDelivered = %q, want delivered", executor.AckDelivered)
	}
	if executor.AckExecuted != "executed" {
		t.Fatalf("AckExecuted = %q, want executed", executor.AckExecuted)
	}
}

func TestStartReqApprovalSlotIsOptional(t *testing.T) {
	var req executor.StartReq
	if req.Approval != nil {
		t.Fatal("zero StartReq.Approval must be nil")
	}
}
