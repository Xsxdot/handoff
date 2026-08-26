// 编制域派发接入（B156.3 K2）：节点绑小队后的 Squad 解析层与排队分支。
//
// 职责：
//   - Override.Squad 非空的节点，先把本次一次性覆盖交给编制域 Admit，用 Binding
//     的有效三元组接管本次派发的目标机/执行者/模型（契约 §5 解析层形态）
//   - 满员（ErrNoSlot）转 Enqueue 持久排队，本轮以排队形态结束：不起 runner、
//     不产生 task、不留痕失败
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
// errors.Is 分流；本函数自己只消化 ErrNoSlot（转排队）。Ready 快照恒 true 的
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
		s.log.Info("小队节点准入成功", "card", cardID, "node", node.Name,
			"squad", binding.Squad, "carrier", binding.Carrier, "target", binding.Target,
			"executor", binding.Executor, "model", binding.Model)
		return binding, squadDispatchAdmitted, nil
	}
	if errors.Is(err, scheduling.ErrNoSlot) {
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

// effectiveCovers 算出本次派发的一次性覆盖三元组：请求覆盖 > 节点覆盖，
// executor/model 沿用 dispatchNodeWithGate（runner.go:311）的成对规则——换掉
// 执行器时切断下层模型，显式重述同一执行器不改变模型。
//
// 为什么装配侧要有这份副本：Squad 解析必须在 ViaTemplate 之前拿到完整覆盖去问
// 编制域，而既有合并在 ledgerstep 内部发生、ledgerstep 又不得 import 编制域
// （契约 §5）。两份公式的等价性由两侧各自的矩阵测试锁住：
//   - 本副本：TestEffectiveCoversParityMatrix（本文件同名测试文件）；
//   - 链路侧：TestRunnerExecutorModelOverridePriorityAndPairRule
//     （internal/ledgerstep/runner_test.go:117）；
//
// 两侧矩阵逐行对应，任何一侧改规则必须同步另一侧，否则其中一处测试翻红。
func effectiveCovers(req proto.CardStepReq, node ledger.NodeDef) (target, executor, model string) {
	target = req.Target
	if target == "" {
		target = node.Override.Target
	}
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
	return target, executor, model
}
