// dispatchroute.go —— B452 裸派发的落点判定、机器亲和与跨机转发（S3、S7）。
//
// 职责：
//   - 受理机对「不带冻结身份」的派发先只读定落点：载体直派读载体登记；小队经
//     SelectWhere（亲和谓词在选人之前过滤）
//   - 落点在本机：载体走既有 AdmitCarrier 准备链；小队走 AdmitFrozen（与卡节点同一套语义）
//   - 落点在他机：带冻结身份四键（carrier/squad/home_dir/frozen_target）+ mode、加防环头，
//     经既有 POST /api/tasks 原样转发；载体机的冻结分支准入并建任务；受理机零写入，
//     成功后即时登记镜像行
//   - 亲和不满足：422 + kind=affinity_unmet，零任务、零出站、不换机
//
// 边界：不改冻结分支（带 carrier 的请求从不选人、从不转发）；不重试（重试会把「已建成
// 但回执丢了」变成重复派发）；编排核心不感知转发（spec 实现决定 3）。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// errAffinityUnmet 是机器亲和不满足（F6，422）。文案前缀即用户可见名。
var errAffinityUnmet = errors.New("机器亲和不满足")

// affinityKind 是 422 响应体的 kind 值（F6）。
const affinityKind = "affinity_unmet"

// dispatchLanding 是一次普通派发的落点判定结果。
type dispatchLanding struct {
	// binding：本机落点时是已准入的身份；他机落点时是登记快照（未占名额，由载体机准入）。
	binding  scheduling.Binding
	resolved scheduling.ResolvedReceiver
	remote   bool
}

// affinityFilter 把请求里的亲和字段解析成（目标机器显示名, 谓词）。无亲和返回 nil 谓词。
//
// here 解析为受理机自身；machine 指本机时同 here。本机判据用 IsSelfMachine，与派发
// 「接收机必须是载体登记机」同一把尺子。
func (s *Server) affinityFilter(req dispatchRequest) (string, func(scheduling.Carrier) bool) {
	selfName := s.conf().Name
	if selfName == "" {
		selfName = "本机"
	}
	switch {
	case req.Here, req.Machine != "" && s.IsSelfMachine(req.Machine):
		return selfName, func(c scheduling.Carrier) bool { return s.IsSelfMachine(c.Machine) }
	case req.Machine != "":
		m := req.Machine
		return m, func(c scheduling.Carrier) bool { return c.Machine == m }
	}
	return "", nil
}

// routeOrdinaryDispatch 判定普通派发（无冻结身份）的落点。本机落点已完成准入；他机落点只读。
func (s *Server) routeOrdinaryDispatch(req dispatchRequest) (dispatchLanding, error) {
	if s.scheduling == nil {
		return dispatchLanding{}, errors.New("编制域服务未装配（SetupAutomation 未执行）")
	}
	overlay := scheduling.PhysicalOverlay{Machine: req.Target, CLI: req.Executor, HomeDir: req.HomeDir}
	want, accept := s.affinityFilter(req)
	resolved, err := s.resolveReceiver(req.Receiver)
	if err != nil {
		return dispatchLanding{}, err
	}
	switch resolved.Kind {
	case scheduling.ReceiverCarrier:
		c, err := s.scheduling.Carrier(resolved.Name)
		if err != nil {
			return dispatchLanding{resolved: resolved}, err
		}
		if accept != nil && !accept(c) {
			return dispatchLanding{resolved: resolved}, fmt.Errorf("%w：要求在 %s 上执行，但载体 %s 在 %s 上（受理机 %s）",
				errAffinityUnmet, want, c.Name, c.Machine, s.selfLabel())
		}
		if s.IsSelfMachine(c.Machine) {
			// 本机载体：保留既有 AdmitCarrier 准备链原样不动
			binding, resolved, err := s.prepareReceiverBinding(req.Receiver, overlay, req.Model)
			return dispatchLanding{binding: binding, resolved: resolved}, err
		}
		// 统一准入判据（B453 S5）：载体未上线或所选账号低额度/无额度都在转发前拒，
		// 点名派发不静默跑；落点机 AdmitFrozen 还会再判一次（同一判据）。
		if err := s.scheduling.CheckUsable(c); err != nil {
			return dispatchLanding{resolved: resolved}, err
		}
		bound, err := scheduling.BindPhysical(scheduling.IdentityOf(c), overlay)
		if err != nil {
			return dispatchLanding{resolved: resolved}, err
		}
		return dispatchLanding{resolved: resolved, remote: true, binding: scheduling.Binding{
			Carrier: c.Name, Target: bound.Machine, Executor: bound.CLI, HomeDir: bound.HomeDir,
			Model: scheduling.EffectiveModel(c.Model, req.Model),
		}}, nil
	case scheduling.ReceiverSquad:
		// B453 F14：重跑链排除谓词与亲和谓词分开组合——亲和改写文案只认亲和本身
		exclude := s.rerunExclusion(req.ExcludeAccounts)
		b, err := s.scheduling.SelectWhere(scheduling.IgnitionRequest{
			Squad: resolved.Name, Model: req.Model, Actor: "dispatch",
		}, andAccept(accept, exclude.accept))
		if err != nil {
			if exclude.active() && errors.Is(err, scheduling.ErrNoHealthy) {
				return dispatchLanding{resolved: resolved}, s.rerunNoMemberError(err, resolved.Name, exclude)
			}
			if accept != nil && errors.Is(err, scheduling.ErrNoHealthy) {
				return dispatchLanding{resolved: resolved}, fmt.Errorf("%w：要求在 %s 上执行，但小队 %s 在该机没有在线成员（成员：%s；受理机 %s）",
					errAffinityUnmet, want, resolved.Name, s.squadMemberMachines(resolved.Name), s.selfLabel())
			}
			return dispatchLanding{resolved: resolved}, err
		}
		bound, err := scheduling.BindPhysical(scheduling.PhysicalIdentity{Machine: b.Target, CLI: b.Executor, HomeDir: b.HomeDir}, overlay)
		if err != nil {
			return dispatchLanding{resolved: resolved}, err
		}
		b.Target, b.Executor, b.HomeDir = bound.Machine, bound.CLI, bound.HomeDir
		if !s.IsSelfMachine(b.Target) {
			return dispatchLanding{resolved: resolved, remote: true, binding: b}, nil
		}
		admitted, err := s.scheduling.AdmitFrozen(b)
		return dispatchLanding{resolved: resolved, binding: admitted}, err
	}
	return dispatchLanding{resolved: resolved}, fmt.Errorf("%w: 未知接收者类型 %s", scheduling.ErrInvalid, resolved.Kind)
}

// selfLabel 是本机在报错文案里的名字（有自名用自名）。
func (s *Server) selfLabel() string {
	if n := s.conf().Name; n != "" {
		return n
	}
	return "本机"
}

// squadMemberMachines 列出小队成员「载体@机器(状态)」，供亲和拒绝文案点名缺的东西。
func (s *Server) squadMemberMachines(squad string) string {
	q, err := s.scheduling.Squad(squad)
	if err != nil {
		return "（读取小队失败：" + err.Error() + "）"
	}
	parts := make([]string, 0, len(q.Members))
	for _, m := range q.Members {
		c, err := s.scheduling.Carrier(m.Carrier)
		if err != nil {
			parts = append(parts, m.Carrier+"@?")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s@%s(%s)", c.Name, c.Machine, c.Status))
	}
	return strings.Join(parts, "、")
}

// forwardDispatch 把落点在他机的派发带冻结身份转发到载体机，并按 F8/F9 回写应答。
//
// raw 是调用方原始请求体：转发在它上面改键而不是重编码结构体，新端加的未知字段不被抹掉
// （与 stripWorktreeCardIDs 同一理由）。
func (s *Server) forwardDispatch(w http.ResponseWriter, r *http.Request, req dispatchRequest, raw []byte, landing dispatchLanding) {
	machine := landing.binding.Target
	b := landing.binding
	s.log.Info("dispatch 落点他机，准备转发", "project", req.ProjectID, "receiver", req.Receiver,
		"machine", machine, "carrier", b.Carrier, "squad", b.Squad, "mode", req.Mode)
	t, ok := s.remoteTarget(machine)
	if !ok {
		s.log.Warn("dispatch 转发被拒：落点机器未登记", "machine", machine, "carrier", b.Carrier)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发派发失败：" + unknownMachineMsg(machine)})
		return
	}
	c, err := s.pool.For(machine)
	if err != nil {
		s.log.Error("dispatch 转发失败：取目标客户端失败", "machine", machine, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发派发到执行机 " + machine + " 失败：" + err.Error()})
		return
	}
	if req.Mode == proto.TaskModeReadonly {
		// F8/Q11：老 agentd 会静默忽略 mode 而在主仓原地切分支——转发前核能力位，缺席即拒。
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		st, serr := c.Status(ctx)
		cancel()
		if serr != nil {
			s.log.Warn("dispatch 转发前探活失败", "machine", machine, "cause", serr)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发派发到执行机 " + machine +
				" 失败（探活未通）：" + serr.Error() + "；请求未发出，可修好连通后重派"})
			return
		}
		if st.SkilledDispatchSupported == nil || !*st.SkilledDispatchSupported {
			s.log.Warn("dispatch 转发被拒：执行机版本过旧不支持只读形态", "machine", machine)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "执行机 " + machine +
				" 的 agentd 版本过旧，不支持只读形态，请升级"})
			return
		}
	}
	body, err := frozenForwardBody(raw, b, req.Mode == proto.TaskModeReadonly)
	if err != nil {
		s.log.Error("dispatch 转发失败：重写请求体", "machine", machine, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体必须是 JSON 对象"})
		return
	}
	status, headers, payload, err := s.forwardJSON(r, machine, c, t.Token, body)
	if err != nil {
		s.log.Error("dispatch 转发失败：执行机不可达或回执丢失", "machine", machine, "carrier", b.Carrier, "cause", err)
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "转发派发到执行机 " + machine + " 失败：" +
			err.Error() + "。若请求已送达，任务可能已在 " + machine + " 上创建，请先 handoff tasks --machine " +
			machine + " 核对再重派"})
		return
	}
	if status != http.StatusOK {
		s.log.Warn("dispatch 执行机拒绝转发的派发", "machine", machine, "status", status, "carrier", b.Carrier)
		writeJSON(w, status, prefixRemoteError(payload, machine))
		return
	}
	var task proto.Task
	if err := json.Unmarshal(payload, &task); err != nil {
		s.log.Error("dispatch 转发成功但应答解码失败", "machine", machine, "cause", err)
	} else if uerr := s.mgr.UpsertMirrorTask(machine, task, time.Now().UTC()); uerr != nil {
		// 只影响「不带机器名的命令立即可见」，任务本身已在载体机建好：降级为日志
		s.log.Error("dispatch 转发成功但登记镜像行失败", "task", task.ID, "machine", machine, "cause", uerr)
	}
	s.log.Info("dispatch 已转发到执行机", "task", task.ID, "machine", machine,
		"carrier", b.Carrier, "squad", b.Squad, "mode", req.Mode)
	copyForwardHeaders(w, headers)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)
}

// frozenForwardBody 在原始请求体上写入冻结身份与形态，删去只属于受理机的亲和键。
func frozenForwardBody(raw []byte, b scheduling.Binding, readonly bool) ([]byte, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		return nil, err
	}
	set := func(k string, v any) {
		enc, _ := json.Marshal(v)
		obj[k] = enc
	}
	set("carrier", b.Carrier)
	if b.Squad != "" {
		set("squad", b.Squad)
	} else {
		delete(obj, "squad")
	}
	set("home_dir", b.HomeDir)
	set("frozen_target", b.Target)
	set("target", b.Target)
	set("executor", b.Executor)
	set("model", b.Model)
	if readonly {
		set("new_worktree", true) // 第二道保险：对端即使误收，也不会落到主仓原地
	}
	delete(obj, "machine")
	delete(obj, "here")
	return json.Marshal(obj)
}

// prefixRemoteError 给载体机的错误体 error 字段加「执行机 <M>：」前缀，其余键原样（含 kind）。
// 非 JSON 应答整体包进 error。
func prefixRemoteError(payload []byte, machine string) map[string]any {
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil || obj == nil {
		return map[string]any{"error": "执行机 " + machine + "：" + strings.TrimSpace(string(payload))}
	}
	msg, _ := obj["error"].(string)
	obj["error"] = "执行机 " + machine + "：" + msg
	return obj
}

// checkAffinityFields 是 F5：machine 与 here 互斥。
func checkAffinityFields(req dispatchRequest) error {
	if req.Here && req.Machine != "" {
		return fmt.Errorf("%w: machine 与 here 互斥（--machine 与 --here 只能给一个）", scheduling.ErrInvalid)
	}
	return nil
}
