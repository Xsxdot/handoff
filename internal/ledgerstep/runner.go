// 环节入口：把「跑一次 review / merge 环节」收口成一个方法，CLI 与看板
// 按钮共用同一份装配逻辑。
//
// 边界：只做装配与分发，决策在 node.go 的两个 Step；本文件不碰 HTTP、
// 不碰 cobra、不做输出编码——那些是各调用方自己的呈现层。
package ledgerstep

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// StepRunner 环节执行的装配器。依赖全部显式注入，调用方各填各的：
//
//	             RepoDir              Dispatcher.Transport
//	CLI          --repo（缺省 CWD）   CLI 的 dispatch 通道
//	agentd       项目登记解析出的路径  agentd 自己的 client
type StepRunner struct {
	St         *ledger.Store
	RepoDir    string
	Dispatcher *Dispatcher
	Endpoints  func(target string) (addr, token string, err error)
	// MainLine 主线分支名，透传给 MergeStep；空则用它的缺省值。
	MainLine string
	// Target 审阅派发目标机；空则使用模板里的 target。
	Target string
}

// Run 跑一次环节。
//
// 参数：cardID 卡；step 只认 "review" | "merge"。
// 返回：Outcome（下一步动作 + 裁决 + 理由）；step 不认识或环节内部失败时返回错误。
//
// 阻塞行为：审阅环节会一直阻塞到被派出去的 task 跑到回合终态
// （几分钟到几十分钟，executor 挂在 waiting_answer 时更久）。调用方要么
// 自己在 goroutine 里跑（agentd 就是这么做的），要么接受前台阻塞（CLI）。
func (r *StepRunner) Run(ctx context.Context, cardID, step string) (Outcome, error) {
	slog.Default().Info("进入环节", "card", cardID, "step", step, "repo_dir", r.RepoDir)
	switch step {
	case "review":
		runner := &ReviewStep{
			St: r.St, Step: "review",
			RunReview: NewDispatchReview(r.St, r.reviewDispatch(), r.Endpoints),
		}
		return runner.RunOnce(ctx, cardID)
	case "merge":
		runner := &MergeStep{
			St:        r.St,
			Objective: NewLocalObjective(r.RepoDir, r.St),
			DoMerge:   NewLocalMerge(r.RepoDir, r.St),
			MainLine:  r.MainLine,
		}
		return runner.RunOnce(ctx, cardID)
	default:
		return Outcome{}, fmt.Errorf("环节只认 review|merge，收到 %q", step)
	}
}

func (r *StepRunner) reviewDispatch() func(ctx context.Context, cardID, template string) (target, taskID string, err error) {
	return func(ctx context.Context, cardID, template string) (target, taskID string, err error) {
		card, err := r.St.GetCard(cardID)
		if err != nil {
			return "", "", err
		}
		result, err := r.Dispatcher.ViaTemplate(ctx, card, TemplateDispatch{
			Template: template,
			Target:   r.Target,
		})
		if err != nil {
			return "", "", err
		}
		return result.Target, result.Task, nil
	}
}
