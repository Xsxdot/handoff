// scheddrain.go —— K5 自动化编排的清队与协调者回合边界。
//
// 职责：按 scheduling.QueueKinds 重放持久队列；执行队列先以
// keystone.Wake(queue_release) 确认现场，再调用 startCardStep；
// 每个 LaunchAdmit 占用的两级名额在 LaunchForCard/Wake 返回后归还。
// 边界：不实现 scheduling 排序/CAS、不实现 keystone 重建、不直调 StepRunner。
package agentd

import (
	"context"
	"encoding/json"
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
//
// B448 r1（Q-r1-1 甲，F48）：起循环之前先同步跑启动调度占用对账，归还上一进程遗留在
// 本机名下的名额。循环首轮就可能准入（事件唤醒 / 清队），对账若晚于它，会把本进程
// 刚准入的活名额当遗留扣掉、日后 Release 再减一次 → 计数偏低超发。顺序由本函数结构
// 保证，不依赖 cmd 里调用的先后。
//
// 返回 error 仅在对账失败（任务表读失败、计数 body 解码失败带 key/version）；此时
// 循环不启动，调用方（cmd setupLedger）应中止启动。依赖未装配返回 nil（今日行为）。
func (s *Server) StartAutomation(ctx context.Context) error {
	if s.scheduling == nil || s.keystone == nil || s.autoLedger == nil {
		s.log.Warn("自动化循环未启动：依赖尚未装配",
			"has_scheduling", s.scheduling != nil,
			"has_keystone", s.keystone != nil, "has_ledger", s.autoLedger != nil)
		return nil
	}
	var startErr error
	s.automationStartOnce.Do(func() {
		if err := orchestration.ReconcileSchedRunning(s.st, s.scheduling, s.log); err != nil {
			s.log.Error("自动化循环未启动：启动调度占用对账失败", "error_kind", "reconcile", "cause", err)
			startErr = fmt.Errorf("启动调度占用对账: %w", err)
			return
		}
		s.log.Info("自动化清队与事件唤醒循环启动",
			"poll", automationPollInterval, "queue_kinds", scheduling.QueueKinds)
		go func() {
			// B444：先清扫上次进程留下的首轮在途驻场行（本机的），再进循环——放在循环 goroutine 里，
			// 清扫再慢也不阻断启动；首个消费轮在清扫之后，不会把被打断的在途行当「暂缓」无限挂住。
			s.sweepInterruptedResidents()
			s.automationLoop(ctx)
		}()
	})
	return startErr
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
	// 一轮一组（B393 复评 major + review-4 major）：出队前取该请求未收尾的
	// 队列轮 roundID（键=IgnitionRequest 身份，首出队新建）；失败回填后 2s
	// 重试是同一请求 → 复用同一 ID，EnsureComment 幂等 → 重试不新增行。
	// 成功收尾才清，下一条 IgnitionRequest（即使同卡换节点）开新一轮。
	roundID := s.retainWakeQueueRound(req)
	result, err := s.wakeCoordinatorRoundID(ctx, req.Card, []keystone.WakeEvent{{
		Kind: keystone.WakeQueueRelease, Card: req.Card, Summary: summary,
	}}, nil, roundID)
	if err != nil {
		return fmt.Errorf("队列出队唤醒失败: %w", err)
	}
	s.clearWakeQueueRound(req)
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
	if err := s.coordinatorSeatGate(card, source, rebind); err != nil {
		return zero, err
	}
	squad, err := s.resolveCoordinatorSquad()
	if err != nil {
		return zero, &coordinatorLookupError{err: err}
	}
	transferExpect := ""
	if rebind {
		transferExpect = expect
	}
	return s.runOnCoordinatorCarrier(coordinatorCarrierPlan{
		squad: squad.Name, releaseKey: card,
		attrs:   []any{"card", card, "source", source, "rebind", rebind},
		workdir: func() string { return s.resolveCoordWorkdir(card) },
		launch: func(round coordinatorCarrierRound) (keystone.RoundResult, error) {
			return s.bindLaunchedCoordinator(ctx, card, source, expect, rebind, round)
		},
		transfer: func(binding scheduling.Binding, carrier scheduling.Carrier) (keystone.RoundResult, error) {
			return s.transferCoordinatorFirstRound(ctx, card, binding, carrier, transferExpect)
		},
	})
}

// launchCoordinatorRoundFrozen 是卡首轮转交的收件端（B448，plan §3）：受理机已选定小队与载体，本机在卡锁内
// 重查席位占用，按冻结载体 AdmitSeatCarrier 准入（F15，不重新选载体），载体不在本机即 not_local 拒绝（F37，
// 绝不再转交），否则走与本机拉起逐字相同的 bindLaunchedCoordinator（含 B446 自坐兜底）。
// expect 非空 = 换绑（Q1 甲，RebindSeatMode(expect) 落座，F14），空 = 首轮（BindSeatMode）。
// 错误形状与 launchCoordinatorRoundWithExpect 相同，另加 *firstRoundNotLocalError。
func (s *Server) launchCoordinatorRoundFrozen(ctx context.Context, card, squad, carrier, expect string) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	const source = "coordinate"
	rebind := expect != ""
	lock := s.coordinatorLock(card)
	lock.Lock()
	defer lock.Unlock()
	if err := s.coordinatorSeatGate(card, source, rebind); err != nil {
		return zero, err
	}
	return s.runOnCoordinatorCarrier(coordinatorCarrierPlan{
		squad: squad, releaseKey: card, frozenCarrier: carrier,
		attrs:   []any{"card", card, "source", source, "rebind", rebind, "transferred", true},
		workdir: func() string { return s.resolveCoordWorkdir(card) },
		launch: func(round coordinatorCarrierRound) (keystone.RoundResult, error) {
			return s.bindLaunchedCoordinator(ctx, card, source, expect, rebind, round)
		},
	})
}

// coordinatorSeatGate 是 Launch 前的席位占用判定（调用方须持卡的 coordinatorLock）：首轮要求空座，换绑要求
// 有座；不符返回包 ledger.ErrCASConflict 的错误（HTTP 409）。
func (s *Server) coordinatorSeatGate(card, source string, rebind bool) error {
	current, err := s.ledger.GetCard(card)
	if err != nil {
		return err
	}
	occupied := current.DriverSession != "" || current.DriverMode != ""
	if !rebind && occupied {
		err := fmt.Errorf("卡 %s 当前席位状态不适合此操作: %w", card, ledger.ErrCASConflict)
		s.log.Warn("协调者拉起在 Launch 前被席位拦截", "card", card, "source", source, "rebind", rebind, "cause", err)
		return err
	}
	if rebind && !occupied {
		err := fmt.Errorf("卡 %s 当前席位状态不适合此操作: %w", card, ledger.ErrCASConflict)
		s.log.Warn("协调者换绑在 Launch 前被席位拦截", "card", card, "source", source, "cause", err)
		return err
	}
	return nil
}

// bindLaunchedCoordinator 是卡「叫机器人」在名额窗口内的卡专属半边：keystone.LaunchForCard 跑首轮 → 编码
// 席位身份 → BindSeatMode / RebindSeatMode 落 push 席位与 bearing。席位没落时留 needs_human 并带回新会话
// 身份（coordinatorSeatConflict）。
func (s *Server) bindLaunchedCoordinator(ctx context.Context, card, source, expect string, rebind bool,
	round coordinatorCarrierRound) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	binding, carrier, spec := round.binding, round.carrier, round.spec
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
		err = s.ledger.RebindSeatMode(card, identity, proto.SeatSourceCoordinate, proto.SeatModePush, expect,
			coordinatorBearing(binding, carrier, spec))
	} else {
		err = s.ledger.BindSeatMode(card, identity, proto.SeatSourceCoordinate, proto.SeatModePush,
			coordinatorBearing(binding, carrier, spec))
	}
	if err != nil && errors.Is(err, ledger.ErrCASConflict) {
		err = s.adoptSelfSeatedCoordinator(card, identity, binding, carrier, spec, err)
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

// adoptSelfSeatedCoordinator 是 launch 收尾撞席后的兜底（B446，真机 B444 R6 卡 D5）：协调者在首轮里自己
// `card bind` / `card rebind --self` 坐下时，席位已是本回合会话、但落成 bind/pull 零承载——pull 席位不会被
// push 唤醒，等于叫不醒。席位身份恰为本回合会话时，以它为 expect 原子改写成 coordinate/push + 承载并视为
// 成功；别的会话占着（或重读失败、改写失败）一律返回错误，交回调用方走 409 + needs_human。
// 参数 cause 是收尾坐下的原始 CAS 错误，非本会话时原样返回。
func (s *Server) adoptSelfSeatedCoordinator(card, identity string, binding scheduling.Binding,
	carrier scheduling.Carrier, spec keysclient.SessionSpec, cause error) error {
	current, err := s.ledger.GetCard(card)
	if err != nil {
		s.log.Error("收尾撞席后重读卡失败", "card", card, "seat", identity, "cause", err)
		return cause
	}
	if current.DriverSession != identity {
		s.log.Warn("收尾撞席：席位被别的会话占着，不接管", "card", card,
			"seat", identity, "holder", current.DriverSession, "cause", cause)
		return cause
	}
	s.log.Warn("协调者首轮已自己坐下，收尾改写为机器人席位", "card", card, "seat", identity,
		"old_source", current.DriverSource, "old_mode", current.DriverMode)
	if err := s.ledger.RebindSeatMode(card, identity, proto.SeatSourceCoordinate, proto.SeatModePush, identity,
		coordinatorBearing(binding, carrier, spec)); err != nil {
		s.log.Error("收尾改写自坐席位失败", "card", card, "seat", identity, "cause", err)
		return err
	}
	s.log.Info("收尾已接管自坐席位", "card", card, "seat", identity)
	return nil
}

// coordinatorCarrierRound 是协调小队一次拉起回合在名额窗口内可用的材料：准入出的 binding、读出的载体登记、
// 规范化后的 SessionSpec（CLI/HOME/模型来自载体与小队，Workdir 由调用方解析）。
type coordinatorCarrierRound struct {
	binding scheduling.Binding
	carrier scheduling.Carrier
	spec    keysclient.SessionSpec
}

// coordinatorCarrierPlan 是 runOnCoordinatorCarrier 的一次调用：
//   - squad：已由调用方选定的协调小队；releaseKey：名额归还日志的归属键（卡号或 resident:<会话>）；
//     attrs：调用方日志上下文。
//   - frozenCarrier 空 = 受理端：LaunchAdmit(squad) 选载体；载体在远端 → 先归还名额（F32）再调 transfer。
//     非空 = 收件端（B448 转交来的）：AdmitSeatCarrier(squad, frozenCarrier) 冻结准入（F15）；载体不在本机 →
//     归还名额并返回 *firstRoundNotLocalError，绝不出站（F37）。
//   - workdir 在本机分支校验之后调用；launch 在名额窗口内执行；transfer 只在受理端远端分支调用，名额已归还。
type coordinatorCarrierPlan struct {
	squad         string
	releaseKey    string
	attrs         []any
	frozenCarrier string
	workdir       func() string
	launch        func(coordinatorCarrierRound) (keystone.RoundResult, error)
	transfer      func(scheduling.Binding, scheduling.Carrier) (keystone.RoundResult, error)
}

// firstRoundNotLocalError 是收件端的防环拒绝（S5）：转交来的首轮，载体却不在本机（两机设备表不一致）。
type firstRoundNotLocalError struct{ carrier, machine string }

func (e *firstRoundNotLocalError) Error() string {
	return fmt.Sprintf("转交到本机的协调者首轮，但载体 %s 不在本机（设备表不一致？）", e.carrier)
}

// runOnCoordinatorCarrier 是「用协调小队拉起一轮新会话」的共用骨架（B444 P2 自卡「叫机器人」抽出，卡席位与
// push 驻场者两个调用方共用，plan §3 P2「抽共用，不复制」），也是 B448 首轮转交的唯一收口点：准入 → 读载体 →
// 载体在远端：受理端归还名额后转交载体机（plan.transfer），收件端防环拒绝 → 载体在本机：名额在回合结束即
// 归还 → 组 spec（workdir 由调用方回调解析）→ 规范化 → 在名额窗口内执行 launch（调用方的 keystone 首轮与落账）。
//
// 返回 launch / transfer 的结果。错误：准入失败 → *coordinatorAdmissionError（调用方据此 409 / 排队）；载体读
// 失败、spec 规范化失败 → 普通错误；收件端载体非本机 → *firstRoundNotLocalError；launch / transfer 的错误原样返回。
// 名额：每条出口都恰归还一次本次准入（released 标志防重复 Release 误减别人的计数）。
func (s *Server) runOnCoordinatorCarrier(plan coordinatorCarrierPlan) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	squad, attrs := plan.squad, plan.attrs
	var binding scheduling.Binding
	var err error
	if plan.frozenCarrier != "" {
		binding, err = s.scheduling.AdmitSeatCarrier(squad, plan.frozenCarrier)
	} else {
		binding, err = s.scheduling.LaunchAdmit(squad)
	}
	if err != nil {
		return zero, &coordinatorAdmissionError{squad: squad, err: err}
	}
	// 名额在本回合结束即归还：协调者是 print 一次性回合（无窗口可挂），
	// 不再依赖「关 tab」释放，否则一次拉起会把该载体的协调者名额永久占住。
	released := false
	release := func() {
		if !released {
			released = true
			s.releaseSchedulingBinding(plan.releaseKey, binding)
		}
	}
	defer release()
	carrier, err := s.scheduling.Carrier(binding.Carrier)
	if err != nil {
		s.log.Error("读协调者载体失败", append(attrs[:len(attrs):len(attrs)],
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)...)
		return zero, fmt.Errorf("读载体 %s: %w", binding.Carrier, err)
	}
	// print 回合由本进程的 hostapi 拉起（载体 CLI 在本机执行）：载体登记在别的机器时，这一轮必须在那台机器
	// 的 agentd 上跑（B448），与续轮唤醒的转交同形。
	if !scheduling.IsLocalMachine(carrier.Machine) && !s.IsSelfTarget(carrier.Machine) {
		// 先归还再出站（F32）：转交可能持续整个回合（≤30m），名额只由执行机持有、执行机归还。
		release()
		if plan.frozenCarrier != "" {
			err := &firstRoundNotLocalError{carrier: binding.Carrier, machine: carrier.Machine}
			s.log.Warn("转交来的协调者首轮载体不在本机，防环拒绝且不再转交", append(attrs[:len(attrs):len(attrs)],
				"squad", binding.Squad, "carrier", binding.Carrier, "machine", carrier.Machine, "cause", err)...)
			return zero, err
		}
		if plan.transfer == nil {
			err := fmt.Errorf("协调者载体 %s 在 %s，调用方未提供转交路径", binding.Carrier, carrier.Machine)
			s.log.Error("协调者载体在远端但无转交路径", append(attrs[:len(attrs):len(attrs)], "cause", err)...)
			return zero, err
		}
		s.log.Info("协调者载体在远端，名额已归还，转交首轮到载体机", append(attrs[:len(attrs):len(attrs)],
			"squad", binding.Squad, "carrier", binding.Carrier, "machine", carrier.Machine)...)
		return plan.transfer(binding, carrier)
	}
	spec := keysclient.SessionSpec{
		CLI: binding.Executor, HomeDir: carrier.HomeDir, Model: binding.Model,
		Workdir: plan.workdir(),
	}
	normalized, err := orchestration.NormalizeCoordinatorSpec(spec)
	if err != nil {
		s.log.Error("规范化协调者 SessionSpec 失败", append(attrs[:len(attrs):len(attrs)],
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)...)
		return zero, err
	}
	spec = normalized
	s.log.Info("自动化拉起协调者回合", append(attrs[:len(attrs):len(attrs)],
		"squad", binding.Squad, "carrier", binding.Carrier,
		"cli", spec.CLI, "home_dir", spec.HomeDir, "workdir", spec.Workdir,
		"machine", carrier.Machine)...)
	return plan.launch(coordinatorCarrierRound{binding: binding, carrier: carrier, spec: spec})
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
// 失败出口（取客户端失败、对端非 2xx）都在本机落一行标明目标机器的 wake_round
// fail（B393 复评：本机认领→转交→对端失败时本机侧也须留痕）。
// roundID 由调用方传入：队列路径跨重试复用同一轮，转交失败行不得随 2s 重试增行。
func (s *Server) transferCoordinatorWake(ctx context.Context, card, seat, machine string,
	raws []proto.LedgerEvent, holder, roundID string) (keystone.RoundResult, error) {
	if roundID == "" {
		roundID = nextWakeRoundID()
	}
	target, err := s.clientForTarget(machine)
	if err != nil {
		s.log.Error("转交唤醒取目标客户端失败", "card", card, "machine", machine, "cause", err)
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("转交唤醒取目标客户端 %s 失败: %v", machine, err))
		return keystone.RoundResult{}, fmt.Errorf("转交唤醒取目标客户端 %s: %w", machine, err)
	}
	resp, err := target.MarkForwarded().CoordinatorWake(ctx, card, proto.CoordinatorWakeReq{
		Seat: seat, Events: raws, Holder: holder,
	})
	if err != nil {
		s.log.Error("转交唤醒到目标机失败", "card", card, "machine", machine, "cause", err)
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("转交唤醒到 %s 失败: %v", machine, err))
		return keystone.RoundResult{}, fmt.Errorf("转交唤醒到 %s: %w", machine, err)
	}
	// resp.LetterUnopened 不再读（B435 X，plan Q2 甲）：它只在被转交端为 B434 卡级桌面端席位写信
	// 时为真，该席位已退役；混版本旧对端若仍回 true，按普通成功回执（woke=false）收尾。
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
// 非队列入口：每次调用新开一轮（roundID 内部生成）。
func (s *Server) wakeCoordinatorRound(ctx context.Context, card string,
	evs []keystone.WakeEvent) (keystone.RoundResult, error) {
	return s.wakeCoordinatorRoundRaw(ctx, card, evs, nil)
}

// wakeCoordinatorRoundRaw 是唤醒回合的现行实现（B389 §3.2）：先读承载记录定
// 归属，本机分支用承载记录构造 SessionSpec 并按冻结载体申请名额（不再
// LaunchAdmit 重选载体），远端分支转交（T4）。raws 是本批原始账本事件，转交面
// 需要它携带本批 seq 集合（D4：queue_release 合成唤醒没有账本行）。
// 非队列路径每次新开一轮。
func (s *Server) wakeCoordinatorRoundRaw(ctx context.Context, card string,
	evs []keystone.WakeEvent, raws []proto.LedgerEvent) (keystone.RoundResult, error) {
	return s.wakeCoordinatorRoundID(ctx, card, evs, raws, "")
}

// wakeCoordinatorRoundID 是唤醒回合的现行实现。roundID 非空时沿用调用方给定的
// 轮次标识（队列路径跨重试复用，一轮一组为上限）；空串则新开一轮。
// pull 席位零回合（只 Forget + Info，不落 wake_round 行）。
// 失败出口审计（B393 复评）：start 之前/转交/回合中的错误分支都尽量本机落一行
// wake_round fail（键含 roundID，一轮一组）。例外两条：
//  1. GetCard 失败——EnsureComment 同样要读卡，落行必败，只上抛错误；
//  2. AdmitSeatCarrier 准入失败——消费循环按 2s 节拍重试（B390 P2 不收尾），
//     每次落行会刷屏；可见性由 recordAdmissionStall 阈值 needs_human 承担。
func (s *Server) wakeCoordinatorRoundID(ctx context.Context, card string,
	evs []keystone.WakeEvent, raws []proto.LedgerEvent, roundID string) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	if roundID == "" {
		roundID = nextWakeRoundID()
	}
	current, err := s.ledger.GetCard(card)
	if err != nil {
		return zero, fmt.Errorf("读取唤醒席位: %w", err)
	}
	if current.DriverSession == "" && current.DriverMode == "" {
		s.keystone.Forget(card)
		s.log.Info("空座跳过协调者唤醒", "card", card, "event_count", len(evs))
		return zero, nil
	}
	if current.DriverMode == string(proto.SeatModePull) {
		// B435 C-316/C-319：pull 席位零回合（queue_release 直接放行、不回填）。
		s.keystone.Forget(card)
		s.log.Info("bind 席位跳过协调者唤醒", "card", card, "event_count", len(evs))
		return zero, nil
	}
	if current.DriverMode != string(proto.SeatModePush) {
		err := fmt.Errorf("唤醒席位模式非法 %q（卡 %s）", current.DriverMode, card)
		s.log.Warn("唤醒被拒：席位模式非法", "card", card, "mode", current.DriverMode, "round_id", roundID, "cause", err)
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, err.Error())
		return zero, err
	}
	if err := proto.ValidateSeat(current.DriverSession, proto.SeatSource(current.DriverSource)); err != nil {
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("唤醒席位非法: %v", err))
		return zero, fmt.Errorf("唤醒席位非法: %w", err)
	}
	bearing, hasBearing, err := s.ledger.SeatBearingOf(card)
	if err != nil {
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("读取唤醒承载记录: %v", err))
		return zero, fmt.Errorf("读取唤醒承载记录: %w", err)
	}
	if !hasBearing {
		// 存量 coordinate 席位无承载：显式修复路径（契约 §2.3）——落恰一条
		// EvSeatBearingMissing 并按「需要人」展示，本轮该卡跳过。
		if err := s.reportSeatBearingMissing(card, current.DriverSession); err != nil {
			s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("卡 %s 缺承载显式路径: %v", card, err))
			return zero, fmt.Errorf("卡 %s 缺承载显式路径: %w", card, err)
		}
		return zero, nil
	}
	if !scheduling.IsLocalMachine(bearing.Machine) && !s.IsSelfTarget(bearing.Machine) {
		if len(raws) == 0 {
			// D4：队列出队的合成唤醒没有 card_events 行，装不进冻结 DTO。显式失败，
			// 由 drainIgnitionRequest 回填队列并留需要人痕迹；不静默本机执行。
			// 转交前置失败同样本机留痕，标明目标机器（B393 复评：转交路径失败出口）。
			s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf(
				"协调者承载在远端 %s，但本批唤醒无账本事件（queue_release 合成唤醒）不可转交",
				bearing.Machine))
			return keystone.RoundResult{}, fmt.Errorf(
				"协调者承载在远端 %s，但本批唤醒无账本事件（queue_release 合成唤醒）不可转交",
				bearing.Machine)
		}
		return s.transferCoordinatorWake(ctx, card, current.DriverSession, bearing.Machine,
			raws, s.wakeClaimHolder(), roundID)
	}
	squad, err := s.resolveCoordinatorSquad()
	if err != nil {
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("识别协调者小队失败: %v", err))
		return zero, &coordinatorLookupError{err: err}
	}
	binding, err := s.scheduling.AdmitSeatCarrier(squad.Name, bearing.Carrier)
	if err != nil {
		// 准入失败不落 wake_round fail：见函数头失败出口审计第 2 条例外。
		return zero, &coordinatorAdmissionError{squad: squad.Name, err: err}
	}
	defer s.releaseSchedulingBinding(card, binding)
	carrier, err := s.scheduling.Carrier(bearing.Carrier)
	if err != nil {
		s.log.Error("读协调者载体失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("读载体 %s 失败: %v", binding.Carrier, err))
		return zero, fmt.Errorf("读载体 %s: %w", binding.Carrier, err)
	}
	cli, _, err := proto.ParseSeatIdentity(current.DriverSession)
	if err != nil {
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("解析唤醒席位 CLI 失败: %v", err))
		return zero, fmt.Errorf("解析唤醒席位 CLI: %w", err)
	}
	spec := keysclient.SessionSpec{
		CLI: cli, HomeDir: bearing.HomeDir, Model: bearing.Model, Workdir: bearing.Workdir,
	}
	normalized, err := orchestration.NormalizeCoordinatorSpec(spec)
	if err != nil {
		s.log.Error("规范化协调者 SessionSpec 失败", "card", card,
			"squad", binding.Squad, "carrier", binding.Carrier, "cause", err)
		s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("规范化协调者 SessionSpec 失败: %v", err))
		return zero, err
	}
	spec = normalized
	s.log.Info("自动化唤醒协调者回合", "card", card,
		"event_count", len(evs), "squad", binding.Squad, "carrier", binding.Carrier,
		"cli", spec.CLI, "home_dir", spec.HomeDir, "frozen_from_bearing", true)
	// start/end/fail 共用同一 roundID（一轮一组为上限）；session 未知（resume 前）
	// → Session 留 nil，序列化边界保留「缺失 vs 零」。
	startPayload, _ := json.Marshal(WakeRoundEvent{Phase: "start"})
	if _, err := s.ledger.EnsureComment(card, WakeRoundDedupePrefix+":start:"+roundID,
		string(startPayload), "agentd"); err != nil {
		s.log.Warn("唤醒回合起止事件写入失败（不覆盖唤醒结果）", "card", card,
			"round_id", roundID, "cause", err)
	}
	started := time.Now()
	s.log.Log(ctx, wakeRoundLogLevel, "唤醒回合开始", "card", card,
		"event_count", len(evs), "squad", binding.Squad, "carrier", binding.Carrier,
		"round_id", roundID)
	result, err := s.keystone.Wake(ctx, card, evs, spec)
	if err != nil {
		class := wakeFailClass(err)
		// 失败终态行：phase=fail + class + 原因摘要（与 start 同 roundID，一轮一组）。
		// 失败分支过去只打日志不下账；本行给账本「没跑 vs 跑了但失败」的区分度，
		// 并让超时轮恰一次落 needs_human（B399 r2 §5）。
		s.writeWakeRoundFail(card, roundID, class, truncateRunes(err.Error(), 400))
		s.log.Log(ctx, wakeRoundLogLevel, "唤醒回合失败", "card", card,
			"event_count", len(evs), "squad", binding.Squad, "carrier", binding.Carrier,
			"round_id", roundID, "class", class, "cause", err)
		// 失败路径补 end 行（B399 r2 用户裁定 4）：start/end 对称，可按 round_id 对账。
		s.log.Log(ctx, wakeRoundLogLevel, "唤醒回合结束", "card", card,
			"round_id", roundID, "class", class, "ok", false)
		return result, fmt.Errorf("唤醒协调者回合失败: %w", err)
	}
	dur := time.Since(started)
	endPayload, _ := json.Marshal(WakeRoundEvent{
		Phase: "end", Session: &result.SessionID, DurationMs: ptrInt64(dur.Milliseconds()),
	})
	if _, err := s.ledger.EnsureComment(card, WakeRoundDedupePrefix+":end:"+roundID,
		string(endPayload), "agentd"); err != nil {
		s.log.Warn("唤醒回合结束事件写入失败", "card", card,
			"round_id", roundID, "cause", err)
	}
	s.log.Log(ctx, wakeRoundLogLevel, "唤醒回合结束", "card", card,
		"rebuilt", result.Rebuilt, "escalated", result.Escalated,
		"session", result.SessionID, "duration", dur.String(), "round_id", roundID,
		"class", "", "ok", true)
	if result.Rebuilt && result.SessionID != "" && result.SessionID != current.DriverSession {
		identity, encodeErr := proto.EncodeSeatIdentity(cli, result.SessionID)
		if encodeErr != nil {
			s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("重建后编码新席位失败: %v", encodeErr))
			return result, fmt.Errorf("重建后编码新席位: %w", encodeErr)
		}
		if rebindErr := s.ledger.RebindSeatMode(card, identity, proto.SeatSourceCoordinate, proto.SeatModePush, current.DriverSession,
			coordinatorBearing(binding, carrier, spec)); rebindErr != nil {
			if errors.Is(rebindErr, ledger.ErrCASConflict) {
				s.log.Error("协调者重建后席位 CAS 冲突，新会话保留待人工回收", "card", card,
					"event_count", len(evs), "session", result.SessionID, "cause", rebindErr)
				s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("重建后席位 CAS 冲突: %v", rebindErr))
				return result, &coordinatorSeatConflict{result: result}
			}
			s.writeWakeRoundFail(card, roundID, wakeFailClassOther, fmt.Sprintf("重建后写协调者席位失败: %v", rebindErr))
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

// wakeQueueRoundKey 是队列路径「一轮」的分组键：同一 IgnitionRequest（失败
// 回填后字段逐位相同）得同一键，共享 roundID；同卡不同请求（如 implement→
// review）键不同，各开一轮——契约是「同一 IgnitionRequest 为一轮」，不是
// 「同一卡为一轮」（B393 review-4 major）。用 JSON 序列化整个请求做身份：
// 字段集就是队列持久化面，回填→出队 round-trip 后逐字节稳定。
func wakeQueueRoundKey(req scheduling.IgnitionRequest) string {
	b, err := json.Marshal(req)
	if err != nil {
		// IgnitionRequest 全是标量字段，Marshal 理论上不会失败；兜底退化为
		// card+node 复合键，仍区分同卡换节点，不静默合并成 card 键。
		return req.Card + "\x00" + req.Node
	}
	return string(b)
}

// retainWakeQueueRound 返回该请求未收尾的队列轮 roundID；没有则新建并记住。
// 队列出队失败回填后 2s 重试复用同一 ID——一轮一组为上限，重试不得新增行
// （B393 复评 major）。成功收尾由 clearWakeQueueRound 摘除。
func (s *Server) retainWakeQueueRound(req scheduling.IgnitionRequest) string {
	key := wakeQueueRoundKey(req)
	s.wakeQueueRoundsMu.Lock()
	defer s.wakeQueueRoundsMu.Unlock()
	if s.wakeQueueRounds == nil {
		s.wakeQueueRounds = make(map[string]string)
	}
	if id, ok := s.wakeQueueRounds[key]; ok && id != "" {
		return id
	}
	id := nextWakeRoundID()
	s.wakeQueueRounds[key] = id
	return id
}

// clearWakeQueueRound 摘除该请求的队列轮身份；下一次出队（同请求重来或
// 换新请求）开新一轮。
func (s *Server) clearWakeQueueRound(req scheduling.IgnitionRequest) {
	key := wakeQueueRoundKey(req)
	s.wakeQueueRoundsMu.Lock()
	defer s.wakeQueueRoundsMu.Unlock()
	delete(s.wakeQueueRounds, key)
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
