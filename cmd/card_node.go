// card dispatch --step 的 CLI 装配层：构造 ledgerstep.StepRunner 并把结果编码成 JSON。
// 编排本身在 internal/ledgerstep——看板按钮（经 /api/cards/{id}/step）装配的是同一个
// StepRunner，只是注入不同的仓路径与传输，单一编排真相源由此落实。
package cmd

import (
	"context"
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/spf13/cobra"
)

// runStepDispatch card dispatch --step 的入口。
func runStepDispatch(cmd *cobra.Command, st *ledger.Store, id, step, actor string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	runner := &ledgerstep.StepRunner{
		St:      st,
		RepoDir: cardDispatchRepo,
		Dispatcher: &ledgerstep.Dispatcher{
			St: st, Transport: cliTransport, Actor: actor,
		},
		Endpoints: targetEndpoint,
		Target:    cardDispatchTarget,
	}
	outcome, err := runner.Run(ctx, id, step)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
}
