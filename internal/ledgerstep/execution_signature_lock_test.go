package ledgerstep

// B233.16 T1 编译期签名锁：卡步编排消费点的依赖面钉在能力接口上。把
// StepRunner.Clients 字段类型改回 func(string) (*client.Client, error)，或把
// clientFinalMessage 的参数改回聚合，本文件编译失败。

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

var (
	_ func(string) (StepClient, error)                                  = (&StepRunner{}).Clients
	_ func(context.Context, finalMessageClient, string) (string, error) = clientFinalMessage
)

func TestExecutionSignatureLocks(t *testing.T) {}

// ---- B233.16 T2：ledgerstep 最小替身注入 ----
//
// 经 StepRunner.Clients 字段注入只实现 StepClient 的替身，分别跑 diffNode /
// awaitNode / finishTask 三条真实消费路径。调用计数是「替身确实被注入」的哨兵；
// 既有真实 HTTP 链路测试（runner_test.go 的 WS/attach/done/diff）保持不动。

type stepClientStub struct {
	diffCalls   atomic.Int32
	attachCalls atomic.Int32
	doneCalls   atomic.Int32
	waitCalls   atomic.Int32
}

var _ StepClient = (*stepClientStub)(nil)

func (s *stepClientStub) Dispatch(context.Context, client.DispatchOpts) (*proto.Task, error) {
	return &proto.Task{ID: "T-step-stub"}, nil
}

func (s *stepClientStub) Reply(context.Context, string, string, string) error { return nil }
func (s *stepClientStub) Continue(context.Context, string, string) error      { return nil }
func (s *stepClientStub) Stop(context.Context, string) (bool, error)          { return true, nil }

func (s *stepClientStub) WaitEvent(context.Context, string, bool) (*proto.Event, error) {
	s.waitCalls.Add(1)
	return &proto.Event{
		Type:    proto.EventTypeCompleted,
		Payload: json.RawMessage(`{"final_text":"final"}`),
	}, nil
}

func (s *stepClientStub) FollowEvents(context.Context, string, bool, time.Duration,
	func(*proto.Event) error, func(*client.BacklogSummary) error) error {
	return nil
}

func (s *stepClientStub) Diff(context.Context, string, string) (string, error) {
	s.diffCalls.Add(1)
	return "diff --git a/docs/out.md b/docs/out.md\n--- a/docs/out.md\n+++ b/docs/out.md\n", nil
}

func (s *stepClientStub) Attach(context.Context, string) (*client.AttachInfo, error) {
	s.attachCalls.Add(1)
	return &client.AttachInfo{
		RecentEvents: []proto.Event{{
			Type:    proto.EventTypeCompleted,
			Payload: json.RawMessage(`{"final_text":"final"}`),
		}},
	}, nil
}

func (s *stepClientStub) Done(context.Context, string, string) (bool, error) {
	s.doneCalls.Add(1)
	return true, nil
}

// TestStepClientStubDoesNotImplementTransport：替身不满足 client.Transport。
func TestStepClientStubDoesNotImplementTransport(t *testing.T) {
	if _, ok := any(&stepClientStub{}).(client.Transport); ok {
		t.Fatal("stepClientStub 实现了 client.Transport（含 HTTPClient/BaseURL），接口隔离破裂")
	}
}

func TestStepRunnerClientsInjectsStepClientOnly(t *testing.T) {
	stub := &stepClientStub{}
	runner := &StepRunner{Clients: func(target string) (StepClient, error) {
		if target != "mac-02" {
			t.Fatalf("Clients 收到 target=%q，want mac-02", target)
		}
		return stub, nil
	}}

	paths, err := runner.diffNode()(context.Background(), "mac-02", "T-1")
	if err != nil {
		t.Fatalf("diffNode: %v", err)
	}
	if got := stub.diffCalls.Load(); got != 1 {
		t.Fatalf("Diff 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if len(paths) != 1 || paths[0] != "docs/out.md" {
		t.Fatalf("diff 投影 = %v，want [docs/out.md]", paths)
	}

	msg, err := runner.awaitNode()(context.Background(), "mac-02", "T-1")
	if err != nil {
		t.Fatalf("awaitNode: %v", err)
	}
	if got := stub.waitCalls.Load(); got != 1 {
		t.Fatalf("WaitEvent 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if got := stub.attachCalls.Load(); got != 1 {
		t.Fatalf("Attach 调用次数 = %d，want 1（最终报文取用经同一替身）", got)
	}
	if msg != "final" {
		t.Fatalf("最终报文 = %q，want final", msg)
	}

	if err := runner.finishTask()(context.Background(), "mac-02", "T-1"); err != nil {
		t.Fatalf("finishTask: %v", err)
	}
	if got := stub.doneCalls.Load(); got != 1 {
		t.Fatalf("Done 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
}
