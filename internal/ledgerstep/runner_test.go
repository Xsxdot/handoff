package ledgerstep

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestRunnerRejectsUnknownNode(t *testing.T) {
	st, c := nodeLedger(t)
	card := c.ID
	runner := &StepRunner{St: st}
	_, err := runner.Run(context.Background(), card, "查无此节点")
	if err == nil {
		t.Fatalf("节点名不在卡钉的工作流里，应报错")
	}
	if !strings.Contains(err.Error(), "查无此节点") {
		t.Fatalf("错误里应带上节点名，实际: %v", err)
	}
}

func TestRunnerFindsNodeInPinnedWorkflowVersion(t *testing.T) {
	st, _ := nodeLedger(t)
	if _, err := st.PutWorkflow("nodeflow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: "待办", Next: "进行中"},
		{Name: "进行中"},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "找节点", Project: "p", Workflow: "nodeflow", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	runner := &StepRunner{St: st}
	// 「待办」在工作流里存在但没有 Dispatch 能力：应报「不可执行」而不是「找不到」。
	_, err = runner.Run(context.Background(), card.ID, "待办")
	if err == nil || !strings.Contains(err.Error(), "Dispatch") {
		t.Fatalf("应报缺 Dispatch 能力，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "老定义") || !strings.Contains(err.Error(), "人工列") {
		t.Fatalf("缺少老定义/人工列的原因指引，实际: %v", err)
	}
	if !strings.Contains(err.Error(), "handoff workflow put") || !strings.Contains(err.Error(), "nodeflow") {
		t.Fatalf("缺少 workflow put 修复指引，实际: %v", err)
	}
}

func TestRunnerPassesNodePurposeAndAcceptanceSwitch(t *testing.T) {
	st, _ := nodeLedger(t)
	if _, err := st.PutWorkflow("runnerflow", ledger.WorkflowDef{Nodes: []ledger.NodeDef{
		{Name: "待办", Next: "待审阅"},
		{
			Name: "待审阅", Dispatch: true, Template: "feature-impl", CarryCardContext: true,
			OmitAcceptance: true, Override: ledger.NodeOverride{Purpose: ledger.PurposeReview},
		},
	}}); err != nil {
		t.Fatalf("写工作流: %v", err)
	}
	card, err := st.CreateCard(ledger.NewCard{
		Title: "runner 透传", Project: "p", Workflow: "runnerflow", Actor: "t",
	})
	if err != nil {
		t.Fatalf("建卡: %v", err)
	}
	const criteria = "go test ./... 全绿且真机跑通"
	if err := st.SetAcceptance(card.ID, criteria, "test"); err != nil {
		t.Fatalf("SetAcceptance: %v", err)
	}
	card, err = st.GetCard(card.ID)
	if err != nil {
		t.Fatalf("GetCard: %v", err)
	}

	var dispatched []DispatchOpts
	d := &Dispatcher{St: st, Actor: "tester", Transport: func(ctx context.Context, opts DispatchOpts) (string, error) {
		dispatched = append(dispatched, opts)
		return fmt.Sprintf("T-runner-%d", len(dispatched)), nil
	}}
	if _, err := d.ViaTemplate(context.Background(), card,
		TemplateDispatch{Template: "feature-impl", Target: "mac-02"}); err != nil {
		t.Fatalf("先派实现轮: %v", err)
	}
	runner := &StepRunner{St: st, Dispatcher: d, Target: "mac-02"}
	if _, err := runner.Run(context.Background(), card.ID, "待审阅"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(dispatched) != 2 {
		t.Fatalf("应有实现轮和 runner 审阅轮两次派发，实得 %d", len(dispatched))
	}
	if want := "cards/" + card.ID + "-review-1"; dispatched[1].Branch != want {
		t.Fatalf("runner 应传用途并走审阅分支 %q，实得 %q", want, dispatched[1].Branch)
	}
	if strings.Contains(dispatched[1].Prompt, criteria) {
		t.Fatalf("runner 应传递 OmitAcceptance，prompt 不应含判据 %q：\n%s", criteria, dispatched[1].Prompt)
	}
}
