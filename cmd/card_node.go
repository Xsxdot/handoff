// card dispatch --step 的 CLI 装配层：构造 ledgerstep.StepRunner 并把结果编码成 JSON。
// 编排本身在 internal/ledgerstep——看板按钮（经 /api/cards/{id}/step）装配的是同一个
// StepRunner，只是注入不同的传输，单一编排真相源由此落实。
package cmd

import (
	"context"
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/spf13/cobra"
)

// runStepDispatch card dispatch --step 的入口。
func runStepDispatch(cmd *cobra.Command, st *ledger.Store, id, node, actor string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	// 本次运行里建过的 relay 隧道，命令结束时统一关。环节是一次性的短命令，
	// 攒起来在这里关比让 ledgerstep 承担关闭责任简单，也不会跨命令泄漏。
	var cleanups []func()
	defer func() {
		for _, done := range cleanups {
			done()
		}
	}()
	runner := &ledgerstep.StepRunner{
		St: st, Session: ledgerSession(),
		Dispatcher: &ledgerstep.Dispatcher{
			St: st, Transport: cliTransport, Actor: actor,
		},
		Clients: func(target string) (*client.Client, error) {
			cl, done, err := targetClient(target)
			if err != nil {
				return nil, err
			}
			cleanups = append(cleanups, done)
			return cl, nil
		},
		Target:   cardDispatchTarget,
		Executor: cardDispatchExecutor,
		Model:    cardDispatchModel,
		Extra:    cardDispatchExtra,
	}
	outcome, err := runner.Run(ctx, id, node)
	if err != nil {
		return err
	}
	return json.NewEncoder(cmd.OutOrStdout()).Encode(outcome)
}
