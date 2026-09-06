package executor_test

import (
	"context"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
)

type pendingThenAllow struct {
	awaited int
}

func (pendingThenAllow) PolicySnapshot(context.Context) (executor.PolicySnapshot, error) {
	return executor.PolicySnapshot{Version: "v"}, nil
}
func (s *pendingThenAllow) Request(context.Context, executor.ApprovalRequest) (executor.ApprovalResult, error) {
	return executor.ApprovalResult{
		Ref:      executor.ApprovalRef{ID: "ref-1"},
		Decision: executor.ApprovalDecision{Status: executor.ApprovalPending},
	}, nil
}
func (s *pendingThenAllow) Await(ctx context.Context, _ executor.ApprovalRef) (executor.ApprovalDecision, error) {
	s.awaited++
	if err := ctx.Err(); err != nil {
		return executor.ApprovalDecision{}, err
	}
	return executor.ApprovalDecision{Status: executor.ApprovalAllow, Rule: "human"}, nil
}
func (*pendingThenAllow) Acknowledge(context.Context, executor.ApprovalAck) error { return nil }

func TestAuthorizeAwaitsWhenPending(t *testing.T) {
	s := &pendingThenAllow{}
	d, err := executor.Authorize(context.Background(), s, executor.ApprovalRequest{NativeID: "p1"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != executor.ApprovalAllow {
		t.Fatalf("status=%q", d.Status)
	}
	if s.awaited != 1 {
		t.Fatalf("Await 次数=%d，pending 必须 Await", s.awaited)
	}
}

func TestAuthorizeNilClientIsNotAllow(t *testing.T) {
	d, err := executor.Authorize(context.Background(), nil, executor.ApprovalRequest{NativeID: "p1"})
	if err == nil {
		t.Fatal("nil client 必须错误")
	}
	if d.Status == executor.ApprovalAllow {
		t.Fatal("nil client 不得 allow")
	}
}

func TestAuthorizeCancelDoesNotLeak(t *testing.T) {
	s := &pendingThenAllow{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := executor.Authorize(ctx, s, executor.ApprovalRequest{NativeID: "p1"})
	if err == nil {
		t.Fatal("已取消 ctx 应返回错误（Await 见 ctx.Err）")
	}
	_ = time.Second
}
