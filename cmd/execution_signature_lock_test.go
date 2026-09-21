package cmd

// B233.16 T1 编译期签名锁：把每个执行消费点的依赖面钉在能力接口上。把任一
// 参数类型改回聚合 *client.Client（或改写收敛后的具名入口签名），本文件编译失败。
// 这是本卡「有牙」的主形态，不依赖字符串扫描。

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var (
	_ func(*cobra.Command, client.ExecutionClient, string) error                              = runStop
	_ func(*cobra.Command, client.ExecutionClient, string, string) error                      = runReply
	_ func(*cobra.Command, client.ExecutionClient, string, string) error                      = runContinue
	_ func(context.Context, client.ExecutionClient, client.DispatchOpts) (*proto.Task, error) = dispatchTask

	_ func(context.Context, dispatchClient, string) (discipline.ResolvedDiscipline, error)                        = resolveBareDiscipline
	_ func(context.Context, dispatchClient, *ledger.Store, string, string) (discipline.ResolvedDiscipline, error) = resolveCardDispatchDiscipline
	_ func(context.Context, compensateClient, string, string, string) error                                       = compensateCardDispatch

	_ func(*cobra.Command, waitClient, string, string) error = runWaitOnce
	_ func(*cobra.Command, string, string, waitClient) error = runUntilDone
	_ func(*cobra.Command, string, string, waitClient) error = runFollow
	_ func(context.Context, waitClient, time.Duration)       = warnIfTimeoutBelowStall
	_ func(*cobra.Command, waitClient, string, *proto.Event) = autoSyncAfterWait
)

func TestExecutionSignatureLocks(t *testing.T) {}

// ---- B233.16 T2：CLI 最小替身注入 ----
//
// 替身只实现各消费点真正需要的接口方法，由已冻具名入口直接接收——证明
//「生产持有 ExecutionClient」的注入面成立（P2=A：不为测试给生产加包级可变全局）。
// 调用计数是「替身确实被注入」的哨兵：若生产仍内联 client.New，计数恒为 0，
// 断言变红。命令入口的 RunE 路径继续由既有 httptest 假 agentd 用例覆盖，
// 两类证据不互替。

type execStub struct {
	dispatchCalls atomic.Int32
	replyCalls    atomic.Int32
	continueCalls atomic.Int32
	stopCalls     atomic.Int32
	waitCalls     atomic.Int32
	followCalls   atomic.Int32

	stopRemoved  bool
	waitEvent    *proto.Event
	followEvent  *proto.Event
	lastDispatch client.DispatchOpts
}

// var _ client.ExecutionClient = (*execStub)(nil) —— 六方法齐备的编译期证据。
var _ client.ExecutionClient = (*execStub)(nil)

func (s *execStub) Dispatch(_ context.Context, opts client.DispatchOpts) (*proto.Task, error) {
	s.dispatchCalls.Add(1)
	s.lastDispatch = opts
	return &proto.Task{ID: "T-exec-stub"}, nil
}

func (s *execStub) Reply(context.Context, string, string, string) error {
	s.replyCalls.Add(1)
	return nil
}

func (s *execStub) Continue(context.Context, string, string) error {
	s.continueCalls.Add(1)
	return nil
}

func (s *execStub) Stop(context.Context, string) (bool, error) {
	s.stopCalls.Add(1)
	return s.stopRemoved, nil
}

func (s *execStub) WaitEvent(context.Context, string, bool) (*proto.Event, error) {
	s.waitCalls.Add(1)
	return s.waitEvent, nil
}

func (s *execStub) FollowEvents(_ context.Context, _ string, _ bool, _ time.Duration,
	onEvent func(*proto.Event) error, _ func(*client.BacklogSummary) error) error {
	s.followCalls.Add(1)
	if s.followEvent != nil {
		return onEvent(s.followEvent)
	}
	return nil
}

// dispatchStub 只比 execStub 多一个 Status——组合面的额外方法恰为消费点今日
// 真实调用（B233.16 契约 #4）。
type dispatchStub struct {
	*execStub
	statusCalls atomic.Int32
	status      *proto.StatusResp
}

func (s *dispatchStub) Status(context.Context) (*proto.StatusResp, error) {
	s.statusCalls.Add(1)
	return s.status, nil
}

var _ dispatchClient = (*dispatchStub)(nil)

// waitStub 是 wait 路径的组合面：额外方法恰为 WaitArchived/Attach/Status（#5）。
type waitStub struct {
	*execStub
	archivedCalls atomic.Int32
	attachCalls   atomic.Int32
	statusCalls   atomic.Int32
	archived      *proto.Event
	attachInfo    *client.AttachInfo
	attachErr     error
	status        *proto.StatusResp
}

func (s *waitStub) WaitArchived(context.Context, string) (*proto.Event, error) {
	s.archivedCalls.Add(1)
	return s.archived, nil
}

func (s *waitStub) Attach(context.Context, string) (*client.AttachInfo, error) {
	s.attachCalls.Add(1)
	return s.attachInfo, s.attachErr
}

func (s *waitStub) Status(context.Context) (*proto.StatusResp, error) {
	s.statusCalls.Add(1)
	return s.status, nil
}

var _ waitClient = (*waitStub)(nil)

// compensateStub 只多一个 StopAndReclaim（#6）。
type compensateStub struct {
	*execStub
	reclaimCalls atomic.Int32
	reclaimErr   error
}

func (s *compensateStub) StopAndReclaim(context.Context, string) error {
	s.reclaimCalls.Add(1)
	return s.reclaimErr
}

var _ compensateClient = (*compensateStub)(nil)

// TestCLIExecutionStubsDoNotImplementTransport 是可观察的接口隔离判据：四个替身
// 都不满足 client.Transport（即都没有 HTTPClient/BaseURL），也没有任何工作区/卡/
// 机器/PTY 方法——若组合面划分被破坏（被迫多实现方法），这里先红。
func TestCLIExecutionStubsDoNotImplementTransport(t *testing.T) {
	stubs := map[string]any{
		"execStub":       &execStub{},
		"dispatchStub":   &dispatchStub{execStub: &execStub{}},
		"waitStub":       &waitStub{execStub: &execStub{}},
		"compensateStub": &compensateStub{execStub: &execStub{}},
	}
	for name, s := range stubs {
		if _, ok := s.(client.Transport); ok {
			t.Fatalf("%s 实现了 client.Transport（含 HTTPClient/BaseURL），接口隔离破裂", name)
		}
	}
}

func TestRunStopUsesInjectedExecutionClient(t *testing.T) {
	stub := &execStub{stopRemoved: false}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runStop(cmd, stub, "T1"); err != nil {
		t.Fatalf("runStop: %v", err)
	}
	if got := stub.stopCalls.Load(); got != 1 {
		t.Fatalf("Stop 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if !strings.Contains(out.String(), "留存") {
		t.Fatalf("runStop 输出应含留存提示，实得 %q", out.String())
	}
}

func TestRunReplyUsesInjectedExecutionClient(t *testing.T) {
	stub := &execStub{}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runReply(cmd, stub, "T1", "allow"); err != nil {
		t.Fatalf("runReply: %v", err)
	}
	if got := stub.replyCalls.Load(); got != 1 {
		t.Fatalf("Reply 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if got := strings.TrimSpace(out.String()); got != `{"ok":true}` {
		t.Fatalf("reply stdout = %q，want 单行 {\"ok\":true}", got)
	}
}

func TestRunContinueUsesInjectedExecutionClient(t *testing.T) {
	stub := &execStub{}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runContinue(cmd, stub, "T1", "改一下"); err != nil {
		t.Fatalf("runContinue: %v", err)
	}
	if got := stub.continueCalls.Load(); got != 1 {
		t.Fatalf("Continue 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if got := strings.TrimSpace(out.String()); got != `{"ok":true}` {
		t.Fatalf("continue stdout = %q，want 单行 {\"ok\":true}", got)
	}
}

func TestDispatchTaskUsesInjectedExecutionClient(t *testing.T) {
	stub := &execStub{}
	task, err := dispatchTask(context.Background(), stub,
		client.DispatchOpts{Prompt: "plan", ProjectName: "proj1"})
	if err != nil {
		t.Fatalf("dispatchTask: %v", err)
	}
	if got := stub.dispatchCalls.Load(); got != 1 {
		t.Fatalf("Dispatch 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if task == nil || task.ID != "T-exec-stub" {
		t.Fatalf("dispatchTask 返回 %+v，want 替身任务", task)
	}
	if stub.lastDispatch.Prompt != "plan" || stub.lastDispatch.ProjectName != "proj1" {
		t.Fatalf("DispatchOpts 透传失真: %+v", stub.lastDispatch)
	}
}

func TestResolveBareDisciplineUsesInjectedDispatchClient(t *testing.T) {
	resetFlags(t)
	configPath = writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"tok\"\n")
	stub := &dispatchStub{
		execStub: &execStub{},
		status:   &proto.StatusResp{DisciplinesSupported: boolPtr(true)},
	}
	if _, err := resolveBareDiscipline(context.Background(), stub, ""); err != nil {
		t.Fatalf("resolveBareDiscipline: %v", err)
	}
	if got := stub.statusCalls.Load(); got != 1 {
		t.Fatalf("Status 调用次数 = %d，want 1（探活必须经注入的组合面）", got)
	}
}

func TestResolveCardDispatchDisciplineUsesInjectedDispatchClient(t *testing.T) {
	resetFlags(t)
	configPath = writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"tok\"\n")
	st, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	stub := &dispatchStub{
		execStub: &execStub{},
		status:   &proto.StatusResp{DisciplinesSupported: boolPtr(true)},
	}
	if _, err := resolveCardDispatchDiscipline(context.Background(), stub, st, "", ""); err != nil {
		t.Fatalf("resolveCardDispatchDiscipline: %v", err)
	}
	if got := stub.statusCalls.Load(); got != 1 {
		t.Fatalf("Status 调用次数 = %d，want 1（探活必须经注入的组合面）", got)
	}
}

func TestCompensateCardDispatchUsesInjectedCompensateClient(t *testing.T) {
	stub := &compensateStub{execStub: &execStub{}}
	if err := compensateCardDispatch(context.Background(), stub, "B1", "mac-02", "T-1"); err != nil {
		t.Fatalf("compensateCardDispatch: %v", err)
	}
	if got := stub.reclaimCalls.Load(); got != 1 {
		t.Fatalf("StopAndReclaim 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
}

func TestRunWaitOnceUsesInjectedWaitClient(t *testing.T) {
	t.Cleanup(func() { waitTimeout, waitNoSync, notifyFlag = 0, false, false })
	waitTimeout, waitNoSync, notifyFlag = 0, false, false
	stub := &waitStub{
		execStub: &execStub{waitEvent: &proto.Event{
			Seq: 7, TaskID: "t1", Type: proto.EventTypeProgress,
			Payload: json.RawMessage(`{}`),
		}},
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runWaitOnce(cmd, stub, "t1", "127.0.0.1:9999"); err != nil {
		t.Fatalf("runWaitOnce: %v", err)
	}
	if got := stub.waitCalls.Load(); got != 1 {
		t.Fatalf("WaitEvent 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if got := strings.Count(strings.TrimSpace(out.String()), "\n"); got != 0 {
		t.Fatalf("一次性 wait 的 stdout 必须严格一行: %q", out.String())
	}
	if !strings.Contains(out.String(), `"seq":7`) {
		t.Fatalf("stdout 应是事件 JSON，实得 %q", out.String())
	}
}

func TestRunUntilDoneUsesInjectedWaitClient(t *testing.T) {
	t.Cleanup(func() { waitTimeout, waitNoSync, notifyFlag = 0, false, false })
	waitTimeout, waitNoSync, notifyFlag = 0, false, false
	stub := &waitStub{
		execStub: &execStub{},
		archived: &proto.Event{Seq: 21, TaskID: "t1", Type: proto.EventTypeArchived},
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runUntilDone(cmd, "t1", "127.0.0.1:9999", stub); err != nil {
		t.Fatalf("runUntilDone: %v", err)
	}
	if got := stub.archivedCalls.Load(); got != 1 {
		t.Fatalf("WaitArchived 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if got := strings.Count(strings.TrimSpace(out.String()), "\n"); got != 0 {
		t.Fatalf("--until-done 成功 stdout 必须严格一行: %q", out.String())
	}
}

func TestRunFollowUsesInjectedWaitClient(t *testing.T) {
	t.Cleanup(func() { waitTimeout, waitNoSync, notifyFlag = 0, false, false })
	waitTimeout, waitNoSync, notifyFlag = 0, false, false
	stub := &waitStub{
		execStub: &execStub{followEvent: &proto.Event{
			Seq: 3, TaskID: "t1", Type: proto.EventTypeProgress,
			Payload: json.RawMessage(`{}`),
		}},
	}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var out bytes.Buffer
	cmd.SetOut(&out)
	if err := runFollow(cmd, "t1", "127.0.0.1:9999", stub); err != nil {
		t.Fatalf("runFollow: %v", err)
	}
	if got := stub.followCalls.Load(); got != 1 {
		t.Fatalf("FollowEvents 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if !strings.Contains(out.String(), `"seq":3`) {
		t.Fatalf("follow 应把事件逐行写出，实得 %q", out.String())
	}
}

func TestWarnIfTimeoutBelowStallUsesInjectedWaitClient(t *testing.T) {
	stub := &waitStub{
		execStub: &execStub{},
		status:   &proto.StatusResp{StallTimeout: "2h"},
	}
	// idle<=0 是「不设 --timeout」：不得探活。
	warnIfTimeoutBelowStall(context.Background(), stub, 0)
	if got := stub.statusCalls.Load(); got != 0 {
		t.Fatalf("idle=0 不应探活，Status 调用次数 = %d", got)
	}
	warnIfTimeoutBelowStall(context.Background(), stub, time.Hour)
	if got := stub.statusCalls.Load(); got != 1 {
		t.Fatalf("idle=1h<stall=2h 应探活一次，Status 调用次数 = %d", got)
	}
}

func TestAutoSyncAfterWaitUsesInjectedWaitClient(t *testing.T) {
	resetFlags(t)
	configPath = writeTestConfig(t, "listen: \"127.0.0.1:1\"\ntoken: \"tok\"\n")
	t.Cleanup(func() { waitNoSync = false })
	waitNoSync = false
	// Attach 返回错误即停在「自动同步跳过」分支，不触发任何 git 操作，
	// 但足以证明跨机 Attach 经注入的 waitClient 走。
	stub := &waitStub{execStub: &execStub{}, attachErr: errors.New("stub attach fail")}
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	var errBuf bytes.Buffer
	cmd.SetErr(&errBuf)
	autoSyncAfterWait(cmd, stub, "127.0.0.1:9999",
		&proto.Event{TaskID: "t1", Type: proto.EventTypeCompleted})
	if got := stub.attachCalls.Load(); got != 1 {
		t.Fatalf("Attach 调用次数 = %d，want 1（替身必须真的被注入）", got)
	}
	if !strings.Contains(errBuf.String(), "自动同步跳过") {
		t.Fatalf("Attach 失败应只打跳过提示，实得 %q", errBuf.String())
	}
}
