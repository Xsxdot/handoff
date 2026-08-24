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
	"sync"
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
	// Session 是本次节点运行的发起方**归属身份**（人尺度，cli:user@host 档）。
	// 它只用于认领「这张卡归谁在推」，不再承担运行互斥——互斥归 RunLock。
	Session string
	// RunHolder 是承载本次编排的那次运行的标识，由 agentd 在每次启动编排时
	// 生成（全局唯一、含机器线索）；它是卡运行锁的持有者。空 = 未装配，
	// 运行锁路径在实现轮必须拒绝放行。
	RunHolder string
	// RenewBeat 续租节拍源；nil 时生产路径使用 RunLockRenewInterval ticker。
	RenewBeat <-chan time.Time
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
// 返回：Outcome；入口失败先落 needs_human 与原文评论，再返回错误。
//
// 阻塞行为：节点开了 Verdict 时会阻塞到被派出去的 task 跑到回合终态
// （几分钟到几十分钟，executor 挂在 waiting_answer 时更久）。调用方要么
// 自己在 goroutine 里跑（agentd 就是这么做的），要么接受前台阻塞（CLI）。
// 归属锁持久保留；运行锁由本轮续租并在回合结束释放。
func (r *StepRunner) Run(ctx context.Context, cardID, nodeName string) (Outcome, error) {
	logger := slog.Default().With("card", cardID, "node", nodeName, "run_holder", r.RunHolder)
	logger.Info("进入节点执行")
	node, err := r.nodeFor(cardID, nodeName)
	if err != nil {
		logger.Warn("读取节点失败", "cause", err)
		o, haltErr := r.haltEntrypoint(cardID, nodeName, "节点解不开",
			fmt.Sprintf("本节点无法从卡钉住的工作流里解开：%s", err.Error()))
		if haltErr != nil {
			return Outcome{}, haltErr
		}
		return o, err
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
		logger.Info("纯人工节点跳过锁与认领")
		return nodeStep.RunOnce(ctx, cardID)
	}

	session := r.Session
	if session == "" && r.Dispatcher != nil {
		// 保持直接构造 StepRunner 的旧调用方可用；生产装配会显式传入会话，
		// 这里仅作为测试和内部调用的兼容兜底。
		session = r.Dispatcher.Actor
	}
	if session == "" {
		err := fmt.Errorf("节点归属会话未设置")
		logger.Error("节点执行被拒", "cause", err)
		o, haltErr := r.haltEntrypoint(cardID, nodeName, "会话未设置",
			"本节点执行被拒：发起方归属会话未设置。\n"+err.Error())
		if haltErr != nil {
			return Outcome{}, haltErr
		}
		return o, err
	}
	if r.RunHolder == "" {
		err := fmt.Errorf("运行标识未装配（RunHolder 为空）")
		logger.Error("运行锁路径拒绝放行", "cause", err)
		return r.haltEntrypoint(cardID, nodeName, "运行标识缺失", "本节点执行被拒："+err.Error())
	}
	lock, acquired, err := r.St.AcquireRunLock(cardID, nodeName, r.RunHolder, ledger.RunLockTTL)
	if err != nil {
		logger.Error("取得运行锁失败", "cause", err)
		o, haltErr := r.haltEntrypoint(cardID, nodeName, "取得运行锁失败", "本节点取得运行锁失败：\n"+err.Error())
		if haltErr != nil {
			return Outcome{}, fmt.Errorf("取得运行锁: %w", err)
		}
		return o, fmt.Errorf("取得运行锁: %w", err)
	}
	if !acquired {
		detail := fmt.Sprintf("卡正由 %s 运行节点 %s，租期到 %s",
			lock.Holder, lock.Node, lock.ExpiresAt.Format(time.RFC3339))
		logger.Warn("运行锁被拒", "lock_holder", lock.Holder,
			"lock_node", lock.Node, "expires_at", lock.ExpiresAt.Format(time.RFC3339))
		o, haltErr := r.haltEntrypoint(cardID, nodeName, "运行锁被他方占用",
			"本节点无法开跑："+detail+"。\n原因原文：AcquireRunLock 返回 acquired=false")
		if haltErr != nil {
			return Outcome{}, haltErr
		}
		return o, fmt.Errorf("运行锁被拒：%s", detail)
	}
	logger.Info("运行锁已取得", "expires_at", lock.ExpiresAt.Format(time.RFC3339))
	defer func() {
		if err := r.St.ReleaseRunLock(cardID, r.RunHolder); err != nil {
			logger.Warn("释放运行锁失败", "cause", err)
			return
		}
		logger.Info("运行锁已释放", "holder", r.RunHolder)
	}()

	if err := r.St.ClaimCard(cardID, session); err != nil {
		logger.Warn("归属认领被拒", "session", session, "cause", err)
		o, haltErr := r.haltEntrypoint(cardID, nodeName, "归属认领被拒",
			fmt.Sprintf("以 %s 认领这张卡被拒：\n%s", session, err.Error()))
		if haltErr != nil {
			return Outcome{}, haltErr
		}
		return o, fmt.Errorf("认领归属: %w", err)
	}
	logger.Info("归属已认领", "session", session)

	done := make(chan struct{})
	finished := make(chan struct{})
	var lostOnce sync.Once
	noteLost := func() {
		lostOnce.Do(func() {
			body := fmt.Sprintf("本轮运行锁已被接手（holder=%s）：本回合自即刻起停止对这张卡的移列、裁决、附件与等人标记写入；已在跑的远端任务继续等待并照常归档。", r.RunHolder)
			if _, err := r.St.AddComment(cardID, body, "普通", "node:"+nodeName); err != nil {
				logger.Warn("失去写权说明落卡失败", "cause", err)
				return
			}
			logger.Info("失去写权说明已落卡")
		})
	}
	beats := r.RenewBeat
	if beats == nil {
		ticker := time.NewTicker(ledger.RunLockRenewInterval)
		defer ticker.Stop()
		beats = ticker.C
	}
	go func() {
		defer close(finished)
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-beats:
				ok, err := r.St.RenewRunLock(cardID, r.RunHolder, ledger.RunLockTTL)
				switch {
				case err != nil:
					logger.Warn("续租出错（下一节拍重试）", "cause", err)
				case ok:
					logger.Info("运行锁已续租", "ttl", ledger.RunLockTTL.String())
				default:
					noteLost()
					logger.Warn("续租被拒：本轮已失去对卡的写权", "holder", r.RunHolder)
					return
				}
			}
		}
	}()
	defer func() { <-finished }()
	defer close(done)

	nodeStep.WriteGate = func() bool {
		ok, err := r.St.RenewRunLock(cardID, r.RunHolder, ledger.RunLockTTL)
		if err != nil {
			logger.Warn("写闸续租判定出错（按失权处理）", "cause", err)
			noteLost()
			return false
		}
		if !ok {
			noteLost()
		}
		return ok
	}
	defer func() {
		logger.Info("节点执行收口", "session", session)
	}()
	return nodeStep.RunOnce(ctx, cardID)
}

// haltEntrypoint 把 RunOnce 之外的入口失败同步写入卡事件流，保证 card wait 有唯一可见证据。
func (r *StepRunner) haltEntrypoint(cardID, nodeName, reason, body string) (Outcome, error) {
	logger := slog.Default().With("card", cardID, "node", nodeName)
	if _, err := r.St.AddComment(cardID, body, "普通", "node:"+nodeName); err != nil {
		logger.Error("入口失败落卡：写评论失败", "reason", reason, "cause", err)
		return Outcome{}, fmt.Errorf("入口失败落卡（原始原因：%s）：%w", body, err)
	}
	if err := r.St.MarkNeedsHuman(cardID, reason, "node:"+nodeName); err != nil {
		logger.Error("入口失败落卡：打等人标记失败", "reason", reason, "cause", err)
		return Outcome{}, fmt.Errorf("入口失败打等人标记（原始原因：%s）：%w", body, err)
	}
	logger.Info("入口失败已落卡", "reason", reason)
	return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
}

// nodeFor 在卡**钉住的**工作流版本里按名字找节点。
//
// why 一定要用卡钉的版本而不是最新版：工作流是不可变版本化的，卡在建卡时
// 钉了版本号。拿最新版去解释一张老卡，等于用今天的流程图判昨天的卡走到哪了。
// ResolveNode 查一张卡在它钉住的工作流里的某个节点定义。
//
// 参数：st 是账本；cardID 是卡号；nodeName 是节点名（= 看板列名）。
// 返回：卡不存在时原样透传 ledger.ErrNotFound（调用方据此映射 404）；工作流取不到、
// 或节点名不在该工作流里，返回带卡号与工作流版本的描述性错误。
//
// 为什么导出：受理一个节点请求是有副作用的（认领驱动、派任务），而编排跑在后台
// goroutine 里——HTTP 层必须能在受理之前用同一份判断拒掉无效输入，否则卡号或节点名
// 打错只会换来一句「已受理」，失败连一条卡事件都留不下。两处必须查同一个真相源，
// 各查各的迟早漂移。
func ResolveNode(st *ledger.Store, cardID, nodeName string) (ledger.NodeDef, error) {
	card, err := st.GetCard(cardID)
	if err != nil {
		return ledger.NodeDef{}, err
	}
	workflow, err := st.GetWorkflow(card.WorkflowName, card.WorkflowVersion)
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

func (r *StepRunner) nodeFor(cardID, nodeName string) (ledger.NodeDef, error) {
	return ResolveNode(r.St, cardID, nodeName)
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
