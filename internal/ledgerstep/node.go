// 审阅环节与合并环节的执行体。依赖全部经函数字段注入（RunReview/
// Objective/DoMerge），决策逻辑与副作用分离——单测覆盖决策，真机
// 判据覆盖副作用（真派发/真 git）。
package ledgerstep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// Action 环节执行的结论。
type Action string

const (
	ActionPass       Action = "pass"
	ActionContinue   Action = "continue"
	ActionNeedsHuman Action = "needs_human"
	// ActionDispatched 表示本节点只负责把任务派出去，不等结果（Verdict=false）。
	ActionDispatched Action = "dispatched"
	ActionMerged     Action = "merged"
)

// Outcome 环节单次执行结果。
type Outcome struct {
	Action  Action
	Verdict Verdict
	Reason  string
}

// ReviewStep 是 Task 5 完成前保留的兼容装配器；实际决策全部委托 NodeStep。
// 新调用方应直接构造 NodeStep，避免把节点语义重新写死成 review。
type ReviewStep struct {
	St        *ledger.Store
	Step      string
	RunReview func(ctx context.Context, card ledger.Card) (string, error)
}

// RunOnce 以兼容字段装配一个审阅形 NodeStep；NodeStep 承担全部状态副作用。
func (n *ReviewStep) RunOnce(ctx context.Context, cardID string) (Outcome, error) {
	step := &NodeStep{
		St:   n.St,
		Node: ledger.NodeDef{Name: n.Step, Dispatch: true, Verdict: true, Template: "review-generic"},
		Dispatch: func(context.Context, ledger.Card, ledger.NodeDef) (string, string, error) {
			return "compat", "compat", nil
		},
		Await: func(ctx context.Context, target, taskID string) (string, error) {
			card, err := n.St.GetCard(cardID)
			if err != nil {
				return "", err
			}
			return n.RunReview(ctx, card)
		},
	}
	return step.RunOnce(ctx, cardID)
}

// NodeStep 通用节点执行体：一个节点该怎么跑，完全由 Node 上的能力开关决定，
// 本类型不认识「审阅」「合并」这些名字。
//
// 依赖经函数字段注入（Dispatch/Await），决策逻辑与副作用分离——单测覆盖
// 决策，真机判据覆盖副作用。
type NodeStep struct {
	St   *ledger.Store
	Node ledger.NodeDef
	// Dispatch 按节点配置把卡派出去，返回目标机与 task id。
	Dispatch func(ctx context.Context, card ledger.Card, node ledger.NodeDef) (target, taskID string, err error)
	// Await 等该 task 跑到回合终态并取回最终报文。只在 Node.Verdict 时调用。
	Await func(ctx context.Context, target, taskID string) (message string, err error)
}

// maxRounds 返回本节点的轮次封顶：节点没配就用包内默认。
func (n *NodeStep) maxRounds() int {
	if n.Node.MaxRounds > 0 {
		return n.Node.MaxRounds
	}
	return MaxRounds
}

// actor 返回本节点写事件时的署名，形如 node:待审阅。
func (n *NodeStep) actor() string { return "node:" + n.Node.Name }

// haltForHuman 落一条说明性 comment（body 非空时）并打等人标记，返回统一的 Outcome。
//
// why 抽出来：这条「留痕 + 打旗 + 返回」三件套在本文件里出现五次，每次漏掉
// 其中任何一件，卡在看板上都会看着一切正常而实际没人在推它。
func (n *NodeStep) haltForHuman(cardID, reason, body string) (Outcome, error) {
	if body != "" {
		if _, err := n.St.AddComment(cardID, body, "普通", n.actor()); err != nil {
			return Outcome{}, err
		}
	}
	if err := n.St.MarkNeedsHuman(cardID, reason, n.actor()); err != nil {
		return Outcome{}, err
	}
	return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
}

// routeTo 把卡移到 to 列；to 为空表示停在本列（不是错误）。
//
// 返回：移动失败时返回错误原文，由调用方转等人——门槛没过（如「待合并」要求
// 验收判据非空）是常态而不是异常，硬失败会让已经落账的裁决白跑。
func (n *NodeStep) routeTo(cardID, to string) error {
	if to == "" {
		return nil
	}
	card, err := n.St.GetCard(cardID)
	if err != nil {
		return err
	}
	if card.Status == to {
		return nil
	}
	return n.St.MoveCard(cardID, to, card.Status, n.actor())
}

// RunOnce 跑一次本节点。
//
// 参数：cardID 卡。
// 返回：Outcome（下一步动作 + 裁决 + 理由）；只有「本节点根本不该被执行」
// （纯人工列）和账本写失败才返回 error，其余异常一律转成 needs_human 并留痕。
//
// 阻塞行为：Node.Verdict 为真时会阻塞到被派出去的 task 跑到回合终态——几分钟
// 到几十分钟，executor 挂在 waiting_answer 时更久。调用方自行决定要不要放
// goroutine 里跑。
func (n *NodeStep) RunOnce(ctx context.Context, cardID string) (Outcome, error) {
	logger := slog.Default().With("node", n.Node.Name, "card", cardID)
	logger.Info("进入节点",
		"dispatch", n.Node.Dispatch, "verdict", n.Node.Verdict,
		"template", n.Node.Template, "max_rounds", n.maxRounds())

	if !n.Node.Dispatch {
		// 纯人工列没有可执行能力。这不是「什么都不做」而是配置错误——
		// 界面上不该给这种列画执行按钮，走到这里说明调用方绕过了判断。
		logger.Warn("纯人工列被要求执行")
		return Outcome{}, fmt.Errorf("节点 %q 没有 Dispatch 能力，不可执行", n.Node.Name)
	}
	card, err := n.St.GetCard(cardID)
	if err != nil {
		return Outcome{}, err
	}
	base, err := n.St.EffectiveBaseBranch(cardID)
	if err != nil {
		return Outcome{}, err
	}
	for _, human := range n.Node.HumanBases {
		if base != human {
			continue
		}
		reason := fmt.Sprintf("基线 %s 在本节点的人工清单里：不自动执行", base)
		logger.Info("基线命中人工清单，跳过派发", "base", base)
		return n.haltForHuman(cardID, reason, "")
	}
	if n.Node.Verdict {
		events, err := n.St.EventsFromAsc([]string{cardID}, 0, 10000)
		if err != nil {
			return Outcome{}, err
		}
		rounds := CountRounds(events, n.Node.Name)
		logger.Info("读取裁决回合数", "rounds", rounds, "max_rounds", n.maxRounds())
		if rounds >= n.maxRounds() {
			reason := fmt.Sprintf("裁决超轮（%d/%d）", rounds, n.maxRounds())
			logger.Info("回合封顶转等人", "rounds", rounds)
			return n.haltForHuman(cardID, reason, "")
		}
	}

	target, taskID, err := n.Dispatch(ctx, card, n.Node)
	if err != nil {
		// 派发失败同样要上浮到「需要你」：卡上不留痕 = 这张卡在看板上看着
		// 一切正常，而实际没人在推它。原文落 timeline 供取证。
		logger.Warn("派发失败，转等人", "cause", err)
		return n.haltForHuman(cardID, "派发失败", "本节点派发失败：\n"+err.Error())
	}
	logger.Info("已派发", "target", target, "task", taskID)
	if !n.Node.Verdict {
		// 不裁决的节点到此为止：任务在对端跑，进展看卡的事件流与 handoff task。
		logger.Info("节点结束（只派发不裁决）", "action", string(ActionDispatched))
		return Outcome{Action: ActionDispatched}, nil
	}

	message, err := n.Await(ctx, target, taskID)
	if err != nil {
		logger.Warn("未取到报文，转等人", "cause", err)
		return n.haltForHuman(cardID, "未取到裁决报文", "本节点未取到裁决报文：\n"+err.Error())
	}
	verdict, parseErr := ParseVerdict(message)
	if parseErr != nil {
		logger.Info("裁决解析失败转等人", "cause", parseErr)
		return n.haltForHuman(cardID, "裁决解析失败", "裁决解析失败，报文原文：\n"+message)
	}
	if err := n.St.RecordReviewVerdict(cardID, n.Node.Name, verdict.Pass, verdict.Raw, n.actor()); err != nil {
		return Outcome{}, err
	}
	logger.Info("裁决落账", "pass", verdict.Pass, "findings", len(verdict.Findings))
	// 裁决落账即代表本节点这一轮真的跑通了。此前若因派发失败、报文取不到或
	// 裁决解析不了打过等人标记，那条标记已被这一轮推翻，由打它的同一个节点撤回
	// ——不撤的话卡上会一直挂着一面已经不成立的红旗，而看板的「需要你」筛选
	// 正是靠它（2026-08-20 真机看到过陈标记挂在抽屉顶上且 Web 无撤除入口）。
	//
	// 失败只告警不中断：裁决已落账，为一次收尾清理失败而让整个节点报错，
	// 代价比留一条陈标记大。
	if cleared, cerr := n.St.ClearNeedsHumanFrom(cardID, n.actor()); cerr != nil {
		logger.Warn("撤回本节点旧等人标记失败", "cause", cerr)
	} else if cleared {
		logger.Info("已撤回本节点此前的等人标记")
	}

	to, action := n.Node.OnFail, ActionContinue
	if verdict.Pass {
		to, action = n.Node.Next, ActionPass
	}
	if err := n.routeTo(cardID, to); err != nil {
		// 门槛没过是常态（例如「待合并」要求验收判据非空），转等人而不是硬失败：
		// 裁决已经落账了，为一次移动失败把整轮判成错误会让人看不出发生了什么。
		reason := fmt.Sprintf("裁决已落账但移到 %q 失败", to)
		logger.Warn("路由失败转等人", "to", to, "cause", err)
		return n.haltForHuman(cardID, reason, reason+"：\n"+err.Error())
	}
	logger.Info("节点结束", "action", string(action), "moved_to", to)
	return Outcome{Action: action, Verdict: verdict}, nil
}

// MergeStep 合并环节。Objective 跑客观判据（测试+gofmt）；DoMerge 执行
// 合并。基线为空代表主线，永远不自动合并。
type MergeStep struct {
	St        *ledger.Store
	Objective func(ctx context.Context, card ledger.Card, base string) error
	DoMerge   func(ctx context.Context, card ledger.Card, base string) error
	// MainLine 主线分支名，空则取 defaultMainLine。基线等于它（或为空）时
	// 一律不自动合——两者是同一件事的两种写法，见 isMainline。
	MainLine string
}

// defaultMainLine 主线分支的缺省名。
const defaultMainLine = "main"

// isMainline 判定一条基线是不是主线。
//
// why 不能只判空串：spec 说「基线就是 main 时该环节不自动合、直接打
// 『待合并』等人」，空串只是「继承/项目默认主线」的表达之一。只认空串的话，
// `card add --base-branch main` 建出的顶层热修卡会被当成集成线**自动合进
// main**，主线的人工门就此失效（2026-08-19 真机验收发现）。
func (m *MergeStep) isMainline(base string) bool {
	mainLine := m.MainLine
	if mainLine == "" {
		mainLine = defaultMainLine
	}
	return base == "" || base == mainLine
}

// mergeFailureReason 把两个合并前后执行阶段的工作分支缺失统一归类。
// 客观判据和实际合并都可能执行补拉阶梯；若只在后者识别，真实链路通常会先
// 被客观判据截住，用户就拿不到可操作的 handoff pull 提示。
func mergeFailureReason(err error, fallback string) string {
	if errors.Is(err, ErrWorkBranchMissing) {
		return "工作分支缺失：先 handoff pull 再重试"
	}
	return fallback
}

// RunOnce 执行合并决策：主线转人工；集成线先跑客观判据，再尝试合并。
func (m *MergeStep) RunOnce(ctx context.Context, cardID string) (Outcome, error) {
	logger := slog.Default().With("step", "merge", "card", cardID)
	logger.Info("进入合并环节")
	card, err := m.St.GetCard(cardID)
	if err != nil {
		return Outcome{}, err
	}
	base, err := m.St.EffectiveBaseBranch(cardID)
	if err != nil {
		return Outcome{}, err
	}
	if m.isMainline(base) {
		reason := "基线是主线：合并永远人工"
		logger.Info("main 层不自动合")
		if workflow, workflowErr := m.St.GetWorkflow(card.WorkflowName, card.WorkflowVersion); workflowErr == nil {
			for _, state := range workflow.Def.States {
				if state == "待合并" && card.Status != state {
					if moveErr := m.St.MoveCard(cardID, state, card.Status, "node:merge"); moveErr != nil {
						logger.Info("推待合并失败，仅标等人", "err", moveErr)
					} else {
						logger.Info("已推入待合并")
					}
					break
				}
			}
		}
		if err := m.St.MarkNeedsHuman(cardID, reason, "node:merge"); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
	}

	logger.Info("运行合并前客观判据", "base", base)
	if err := m.Objective(ctx, card, base); err != nil {
		reason := mergeFailureReason(err, "合并判据未过")
		logger.Info("客观判据红转等人", "reason", reason, "err", err)
		if _, commentErr := m.St.AddComment(cardID, "合并前客观判据未过：\n"+err.Error(), "普通", "node:merge"); commentErr != nil {
			return Outcome{}, commentErr
		}
		if err := m.St.MarkNeedsHuman(cardID, reason, "node:merge"); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
	}
	logger.Info("客观判据通过，执行合并", "base", base)
	if err := m.DoMerge(ctx, card, base); err != nil {
		reason := mergeFailureReason(err, "合并冲突")
		logger.Info("合并执行失败转等人", "reason", reason, "err", err)
		if _, commentErr := m.St.AddComment(cardID, "合并失败（冲突清单/报错）：\n"+err.Error(), "普通", "node:merge"); commentErr != nil {
			return Outcome{}, commentErr
		}
		if err := m.St.MarkNeedsHuman(cardID, reason, "node:merge"); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
	}
	logger.Info("已自动合回基线并推 origin", "base", base)
	branch, branchErr := taskBranch(m.St, card)
	if branchErr != nil {
		// 分支名取不到不推翻已经完成的合并——合并是真的做了，落账缺一条
		// 比谎报失败好。留 Warn 供事后追。
		logger.Warn("合并已完成但取工作分支名失败，事件缺分支字段", "err", branchErr)
		branch = ""
	}
	if err := m.St.RecordBranchMerged(cardID, branch, base, true, "node:merge"); err != nil {
		// 合并已经完成并推上 origin，不能因落账失败把成功动作伪装成失败。
		logger.Warn("合并已完成但落账失败", "err", err)
	}
	return Outcome{Action: ActionMerged}, nil
}
