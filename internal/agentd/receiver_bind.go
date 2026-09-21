// receiver_bind.go —— 裸派发与卡节点共用的接收者准备链（B233.5）。
//
// 职责：把接收者名称解析为载体或小队，完成一次准入，并冻结机器、CLI、HOME
// 与模型快照。边界：不启动任务、不写 Task、不读卡列；Manager 不得调用本文件。
package agentd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Xsxdot/handoff/internal/scheduling"
)

// resolveReceiver 通过编制域统一解析显式或默认接收者。
// 显式名称不依赖默认记录；空名称的默认记录错误原样上浮。
func (s *Server) resolveReceiver(requested string) (scheduling.ResolvedReceiver, error) {
	if s.scheduling == nil {
		err := errors.New("编制域服务未装配（SetupAutomation 未执行）")
		s.log.Error("接收者解析失败", "requested", requested, "error_kind", "scheduling_unavailable", "cause", err)
		return scheduling.ResolvedReceiver{}, err
	}

	defaultName, defErr := s.scheduling.DefaultCarrier()
	if defErr != nil && strings.TrimSpace(requested) == "" {
		s.log.Warn("派发无有效默认载体", "requested", requested, "error_kind", "no_default", "cause", defErr)
		return scheduling.ResolvedReceiver{}, defErr
	}
	if defErr != nil {
		defaultName = ""
	}

	var lookupErr error
	lookup := func(name string) (bool, bool) {
		_, carrierErr := s.scheduling.Carrier(name)
		_, squadErr := s.scheduling.Squad(name)
		if carrierErr != nil && !errors.Is(carrierErr, scheduling.ErrNotFound) {
			lookupErr = carrierErr
		}
		if squadErr != nil && !errors.Is(squadErr, scheduling.ErrNotFound) && lookupErr == nil {
			lookupErr = squadErr
		}
		return carrierErr == nil, squadErr == nil
	}
	resolved, err := scheduling.ResolveLookup(requested, defaultName, lookup)
	if lookupErr != nil {
		s.log.Error("接收者登记查询失败", "requested", requested, "default", defaultName, "error_kind", "lookup", "cause", lookupErr)
		return scheduling.ResolvedReceiver{}, lookupErr
	}
	if err != nil {
		s.log.Warn("接收者解析失败", "requested", requested, "default", defaultName, "error_kind", "resolve", "cause", err)
		return scheduling.ResolvedReceiver{}, err
	}
	s.log.Info("接收者已解析", "requested", requested, "name", resolved.Name,
		"kind", string(resolved.Kind), "via_default", resolved.ViaDefault, "error_kind", "success")
	return resolved, nil
}

// prepareReceiverBinding 是裸派发的完整准备链：ResolveLookup → 准入 → BindPhysical。
// 返回的 Binding 是可交给 Manager 的身份快照；准入已成功但后续绑定失败时，本函数
// 负责释放本次占用。
func (s *Server) prepareReceiverBinding(requested string, overlay scheduling.PhysicalOverlay, requestModel string) (scheduling.Binding, scheduling.ResolvedReceiver, error) {
	resolved, err := s.resolveReceiver(requested)
	if err != nil {
		return scheduling.Binding{}, scheduling.ResolvedReceiver{}, err
	}

	var binding scheduling.Binding
	switch resolved.Kind {
	case scheduling.ReceiverCarrier:
		binding, err = s.scheduling.AdmitCarrier(resolved.Name)
	case scheduling.ReceiverSquad:
		binding, err = s.scheduling.Admit(scheduling.IgnitionRequest{
			Squad: resolved.Name, Model: requestModel, Actor: "dispatch",
		})
	default:
		err = fmt.Errorf("%w: 未知接收者类型 %s", scheduling.ErrInvalid, resolved.Kind)
	}
	if err != nil {
		s.log.Error("接收者准入失败", "requested", requested, "name", resolved.Name,
			"kind", string(resolved.Kind), "error_kind", "admit", "cause", err)
		return scheduling.Binding{}, resolved, err
	}

	id := scheduling.PhysicalIdentity{Machine: binding.Target, CLI: binding.Executor, HomeDir: binding.HomeDir}
	bound, err := scheduling.BindPhysical(id, overlay)
	if err != nil {
		s.log.Warn("物理覆盖被拒", "requested", requested, "carrier", binding.Carrier,
			"squad", binding.Squad, "error_kind", "physical_override", "cause", err)
		if relErr := s.scheduling.Release(binding.Squad, binding.Carrier); relErr != nil {
			s.log.Error("物理覆盖拒绝后释放占用失败", "requested", requested,
				"carrier", binding.Carrier, "squad", binding.Squad, "error_kind", "release", "cause", relErr)
		}
		return scheduling.Binding{}, resolved, err
	}
	binding.Target = bound.Machine
	binding.Executor = bound.CLI
	binding.HomeDir = bound.HomeDir
	binding.Model = scheduling.EffectiveModel(binding.Model, requestModel)
	s.log.Info("接收者已绑定", "requested", requested, "name", resolved.Name,
		"kind", string(resolved.Kind), "via_default", resolved.ViaDefault,
		"carrier", binding.Carrier, "squad", binding.Squad, "machine", binding.Target,
		"cli", binding.Executor, "home_dir", binding.HomeDir, "model", binding.Model,
		"error_kind", "success")
	return binding, resolved, nil
}
