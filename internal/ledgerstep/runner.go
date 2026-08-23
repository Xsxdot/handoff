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
	"time"

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
	// Session 是本次节点运行的驱动标识。CLI 使用带 pid 的运行会话，
	// agentd 使用请求 actor；它必须能区分两个并发驱动者。
	Session string
	// Clients 按 target 名取一个已装配好的 agentd 客户端。
	//
	// why 这里要的是客户端而不是 (addr, token)：relay 形态的机器根本没有 addr，
	// 拿地址自己 client.New 对它们恒失败（会退化成一个没有 Host 的 URL）。
	// 选路归 agentd 的 target 客户端池管，本包只消费。
	Clients func(target string) (*client.Client, error)
	// Target 覆盖节点/模板里的目标机；空则用节点覆盖或模板的 target。
	Target string
	// Executor/Model 是本次 CLI 节点派发的一次性覆盖；空值表示不覆盖该字段。
	// 当 Executor 非空而 Model 为空时，ViaTemplate 会按成对规则切断下层模型。
	Executor string
	Model    string
	// Extra 本次执行的临时补充说明，透传进 prompt 的第三段；可为空。
	Extra string
	// Now 只为路径日期提供可注入时钟；nil 使用 time.Now。
	Now func() time.Time
}

// Run 跑一次节点。
//
// 参数：cardID 卡；nodeName 节点名（= 看板的列名），从卡钉住的工作流版本里查。
// 返回：Outcome；节点不存在、没有 Dispatch 能力或执行内部失败时返回错误。
//
// 阻塞行为：节点开了 Verdict 时会阻塞到被派出去的 task 跑到回合终态
// （几分钟到几十分钟，executor 挂在 waiting_answer 时更久）。调用方要么
// 自己在 goroutine 里跑（agentd 就是这么做的），要么接受前台阻塞（CLI）。
// 认领时刻不会自动续期；异常遗留归属由 takeover/release 显式处置。
func (r *StepRunner) Run(ctx context.Context, cardID, nodeName string) (Outcome, error) {
	logger := slog.Default().With("card", cardID, "node", nodeName)
	logger.Info("进入节点执行")
	node, err := r.nodeFor(cardID, nodeName)
	if err != nil {
		logger.Warn("读取节点失败", "cause", err)
		return Outcome{}, err
	}
	outputPath := ""
	nodeStep := &NodeStep{
		St:         r.St,
		Node:       node,
		Dispatch:   r.dispatchNode(&outputPath),
		Await:      r.awaitNode(),
		OutputPath: func() string { return outputPath },
		Diff:       r.diffNode(),
		Attach: func(cardID, kind, path, actor string) error {
			return r.St.AttachFile(cardID, kind, path, actor)
		},
	}
	if !node.Dispatch {
		// 纯人工列没有执行能力，不应因为被误点而留下驱动归属。
		logger.Info("纯人工节点跳过驱动认领")
		return nodeStep.RunOnce(ctx, cardID)
	}

	session := r.Session
	if session == "" && r.Dispatcher != nil {
		// 保持直接构造 StepRunner 的旧调用方可用；生产装配会显式传入会话，
		// 这里仅作为测试和内部调用的兼容兜底。
		session = r.Dispatcher.Actor
	}
	if session == "" {
		err := fmt.Errorf("节点驱动会话未设置")
		logger.Error("节点执行被拒", "cause", err)
		return Outcome{}, err
	}
	if err := r.St.ClaimDriver(cardID, session); err != nil {
		logger.Warn("认领节点驱动失败", "session", session, "cause", err)
		return Outcome{}, fmt.Errorf("认领节点驱动: %w", err)
	}
	logger.Info("节点驱动已认领", "session", session)
	defer func() {
		if err := r.St.ReleaseCard(cardID, session); err != nil {
			logger.Warn("释放节点驱动失败", "session", session, "cause", err)
			return
		}
		logger.Info("节点驱动已释放", "session", session)
	}()
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
func (r *StepRunner) dispatchNode(outputPath *string) func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
	return func(ctx context.Context, card ledger.Card, node ledger.NodeDef) (string, string, error) {
		target := r.Target
		if target == "" {
			target = node.Override.Target
		}
		executor := node.Override.Executor
		model := node.Override.Model
		if r.Executor != "" {
			// CLI executor 与 model 是同层覆盖。只有 CLI executor 真正
			// 换掉节点 executor 时，空 model 才切断节点/模板的下层模型；
			// 同名 executor 是显式重述，不应丢掉节点 model。
			executor = r.Executor
			if r.Model != "" || r.Executor != node.Override.Executor {
				model = r.Model
			}
		} else if r.Model != "" {
			model = r.Model
		}
		renderedPath := ""
		if node.Produces != nil {
			renderedPath = RenderOutputPath(node.Produces.Path, card, node, r.now())
		}
		if outputPath != nil {
			*outputPath = renderedPath
		}
		slog.Default().Info("准备派发节点", "card", card.ID, "node", node.Name,
			"target", target, "kind", outputKind(node), "output_path", renderedPath)
		result, err := r.Dispatcher.ViaTemplate(ctx, card, TemplateDispatch{
			Template:           node.Template,
			Target:             target,
			DisciplineOverride: node.Override.Discipline,
			ExecutorOverride:   executor,
			ModelOverride:      model,
			CarryCardContext:   node.CarryCardContext,
			PurposeOverride:    node.Override.Purpose,
			OmitAcceptance:     node.OmitAcceptance,
			Extra:              r.Extra,
			OutputPath:         renderedPath,
		})
		if err != nil {
			slog.Default().Warn("节点派发失败", "card", card.ID, "node", node.Name,
				"target", target, "output_path", renderedPath, "cause", err)
			return "", "", err
		}
		slog.Default().Info("节点派发完成", "card", card.ID, "node", node.Name,
			"target", result.Target, "task", result.Task, "output_path", renderedPath)
		return result.Target, result.Task, nil
	}
}

// now 返回路径渲染使用的时钟；可注入固定时间以保证同一运行的测试和审计稳定。
func (r *StepRunner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func outputKind(node ledger.NodeDef) string {
	if node.Produces == nil {
		return ""
	}
	return node.Produces.Kind
}

// diffNode 只通过已装配的 Client.Diff 取得本轮改动，再投影成路径清单。
// 不访问目标机的其它文件 API，确保产出存在性判定仍以同一 diff 通道为准。
func (r *StepRunner) diffNode() func(context.Context, string, string) ([]string, error) {
	return func(ctx context.Context, target, taskID string) ([]string, error) {
		logger := slog.Default().With("target", target, "task", taskID)
		logger.Info("读取节点本轮 diff")
		if r.Clients == nil {
			err := fmt.Errorf("节点 diff 客户端未装配")
			logger.Warn("取得节点 diff 客户端失败", "cause", err)
			return nil, err
		}
		cl, err := r.Clients(target)
		if err != nil {
			logger.Warn("取得节点 diff 客户端失败", "cause", err)
			return nil, err
		}
		raw, err := cl.Diff(ctx, taskID, "")
		if err != nil {
			logger.Warn("取得节点 diff 失败", "cause", err)
			return nil, err
		}
		paths := ChangedPaths(raw)
		logger.Info("节点本轮 diff 已投影", "changed_paths", paths)
		return paths, nil
	}
}

// awaitNode 生产 NodeStep.Await：等回合终态并取最终报文，取到后归档该 task。
func (r *StepRunner) awaitNode() func(context.Context, string, string) (string, error) {
	return func(ctx context.Context, target, taskID string) (string, error) {
		if r.Clients == nil {
			err := fmt.Errorf("节点等待客户端未装配")
			slog.Default().Warn("取得节点等待客户端失败", "target", target, "task", taskID, "cause", err)
			return "", err
		}
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
