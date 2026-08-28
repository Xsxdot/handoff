// scheddrain.go —— K5 自动化编排的清队与协调者回合边界。
//
// 职责：按 scheduling.QueueKinds 重放持久队列；执行队列先以
// keystone.Wake(queue_release) 确认现场，再调用 startCardStep；
// 每个 LaunchAdmit 占用的两级名额在 LaunchForCard/Wake 返回后归还。
// 边界：不实现 scheduling 排序/CAS、不实现 keystone 重建、不直调 StepRunner。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

const automationPollInterval = 2 * time.Second
const automationBatchLimit = 100

type coordinatorAdmissionError struct {
	squad string
	err   error
}

func (e *coordinatorAdmissionError) Error() string {
	return fmt.Sprintf("协调者小队 %s 准入失败: %v", e.squad, e.err)
}

func (e *coordinatorAdmissionError) Unwrap() error { return e.err }

type coordinatorLookupError struct{ err error }

func (e *coordinatorLookupError) Error() string {
	return fmt.Sprintf("识别协调者小队失败: %v", e.err)
}

func (e *coordinatorLookupError) Unwrap() error { return e.err }

// StartAutomation 启动 agentd 生命周期内唯一的自动化循环；依赖未装配时只记录可行动告警。
func (s *Server) StartAutomation(ctx context.Context) {
	if s.scheduling == nil || s.keystone == nil || s.autoLedger == nil {
		s.log.Warn("自动化循环未启动：依赖尚未装配",
			"has_scheduling", s.scheduling != nil,
			"has_keystone", s.keystone != nil, "has_ledger", s.autoLedger != nil)
		return
	}
	s.automationStartOnce.Do(func() {
		s.log.Info("自动化清队与事件唤醒循环启动",
			"poll", automationPollInterval, "queue_kinds", scheduling.QueueKinds)
		go s.automationLoop(ctx)
	})
}

func (s *Server) automationLoop(ctx context.Context) {
	s.runAutomationPass(ctx)
	ticker := time.NewTicker(automationPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.log.Info("自动化循环停止", "cause", ctx.Err())
			return
		case <-ticker.C:
			s.runAutomationPass(ctx)
		case <-s.automationKick:
			s.runAutomationPass(ctx)
		}
	}
}

func (s *Server) runAutomationPass(ctx context.Context) {
	if _, _, err := s.consumeAutomationEventsOnce(ctx); err != nil {
		s.log.Error("自动化事件消费轮失败", "cause", err)
	}
	if _, err := s.drainQueuesOnce(ctx); err != nil {
		s.log.Error("自动化队列清队轮失败", "cause", err)
	}
}

// drainQueuesOnce 按 QueueKinds 法定顺序最多处理 100 行；失败行回填并停止当前 kind。
func (s *Server) drainQueuesOnce(ctx context.Context) (processed int, err error) {
	if s.scheduling == nil {
		s.log.Error("自动化队列清队失败：编制域未装配")
		return 0, errors.New("自动化队列清队：编制域未装配")
	}
	for _, kind := range scheduling.QueueKinds {
		for processed < automationBatchLimit {
			req, ok, popErr := s.scheduling.PopReady(kind)
			if popErr != nil {
				s.log.Error("自动化队列出队失败", "kind", kind, "cause", popErr)
				return processed, fmt.Errorf("出队 %s 失败: %w", kind, popErr)
			}
			if !ok {
				break
			}
			processed++
			s.log.Info("自动化队列出队", "kind", kind, "card", req.Card,
				"node", req.Node, "squad", req.Squad, "priority", req.Priority)
			switch kind {
			case scheduling.KindLaunchQueue:
				if _, launchErr := s.launchCoordinatorRound(ctx, req.Card, "manual"); launchErr != nil {
					s.requeueAutomation(req, kind, launchErr)
					return processed, nil
				}
			case scheduling.KindIgnitionQueue:
				if drainErr := s.drainIgnitionRequest(ctx, req); drainErr != nil {
					s.requeueAutomation(req, kind, drainErr)
					return processed, nil
				}
			default:
				s.log.Error("自动化清队遇到未声明 kind", "kind", kind, "card", req.Card)
				return processed, fmt.Errorf("清队遇到未声明 kind %q", kind)
			}
		}
		if processed >= automationBatchLimit {
			break
		}
	}
	return processed, nil
}

func (s *Server) drainIgnitionRequest(ctx context.Context, req scheduling.IgnitionRequest) error {
	backlog := 0
	if rows, err := s.scheduling.QueueSnapshot(); err != nil {
		s.log.Warn("生成出队简报失败，仍继续唤醒",
			"card", req.Card, "node", req.Node, "cause", err)
	} else {
		backlog = len(rows)
	}
	summary := fmt.Sprintf("queue_release kind=%s card=%s node=%s backlog=%d",
		scheduling.KindIgnitionQueue, req.Card, req.Node, backlog)
	decision := s.keystone.Decide(keystone.WakeEvent{
		Kind: keystone.WakeQueueRelease, Card: req.Card, Summary: summary,
	})
	if !decision.Wake {
		s.log.Info("队列出队被人工接管暂缓", "card", req.Card,
			"node", req.Node, "reason", decision.Reason)
		return fmt.Errorf("队列出队暂缓：%s", decision.Reason)
	}
	result, err := s.wakeCoordinatorRound(ctx, req.Card, []keystone.WakeEvent{{
		Kind: keystone.WakeQueueRelease, Card: req.Card, Summary: summary,
	}})
	if err != nil {
		return fmt.Errorf("队列出队唤醒失败: %w", err)
	}
	if !result.Woke {
		s.log.Error("队列出队唤醒未运行协调者回合", "card", req.Card, "node", req.Node)
		return errors.New("队列出队唤醒未运行协调者回合")
	}
	// K2/K5 的唯一再入口：不得把 req 直接转成 runner。
	s.log.Info("队列出队唤醒完成，进入节点再入口", "card", req.Card, "node", req.Node)
	return s.startCardStep(req.Card, proto.CardStepReq{
		Step: req.Node, Target: req.Target, Executor: req.Executor,
		Model: req.Model, Actor: req.Actor,
	})
}

func (s *Server) requeueAutomation(req scheduling.IgnitionRequest, kind string, cause error) {
	position, err := s.scheduling.Enqueue(req, kind)
	if err != nil {
		s.log.Error("自动化队列回填失败", "kind", kind, "card", req.Card,
			"node", req.Node, "cause", cause, "requeue_error", err)
		return
	}
	s.log.Warn("自动化请求暂缓并回填队列", "kind", kind, "card", req.Card,
		"node", req.Node, "position", position, "cause", cause)
}

// launchCoordinatorRound 是 HTTP 手动拉起与 launch_queue 共用的入口。
// 只有 LaunchAdmit 错误使用 coordinatorAdmissionError，LaunchForCard 失败仍为 502。
func (s *Server) launchCoordinatorRound(ctx context.Context, card, source string) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	squad, err := s.resolveCoordinatorSquad()
	if err != nil {
		return zero, &coordinatorLookupError{err: err}
	}
	binding, err := s.scheduling.LaunchAdmit(squad.Name)
	if err != nil {
		return zero, &coordinatorAdmissionError{squad: squad.Name, err: err}
	}
	defer s.releaseSchedulingBinding(card, binding)
	carrier, err := s.scheduling.Carrier(binding.Carrier)
	if err != nil {
		s.log.Error("读协调者载体失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, fmt.Errorf("读载体 %s: %w", binding.Carrier, err)
	}
	spec := keysclient.SessionSpec{
		CLI: binding.Executor, HomeDir: carrier.HomeDir, Model: binding.Model,
		Workdir: s.resolveCoordWorkdir(card),
	}
	s.log.Info("自动化拉起协调者回合", "card", card, "source", source,
		"squad", binding.Squad, "carrier", binding.Carrier,
		"cli", spec.CLI, "home_dir", spec.HomeDir, "workdir", spec.Workdir)
	result, err := s.keystone.LaunchForCard(ctx, card, source, spec)
	if err != nil {
		s.log.Error("自动化拉起协调者回合失败", "card", card, "source", source,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return result, fmt.Errorf("拉起协调者回合失败: %w", err)
	}
	s.log.Info("自动化拉起协调者回合结束", "card", card, "source", source,
		"session", result.SessionID, "rebuilt", result.Rebuilt, "escalated", result.Escalated)
	return result, nil
}

// wakeCoordinatorRound 为 Wake 临时占用协调者回合名额，Wake 返回后释放。
// Release 失败只留完整身份日志，不覆盖 Wake 原始结果；启动对账仅是兜底。
func (s *Server) wakeCoordinatorRound(ctx context.Context, card string,
	evs []keystone.WakeEvent) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	squad, err := s.resolveCoordinatorSquad()
	if err != nil {
		return zero, &coordinatorLookupError{err: err}
	}
	binding, err := s.scheduling.LaunchAdmit(squad.Name)
	if err != nil {
		return zero, &coordinatorAdmissionError{squad: squad.Name, err: err}
	}
	defer s.releaseSchedulingBinding(card, binding)
	s.log.Info("自动化唤醒协调者回合", "card", card,
		"event_count", len(evs), "squad", binding.Squad, "carrier", binding.Carrier)
	result, err := s.keystone.Wake(ctx, card, evs)
	if err != nil {
		s.log.Error("自动化唤醒协调者回合失败", "card", card,
			"event_count", len(evs), "squad", binding.Squad,
			"carrier", binding.Carrier, "cause", err)
		return result, fmt.Errorf("唤醒协调者回合失败: %w", err)
	}
	s.log.Info("自动化唤醒协调者回合结束", "card", card,
		"event_count", len(evs), "session", result.SessionID,
		"rebuilt", result.Rebuilt, "escalated", result.Escalated)
	return result, nil
}

// releaseSchedulingBinding 释放一次准入产生的两级计数；空 binding 是存量直绑路径。
func (s *Server) releaseSchedulingBinding(card string, binding scheduling.Binding) {
	if binding.Squad == "" || binding.Carrier == "" || s.scheduling == nil {
		return
	}
	if err := s.scheduling.Release(binding.Squad, binding.Carrier); err != nil {
		s.log.Error("自动化名额归还失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return
	}
	s.log.Info("自动化名额已归还", "card", card,
		"squad", binding.Squad, "carrier", binding.Carrier)
	s.kickAutomation()
}

func (s *Server) kickAutomation() {
	select {
	case s.automationKick <- struct{}{}:
	default:
	}
}
