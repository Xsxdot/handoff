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
	"os"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
)

// errStepInFlight 表示该卡已有环节在跑，调用方应答 409。
var errStepInFlight = errors.New("该卡已有环节在运行")

// startCardStep 装配并异步启动一个卡节点。
//
// 参数：cardID 是卡号；req 是已完成 actor 归一和字段校验的规范请求。
// 返回：同卡已有在飞节点时返回 errStepInFlight；其它前置校验错误原样返回；成功
// 只表示 goroutine 已启动，不表示节点完成。后台结束时必须释放既有卡槽位。
//
// 为什么必须异步：审阅环节会阻塞到被派出去的 task 跑到回合终态——几分钟到
// 几十分钟，executor 挂在 waiting_answer 时更久。HTTP 请求扛不住这个时长，
// 界面靠已有的卡事件流看进展。
//
// 注意：返回 nil 不代表环节成功，只代表它启动了。成败落在卡的事件流上。
func (s *Server) startCardStep(cardID string, req proto.CardStepReq) error {
	if !s.claimCardStep(cardID) {
		return fmt.Errorf("%w: %s 的 %s 节点正在运行", errStepInFlight, cardID, req.Step)
	}
	// B229 缝 1：解析在同步段完成——拒发发生在 goroutine 启动之前，槽位当场
	// 释放，HTTP 层拿到的是带升级指引的可行动错误。有效（角色名，目标机）与
	// ViaTemplate 的裁决同序（共用 PreflightDiscipline）。
	node, err := ledgerstep.ResolveNode(s.ledger, cardID, req.Step)
	if err != nil {
		s.releaseCardStep(cardID)
		return err
	}
	resolved, err := s.resolveStepDiscipline(node, req.Target)
	if err != nil {
		s.releaseCardStep(cardID)
		return err
	}
	host, _ := os.Hostname()
	runner := &ledgerstep.StepRunner{
		St: s.ledger, Session: req.Actor,
		RunHolder: fmt.Sprintf("run:%s#%d#%d", host, os.Getpid(), time.Now().UnixNano()),
		Dispatcher: &ledgerstep.Dispatcher{
			St: s.ledger, Transport: s.stepTransport, Actor: req.Actor,
			DisciplineText:    resolved.Text,
			DisciplineVersion: resolved.Version,
		},
		Clients:  s.pool.For,
		Target:   req.Target,
		Executor: req.Executor,
		Model:    req.Model,
		Extra:    req.Extra,
	}
	s.log.Info("卡节点装配完成", "card", cardID, "node", req.Step,
		"actor", req.Actor, "target", req.Target, "executor", req.Executor,
		"model", req.Model, "run_holder", runner.RunHolder,
		"has_extra", strings.TrimSpace(req.Extra) != "")
	go func() {
		defer s.releaseCardStep(cardID)
		s.runStepFn(context.Background(), runner, cardID, req.Step)
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

// resolveStepDiscipline 是环节派发的缝 1 收口（B229 契约 §2.2）：按与 ViaTemplate
// 完全相同的裁决顺序算出有效（角色名，目标机），绑账本 lookup 与能力位探活各一次，
// 经 discipline.ResolveDispatch 产出随派发下发的正文三元组。未点名模板同样过闸，
// 产物是纯平台层正文、版本 0（§3.1：拒发闸覆盖一切带正文派发）。
//
// 为什么探活失败也按不支持处置：能力位缺席(nil)的保守方向由失败的可见性决定
// （契约 §2.4），网络错误时放行等于把「不知道」当成「支持」——那是缺陷三的
// 静默降级换马甲。探活的错误原因带上下文记 Warn，拒发文案本身含升级指引。
func (s *Server) resolveStepDiscipline(node ledger.NodeDef, reqTarget string) (discipline.ResolvedDiscipline, error) {
	name, target, err := ledgerstep.PreflightDiscipline(s.ledger, node.Template, node.Override.Discipline, reqTarget)
	if err != nil {
		return discipline.ResolvedDiscipline{}, err
	}
	if target == "" {
		// 目标机未定：这次派发必然在 ViaTemplate 处失败（既有异步语义，错误落
		// 卡的事件流）。这里不抢答也不放探活——没有目标机就没有闸可过；返回空
		// 产物让流程走原有失败路径，避免把既有 202 合约改成 400。
		s.log.Warn("环节派发跳过纪律解析：目标机未定", "node", node.Name)
		return discipline.ResolvedDiscipline{}, nil
	}
	var cap *bool
	cl, err := s.pool.For(target)
	if err != nil {
		s.log.Warn("环节派发前取得目标机客户端失败", "target", target, "cause", err)
	} else {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		status, serr := cl.Status(ctx)
		if serr != nil {
			s.log.Warn("环节派发前能力位探活失败", "target", target, "cause", serr)
		} else {
			cap = status.DisciplinesSupported
		}
	}
	lookup := func(n string) (int, string, error) {
		d, gerr := s.ledger.GetDiscipline(n, 0)
		if gerr != nil {
			return 0, "", gerr
		}
		return d.Version, d.Body, nil
	}
	res, err := discipline.ResolveDispatch(lookup, discipline.DisciplineRef{Name: name},
		s.conf().PlatformInvariantsEnabled(), cap)
	if err != nil {
		s.log.Warn("环节派发被拒发闸拦下", "node", node.Name, "target", target,
			"discipline", name, "cap_absent", cap == nil, "cause", err)
		return discipline.ResolvedDiscipline{}, fmt.Errorf("环节 %s 派发前纪律解析失败: %w", node.Name, err)
	}
	s.log.Info("环节派发纪律正文已就绪", "node", node.Name, "target", target,
		"discipline", name, "version", res.Version, "bytes", len(res.Text))
	return res, nil
}

func (s *Server) stepTransport(ctx context.Context, opts ledgerstep.DispatchOpts) (string, error) {
	// 走 target 客户端池而不是自己 client.New：relay 形态的机器没有 addr，
	// 直连构造对它们恒失败（见 internal/targetclient 与 nodirectclient_test）。
	s.log.Info("agentd 节点派发请求", "target", opts.Target, "executor", opts.Executor,
		"model", opts.Model, "prompt_bytes", len(opts.Prompt),
		"discipline", opts.Discipline, "discipline_version", opts.DisciplineVersion,
		"discipline_bytes", len(opts.DisciplineText))
	cl, err := s.pool.For(opts.Target)
	if err != nil {
		s.log.Warn("取得节点派发客户端失败", "target", opts.Target, "cause", err)
		return "", err
	}
	task, err := cl.Dispatch(ctx, client.DispatchOpts{
		Prompt: opts.Prompt, Target: opts.Target,
		NewBranch: opts.Branch, Branch: opts.ExistingBranch,
		ProjectName: opts.Project, Executor: opts.Executor, Model: opts.Model,
		Discipline:        opts.Discipline,
		DisciplineText:    opts.DisciplineText,
		DisciplineVersion: opts.DisciplineVersion,
		PlanB64:           opts.PlanB64, PlanName: opts.PlanName, Base: opts.Base,
		ResolveDefaultBase: opts.ResolveDefaultBase,
		LocalBaseBranch:    opts.LocalBaseBranch,
		NewWorktree:        opts.NewWorktree,
	})
	if err != nil {
		s.log.Warn("agentd 节点派发失败", "target", opts.Target, "executor", opts.Executor,
			"model", opts.Model, "cause", err)
		return "", err
	}
	s.log.Info("agentd 节点派发已受理", "target", opts.Target, "task", task.ID)
	return task.ID, nil
}

// requiresInlineLocalFile 判断一次 step 请求是否要求把调用方 CWD 的本地文件
// 内联发送给 agentd。
//
// 今天恒为 false 是有意的：CardStepReq 没有 PlanPath 或本地文件字段，StepRunner
// 也没有 PlanPath；PlanPath 只由不带 --step 的 CLI TemplateDispatch 在调用方 CWD 读取。
// 如果未来增加本地文件字段，必须先改冻结契约并把拒绝测试落在同一条 wire 上。
func requiresInlineLocalFile(req proto.CardStepReq) bool {
	return false
}
