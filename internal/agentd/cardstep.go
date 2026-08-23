// 看板节点动作的 agentd 侧装配：把一张卡的工作流节点跑起来。
//
// 职责：
//   - 解析目标机（配置），装配 ledgerstep.StepRunner
//   - 守「同一张卡同时只允许一个环节在飞」
//   - 起 goroutine 异步执行，HTTP 侧立刻返回
//
// 边界：
//   - 不做编排——那在 internal/ledgerstep，CLI 与本文件共用同一份
//   - 不做恢复：在飞集合是进程内状态，agentd 重启即清空。此时卡上留下的是
//     一次没有终态事件的环节，与 CLI 跑到一半被 Ctrl-C 的形态一致，人从
//     timeline 看得出来。本轮不做恢复是刻意的取舍，不是遗漏
//   - 不做实现类派发：那要挂 plan 文件，浏览器里没有
package agentd

import (
	"context"
	"errors"
	"fmt"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
)

// errStepInFlight 表示该卡已有环节在跑，调用方应答 409。
var errStepInFlight = errors.New("该卡已有环节在运行")

// startCardStep 起一个卡环节。
//
// 参数：cardID 卡；node 节点名（= 看板列名）；actor 发起人（web:<addr>）。
// 返回：前置校验失败时返回错误（调用方翻成 400/404/409）；校验通过后
// 立刻返回 nil，环节在后台 goroutine 里跑。
//
// 为什么必须异步：审阅环节会阻塞到被派出去的 task 跑到回合终态——几分钟到
// 几十分钟，executor 挂在 waiting_answer 时更久。HTTP 请求扛不住这个时长，
// 界面靠已有的卡事件流看进展。
//
// 注意：返回 nil 不代表环节成功，只代表它启动了。成败落在卡的事件流上。
func (s *Server) startCardStep(cardID, node, actor string) error {
	if !s.claimCardStep(cardID) {
		return fmt.Errorf("%w: %s 的 %s 节点正在运行", errStepInFlight, cardID, node)
	}
	runner := &ledgerstep.StepRunner{
		St: s.ledger, Session: actor,
		Dispatcher: &ledgerstep.Dispatcher{
			St: s.ledger, Transport: s.stepTransport, Actor: actor,
		},
		Clients: s.pool.For,
	}
	s.log.Info("节点已受理", "card", cardID, "node", node, "actor", actor)
	go func() {
		defer s.releaseCardStep(cardID)
		s.runStepFn(context.Background(), runner, cardID, node)
	}()
	return nil
}

// runStep 是 runStepFn 的生产实现：跑环节并把结果记进日志。
//
// 为什么错误只进日志不往上抛：调用它的 goroutine 没有上游。环节的成败
// 由 ledgerstep 落进卡的事件流，那是界面看得见的地方；日志是排查时的第二现场。
func (s *Server) runStep(ctx context.Context, runner *ledgerstep.StepRunner, cardID, node string) {
	outcome, err := runner.Run(ctx, cardID, node)
	if err != nil {
		s.log.Error("节点失败", "card", cardID, "node", node, "cause", err)
		return
	}
	s.log.Info("节点结束", "card", cardID, "node", node,
		"action", string(outcome.Action), "reason", outcome.Reason)
}

func (s *Server) claimCardStep(cardID string) bool {
	s.cardStepMu.Lock()
	defer s.cardStepMu.Unlock()
	if s.cardStepFlight[cardID] {
		return false
	}
	s.cardStepFlight[cardID] = true
	return true
}

func (s *Server) releaseCardStep(cardID string) {
	s.cardStepMu.Lock()
	delete(s.cardStepFlight, cardID)
	s.cardStepMu.Unlock()
}

func (s *Server) cardStepInFlight(cardID string) bool {
	s.cardStepMu.Lock()
	defer s.cardStepMu.Unlock()
	return s.cardStepFlight[cardID]
}

func (s *Server) stepTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (string, error) {
	// 走 target 客户端池而不是自己 client.New：relay 形态的机器没有 addr，
	// 直连构造对它们恒失败（见 internal/targetclient 与 nodirectclient_test）。
	cl, err := s.pool.For(opts.Target)
	if err != nil {
		return "", err
	}
	task, err := cl.Dispatch(ctx, client.DispatchOpts{
		Prompt: opts.Prompt, Target: opts.Target,
		NewBranch: opts.Branch, Branch: opts.ExistingBranch,
		ProjectName: opts.Project, Executor: opts.Executor, Model: opts.Model,
		Discipline: opts.Discipline,
		PlanB64:    opts.PlanB64, PlanName: opts.PlanName, Base: opts.Base,
		ResolveDefaultBase: opts.ResolveDefaultBase,
		LocalBaseBranch:    opts.LocalBaseBranch,
		NewWorktree:        opts.NewWorktree,
	})
	if err != nil {
		return "", err
	}
	return task.ID, nil
}
