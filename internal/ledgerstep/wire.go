// 环节注入点的生产实现：审阅派发走 dispatch 通道 + wait 终态 + 取
// 报文；客观判据/合并在协调机本地工作区跑真命令。审阅 task 生命周期
// 在此收口（裁决落账后 done 归档，不留孤儿）。
package ledgerstep

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
)

// NewDispatchReview 生产 RunReview：按 review 模板派发审阅 task →
// wait 终态 → 取最终报文 → done 归档 → 返回报文。
func NewDispatchReview(st *ledger.Store,
	dispatch func(ctx context.Context, cardID, template string) (target, taskID string, err error),
	clients func(target string) (*client.Client, error),
) func(ctx context.Context, card ledger.Card) (string, error) {
	return func(ctx context.Context, card ledger.Card) (string, error) {
		target, taskID, err := dispatch(ctx, card.ID, "review-generic")
		if err != nil {
			return "", fmt.Errorf("派发审阅: %w", err)
		}
		cl, err := clients(target)
		if err != nil {
			return "", err
		}
		if err := waitForTurnEnd(ctx, func(ctx context.Context) (*proto.Event, error) {
			return cl.WaitEvent(ctx, taskID, false)
		}); err != nil {
			return "", fmt.Errorf("等审阅终态: %w", err)
		}
		message, err := clientFinalMessage(ctx, cl, taskID)
		if err != nil {
			return "", err
		}
		if _, err := cl.Done(ctx, taskID, ""); err != nil {
			return message, fmt.Errorf("归档审阅 task（报文已取到，仅归档失败）: %w", err)
		}
		return message, nil
	}
}

// waitForTurnEnd 反复 wait 直到出现回合终态事件（completed/turn_failed/
// failed），中途的权限门与工单一律跳过继续等。
//
// why 要循环：WaitEvent 返回的是「首个可动作事件」而非终态。审阅虽只跑
// 只读命令，但同样要过权限门，也可能发工单——2026-08-19 真机实测，环节
// 几乎必然醒在 permission_request/question 上，随即去取最终报文，报
// 「事件流中没有 completed/failed 最终报文」，一轮审阅白跑。函数头写的
// 「wait 终态」是意图，WaitEvent 的语义不是，这里补上差额。
//
// 阻塞行为：executor 发了工单又不收尾时本函数会一直等，等价于任何一条
// handoff task 挂在 waiting_answer——审核者从 wait --card 上看得见。
func waitForTurnEnd(ctx context.Context, wait func(context.Context) (*proto.Event, error)) error {
	for {
		event, err := wait(ctx)
		if err != nil {
			return err
		}
		if event == nil {
			continue
		}
		switch event.Type {
		case proto.EventTypeCompleted, proto.EventTypeTurnFailed, proto.EventTypeFailed:
			return nil
		}
	}
}

// clientFinalMessage 从 attach 快照取最后一条 completed/turn_failed/failed
// 事件，按真实协议字段返回最终报文；缺失即报错，不拿 progress 凑数。
func clientFinalMessage(ctx context.Context, cl *client.Client, taskID string) (string, error) {
	info, err := cl.Attach(ctx, taskID)
	if err != nil {
		return "", fmt.Errorf("取审阅最终报文: %w", err)
	}
	message, err := finalMessageFromEvents(info.RecentEvents)
	if err != nil {
		return "", fmt.Errorf("取审阅最终报文: %w", err)
	}
	return message, nil
}

// finalMessageFromEvents 是协议字段解析的纯函数，供 wire 层单测固定
// completed.summary 与 turn_failed/failed.fail_reason 的真实线格式。
func finalMessageFromEvents(events []proto.Event) (string, error) {
	// completed 优先于失败类事件，即使失败排在后面：codex 收尾时常在
	// completed 之后再补一条 turn_failed（app-server 的 WebSocket 断开），
	// 那是传输层的假警报，不是回合失败——报告已经在 completed 里了。
	// 环节执行器每轮审阅都派一条新 task 并等它的首个终态，所以「本次
	// 生命周期内出现过 completed」就意味着报文存在，取它不会串到上一轮。
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].Type != proto.EventTypeCompleted {
			continue
		}
		var payload struct {
			Summary string `json:"summary"`
		}
		if err := json.Unmarshal(events[i].Payload, &payload); err != nil {
			return "", fmt.Errorf("completed payload 解析失败: %w", err)
		}
		if payload.Summary == "" {
			return "", fmt.Errorf("completed payload 缺 summary")
		}
		return payload.Summary, nil
	}
	for i := len(events) - 1; i >= 0; i-- {
		event := events[i]
		switch event.Type {
		case proto.EventTypeCompleted:
			var payload struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return "", fmt.Errorf("completed payload 解析失败: %w", err)
			}
			if payload.Summary == "" {
				return "", fmt.Errorf("completed payload 缺 summary")
			}
			return payload.Summary, nil
		case proto.EventTypeTurnFailed, proto.EventTypeFailed:
			var payload struct {
				FailReason string `json:"fail_reason"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				return "", fmt.Errorf("failed payload 解析失败: %w", err)
			}
			if payload.FailReason == "" {
				return "", fmt.Errorf("failed payload 缺 fail_reason")
			}
			return payload.FailReason, nil
		}
	}
	return "", fmt.Errorf("事件流中没有 completed/failed 最终报文")
}

// taskBranch 从卡的最新 dispatched 快照取实际工作分支名。
func taskBranch(st *ledger.Store, card ledger.Card) (string, error) {
	// 走账本的 WorkBranch：它跳过审阅轮的快照。直接取「最后一条 dispatched」
	// 会在审阅之后指向审阅分支，合并环节就会去合一条只读分支
	return st.WorkBranch(card.ID)
}

// objectiveScript 客观判据脚本：补齐工作分支 → fetch → 在临时 worktree 里
// 跑 gofmt 与测试。临时 worktree 的落点是 origin/<工作分支>，天然脱头。
func objectiveScript(branch, base string) string {
	return strings.Join([]string{
		"set -e",
		syncWorkBranchScript(branch),
		"git fetch origin " + shellQuote(branch) + " " + shellQuote(base),
		"tmp=$(mktemp -d)",
		"git worktree add \"$tmp\" " + shellQuote("origin/"+branch),
		`trap 'git worktree remove --force "$tmp"' EXIT`,
		"cd \"$tmp\"",
		"test -z \"$(gofmt -l .)\"",
		"go test ./...",
	}, "\n")
}

// mergeScript 合并脚本：补齐工作分支 → fetch → 在**脱头**的临时 worktree 里
// 合并 → 推基线。
//
// 为什么落点是 origin/<基线> 而不是本地基线分支名（两条原因，缺一都会踩）：
//  1. git 不允许同一分支同时在两个 worktree 里被 checkout——协调者主工作区
//     恰好停在基线分支上时（合并完想看结果，很常见），worktree add 会直接失败
//  2. 用刚 fetch 的 origin/<基线> 作落点，顺带消灭「本地基线陈旧」这个变量
//
// 随之而来的行为：协调者本地的基线分支引用**不再被推进**，新合并提交只落
// origin。这是「origin 为权威」的直接推论，也免去一份会漂移的影子引用。
func mergeScript(branch, base string) string {
	return strings.Join([]string{
		"set -e",
		syncWorkBranchScript(branch),
		"git fetch origin " + shellQuote(branch) + " " + shellQuote(base),
		"tmp=$(mktemp -d)",
		"git worktree add --detach \"$tmp\" " + shellQuote("origin/"+base),
		`trap 'git worktree remove --force "$tmp"' EXIT`,
		"cd \"$tmp\"",
		"git merge --no-ff " + shellQuote("origin/"+branch) +
			" || { git diff --name-only --diff-filter=U; git merge --abort; exit 1; }",
		"git push origin HEAD:" + shellQuote(base),
	}, "\n")
}

// NewLocalObjective 生产客观判据：在 repoDir 内 fetch 后，在临时 worktree
// 里跑 gofmt 与 go test。stores 为兼容计划单参数 API 的可选账本句柄；真实
// CLI 传入它，以便从 dispatched 快照取任务分支。
func NewLocalObjective(repoDir string, stores ...*ledger.Store) func(ctx context.Context, card ledger.Card, base string) error {
	return func(ctx context.Context, card ledger.Card, base string) error {
		if len(stores) == 0 || stores[0] == nil {
			return fmt.Errorf("客观判据缺账本句柄，无法读取 task branch")
		}
		branch, err := taskBranch(stores[0], card)
		if err != nil {
			return err
		}
		logger := slog.Default().With("step", "objective", "card", card.ID, "branch", branch, "base", base)
		logger.Info("运行客观判据")
		cmd := exec.CommandContext(ctx, "bash", "-c", objectiveScript(branch, base))
		cmd.Dir = repoDir
		out, runErr := cmd.CombinedOutput()
		if cerr := classifyScriptError(out, runErr, "客观判据"); cerr != nil {
			logger.Error("客观判据失败", "err", cerr)
			return cerr
		}
		logger.Info("客观判据通过")
		return nil
	}
}

// NewLocalMerge 生产合并：--no-ff 合 task 分支进基线并 push；冲突时
// abort 并返回冲突文件清单。stores 用于读取 dispatched 分支快照。
func NewLocalMerge(repoDir string, stores ...*ledger.Store) func(ctx context.Context, card ledger.Card, base string) error {
	return func(ctx context.Context, card ledger.Card, base string) error {
		if len(stores) == 0 || stores[0] == nil {
			return fmt.Errorf("合并缺账本句柄，无法读取 task branch")
		}
		branch, err := taskBranch(stores[0], card)
		if err != nil {
			return err
		}
		logger := slog.Default().With("step", "merge", "card", card.ID, "branch", branch, "base", base)
		logger.Info("运行合并")
		cmd := exec.CommandContext(ctx, "bash", "-c", mergeScript(branch, base))
		cmd.Dir = repoDir
		out, runErr := cmd.CombinedOutput()
		if cerr := classifyScriptError(out, runErr, "合并"); cerr != nil {
			logger.Error("合并失败", "err", cerr)
			return cerr
		}
		logger.Info("合并完成并已推 origin", "pushed_base", base)
		return nil
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
