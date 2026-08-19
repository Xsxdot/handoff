// 节点注入点的生产实现：审阅派发走 dispatch 通道 + wait 终态 + 取
// 报文；客观判据/合并在协调机本地工作区跑真命令。审阅 task 生命周期
// 在此收口（裁决落账后 done 归档，不留孤儿）。
package ledgernode

import (
	"context"
	"encoding/json"
	"fmt"
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
	endpoints func(target string) (addr, token string, err error),
) func(ctx context.Context, card ledger.Card) (string, error) {
	return func(ctx context.Context, card ledger.Card) (string, error) {
		target, taskID, err := dispatch(ctx, card.ID, "review-generic")
		if err != nil {
			return "", fmt.Errorf("派发审阅: %w", err)
		}
		addr, token, err := endpoints(target)
		if err != nil {
			return "", err
		}
		cl := client.New(addr, token)
		if _, err := cl.WaitEvent(ctx, taskID, false); err != nil {
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
	events, err := st.EventsFromAsc([]string{card.ID}, 0, 10000)
	if err != nil {
		return "", fmt.Errorf("读卡 dispatched 事件: %w", err)
	}
	var branch string
	for _, event := range events {
		if event.Type != ledger.EvDispatched {
			continue
		}
		var snapshot ledger.DispatchSnapshot
		if err := json.Unmarshal(event.Payload, &snapshot); err != nil {
			continue
		}
		if snapshot.Branch != "" {
			branch = snapshot.Branch
		}
	}
	if branch == "" {
		return "", fmt.Errorf("卡 %s 没有带 branch 的 dispatched 快照", card.ID)
	}
	return branch, nil
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
		script := strings.Join([]string{
			"set -e",
			"git fetch origin " + shellQuote(branch) + " " + shellQuote(base),
			"tmp=$(mktemp -d)",
			"git worktree add \"$tmp\" " + shellQuote("origin/"+branch),
			"trap 'git worktree remove --force \"$tmp\"' EXIT",
			"cd \"$tmp\"",
			"test -z \"$(gofmt -l .)\"",
			"go test ./...",
		}, "\n")
		cmd := exec.CommandContext(ctx, "bash", "-c", script)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("客观判据未过:\n%s", out)
		}
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
		script := strings.Join([]string{
			"set -e",
			"git fetch origin " + shellQuote(branch) + " " + shellQuote(base),
			"git checkout " + shellQuote(base),
			"git merge --no-ff " + shellQuote("origin/"+branch) +
				" || { git diff --name-only --diff-filter=U; git merge --abort; exit 1; }",
			"git push origin " + shellQuote(base),
		}, "\n")
		cmd := exec.CommandContext(ctx, "bash", "-c", script)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("合并失败:\n%s", out)
		}
		return nil
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
