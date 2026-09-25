// b402_retry_test.go —— B402 派发节点零文本故障的单次自动续接与归档边界。
//
// 主红测：首个终态 turn_failed(zero_text) 自动续接恰一次，第二个终态才是裁决
// 输入；无分类/未知分类/续接失败/二次仍零文本都不归档。
package ledgerstep

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// b402Client 是分阶段推进事件的最小 StepClient 替身：WaitEvent 按序吐事件，
// Attach 返回固定快照，Continue 只计数。不依赖 sleep。
type b402Client struct {
	waits        []*proto.Event
	waitIdx      int
	attachEvents []proto.Event
	continues    int
	lastInstr    string
	continueErr  error
}

var _ StepClient = (*b402Client)(nil)

func (c *b402Client) Dispatch(context.Context, client.DispatchOpts) (*proto.Task, error) {
	return &proto.Task{ID: "t"}, nil
}
func (c *b402Client) Reply(context.Context, string, string, string) error { return nil }
func (c *b402Client) Continue(_ context.Context, _ string, instructions string) error {
	c.continues++
	c.lastInstr = instructions
	return c.continueErr
}
func (c *b402Client) Stop(context.Context, string) (bool, error) { return false, nil }
func (c *b402Client) WaitEvent(context.Context, string, bool) (*proto.Event, error) {
	if c.waitIdx >= len(c.waits) {
		return nil, context.DeadlineExceeded
	}
	ev := c.waits[c.waitIdx]
	c.waitIdx++
	return ev, nil
}
func (c *b402Client) FollowEvents(context.Context, string, bool, time.Duration,
	func(*proto.Event) error, func(*client.BacklogSummary) error) error {
	return nil
}
func (c *b402Client) Diff(context.Context, string, string) (string, error) { return "", nil }
func (c *b402Client) Attach(context.Context, string) (*client.AttachInfo, error) {
	return &client.AttachInfo{RecentEvents: c.attachEvents}, nil
}
func (c *b402Client) Done(context.Context, string, string) (bool, error) { return true, nil }

func zeroTextEvent() *proto.Event {
	return &proto.Event{Type: proto.EventTypeTurnFailed,
		Payload: json.RawMessage(`{"fail_reason":"与实现无关的文案","failure_class":"zero_text"}`)}
}

func completedEvent(text string) *proto.Event {
	payload, _ := json.Marshal(map[string]string{"final_text": text})
	return &proto.Event{Type: proto.EventTypeCompleted, Payload: payload}
}

func newB402Runner(c *b402Client) *StepRunner {
	return &StepRunner{Clients: func(string) (StepClient, error) { return c, nil }}
}

// TestAwaitNodeAutoContinuesZeroTextOnce 主回路：zero_text → completed。
func TestAwaitNodeAutoContinuesZeroTextOnce(t *testing.T) {
	final := "```handoff-verdict\n{\"verdict\":\"pass\"}\n```"
	c := &b402Client{
		waits:        []*proto.Event{zeroTextEvent(), completedEvent(final)},
		attachEvents: []proto.Event{*completedEvent(final)},
	}
	msg, err := newB402Runner(c).awaitNode()(context.Background(), "mac-02", "t")
	if err != nil {
		t.Fatalf("awaitNode: %v", err)
	}
	if c.continues != 1 {
		t.Fatalf("自动续接次数 = %d，want 1", c.continues)
	}
	if c.waitIdx != 2 {
		t.Fatalf("续接后必须再等一次终态，wait 次数 = %d，want 2", c.waitIdx)
	}
	if msg != final {
		t.Fatalf("应返回第二个终态的报文，实得 %q", msg)
	}
	if c.lastInstr == "" {
		t.Fatal("续接指令不得为空")
	}
}

// TestAwaitNodeContinuesAtMostOnce 耗尽反例：zero_text → zero_text 也只续接一次。
func TestAwaitNodeContinuesAtMostOnce(t *testing.T) {
	c := &b402Client{
		waits:        []*proto.Event{zeroTextEvent(), zeroTextEvent()},
		attachEvents: []proto.Event{*zeroTextEvent()},
	}
	if _, err := newB402Runner(c).awaitNode()(context.Background(), "mac-02", "t"); err != nil {
		t.Fatalf("awaitNode: %v", err)
	}
	if c.continues != 1 {
		t.Fatalf("一个 RunOnce 最多自动续接一次，实际 %d", c.continues)
	}
	if c.waitIdx != 2 {
		t.Fatalf("应恰好等两个终态，wait 次数 = %d", c.waitIdx)
	}
}

// TestAwaitNodeDoesNotContinueWithoutClass 无分类/非 zero_text 不续接。
func TestAwaitNodeDoesNotContinueWithoutClass(t *testing.T) {
	c := &b402Client{
		waits: []*proto.Event{{Type: proto.EventTypeTurnFailed,
			Payload: json.RawMessage(`{"fail_reason":"没有分类"}`)}},
		attachEvents: []proto.Event{{Type: proto.EventTypeTurnFailed,
			Payload: json.RawMessage(`{"fail_reason":"没有分类"}`)}},
	}
	if _, err := newB402Runner(c).awaitNode()(context.Background(), "mac-02", "t"); err != nil {
		t.Fatalf("awaitNode: %v", err)
	}
	if c.continues != 0 {
		t.Fatalf("无分类不得续接，实际 %d 次", c.continues)
	}
}

// TestAwaitNodeContinueFailureFailsClosed 续接失败（含外部抢先续接 409）必须
// fail-closed：不进入第二次等待，错误上浮由节点落等人。
func TestAwaitNodeContinueFailureFailsClosed(t *testing.T) {
	c := &b402Client{
		waits:       []*proto.Event{zeroTextEvent()},
		continueErr: errors.New("任务状态不允许续接: 409"),
	}
	_, err := newB402Runner(c).awaitNode()(context.Background(), "mac-02", "t")
	if err == nil {
		t.Fatal("续接失败必须返回错误")
	}
	if c.continues != 1 {
		t.Fatalf("应尝试续接一次，实际 %d", c.continues)
	}
	if c.waitIdx != 1 {
		t.Fatalf("续接失败后不得再等，wait 次数 = %d，want 1", c.waitIdx)
	}
}

// TestNodeStepDoesNotFinishTaskOnParseFailure 生命周期：解析失败等早退路径
// 不得归档 task，任务留在 waiting_review。
func TestNodeStepDoesNotFinishTaskOnParseFailure(t *testing.T) {
	st, card := nodeLedger(t)
	finished := 0
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "implement", Dispatch: true, Verdict: true, Template: "feature-impl",
			Next: ledger.StatusReview, OnFail: ledger.StatusDoing,
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "mac-02", "task-b402", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return "没有 handoff-verdict 的报文", nil
		},
		FinishTask: func(context.Context, string, string) error {
			finished++
			return nil
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("解析失败应转等人，Action = %q", out.Action)
	}
	if finished != 0 {
		t.Fatalf("解析失败不得归档 task，FinishTask 调用 %d 次", finished)
	}
}

// TestNodeStepFinishTaskFailureHaltsForHuman 归档失败必须显式转人工，不得
// 把「已移卡」伪装成「task 已归档」。
func TestNodeStepFinishTaskFailureHaltsForHuman(t *testing.T) {
	st, card := nodeLedger(t)
	step := &NodeStep{
		St: st,
		Node: ledger.NodeDef{
			Name: "implement", Dispatch: true, Verdict: true, Template: "feature-impl",
			Next: ledger.StatusReview, OnFail: ledger.StatusDoing,
		},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "mac-02", "task-b402", nil
		},
		Await: func(context.Context, string, string) (string, error) {
			return nodePassMessage(), nil
		},
		FinishTask: func(context.Context, string, string) error {
			return errors.New("done 404：task 已被外部归档")
		},
	}
	out, err := step.RunOnce(context.Background(), card.ID)
	if err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	if out.Action != ActionNeedsHuman {
		t.Fatalf("归档失败应转等人，Action = %q", out.Action)
	}
	if out.Reason != "task 归档失败" {
		t.Fatalf("Reason = %q，want task 归档失败", out.Reason)
	}
}
