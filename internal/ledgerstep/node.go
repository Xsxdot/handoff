// 工作流节点的通用执行体。节点行为由 ledger.NodeDef 的能力开关驱动，
// 依赖经函数字段注入，决策逻辑与副作用分离——单测覆盖决策，真机判据覆盖
// 真派发。
//
// 本地合并已于 2026-08-21 退役：合并改为普通派发节点（Dispatch+Verdict +
// finishing 纪律块），由 executor 在任务分支上完成，协调机不再执行 git 写操作。
package ledgerstep

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// ErrWriteGateClosed 表示本轮运行锁已失去，后续卡写必须停止。
var ErrWriteGateClosed = errors.New("运行锁已失去写权")

// Action 环节执行的结论。
type Action string

const (
	ActionPass       Action = "pass"
	ActionContinue   Action = "continue"
	ActionNeedsHuman Action = "needs_human"
	// ActionDispatched 表示本节点只负责把任务派出去，不等结果（Verdict=false）。
	ActionDispatched Action = "dispatched"
)

// Outcome 环节单次执行结果。
type Outcome struct {
	Action  Action
	Verdict Verdict
	Reason  string
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
	// OutputPath 返回本次派发已经渲染的单一路径；只有 Node.Produces 非 nil 时读取。
	OutputPath func() string
	// Diff 返回本次 task 相对协调者基线的 changed paths；实现方负责把 Client.Diff 投影为路径。
	Diff func(ctx context.Context, target, taskID string) ([]string, error)
	// Attach 将法定 kind/path 以节点 actor 挂到协调者账本；同 kind、path 由 Store 保证幂等。
	Attach func(cardID, kind, path, actor string) error
	// WriteGate 是生产编排注入的卡写闸；nil 表示不设闸。
	WriteGate func() bool
	// PublishWorkBranch 把工作分支推到 origin。nil 表示本轮不发布（单测）。
	// 失败必须 needs_human，不得假装下一台已经能 fetch。
	PublishWorkBranch func(ctx context.Context, target, branch, taskID string) error
	// FinishTask 归档本轮 task。必须在 PublishWorkBranch 之后调用：Done 会回收
	// managed worktree，先归档再 push 会 400「工作目录不存在」。
	FinishTask func(ctx context.Context, target, taskID string) error
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

// gatedWrite 在实际账本写入前确认本轮仍拥有运行锁；关闭时 fail-closed，
// 防止续租失败后的编排继续移列、落裁决、挂附件或改等人标记。
func (n *NodeStep) gatedWrite(action string) error {
	if n.WriteGate == nil || n.WriteGate() {
		return nil
	}
	return fmt.Errorf("%s被拒：%w", action, ErrWriteGateClosed)
}

// haltForHuman 落一条说明性 comment（body 非空时）并打等人标记，返回统一的 Outcome。
//
// why 抽出来：这条「留痕 + 打旗 + 返回」三件套在本文件里出现五次，每次漏掉
// 其中任何一件，卡在看板上都会看着一切正常而实际没人在推它。
func (n *NodeStep) haltForHuman(cardID, reason, body string) (Outcome, error) {
	if err := n.gatedWrite("等人留痕"); err != nil {
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, err
	}
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

// haltForHumanEnsure 与 haltForHuman 同构，但说明评论走 EnsureComment 幂等
// 留痕：同 dedupeKey 不产生第二条评论。供「终态遗留裁决」补解析（契约 §3.7）
// 用——重复驱动同一张卡只留一条说明，避免看板被刷屏；也绝不伪造
// decision_answered（答案是用户的事实，系统不代答）。
func (n *NodeStep) haltForHumanEnsure(cardID, reason, dedupeKey, body string) (Outcome, error) {
	if err := n.gatedWrite("等人留痕"); err != nil {
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, err
	}
	if body != "" {
		if _, err := n.St.EnsureComment(cardID, dedupeKey, body, n.actor()); err != nil {
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
	if err := n.gatedWrite("移列"); err != nil {
		return err
	}
	return n.St.MoveCard(cardID, to, card.Status, n.actor())
}

var reviewLedgerPathPrefixes = [...]string{
	"docs/superpowers/ledgers",
	"docs/ledgers",
}

// isReviewLedgerPath 只允许审阅节点写入两类台账目录自身或其 POSIX 子路径。
// 使用字符串边界而非 filepath，是因为 Diff 契约传的是仓内 POSIX 路径，不能让执行平台
// 的分隔符转换改变白名单；尾斜杠边界也防止 docs/ledgers-extra 越过目录边界。
func isReviewLedgerPath(path string) bool {
	for _, prefix := range reviewLedgerPathPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// reviewReadOnlyViolations 按 Diff 返回顺序保留全部白名单外路径，不去重、不排序，供
// review 节点在裁决落账前形成可审计的普通评论。
func reviewReadOnlyViolations(paths []string) []string {
	violations := make([]string, 0)
	for _, path := range paths {
		if !isReviewLedgerPath(path) {
			violations = append(violations, path)
		}
	}
	return violations
}

// RunOnce 跑一次本节点；review 用途且裁决为 pass 时，会在裁决落账前用 Diff
// 执行只读闸；成功抢救裁决时会为被丢弃字段写普通评论，写评论失败原样返回。
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

	card, err := n.St.GetCard(cardID)
	if err != nil {
		logger.Warn("读取节点所属卡失败", "cause", err)
		return Outcome{}, err
	}
	logger.Debug("读取节点所属卡完成", "workflow", card.WorkflowName,
		"workflow_version", card.WorkflowVersion, "status", card.Status)
	// 终态遗留裁决补解析（B156.2 契约 §3.7）：卡已到终态但仍挂着 open 裁决，
	// 是协调者死亡窗口的产物。继续推进没有意义（终态没有下一列），而答案是
	// 用户的事实，系统绝不代答（绝不伪造 decision_answered）。转等人，并走
	// EnsureComment 幂等留痕——重复驱动同一张卡不产生第二条说明评论。
	// 检测放 !n.Node.Dispatch 检查之前：真实恢复场景是终态叶子列（无 Dispatch）
	// 被再驱动，runner.go 对纯人工列直接调 RunOnce 不装配 WriteGate，放后面
	// 会被「纯人工列不可执行」的配置错误吞掉。
	if card.Status == ledger.StatusDone || card.Status == ledger.StatusClosed {
		open, openErr := n.St.ListDecisions(true)
		if openErr != nil {
			logger.Warn("读取 open 裁决失败", "cause", openErr)
			return Outcome{}, openErr
		}
		var leftover []ledger.Decision
		for _, d := range open {
			if d.CardID == cardID {
				leftover = append(leftover, d)
			}
		}
		if len(leftover) > 0 {
			ids := make([]string, 0, len(leftover))
			for _, d := range leftover {
				ids = append(ids, fmt.Sprint(d.ID))
			}
			reason := fmt.Sprintf("终态遗留裁决：卡已到终态（%s）但仍有 %d 条未答复裁决", card.Status, len(leftover))
			body := "本卡已到终态但仍有 open 裁决未答复（协调者死亡窗口的产物）。答案属于用户，系统不代答（绝不伪造 decision_answered）——请恢复推进的协调者把裁决转达用户。open 裁决 id：" + strings.Join(ids, ", ")
			logger.Warn("检测到终态遗留裁决，转等人", "status", card.Status, "open_decisions", len(leftover))
			return n.haltForHumanEnsure(cardID, reason, "终态遗留裁决:"+cardID, body)
		}
	}
	if !n.Node.Dispatch {
		// 纯人工列没有可执行能力。这不是「什么都不做」而是配置错误——
		// 界面上不该给这种列画执行按钮，走到这里说明调用方绕过了判断。
		logger.Warn("纯人工列被要求执行", "workflow", card.WorkflowName,
			"hint", "用 handoff workflow put 发布节点形定义")
		return Outcome{}, fmt.Errorf("节点 %q 没有 Dispatch 能力，不可执行；这条流是老定义 / 这一列是人工列，要让它可派发用 `handoff workflow put %s --file <定义文件>`",
			n.Node.Name, card.WorkflowName)
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
	if n.FinishTask != nil {
		defer func() {
			if finErr := n.FinishTask(ctx, target, taskID); finErr != nil {
				logger.Warn("归档节点 task 失败（报文已取到）", "cause", finErr)
			} else {
				logger.Info("节点 task 已归档")
			}
		}()
	}
	verdict, parseErr := ParseVerdict(message)
	if parseErr != nil {
		logger.Info("裁决解析失败转等人", "cause", parseErr)
		return n.haltForHuman(cardID, "裁决解析失败", "裁决解析失败，报文原文：\n"+message)
	}
	if verdict.salvaged && (verdict.notesDropped || verdict.findingsDropped) {
		dropped := make([]string, 0, 2)
		if verdict.notesDropped {
			dropped = append(dropped, "notes")
		}
		if verdict.findingsDropped {
			dropped = append(dropped, "findings")
		}
		body := fmt.Sprintf("裁决 JSON 已抢救，仍按 %t 路由；以下字段因 JSON 损坏被丢弃：%s。Raw 保留在裁决事件中。",
			verdict.Pass, strings.Join(dropped, "、"))
		if err := n.gatedWrite("裁决抢救留痕"); err != nil {
			return Outcome{}, err
		}
		if _, err := n.St.AddComment(cardID, body, "普通", n.actor()); err != nil {
			logger.Warn("裁决抢救留痕失败", "dropped", dropped, "cause", err)
			return Outcome{}, err
		}
		logger.Warn("裁决抢救字段已丢弃并留普通评论", "dropped", dropped)
	}
	// review 节点的只读闸必须在 RecordReviewVerdict 前取 Diff：Diff 失败不应
	// 消耗轮次，越界则把内存 verdict 改为 fail 后走已有 on_fail 路由。
	if verdict.Pass && n.Node.Override.Purpose == ledger.PurposeReview {
		logger.Info("开始校验 review 节点只读改动", "target", target, "task", taskID)
		if n.Diff == nil {
			err := fmt.Errorf("review 节点 diff 依赖未装配")
			logger.Error("读取审阅改动失败", "target", target, "task", taskID, "cause", err)
			return n.haltForHuman(cardID, "读取审阅改动失败",
				"本节点无法确认审阅轮是否只读：\n"+err.Error())
		}
		changedPaths, diffErr := n.Diff(ctx, target, taskID)
		if diffErr != nil {
			logger.Warn("读取审阅改动失败", "target", target, "task", taskID, "cause", diffErr)
			return n.haltForHuman(cardID, "读取审阅改动失败",
				"本节点无法确认审阅轮是否只读：\n"+diffErr.Error())
		}
		violations := reviewReadOnlyViolations(changedPaths)
		if len(violations) == 0 {
			logger.Info("review 节点只读改动通过", "target", target, "task", taskID,
				"changed_paths", changedPaths)
		} else {
			verdict.Pass = false
			body := "审阅节点检测到白名单外改动，按 fail 处理；越界路径：\n" +
				strings.Join(violations, "\n")
			if err := n.gatedWrite("审阅只读违规留痕"); err != nil {
				logger.Warn("审阅只读违规留痕被写闸拒绝", "target", target, "task", taskID,
					"paths", violations, "cause", err)
				return Outcome{}, err
			}
			if _, err := n.St.AddComment(cardID, body, "普通", n.actor()); err != nil {
				logger.Warn("审阅只读违规评论写入失败", "target", target, "task", taskID,
					"paths", violations, "cause", err)
				return Outcome{}, err
			}
			logger.Warn("review 节点只读闸未通过，按 fail 路由", "target", target, "task", taskID,
				"out_of_scope_paths", violations)
		}
	}
	if err := n.gatedWrite("裁决落账"); err != nil {
		return Outcome{}, err
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
	if err := n.gatedWrite("撤等人标记"); err != nil {
		return Outcome{}, err
	}
	if cleared, cerr := n.St.ClearNeedsHumanFrom(cardID, n.actor()); cerr != nil {
		logger.Warn("撤回本节点旧等人标记失败", "cause", cerr)
	} else if cleared {
		logger.Info("已撤回本节点此前的等人标记")
	}

	// 先挂产出再路由：下一列的附件 gate 必须能看到本轮刚确认的文件。
	if verdict.Pass && n.Node.Produces != nil {
		output := n.Node.Produces
		if n.OutputPath == nil || n.Diff == nil || n.Attach == nil {
			logger.Error("节点声明产出但输出依赖未装配",
				"kind", output.Kind, "declared_path", output.Path)
			return n.haltForHuman(cardID, "产出物校验未装配",
				"本节点声明了产出物，但协调者未装配输出校验依赖")
		}
		declaredPath := n.OutputPath()
		logger.Info("开始校验节点产出物",
			"kind", output.Kind, "declared_path", declaredPath, "target", target, "task", taskID)
		changedPaths, diffErr := n.Diff(ctx, target, taskID)
		if diffErr != nil {
			logger.Warn("读取本轮改动失败，转等人",
				"kind", output.Kind, "declared_path", declaredPath,
				"target", target, "task", taskID, "cause", diffErr)
			return n.haltForHuman(cardID, "读取产出物改动失败",
				"本节点无法确认产出物是否在本轮改动中：\n"+diffErr.Error())
		}
		logger.Info("本轮改动已取得",
			"kind", output.Kind, "declared_path", declaredPath,
			"changed_paths", changedPaths)
		if declaredPath == "" || !containsPath(changedPaths, declaredPath) {
			body := "本节点要求的产出物路径：\n" + declaredPath +
				"\n本轮实际改动文件：\n" + changedPathsText(changedPaths)
			if actualPath, ok := datePrefixedDeclaredPath(declaredPath, changedPaths); ok {
				body += "\n检测到日期前缀文件名：" + actualPath +
					"；这是日期前缀，请改名为：" + declaredPath
				logger.Warn("检测到日期前缀法定产出",
					"kind", output.Kind, "declared_path", declaredPath,
					"changed_paths", changedPaths, "actual_path", actualPath,
					"cause", "产出路径必须逐字匹配")
			} else {
				logger.Warn("法定产出物未出现在本轮改动",
					"kind", output.Kind, "declared_path", declaredPath,
					"changed_paths", changedPaths, "cause", "未找到精确路径或日期前缀版本")
			}
			return n.haltForHuman(cardID, "缺少约定产出物", body)
		}
		if err := n.gatedWrite("挂附件"); err != nil {
			return Outcome{}, err
		}
		if attachErr := n.Attach(cardID, output.Kind, declaredPath, n.actor()); attachErr != nil {
			logger.Warn("挂载节点产出物失败，继续尝试路由",
				"kind", output.Kind, "path", declaredPath, "target", target, "task", taskID, "cause", attachErr)
		} else {
			logger.Info("节点产出物已挂载",
				"kind", output.Kind, "path", declaredPath, "actor", n.actor())
		}
	}

	if verdict.Pass && n.PublishWorkBranch != nil {
		info, workErr := n.St.WorkBranch(cardID)
		if workErr != nil && !errors.Is(workErr, ledger.ErrNotFound) {
			logger.Error("pass 后读取工作分支失败", "cause", workErr)
			return n.haltForHuman(cardID, "读取工作分支失败",
				"裁决已通过，但无法读取工作分支去推 origin：\n"+workErr.Error())
		}
		if workErr == nil && info.Branch != "" {
			already, publishedErr := n.St.WorkBranchPublished(cardID, info.Branch)
			if publishedErr != nil {
				logger.Error("查询工作分支是否已发布失败", "branch", info.Branch, "cause", publishedErr)
				return n.haltForHuman(cardID, "查询工作分支发布状态失败",
					"裁决已通过，但无法确认工作分支是否已在 origin：\n"+publishedErr.Error())
			}
			if already {
				// implement 已经推过并归档了 task；review 再拿那条 WorkBranch
				// 去 push 会打到已回收的 worktree（B346）。账本发布事件才是
				// 「系统推过」的事实，不拿 origin 上碰巧有同名分支冒充。
				logger.Info("工作分支已在 origin，跳过再推", "branch", info.Branch,
					"target", info.Target, "task", info.TaskID)
			} else {
				pushTarget, pushTask := info.Target, info.TaskID
				if pushTarget == "" {
					pushTarget = target
				}
				if pushTask == "" {
					pushTask = taskID
				}
				logger.Info("开始把工作分支推到 origin", "branch", info.Branch,
					"target", pushTarget, "task", pushTask)
				if pubErr := n.PublishWorkBranch(ctx, pushTarget, info.Branch, pushTask); pubErr != nil {
					logger.Error("工作分支未能推到 origin", "branch", info.Branch,
						"target", pushTarget, "task", pushTask, "cause", pubErr)
					return n.haltForHuman(cardID, "工作分支未能推到 origin",
						"裁决已通过，但 git push origin "+info.Branch+" 失败：\n"+pubErr.Error())
				}
				if err := n.gatedWrite("工作分支 origin 发布落账"); err != nil {
					return Outcome{}, err
				}
				if recErr := n.St.RecordWorkBranchPublished(cardID, info.Branch, pushTarget, pushTask, n.actor()); recErr != nil {
					logger.Error("工作分支发布落账失败", "branch", info.Branch, "cause", recErr)
					return n.haltForHuman(cardID, "工作分支发布落账失败",
						"origin 已推送，但账本未能记下发布事件：\n"+recErr.Error())
				}
				logger.Info("工作分支已发布到 origin", "branch", info.Branch, "target", pushTarget)
			}
		}
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

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

// datePrefixedDeclaredPath 只识别与 declaredPath 同目录、且 basename 形如
// YYYY-MM-DD-+声明 basename 的改名错误；其它日期文件仍按普通缺失处理。
func datePrefixedDeclaredPath(declaredPath string, changedPaths []string) (actualPath string, ok bool) {
	if declaredPath == "" {
		return "", false
	}
	declaredDir := filepath.Dir(declaredPath)
	declaredBase := filepath.Base(declaredPath)
	const datePrefixLength = len("YYYY-MM-DD-")
	for _, changedPath := range changedPaths {
		if filepath.Dir(changedPath) != declaredDir {
			continue
		}
		actualBase := filepath.Base(changedPath)
		if len(actualBase) != datePrefixLength+len(declaredBase) ||
			actualBase[datePrefixLength:] != declaredBase ||
			actualBase[4] != '-' || actualBase[7] != '-' || actualBase[10] != '-' {
			continue
		}
		validDigits := true
		for i := 0; i < 10; i++ {
			if i == 4 || i == 7 {
				continue
			}
			if actualBase[i] < '0' || actualBase[i] > '9' {
				validDigits = false
				break
			}
		}
		if validDigits {
			return changedPath, true
		}
	}
	return "", false
}
