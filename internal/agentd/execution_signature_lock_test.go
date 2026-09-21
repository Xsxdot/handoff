package agentd

// B233.16 T1 编译期签名锁：agentd 侧执行消费点的依赖面钉在能力接口上。把任一
// 参数类型改回聚合 *client.Client（或改写具名入口签名），本文件编译失败。

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
)

var (
	_ func(context.Context, client.ExecutionClient, ledgerstep.DispatchOpts, string) (*proto.Task, error) = dispatchStep
	_ func(context.Context, statusClient) (*proto.StatusResp, error)                                      = probeStatus

	_ func(*Server, context.Context, stopReclaimClient, string, string, string) error = (*Server).compensateStep
	_ func(*Server, context.Context, runClient, string, string, string) error         = (*Server).pushWorkBranchVia
	_ func(*Server, context.Context, ledgerstep.DispatchOpts) (string, string, error) = (*Server).stepTransport
)

func TestExecutionSignatureLocks(t *testing.T) {}

// ---- B233.16 T2：agentd 最小替身注入 ----
//
// 四个执行消费点各注入只实现所需方法的替身：dispatchStep（ExecutionClient）、
// probeStatus（+Status）、compensateStep（+StopAndReclaim）、pushWorkBranchVia（+Run）。
// 调用计数是「替身确实被注入」的哨兵；替身均不满足 client.Transport。

type executionStub struct {
	dispatchCalls atomic.Int32
	replyCalls    atomic.Int32
	continueCalls atomic.Int32
	stopCalls     atomic.Int32
	waitCalls     atomic.Int32
	followCalls   atomic.Int32

	lastDispatch client.DispatchOpts
}

var _ client.ExecutionClient = (*executionStub)(nil)

func (s *executionStub) Dispatch(_ context.Context, opts client.DispatchOpts) (*proto.Task, error) {
	s.dispatchCalls.Add(1)
	s.lastDispatch = opts
	return &proto.Task{ID: "T-agentd-stub"}, nil
}

func (s *executionStub) Reply(context.Context, string, string, string) error {
	s.replyCalls.Add(1)
	return nil
}

func (s *executionStub) Continue(context.Context, string, string) error {
	s.continueCalls.Add(1)
	return nil
}

func (s *executionStub) Stop(context.Context, string) (bool, error) {
	s.stopCalls.Add(1)
	return true, nil
}

func (s *executionStub) WaitEvent(context.Context, string, bool) (*proto.Event, error) {
	s.waitCalls.Add(1)
	return nil, nil
}

func (s *executionStub) FollowEvents(context.Context, string, bool, time.Duration,
	func(*proto.Event) error, func(*client.BacklogSummary) error) error {
	s.followCalls.Add(1)
	return nil
}

type statusStub struct {
	*executionStub
	statusCalls    atomic.Int32
	statusDeadline atomic.Int64
	status         *proto.StatusResp
}

func (s *statusStub) Status(ctx context.Context) (*proto.StatusResp, error) {
	s.statusCalls.Add(1)
	if dl, ok := ctx.Deadline(); ok {
		s.statusDeadline.Store(int64(time.Until(dl)))
	}
	return s.status, nil
}

var _ statusClient = (*statusStub)(nil)

type compensateStepStub struct {
	*executionStub
	reclaimCalls atomic.Int32
}

func (s *compensateStepStub) StopAndReclaim(context.Context, string) error {
	s.reclaimCalls.Add(1)
	return nil
}

var _ stopReclaimClient = (*compensateStepStub)(nil)

type runStub struct {
	*executionStub
	runCalls   atomic.Int32
	lastRunCmd string
}

func (s *runStub) Run(_ context.Context, _, cmd string) (string, int, error) {
	s.runCalls.Add(1)
	s.lastRunCmd = cmd
	return "", 0, nil
}

var _ runClient = (*runStub)(nil)

// TestAgentdExecutionStubsDoNotImplementTransport 同 CLI 侧：接口隔离的可观察判据。
func TestAgentdExecutionStubsDoNotImplementTransport(t *testing.T) {
	stubs := map[string]any{
		"executionStub":      &executionStub{},
		"statusStub":         &statusStub{executionStub: &executionStub{}},
		"compensateStepStub": &compensateStepStub{executionStub: &executionStub{}},
		"runStub":            &runStub{executionStub: &executionStub{}},
	}
	for name, s := range stubs {
		if _, ok := s.(client.Transport); ok {
			t.Fatalf("%s 实现了 client.Transport（含 HTTPClient/BaseURL），接口隔离破裂", name)
		}
	}
}

func TestDispatchStepUsesInjectedExecutionClient(t *testing.T) {
	stub := &executionStub{}
	task, err := dispatchStep(context.Background(), stub,
		ledgerstep.DispatchOpts{Prompt: "p", Project: "proj"}, "mac-02")
	if err != nil {
		t.Fatalf("dispatchStep: %v", err)
	}
	if got := stub.dispatchCalls.Load(); got != 1 {
		t.Fatalf("Dispatch 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if task == nil || task.ID != "T-agentd-stub" {
		t.Fatalf("dispatchStep 返回 %+v，want 替身任务", task)
	}
	if stub.lastDispatch.Target != "mac-02" || stub.lastDispatch.Prompt != "p" {
		t.Fatalf("DispatchOpts 映射失真（Target 必须取具名 dispatchStep 的 target 参数）: %+v",
			stub.lastDispatch)
	}
}

func TestProbeStatusUsesInjectedStatusClientAndTenSecondDeadline(t *testing.T) {
	yes := true
	stub := &statusStub{
		executionStub: &executionStub{},
		status:        &proto.StatusResp{DisciplinesSupported: &yes},
	}
	resp, err := probeStatus(context.Background(), stub)
	if err != nil {
		t.Fatalf("probeStatus: %v", err)
	}
	if got := stub.statusCalls.Load(); got != 1 {
		t.Fatalf("Status 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if resp == nil || resp.DisciplinesSupported == nil || !*resp.DisciplinesSupported {
		t.Fatalf("probeStatus 返回 %+v，want 能力位 true", resp)
	}
	got := time.Duration(stub.statusDeadline.Load())
	if got < 8*time.Second || got > 10*time.Second {
		t.Fatalf("probeStatus 施加的探活时限 = %v，want ≈10s（契约 #49）", got)
	}
}

func TestCompensateStepUsesInjectedStopReclaimClient(t *testing.T) {
	s := &Server{log: discardLogger()}
	stub := &compensateStepStub{executionStub: &executionStub{}}
	if err := s.compensateStep(context.Background(), stub, "B1", "mac-02", "T-1"); err != nil {
		t.Fatalf("compensateStep: %v", err)
	}
	if got := stub.reclaimCalls.Load(); got != 1 {
		t.Fatalf("StopAndReclaim 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
}

func TestPushWorkBranchViaUsesInjectedRunClient(t *testing.T) {
	s := &Server{log: discardLogger()}
	stub := &runStub{executionStub: &executionStub{}}
	if err := s.pushWorkBranchVia(context.Background(), stub, "mac-02", "cards/B1", "T-1"); err != nil {
		t.Fatalf("pushWorkBranchVia: %v", err)
	}
	if got := stub.runCalls.Load(); got != 1 {
		t.Fatalf("Run 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if want := `git push origin "cards/B1"`; stub.lastRunCmd != want {
		t.Fatalf("Run 命令 = %q，want %q", stub.lastRunCmd, want)
	}
}
