// 编制域派发接入（B156.3 K2）：节点绑小队后的 Squad 解析层与排队分支。
//
// 职责：
//   - Override.Squad 非空的节点，先把本次一次性覆盖交给编制域 Admit，用 Binding
//     的有效三元组接管本次派发的目标机/执行者/模型（契约 §5 解析层形态）
//   - 满员（ErrNoSlot）或准入重试预算耗尽（ErrRetryExhausted，瞬态争用）转
//     Enqueue 持久排队，本轮以排队形态结束：不起 runner、不产生 task、不留痕
//     失败（后者必须先落 WARN 标记词，预算耗尽信号不许静默吞掉）
//   - 其余错误（ErrNoHealthy/角色不符/未装配等）上浮为受理失败，与排队静默可区分
//
// 边界：
//   - 只服务执行者小队；协调者小队的 LaunchAdmit 归 keystone 拉起链，不经此文件
//   - ledgerstep 对编制域零认识：排队分支全部住在装配侧注入链（契约 §5 铁律，
//     grep 反面判据归 Task D）
//   - 出队后的真派不在本卡范围（清队循环归 K5），届时再次走同一步入口
package agentd

import (
	"errors"
	"fmt"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// squadDispatchOutcome 区分一次小队节点受理的同步结局。
type squadDispatchOutcome int

const (
	// squadDispatchAdmitted 准入成功：binding 有效，调用方继续装配派发。
	squadDispatchAdmitted squadDispatchOutcome = iota
	// squadDispatchQueued 满员已持久入队：本轮以排队形态结束，不得再起 runner。
	squadDispatchQueued
)

// admitSquadStep 对绑了小队的节点做准入或入队。
//
// 参数：cardID 卡号；req 规范环节请求；node 已解出的节点定义（Override.Squad 非空，
// 由调用方保证）。返回：admitted 时 binding 为有效三级组；queued 时 binding 为零值、
// 请求已在 ignition_queue 持久排队；error 为受理失败，调用方应释放卡槽位并上浮。
//
// 注意：ErrNoHealthy/ErrNotFound/ErrRoleMismatch 都经 %w 包装上浮，调用方继续用
// errors.Is 分流；本函数自己消化 ErrNoSlot 与 ErrRetryExhausted（都转排队，收敛
// 在同一个 Enqueue 调用点；预算耗尽先落 WARN 标记词）。Ready 快照恒 true 的
// 取值决策见 plan §D4。
func (s *Server) admitSquadStep(cardID string, req proto.CardStepReq, node ledger.NodeDef) (scheduling.Binding, squadDispatchOutcome, error) {
	if s.scheduling == nil {
		return scheduling.Binding{}, 0, fmt.Errorf(
			"节点 %s 绑定了小队 %q，但编制域服务未装配（SetupAutomation 未执行或 SetScheduling 未注入）",
			node.Name, node.Override.Squad)
	}
	card, err := s.ledger.GetCard(cardID)
	if err != nil {
		return scheduling.Binding{}, 0, fmt.Errorf("读卡取优先级快照: %w", err)
	}
	resolved, err := s.resolveReceiver(node.Override.Squad)
	if err != nil {
		return scheduling.Binding{}, 0, fmt.Errorf("节点 %s 解析小队 %q: %w", node.Name, node.Override.Squad, err)
	}
	if resolved.Kind != scheduling.ReceiverSquad {
		err := fmt.Errorf("%w: 节点 %s 绑定的接收者 %q 不是小队", scheduling.ErrInvalid, node.Name, resolved.Name)
		s.log.Error("小队节点接收者类型错误", "card", cardID, "node", node.Name,
			"receiver", resolved.Name, "kind", string(resolved.Kind), "cause", err)
		return scheduling.Binding{}, 0, err
	}
	target, executor, model := effectiveCovers(req, node)
	ireq := scheduling.IgnitionRequest{
		Card: cardID, Squad: node.Override.Squad, Node: req.Step,
		Target: target, Executor: executor, Model: model,
		Priority: card.Priority,
		Ready:    true, // 入队快照恒就绪，决策与备选方案见 plan §D4
		Actor:    req.Actor,
	}
	s.log.Info("小队节点准入开始", "card", cardID, "node", node.Name,
		"squad", node.Override.Squad, "cover_target", target,
		"cover_executor", executor, "cover_model", model, "priority", card.Priority)
	binding, err := s.scheduling.Admit(ireq)
	if err == nil {
		id := scheduling.PhysicalIdentity{Machine: binding.Target, CLI: binding.Executor, HomeDir: binding.HomeDir}
		bound, bindErr := scheduling.BindPhysical(id, scheduling.PhysicalOverlay{})
		if bindErr != nil {
			s.log.Error("小队节点绑定物理身份失败", "card", cardID, "node", node.Name,
				"squad", binding.Squad, "carrier", binding.Carrier, "cause", bindErr)
			if relErr := s.scheduling.Release(binding.Squad, binding.Carrier); relErr != nil {
				s.log.Error("物理身份绑定失败后的准入回滚失败", "card", cardID,
					"squad", binding.Squad, "carrier", binding.Carrier, "cause", relErr)
			}
			return scheduling.Binding{}, 0, bindErr
		}
		binding.Target = bound.Machine
		binding.Executor = bound.CLI
		binding.HomeDir = bound.HomeDir
		binding.Model = scheduling.EffectiveModel(binding.Model, model)
		s.log.Info("小队节点准入成功", "card", cardID, "node", node.Name,
			"squad", binding.Squad, "carrier", binding.Carrier, "target", binding.Target,
			"executor", binding.Executor, "model", binding.Model)
		return binding, squadDispatchAdmitted, nil
	}
	if errors.Is(err, scheduling.ErrNoSlot) || errors.Is(err, scheduling.ErrRetryExhausted) {
		if errors.Is(err, scheduling.ErrRetryExhausted) {
			// 预算耗尽是瞬态争用（请求合法、容量可能存在），停去等人是错的：
			// 与满员同处置转排队；但它是真实信号，WARN 落痕不许吞（协调者裁决
			// 2026-08-26）。载体身份在 cause 的计数键里（carrier/<名>）——失败时
			// Binding 为零值，公开面本轮冻结加不了离散载体字段。
			s.log.Warn("准入重试预算耗尽，按满员转排队", "card", cardID,
				"node", node.Name, "squad", node.Override.Squad, "actor", req.Actor,
				"cause", err)
		}
		position, enqErr := s.scheduling.Enqueue(ireq, scheduling.KindIgnitionQueue)
		if enqErr != nil {
			return scheduling.Binding{}, 0, fmt.Errorf("满员转排队失败: %w", enqErr)
		}
		s.log.Info("小队满员，点火请求已持久排队", "card", cardID, "node", node.Name,
			"squad", node.Override.Squad, "queue_position", position, "actor", req.Actor)
		return scheduling.Binding{}, squadDispatchQueued, nil
	}
	return scheduling.Binding{}, 0, fmt.Errorf("小队 %q 准入被拒: %w", node.Override.Squad, err)
}

// effectiveCovers 只算模型覆盖；物理身份必须由已准入载体提供。
// 返回的 target/executor 恒为空，避免卡节点在绑定前后形成第二条物理落点路径。
//
// 模型的成对规则仍与 dispatchNodeWithGate 一致；ledgerstep 不再接收卡节点物理
// 覆盖。模型行由本文件的矩阵测试锁定，物理恒空是本卡 P7(a) 的新边界。
func effectiveCovers(req proto.CardStepReq, node ledger.NodeDef) (target, executor, model string) {
	executor = node.Override.Executor
	model = node.Override.Model
	if req.Executor != "" {
		executor = req.Executor
		if req.Model != "" || req.Executor != node.Override.Executor {
			model = req.Model
		}
	} else if req.Model != "" {
		model = req.Model
	}
	return "", "", model
}
