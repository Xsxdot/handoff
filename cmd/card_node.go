// card dispatch --step 的 CLI 提交层：把一次性节点请求交给本机 agentd。
// 节点编排只在 agentd 内运行；CLI 只持有短 HTTP 请求并输出受理入口。
package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// runStepDispatch 向本机 agentd 提交一次卡节点并在 202 后立即返回。
//
// 参数：cmd 提供 context、stdout/stderr；id 是卡号；node 是卡钉工作流节点名。
// 返回：本机 endpoint 配置、HTTP 受理或 agentd 错误；成功只表示受理。
// 注意：--target/--executor/--model/--extra 是请求覆盖项；--target 不改变本机
// agentd 拨号端点；--plan 与 --step 组合直接拒绝，绝不读取或上传调用方文件。
func runStepDispatch(cmd *cobra.Command, id, node string) error {
	if cardDispatchPlan != "" {
		slog.Default().Warn("card step 拒绝本地 plan", "card", id, "node", node,
			"plan", cardDispatchPlan)
		return fmt.Errorf("card dispatch --step 不接受 --plan：调用方本地文件不会被上传")
	}
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	addr, token, err := LocalEndpoint()
	if err != nil {
		slog.Default().Warn("读取本机 agentd 端点失败", "card", id, "node", node, "cause", err)
		return err
	}
	cl := client.New(addr, token)
	req := proto.CardStepReq{
		Step: node, Target: cardDispatchTarget, Executor: cardDispatchExecutor,
		Model: cardDispatchModel, Extra: cardDispatchExtra, Actor: ledgerSession(),
	}
	slog.Default().Info("CLI 提交卡节点", "card", id, "node", node, "agentd", cl.BaseURL(),
		"target", req.Target, "executor", req.Executor, "model", req.Model,
		"has_extra", strings.TrimSpace(req.Extra) != "", "actor", req.Actor)
	if err := cl.CardStep(ctx, id, req); err != nil {
		slog.Default().Warn("CLI 卡节点未受理", "card", id, "node", node, "cause", err)
		return err
	}
	slog.Default().Info("CLI 卡节点已受理", "card", id, "node", node, "actor", req.Actor)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "卡 %s 的节点 %s 已受理；进展见 handoff card wait %s\n", id, node, id)
	return nil
}
