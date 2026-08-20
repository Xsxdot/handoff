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
	ActionMerged     Action = "merged"
)

// Outcome 环节单次执行结果。
type Outcome struct {
	Action  Action
	Verdict Verdict
	Reason  string
}

// ReviewStep 审阅环节。RunReview 跑一次审阅并返回最终报文。
type ReviewStep struct {
	St        *ledger.Store
	Step      string
	RunReview func(ctx context.Context, card ledger.Card) (string, error)
}

// RunOnce 执行一轮审阅：查回合 → 超限转等人 → 跑审阅 → 解析裁决 →
// 落账 → 给出下一步。
func (n *ReviewStep) RunOnce(ctx context.Context, cardID string) (Outcome, error) {
	logger := slog.Default().With("step", n.Step, "card", cardID)
	logger.Info("进入审阅环节")
	card, err := n.St.GetCard(cardID)
	if err != nil {
		return Outcome{}, err
	}
	events, err := n.St.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		return Outcome{}, err
	}
	rounds := CountRounds(events, n.Step)
	logger.Info("读取审阅回合数", "rounds", rounds, "max_rounds", MaxRounds)
	if rounds >= MaxRounds {
		reason := fmt.Sprintf("审阅超轮（%d/%d）", rounds, MaxRounds)
		logger.Info("回合封顶转等人", "rounds", rounds)
		if err := n.St.MarkNeedsHuman(cardID, reason, "node:"+n.Step); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
	}
	message, err := n.RunReview(ctx, card)
	if err != nil {
		// 审阅没跑出报文（派发失败、卡在工单上、连接断了……）同样要上浮到
		// 「需要你」，不能只让调用方拿到一个错误码：卡上不留痕 = 这张卡在
		// 看板上看着一切正常，而实际没人在推它。原文落 timeline 供取证。
		logger.Warn("审阅未取到报文，转等人", "err", err)
		if _, cerr := n.St.AddComment(cardID,
			"审阅未取到裁决报文：\n"+err.Error(), "普通", "node:"+n.Step); cerr != nil {
			return Outcome{}, cerr
		}
		if merr := n.St.MarkNeedsHuman(cardID, "审阅未取到报文", "node:"+n.Step); merr != nil {
			return Outcome{}, merr
		}
		return Outcome{Action: ActionNeedsHuman, Reason: "审阅未取到报文"}, nil
	}
	verdict, parseErr := ParseVerdict(message)
	if parseErr != nil {
		logger.Info("裁决解析失败转等人", "err", parseErr)
		if _, err := n.St.AddComment(cardID, "裁决解析失败，审阅原文：\n"+message, "普通", "node:"+n.Step); err != nil {
			return Outcome{}, err
		}
		if err := n.St.MarkNeedsHuman(cardID, "裁决解析失败", "node:"+n.Step); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: "裁决解析失败"}, nil
	}
	if err := n.St.RecordReviewVerdict(cardID, n.Step, verdict.Pass, verdict.Raw, "node:"+n.Step); err != nil {
		return Outcome{}, err
	}
	logger.Info("审阅裁决完成", "pass", verdict.Pass, "findings", len(verdict.Findings))
	// 裁决落账即代表本环节这一轮真的跑通了。此前若因为派发失败、报文取不到
	// 或裁决解析不了打过等人标记，那条标记已被这一轮推翻，由打它的同一个节点
	// 撤回——不撤的话卡上会一直挂着一面已经不成立的红旗，而看板的「需要你」
	// 筛选正是靠它（2026-08-20 真机看到：第二轮出了裁决，第一轮的
	// 「审阅未取到报文」仍挂在抽屉顶上，且 Web 上没有任何撤除入口）。
	//
	// 失败只告警不中断：裁决已经落账，为一次收尾清理失败而让整个环节报错，
	// 代价比留一条陈标记大。
	if cleared, cerr := n.St.ClearNeedsHumanFrom(cardID, "node:"+n.Step); cerr != nil {
		logger.Warn("撤回本环节旧等人标记失败", "err", cerr)
	} else if cleared {
		logger.Info("已撤回本环节此前的等人标记")
	}
	if verdict.Pass {
		return Outcome{Action: ActionPass, Verdict: verdict}, nil
	}
	return Outcome{Action: ActionContinue, Verdict: verdict}, nil
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
