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
	"os"
	"time"

	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/orchestration"
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
	processed, escalated, err := s.consumeAutomationEventsOnce(ctx)
	if err != nil {
		s.log.Error("自动化事件消费轮失败", "cause", err)
	} else if processed > 0 || escalated {
		// B390 R4：正常空轮不刷屏，但「本轮有事发生」必须可见——默认日志级别是
		// warn（logx 缺省，见 H5），故用 Warn 而非 Info，否则成功心跳仍被吞。
		s.log.Warn("自动化事件消费轮完成", "processed", processed, "escalated", escalated)
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
	if !rebind && occupied {
		err := fmt.Errorf("卡 %s 当前席位状态不适合此操作: %w", card, ledger.ErrCASConflict)
		s.log.Warn("协调者拉起在 Launch 前被席位拦截", "card", card, "source", source, "rebind", rebind, "cause", err)
		return zero, err
	}
	if rebind && !occupied {
		err := fmt.Errorf("卡 %s 当前席位状态不适合此操作: %w", card, ledger.ErrCASConflict)
		s.log.Warn("协调者换绑在 Launch 前被席位拦截", "card", card, "source", source, "cause", err)
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
	// 名额在本回合结束即归还：协调者是 print 一次性回合（无窗口可挂），
	// 不再依赖「关 tab」释放，否则一次拉起会把该载体的协调者名额永久占住。
	defer s.releaseSchedulingBinding(card, binding)
	carrier, err := s.scheduling.Carrier(binding.Carrier)
	if err != nil {
		s.log.Error("读协调者载体失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, fmt.Errorf("读载体 %s: %w", binding.Carrier, err)
	}
	// 远端载体 fail-closed：print 回合由本进程的 hostapi 拉起（载体 CLI 在本机执行），
	// 载体登记的那台机器若不是本机，会话就落在错误的机器上、且 HomeDir 是对端路径。
	// 旧 TUI 形态有远端 PTY 分支，print 形态没有——故显式拒绝，不静默跑错机器。
	if !scheduling.IsLocalMachine(carrier.Machine) && !s.IsSelfTarget(carrier.Machine) {
		err := fmt.Errorf("协调者载体 %s 在机器 %s（远端）：print 协调者只支持本机载体，"+
			"请把协调者小队的成员指向本机载体", binding.Carrier, carrier.Machine)
		s.log.Warn("协调者载体非本机，拒绝拉起", "card", card, "carrier", binding.Carrier,
			"machine", carrier.Machine, "cause", err)
		return zero, err
	}
	spec := keysclient.SessionSpec{
		CLI: binding.Executor, HomeDir: carrier.HomeDir, Model: binding.Model,
		Workdir: s.resolveCoordWorkdir(card),
	}
	normalized, err := orchestration.NormalizeCoordinatorSpec(spec)
	if err != nil {
		s.log.Error("规范化协调者 SessionSpec 失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, err
	}
	spec = normalized
	s.log.Info("自动化拉起协调者回合", "card", card, "source", source,
		"squad", binding.Squad, "carrier", binding.Carrier,
		"cli", spec.CLI, "home_dir", spec.HomeDir, "workdir", spec.Workdir,
		"machine", carrier.Machine, "rebind", rebind)
	// print 一次性回合：载体跑完这一轮即退出，会话身份由载体自报（agentd 不伪造）。
	result, err := s.keystone.LaunchForCard(ctx, card, source, spec)
	if err != nil {
		s.log.Error("拉起协调者回合失败", "card", card, "source", source,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, fmt.Errorf("拉起协调者回合失败: %w", err)
	}
	if result.SessionID == "" {
		// 没有会话身份就落不了席位，也就永远唤不醒：宁可显式失败，不返回一个
		// 看起来成功、实际叫不醒的回合。
		err := fmt.Errorf("协调者回合未返回会话身份，无法落席位（card=%s）", card)
		s.log.Error("协调者回合缺会话身份", "card", card, "source", source, "cause", err)
		return zero, err
	}
	identity, err := proto.EncodeSeatIdentity(spec.CLI, result.SessionID)
	if err != nil {
		return zero, fmt.Errorf("编码协调者席位身份: %w", err)
	}
	if rebind {
		err = s.ledger.RebindSeat(card, identity, proto.SeatSourceCoordinate, expect,
			coordinatorBearing(binding, carrier, spec))
	} else {
		err = s.ledger.BindSeat(card, identity, proto.SeatSourceCoordinate,
			coordinatorBearing(binding, carrier, spec))
	}
	if err != nil {
		// 席位没落 = 这个会话永远不会被唤醒（空座在 wakeCoordinatorRound 里直接跳过），
		// 因此不能静默成功：留 needs_human + 把新会话身份带回给调用方（409 体里有 session_id），
		// 由人决定回收还是重绑。
		s.log.Error("协调者回合已跑但席位未落", "card", card, "source", source,
			"session", result.SessionID, "rebind", rebind, "cause", err)
		_ = s.ledger.MarkNeedsHuman(card,
			"协调者回合已跑但席位未落，该会话不会被唤醒，请人工处置（可 card rebind --self 或回收会话）", "coordinator")
		return keystone.RoundResult{SessionID: result.SessionID}, &coordinatorSeatConflict{result: result}
	}
	s.log.Info("自动化拉起协调者回合结束", "card", card, "source", source,
		"session", result.SessionID, "seat", identity, "rebind", rebind)
	return keystone.RoundResult{Woke: true, SessionID: result.SessionID}, nil
}

// localMachineName 返回本机名（与 IsLocalMachine 的 hostname 判据同源），用于
// 转交响应回报实际执行机器。
func (s *Server) localMachineName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "local"
	}
	return host
}

// transferCoordinatorWake 把唤醒请求转交给承载席位所在机器的 agentd（契约 §3.3）：
// 出站走 target 客户端池并带防环头；对端不可达/超时/409 都视为失败，不重选载体。
func (s *Server) transferCoordinatorWake(ctx context.Context, card, seat, machine string,
	raws []proto.LedgerEvent, holder string) (keystone.RoundResult, error) {
	target, err := s.clientForTarget(machine)
	if err != nil {
		s.log.Error("转交唤醒取目标客户端失败", "card", card, "machine", machine, "cause", err)
		return keystone.RoundResult{}, fmt.Errorf("转交唤醒取目标客户端 %s: %w", machine, err)
	}
	resp, err := target.MarkForwarded().CoordinatorWake(ctx, card, proto.CoordinatorWakeReq{
		Seat: seat, Events: raws, Holder: holder,
	})
	if err != nil {
		s.log.Error("转交唤醒到目标机失败", "card", card, "machine", machine, "cause", err)
		return keystone.RoundResult{}, fmt.Errorf("转交唤醒到 %s: %w", machine, err)
	}
	s.log.Info("协调者唤醒已转交", "card", card, "machine", machine,
		"handled_by", resp.HandledBy, "event_count", len(raws))
	return keystone.RoundResult{
		Woke: resp.Woke, SessionID: resp.SessionID,
		Rebuilt: resp.Rebuilt, Escalated: resp.Escalated, Output: resp.Output,
	}, nil
}

// reportSeatBearingMissing 是缺承载的显式路径（契约 §2.3）：落恰一条
// EvSeatBearingMissing（幂等），并按「需要人」展示（额外 MarkNeedsHuman，
// 让卡级订阅与会话列表待办照常亮起），本轮该卡跳过。返回：落事件或打展示
// 失败显式上抛，不静默吞。
func (s *Server) reportSeatBearingMissing(card, seat string) error {
	written, err := s.ledger.ReportSeatBearingMissing(card, seat)
	if err != nil {
		return err
	}
	if !written {
		s.log.Info("席位缺承载已报告过，跳过重复展示", "card", card)
		return nil
	}
	if err := s.ledger.MarkNeedsHuman(card,
		"协调者席位缺承载记录：请 handoff card seat bearing set <id> --carrier <carrier>，"+
			"或 card rebind --launch 重建会话", "agentd"); err != nil {
		s.log.Error("缺承载落地后打等人标记失败", "card", card, "cause", err)
		return err
	}
	s.log.Warn("协调者席位缺承载记录，已落需要人展示", "card", card, "seat", seat)
	return nil
}

// wakeCoordinatorRound 为 Wake 临时占用协调者回合名额，Wake 返回后释放。
// Release 失败只留完整身份日志，不覆盖 Wake 原始结果；启动对账仅是兜底。
func (s *Server) wakeCoordinatorRound(ctx context.Context, card string,
	evs []keystone.WakeEvent) (keystone.RoundResult, error) {
	return s.wakeCoordinatorRoundRaw(ctx, card, evs, nil)
}

// wakeCoordinatorRoundRaw 是唤醒回合的现行实现（B389 §3.2）：先读承载记录定
// 归属，本机分支用承载记录构造 SessionSpec 并按冻结载体申请名额（不再
// LaunchAdmit 重选载体），远端分支转交（T4）。raws 是本批原始账本事件，转交面
// 需要它携带本批 seq 集合（D4：queue_release 合成唤醒没有账本行）。
func (s *Server) wakeCoordinatorRoundRaw(ctx context.Context, card string,
	evs []keystone.WakeEvent, raws []proto.LedgerEvent) (keystone.RoundResult, error) {
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
	bearing, hasBearing, err := s.ledger.SeatBearingOf(card)
	if err != nil {
		return zero, fmt.Errorf("读取唤醒承载记录: %w", err)
	}
	if !hasBearing {
		// 存量 coordinate 席位无承载：显式修复路径（契约 §2.3）——落恰一条
		// EvSeatBearingMissing 并按「需要人」展示，本轮该卡跳过。
		if err := s.reportSeatBearingMissing(card, current.DriverSession); err != nil {
			return zero, fmt.Errorf("卡 %s 缺承载显式路径: %w", card, err)
		}
		return zero, nil
	}
	if !scheduling.IsLocalMachine(bearing.Machine) && !s.IsSelfTarget(bearing.Machine) {
		if len(raws) == 0 {
			// D4：队列出队的合成唤醒没有 card_events 行，装不进冻结 DTO。显式失败，
			// 由 drainIgnitionRequest 回填队列并留需要人痕迹；不静默本机执行。
			return keystone.RoundResult{}, fmt.Errorf(
				"协调者承载在远端 %s，但本批唤醒无账本事件（queue_release 合成唤醒）不可转交",
				bearing.Machine)
		}
		return s.transferCoordinatorWake(ctx, card, current.DriverSession, bearing.Machine,
			raws, s.wakeClaimHolder())
	}
	squad, err := s.resolveCoordinatorSquad()
	if err != nil {
		return zero, &coordinatorLookupError{err: err}
	}
	binding, err := s.scheduling.AdmitSeatCarrier(squad.Name, bearing.Carrier)
	if err != nil {
		return zero, &coordinatorAdmissionError{squad: squad.Name, err: err}
	}
	defer s.releaseSchedulingBinding(card, binding)
	carrier, err := s.scheduling.Carrier(bearing.Carrier)
	if err != nil {
		s.log.Error("读协调者载体失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, fmt.Errorf("读载体 %s: %w", binding.Carrier, err)
	}
	cli, _, err := proto.ParseSeatIdentity(current.DriverSession)
	if err != nil {
		return zero, fmt.Errorf("解析唤醒席位 CLI: %w", err)
	}
	spec := keysclient.SessionSpec{
		CLI: cli, HomeDir: bearing.HomeDir, Model: bearing.Model, Workdir: bearing.Workdir,
	}
	normalized, err := orchestration.NormalizeCoordinatorSpec(spec)
	if err != nil {
		s.log.Error("规范化协调者 SessionSpec 失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		return zero, err
	}
	spec = normalized
	s.log.Info("自动化唤醒协调者回合", "card", card,
		"event_count", len(evs), "squad", binding.Squad, "carrier", binding.Carrier,
		"cli", spec.CLI, "home_dir", spec.HomeDir, "frozen_from_bearing", true)
	result, err := s.keystone.Wake(ctx, card, evs, spec)
	if err != nil {
		s.log.Error("自动化唤醒协调者回合失败", "card", card,
			"event_count", len(evs), "squad", binding.Squad,
			"carrier", binding.Carrier, "cause", err)
		return result, fmt.Errorf("唤醒协调者回合失败: %w", err)
	}
	if result.Rebuilt && result.SessionID != "" && result.SessionID != current.DriverSession {
		identity, encodeErr := proto.EncodeSeatIdentity(cli, result.SessionID)
		if encodeErr != nil {
			return result, fmt.Errorf("重建后编码新席位: %w", encodeErr)
		}
		if rebindErr := s.ledger.RebindSeat(card, identity, proto.SeatSourceCoordinate, current.DriverSession,
			coordinatorBearing(binding, carrier, spec)); rebindErr != nil {
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

// coordinatorBearing 把本次实际挑中的载体与恢复环境固化成承载记录（B389）：
// 首次拉起挑载体，后续唤醒只认这份记录，不再重选。
func coordinatorBearing(binding scheduling.Binding, carrier scheduling.Carrier,
	spec keysclient.SessionSpec) ledger.SeatBearing {
	return ledger.SeatBearing{
		Carrier: binding.Carrier,
		Machine: carrier.Machine,
		HomeDir: spec.HomeDir,
		Workdir: spec.Workdir,
		Model:   spec.Model,
	}
}

// releaseSchedulingBinding 释放一次准入产生的计数；直派 binding 只有载体键。
func (s *Server) releaseSchedulingBinding(card string, binding scheduling.Binding) {
	if binding.Carrier == "" || s.scheduling == nil {
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
