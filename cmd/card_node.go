// card dispatch --step 的 CLI 提交层：把一次性节点请求交给本机 agentd。
// 节点编排只在 agentd 内运行；CLI 只持有短 HTTP 请求并输出受理入口。
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

var (
	stepFirstStateTimeout      = 20 * time.Second
	stepFirstStatePollInterval = 100 * time.Millisecond
)

var errStepFirstState = errors.New("card step 首态已到")

// stepFirstState 是 card dispatch --step 在 POST 后从本机账本观察到的首个可见结果。
// Dispatch 与 FailureComment 互斥：前者表示派发成功，后者保留 haltForHuman 的原文。
type stepFirstState struct {
	Dispatch       *ledger.DispatchSnapshot
	FailureComment string
}

// waitStepFirstState 从 POST 前水位之后排他消费当前卡事件，直到发现派发快照、派发失败，
// 或自身短等窗口结束。账本由调用方打开并拥有；ctx 取消、账本读取和回调错误原样返回。
func waitStepFirstState(ctx context.Context, st *ledger.Store, cardID string, watermark int64) (stepFirstState, error) {
	var observed stepFirstState
	logger := slog.Default().With("card", cardID, "watermark", watermark)
	waitCtx, cancel := context.WithTimeout(ctx, stepFirstStateTimeout)
	defer cancel()
	err := st.Follow(waitCtx, func() ([]string, error) {
		return []string{cardID}, nil
	}, watermark, stepFirstStatePollInterval, func(event ledger.Event) error {
		logger.Debug("短等收到卡事件", "seq", event.Seq, "type", event.Type)
		switch event.Type {
		case ledger.EvComment:
			var payload struct {
				Body string `json:"body"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				logger.Warn("短等忽略无法解码的 comment", "seq", event.Seq, "cause", err)
				return nil
			}
			if strings.HasPrefix(payload.Body, "本节点派发失败：\n") {
				observed.FailureComment = payload.Body
			}
		case ledger.EvDispatched:
			var snapshot ledger.DispatchSnapshot
			if err := json.Unmarshal(event.Payload, &snapshot); err != nil {
				logger.Warn("短等忽略无法解码的 dispatched", "seq", event.Seq, "cause", err)
				return nil
			}
			observed.Dispatch = &snapshot
			logger.Info("短等发现 dispatched 首态", "seq", event.Seq,
				"target", snapshot.Target, "branch", snapshot.Branch, "base", snapshot.Base,
				"base_commit", snapshot.BaseCommit, "discipline", snapshot.DisciplineName)
			return errStepFirstState
		case ledger.EvNeedsHuman:
			var payload struct {
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				logger.Warn("短等忽略无法解码的 needs_human", "seq", event.Seq, "cause", err)
				return nil
			}
			if payload.Reason != "派发失败" {
				return nil
			}
			logger.Info("短等发现派发失败首态", "seq", event.Seq,
				"reason", payload.Reason, "has_comment", observed.FailureComment != "")
			return errStepFirstState
		}
		return nil
	})
	if errors.Is(err, errStepFirstState) {
		return observed, nil
	}
	return observed, err
}

// writeStepDispatchResult 将账本里的派发快照写成 CLI 成功决议。
// 参数：w 是 stdout；cardID/node 标识卡和节点；snap 是本次首态快照。空 target/base/sha
// 使用本机、无起点分支、无 sha 文案；非空 sha 最多展示七字节，不输出本地 ref 或 origin。
func writeStepDispatchResult(w io.Writer, cardID, node string, snap ledger.DispatchSnapshot) {
	target := snap.Target
	if target == "" {
		target = "本机"
	}
	base := snap.Base
	if base == "" {
		base = "无起点分支"
	}
	baseCommit := snap.BaseCommit
	if baseCommit == "" {
		baseCommit = "无 sha"
	} else if len(baseCommit) > 7 {
		baseCommit = baseCommit[:7]
	}
	_, _ = fmt.Fprintf(w,
		"卡 %s 的节点 %s 已派发；目标机：%s；新分支：%s；起点分支：%s；起点短号：%s；纪律块：%s\n",
		cardID, node, target, snap.Branch, base, baseCommit, snap.DisciplineName)
}

// runStepDispatch 向本机 agentd 提交一次卡节点并在 202 后短等本机账本首态。
//
// 参数：cmd 提供 context、stdout/stderr；id 是卡号；node 是卡钉工作流节点名。
// 返回：本机 endpoint 配置、HTTP 受理或 agentd 错误；202 后若观察到首态则输出
// 派发决议，若短等超时则只表示请求已受理，不携带完整回合结论。
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
	st, err := openLedger()
	if err != nil {
		slog.Default().Warn("打开本机账本失败", "card", id, "node", node, "cause", err)
		return err
	}
	defer st.Close()
	watermark, err := st.MaxSeq()
	if err != nil {
		slog.Default().Warn("读取 card step POST 前水位失败", "card", id, "node", node, "cause", err)
		return err
	}
	slog.Default().Info("记录 card step POST 前水位", "card", id, "node", node, "watermark", watermark)
	addr, token, err := LocalEndpoint()
	if err != nil {
		slog.Default().Warn("读取本机 agentd 端点失败", "card", id, "node", node, "cause", err)
		return err
	}
	cl := client.New(addr, token)
	req := proto.CardStepReq{
		Step: node, Target: cardDispatchTarget, Executor: cardDispatchExecutor,
		Model: cardDispatchModel, Extra: cardDispatchExtra, Actor: ledgerActor(),
	}
	slog.Default().Info("CLI 提交卡节点", "card", id, "node", node, "agentd", cl.BaseURL(),
		"target", req.Target, "executor", req.Executor, "model", req.Model,
		"has_extra", strings.TrimSpace(req.Extra) != "", "actor", req.Actor)
	if err := cl.CardStep(ctx, id, req); err != nil {
		slog.Default().Warn("CLI 卡节点未受理", "card", id, "node", node, "cause", err)
		return err
	}
	slog.Default().Info("CLI 卡节点已收到 202，开始短等首态", "card", id, "node", node,
		"watermark", watermark, "timeout", stepFirstStateTimeout)
	state, waitErr := waitStepFirstState(ctx, st, id, watermark)
	if waitErr != nil {
		if ctx.Err() != nil {
			slog.Default().Warn("card step 首态等待被调用方取消", "card", id, "node", node,
				"cause", waitErr)
			return fmt.Errorf("等待 card step 首态: %w", waitErr)
		}
		if errors.Is(waitErr, context.DeadlineExceeded) {
			slog.Default().Info("card step 首态短等超时，仍按已受理返回", "card", id, "node", node,
				"watermark", watermark, "timeout", stepFirstStateTimeout)
			_, _ = fmt.Fprintf(cmd.OutOrStdout(),
				"卡 %s 的节点 %s 已受理，首态未到；进展见 handoff card wait %s\n", id, node, id)
			return nil
		}
		slog.Default().Warn("读取 card step 首态失败", "card", id, "node", node, "cause", waitErr)
		return fmt.Errorf("读取 card step 首态: %w", waitErr)
	}
	if state.Dispatch != nil {
		slog.Default().Info("CLI 输出 card step dispatched 决议", "card", id, "node", node)
		writeStepDispatchResult(cmd.OutOrStdout(), id, node, *state.Dispatch)
		return nil
	}
	if state.FailureComment == "" {
		slog.Default().Error("派发失败首态缺少 comment 正文", "card", id, "node", node)
		return fmt.Errorf("card dispatch --step 派发失败首态缺少 comment 正文")
	}
	slog.Default().Warn("CLI 输出 card step 派发失败 comment", "card", id, "node", node)
	_, _ = fmt.Fprint(cmd.ErrOrStderr(), state.FailureComment)
	return fmt.Errorf("card dispatch --step 发现派发失败首态")
}
