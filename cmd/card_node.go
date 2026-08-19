// card dispatch --node 的真实现：构造节点执行体并跑一轮。
// 审阅与合并都不走认领语义：审阅/合并是待审阅卡上的节点动作，不应把卡
// 拉回进行中。看板动作按钮也应调用这一实现，保持单一编排真相源。
package cmd

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgernode"
	"github.com/spf13/cobra"
)

// runNodeDispatch card dispatch --node 的入口。
func runNodeDispatch(cmd *cobra.Command, st *ledger.Store, id, node, actor string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	var outcome ledgernode.Outcome
	var err error
	switch node {
	case "review":
		nodeRunner := &ledgernode.ReviewNode{
			St: st, Node: "review",
			RunReview: ledgernode.NewDispatchReview(st, reviewDispatchFn(st, actor), targetEndpoint),
		}
		outcome, err = nodeRunner.RunOnce(ctx, id)
	case "merge":
		nodeRunner := &ledgernode.MergeNode{
			St:        st,
			Objective: ledgernode.NewLocalObjective(cardDispatchRepo, st),
			DoMerge:   ledgernode.NewLocalMerge(cardDispatchRepo, st),
		}
		outcome, err = nodeRunner.RunOnce(ctx, id)
	default:
		return fmt.Errorf("--node 只认 review|merge，收到 %q", node)
	}
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
}

// reviewDispatchFn 审阅派发：模板派发共用段，不认领、不动卡状态。
func reviewDispatchFn(st *ledger.Store, actor string) func(ctx context.Context, cardID, template string) (string, string, error) {
	return func(ctx context.Context, cardID, template string) (string, string, error) {
		card, err := st.GetCard(cardID)
		if err != nil {
			return "", "", err
		}
		result, err := dispatchViaTemplate(st, card, template, cardDispatchTarget, "", cardDispatchDiscipline, actor)
		if err != nil {
			return "", "", err
		}
		return result.Target, result.Task, nil
	}
}
