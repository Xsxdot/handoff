// 节点入口：把「跑一次工作流节点」收口成一个方法，CLI 与看板按钮共用同一份
// 装配逻辑。
//
// 边界：只做装配与分发，决策在 node.go 的 NodeStep；本文件不碰 HTTP、
// 不碰 cobra、不做输出编码——那些是各调用方自己的呈现层。
package ledgerstep

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// StepRunner 节点执行的装配器。依赖全部显式注入，调用方各填各的。
//
// 本地仓路径与主线名已随本地合并退役一并删除：合并现在是普通派发节点，
// 由 executor 在任务分支上完成，协调机不再执行任何 git 写操作。
type StepRunner struct {
	St         *ledger.Store
	Dispatcher *Dispatcher
	// Clients 按 target 名取一个已装配好的 agentd 客户端。
	//
	// why 这里要的是客户端而不是 (addr, token)：relay 形态的机器根本没有 addr，
	// 拿地址自己 client.New 对它们恒失败（会退化成一个没有 Host 的 URL）。
	// 选路归 agentd 的 target 客户端池管，本包只消费。
	Clients func(target string) (*client.Client, error)
	// Target 覆盖节点/模板里的目标机；空则用节点覆盖或模板的 target。
	Target string
	// Extra 本次执行的临时补充说明，透传进 prompt 的第三段；可为空。
	Extra string
}

// Run 跑一次节点。
//
// 参数：cardID 卡；nodeName 节点名（= 看板的列名），从卡钉住的工作流版本里查。
// 返回：Outcome；节点不存在、没有 Dispatch 能力或执行内部失败时返回错误。
//
// 阻塞行为：节点开了 Verdict 时会阻塞到被派出去的 task 跑到回合终态
// （几分钟到几十分钟，executor 挂在 waiting_answer 时更久）。调用方要么
// 自己在 goroutine 里跑（agentd 就是这么做的），要么接受前台阻塞（CLI）。
func (r *StepRunner) Run(ctx context.Context, cardID, nodeName string) (Outcome, error) {
	slog.Default().Info("进入节点执行", "card", cardID, "node", nodeName)
	node, err := r.nodeFor(cardID, nodeName)
	if err != nil {
		return Outcome{}, err
	}
	nodeStep := &NodeStep{
		St:       r.St,
		Node:     node,
		Dispatch: r.dispatchNode(),
		Await:    r.awaitNode(),
	}
	return nodeStep.RunOnce(ctx, cardID)
}

// nodeFor 在卡**钉住的**工作流版本里按名字找节点。
//
// why 一定要用卡钉的版本而不是最新版：工作流是不可变版本化的，卡在建卡时
// 钉了版本号。拿最新版去解释一张老卡，等于用今天的流程图判昨天的卡走到哪了。
func (r *StepRunner) nodeFor(cardID, nodeName string) (ledger.NodeDef, error) {
	card, err := r.St.GetCard(cardID)
	if err != nil {
		return ledger.NodeDef{}, err
	}
	workflow, err := r.St.GetWorkflow(card.WorkflowName, card.WorkflowVersion)
	if err != nil {
		return ledger.NodeDef{}, fmt.Errorf("取卡 %s 钉住的工作流 %s v%d: %w",
			cardID, card.WorkflowName, card.WorkflowVersion, err)
	}
	for _, node := range workflow.Def.Nodes {
		if node.Name == nodeName {
			return node, nil
		}
	}
	return ledger.NodeDef{}, fmt.Errorf("节点 %q 不在卡 %s 的工作流 %s v%d 里",
		nodeName, cardID, card.WorkflowName, card.WorkflowVersion)
}

// dispatchNode 生产 NodeStep.Dispatch：按节点的模板引用 + 单字段覆盖派发。
func (r *StepRunner) dispatchNode() func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
	return func(ctx context.Context, card ledger.Card, node ledger.NodeDef) (string, string, error) {
		target := r.Target
		if target == "" {
			target = node.Override.Target
		}
		result, err := r.Dispatcher.ViaTemplate(ctx, card, TemplateDispatch{
			Template:           node.Template,
			Target:             target,
			DisciplineOverride: node.Override.Discipline,
			ExecutorOverride:   node.Override.Executor,
			ModelOverride:      node.Override.Model,
			CarryCardContext:   node.CarryCardContext,
			PurposeOverride:    node.Override.Purpose,
			OmitAcceptance:     node.OmitAcceptance,
			Extra:              r.Extra,
		})
		if err != nil {
			return "", "", err
		}
		return result.Target, result.Task, nil
	}
}

// awaitNode 生产 NodeStep.Await：等回合终态并取最终报文，取到后归档该 task。
func (r *StepRunner) awaitNode() func(context.Context, string, string) (string, error) {
	return func(ctx context.Context, target, taskID string) (string, error) {
		cl, err := r.Clients(target)
		if err != nil {
			return "", err
		}
		if err := waitForTurnEnd(ctx, func(ctx context.Context) (*proto.Event, error) {
			return cl.WaitEvent(ctx, taskID, false)
		}); err != nil {
			return "", fmt.Errorf("等回合终态: %w", err)
		}
		message, err := clientFinalMessage(ctx, cl, taskID)
		if err != nil {
			return "", err
		}
		// 报文已经拿到，归档只是回收资源；失败不该把报文丢掉，所以带着报文一起返回错误。
		if _, err := cl.Done(ctx, taskID, ""); err != nil {
			slog.Default().Warn("归档节点 task 失败（报文已取到）", "task", taskID, "cause", err)
		}
		return message, nil
	}
}
