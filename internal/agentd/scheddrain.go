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
	"github.com/Xsxdot/handoff/internal/ledger"
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

type coordinatorSeatConflict struct{ result keystone.RoundResult }

func (e *coordinatorSeatConflict) Error() string {
	return fmt.Sprintf("协调者已启动但席位 CAS 冲突：session=%s", e.result.SessionID)
}

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

// drainQueuesOnce 按 QueueKinds 法定顺序最多处理 100 行。局部 deferred 只在本次
// 清队轮次存活，且只延后协调者 ErrNoSlot 请求；轮末统一回填这些请求后才返回，
// 因而一个载体暂时无位不会阻断其它载体的执行者请求。
func (s *Server) drainQueuesOnce(ctx context.Context) (processed int, err error) {
	if s.scheduling == nil {
		s.log.Error("自动化队列清队失败：编制域未装配")
		return 0, errors.New("自动化队列清队：编制域未装配")
	}
	type deferredLaunch struct {
		req   scheduling.IgnitionRequest
		cause error
	}
	deferred := make([]deferredLaunch, 0)
	flushDeferred := func() int {
		count := len(deferred)
		for _, item := range deferred {
			s.requeueAutomation(item.req, scheduling.KindLaunchQueue, item.cause)
		}
		deferred = nil
		if count > 0 {
			s.log.Info("协调者无位请求已回填", "kind", scheduling.KindLaunchQueue,
				"deferred_count", count)
		}
		return count
	}
	for _, kind := range scheduling.QueueKinds {
		for processed < automationBatchLimit {
			req, ok, popErr := s.scheduling.PopReady(kind)
			if popErr != nil {
				requeued := flushDeferred()
				s.log.Error("自动化队列出队失败", "kind", kind, "cause", popErr)
				s.log.Warn("自动化队列清队提前结束", "kind", kind,
					"deferred_count", requeued, "cause", popErr)
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
				if _, launchErr := s.launchCoordinatorRound(ctx, req.Card, "coordinate"); launchErr != nil {
					if errors.Is(launchErr, scheduling.ErrNoSlot) {
						deferred = append(deferred, deferredLaunch{req: req, cause: launchErr})
						s.log.Warn("协调者准入无位，延后到本轮末回填", "kind", kind,
							"card", req.Card, "node", req.Node,
							"deferred_count", len(deferred), "cause", launchErr)
						continue
					}
					s.requeueAutomation(req, kind, launchErr)
					flushDeferred()
					s.log.Warn("协调者清队因非无位错误停止", "kind", kind,
						"card", req.Card, "node", req.Node, "cause", launchErr)
					return processed, nil
				}
			case scheduling.KindIgnitionQueue:
				if drainErr := s.drainIgnitionRequest(ctx, req); drainErr != nil {
					s.requeueAutomation(req, kind, drainErr)
					flushDeferred()
					s.log.Warn("执行者清队因错误停止", "kind", kind,
						"card", req.Card, "node", req.Node, "cause", drainErr)
					return processed, nil
				}
			default:
				s.log.Error("自动化清队遇到未声明 kind", "kind", kind, "card", req.Card)
				flushDeferred()
				return processed, fmt.Errorf("清队遇到未声明 kind %q", kind)
			}
		}
		if processed >= automationBatchLimit {
			break
		}
	}
	flushDeferred()
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
	if result.Woke {
		s.log.Info("队列出队唤醒完成，进入节点再入口", "card", req.Card, "node", req.Node)
	} else {
		s.log.Info("队列出队无协调者席位，直接进入节点再入口", "card", req.Card, "node", req.Node)
	}
	// K2/K5 的唯一再入口：不得把 req 直接转成 runner。
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
	return s.launchCoordinatorRoundWithExpect(ctx, card, source, "", false)
}

func (s *Server) launchCoordinatorRoundForRebind(ctx context.Context, card, source, expect string) (keystone.RoundResult, error) {
	return s.launchCoordinatorRoundWithExpect(ctx, card, source, expect, true)
}

func (s *Server) launchCoordinatorRoundWithExpect(ctx context.Context, card, source, expect string, rebind bool) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	if source != "coordinate" {
		return zero, fmt.Errorf("协调者拉起来源必须是 coordinate，收到 %q", source)
	}
	lock := s.coordinatorLock(card)
	lock.Lock()
	defer lock.Unlock()
	current, err := s.ledger.GetCard(card)
	if err != nil {
		return zero, err
	}
	occupied := current.DriverSession != "" || current.DriverSource != ""
	if (!rebind && occupied) || (rebind && !occupied) {
		err := fmt.Errorf("卡 %s 当前席位状态不适合此操作: %w", card, ledger.ErrCASConflict)
		s.log.Warn("协调者拉起在 Launch 前被席位拦截", "card", card, "source", source, "rebind", rebind, "cause", err)
		return zero, err
	}
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
	spec, err = normalizeCoordinatorSpec(spec)
	if err != nil {
		s.log.Error("自动化拉起协调者 HOME 展开失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, err
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
	identity, err := proto.EncodeSeatIdentity(spec.CLI, result.SessionID)
	if err != nil {
		s.log.Error("协调者拉起返回了不可用席位身份", "card", card, "source", source, "cause", err)
		return result, fmt.Errorf("编码协调者席位: %w", err)
	}
	if rebind {
		err = s.ledger.RebindSeat(card, identity, proto.SeatSourceCoordinate, expect)
	} else {
		err = s.ledger.BindSeat(card, identity, proto.SeatSourceCoordinate)
	}
	if err != nil {
		if errors.Is(err, ledger.ErrCASConflict) {
			s.log.Error("协调者拉起后席位 CAS 冲突，新会话保留待人工回收", "card", card, "source", source, "rebind", rebind, "session", result.SessionID, "cause", err)
			return result, &coordinatorSeatConflict{result: result}
		}
		return result, fmt.Errorf("写协调者席位: %w", err)
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
	current, err := s.ledger.GetCard(card)
	if err != nil {
		return zero, fmt.Errorf("读取唤醒席位: %w", err)
	}
	if current.DriverSession == "" && current.DriverSource == "" {
		s.keystone.Forget(card)
		s.log.Info("空座跳过协调者唤醒", "card", card, "event_count", len(evs))
		return zero, nil
	}
	if current.DriverSource == string(proto.SeatSourceBind) {
		s.keystone.Forget(card)
		s.log.Info("bind 席位跳过协调者唤醒", "card", card, "event_count", len(evs))
		return zero, nil
	}
	if err := proto.ValidateSeat(current.DriverSession, proto.SeatSource(current.DriverSource)); err != nil {
		return zero, fmt.Errorf("唤醒席位非法: %w", err)
	}
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
	spec, err = normalizeCoordinatorSpec(spec)
	if err != nil {
		s.log.Error("自动化唤醒协调者 HOME 展开失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, err
	}
	s.log.Info("自动化唤醒协调者回合", "card", card,
		"event_count", len(evs), "squad", binding.Squad, "carrier", binding.Carrier,
		"cli", spec.CLI, "home_dir", spec.HomeDir)
	result, err := s.keystone.Wake(ctx, card, evs, spec)
	if err != nil {
		s.log.Error("自动化唤醒协调者回合失败", "card", card,
			"event_count", len(evs), "squad", binding.Squad,
			"carrier", binding.Carrier, "cause", err)
		return result, fmt.Errorf("唤醒协调者回合失败: %w", err)
	}
	if result.Rebuilt && result.SessionID != "" && result.SessionID != current.DriverSession {
		cli, _, parseErr := proto.ParseSeatIdentity(current.DriverSession)
		if parseErr != nil {
			return result, fmt.Errorf("重建后解析旧席位: %w", parseErr)
		}
		identity, encodeErr := proto.EncodeSeatIdentity(cli, result.SessionID)
		if encodeErr != nil {
			return result, fmt.Errorf("重建后编码新席位: %w", encodeErr)
		}
		if rebindErr := s.ledger.RebindSeat(card, identity, proto.SeatSourceCoordinate, current.DriverSession); rebindErr != nil {
			if errors.Is(rebindErr, ledger.ErrCASConflict) {
				s.log.Error("协调者重建后席位 CAS 冲突，新会话保留待人工回收", "card", card,
					"event_count", len(evs), "session", result.SessionID, "cause", rebindErr)
				return result, &coordinatorSeatConflict{result: result}
			}
			return result, fmt.Errorf("重建后写协调者席位: %w", rebindErr)
		}
		s.log.Info("协调者重建后席位已更新", "card", card, "event_count", len(evs), "session", result.SessionID)
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
