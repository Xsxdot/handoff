// 本文件实现机器投影与探活：GET /api/machines。
//
// 职责：
//   - 把 cfg.Targets + 本机自身投影成一张机器列表
//   - 并发探活（每台打一次 GET /api/status），共 machineProbeBudget 预算
//
// 边界：
//   - **不建表**：~/.handoff/config.yaml 的 targets 段已经是机器的真相
//     （addr/user/token），再建表就是两份真相——改了配置忘了改表，就会有
//     一台早已删除的机器永远躺在列表里
//   - 只读：执行者开关、审批器配置、重启 agent 等写操作明确不在范围内
//   - 不 ssh：探活走 HTTP，与 CLI 的 handoff status 同源同凭据
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/proto"
)

// machineProbeBudget 是整轮扇出探活的总预算。
//
// 3s 短于任何调用方超时：单台黑洞对端不能把整个列表拖垮。
const machineProbeBudget = 3 * time.Second

// probeMachines 投影并探活全部机器。
//
// 返回：
//   - 机器列表（本机恒在第一条，其余按名字排序）；**永不返回错误**——
//     单台不可达是数据，整体恒 200
func (s *Server) probeMachines(ctx context.Context) proto.MachinesResp {
	ctx, cancel := context.WithTimeout(ctx, machineProbeBudget)
	defer cancel()

	out := make([]proto.Machine, 0, len(s.conf().Targets)+1)
	out = append(out, s.localMachine())

	names := make([]string, 0, len(s.conf().Targets))
	for name := range s.conf().Targets {
		names = append(names, name)
	}
	sort.Strings(names) // 顺序稳定：UI 列表不该每次刷新都跳

	remote := make([]proto.Machine, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string) {
			defer wg.Done()
			remote[i] = s.probeRemote(ctx, name)
		}(i, name)
	}
	wg.Wait()
	out = append(out, remote...)

	unreachable := 0
	for _, m := range out {
		if !m.Reachable {
			unreachable++
		}
	}
	s.log.Info("机器探活完成", "machines", len(out), "unreachable", unreachable)
	return proto.MachinesResp{Machines: out}
}

// localMachine 投影本机：进程内直查，不自拨 HTTP（自拨会在 agentd 忙时把
// 自己的健康状态也一起拖垮，且毫无必要）。
func (s *Server) localMachine() proto.Machine {
	m := proto.Machine{
		Name: "", Addr: s.conf().Listen, Reachable: true,
		Executors: []string{}, ProbeMs: 0, ScratchRoot: s.scratchRoot(),
	}
	if s.mgr == nil {
		// manager 未注入时本机确实答不出运行数据，但它显然“在”——
		// 如实降级：可达但字段留空，附原因
		m.Error = "manager 未就绪"
		return m
	}
	st, err := s.mgr.Status()
	if err != nil {
		s.log.Warn("本机探活：聚合状态失败", "cause", err)
		m.Error = err.Error()
		return m
	}
	fillFromStatus(&m, st)

	// 本机能力位就地填：localMachine 直调 mgr.Status()，不走 HTTP，而能力位
	// 只在 handleStatus 组装 HTTP 响应时才有；本机的平台支持度只有这里知道。
	ptyOK := s.pty.Supported()
	m.PtySupported = &ptyOK
	revealOK := revealSupportedOS
	m.RevealSupported = &revealOK
	return m
}

// probeRemote 探活一台远程机器。
func (s *Server) probeRemote(ctx context.Context, name string) proto.Machine {
	t := s.conf().Targets[name]
	m := proto.Machine{Name: name, Addr: t.Addr, Executors: []string{}}
	start := time.Now()
	// 注意：token 只进请求头，绝不进日志
	st, err := client.New(t.Addr, t.Token).Status(ctx)
	m.ProbeMs = time.Since(start).Milliseconds()
	if err != nil {
		s.log.Warn("机器探活失败", "machine", name, "addr", t.Addr,
			"probe_ms", m.ProbeMs, "cause", err)
		m.Error = err.Error()
		return m
	}
	m.Reachable = true
	fillFromStatus(&m, st)
	s.log.Debug("机器探活成功", "machine", name, "probe_ms", m.ProbeMs,
		"active_tasks", m.ActiveTasks)
	return m
}

// fillFromStatus 把 GET /api/status 的响应投影进机器条目。
//
// executors / default_executor 是探活本来就拿到的东西，丢掉纯属浪费；
// active_tasks 是活跃任务条数。
func fillFromStatus(m *proto.Machine, st *proto.StatusResp) {
	m.Reachable = true
	m.Version = st.Version.Version
	if st.Executors != nil {
		m.Executors = st.Executors
	}
	m.DefaultExecutor = st.DefaultExecutor
	m.ActiveTasks = len(st.Active)

	// 能力位原样搬运，包括 nil：探到了但对端没这个字段，结论就是「没上报」
	m.PtySupported = st.PtySupported
	m.RevealSupported = st.RevealSupported
	m.ScratchRoot = st.ScratchRoot
}

// handleMachines 处理 GET /api/machines。
func (s *Server) handleMachines(w http.ResponseWriter, r *http.Request) {
	s.log.Info("机器列表请求", "remote_addr", r.RemoteAddr)
	writeJSON(w, http.StatusOK, s.probeMachines(r.Context()))
}

// addMachineProbeBudget 是新增开发机时那一次可达性探测的时限。
//
// 比整轮扇出的 machineProbeBudget 宽松：这里是用户点了「添加」在等结果，
// 一次往返慢一点可以接受，误判成不可达才是真的坏体验。
const addMachineProbeBudget = 5 * time.Second

// handleAddMachine 处理 POST /api/machines。
//
// 流程：反序列化 → 校验 → （非 force 时）可达性探测 → 落库 → 返回新列表。
//
// 状态码：
//   - 400 请求体不合法，或探测不通（体内带探测失败原文，供前端原样展示）
//   - 409 同名开发机已存在
//   - 500 落盘失败
//
// 注意：响应体是 proto.MachinesResp，其中的 proto.Machine 没有 Token 字段
// ——令牌只进不出，这条由类型本身保证。
func (s *Server) handleAddMachine(w http.ResponseWriter, r *http.Request) {
	var req proto.AddMachineReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("新增开发机：请求体无法解析", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 先做纯校验：地址粘错时不必浪费一次 5 秒探测
	if err := validateAddMachine(req, s.conf().Targets); err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrMachineExists) {
			code = http.StatusConflict
		}
		s.log.Warn("新增开发机：校验未通过", "name", req.Name, "addr", req.Addr, "cause", err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	if !req.Force {
		ctx, cancel := context.WithTimeout(r.Context(), addMachineProbeBudget)
		defer cancel()
		s.log.Info("新增开发机：开始可达性探测", "name", req.Name, "addr", req.Addr)
		if _, err := client.New(req.Addr, req.Token).NoRedirect().Status(ctx); err != nil {
			// 原文回给前端：绝大多数失败是地址或令牌粘错，原文是唯一能让人
			// 一眼看出「是连不上还是没授权」的东西
			s.log.Warn("新增开发机：探测不通", "name", req.Name, "addr", req.Addr, "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": fmt.Sprintf("探测 %s 失败：%v", req.Addr, err),
			})
			return
		}
		s.log.Info("新增开发机：探测通过", "name", req.Name, "addr", req.Addr)
	}
	if err := s.addMachine(req); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrMachineExists) {
			code = http.StatusConflict
		}
		s.log.Error("新增开发机失败", "name", req.Name, "cause", err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("新增开发机成功", "name", req.Name, "addr", req.Addr, "force", req.Force)
	writeJSON(w, http.StatusOK, s.probeMachines(r.Context()))
}

// handleDeleteMachine 处理 DELETE /api/machines/{name}。
//
// 状态码：
//   - 404 该名字不存在
//   - 500 落盘失败
//
// 注意：删除只改本机配置里的 targets，**不去动对端**——对端 agentd 与其
// 上正在跑的任务与本操作无关，删的只是「本机记得这台机器」这件事。
func (s *Server) handleDeleteMachine(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if err := s.removeMachine(name); err != nil {
		code := http.StatusInternalServerError
		if errors.Is(err, ErrMachineNotFound) {
			code = http.StatusNotFound
		}
		s.log.Warn("删除开发机失败", "name", name, "cause", err)
		writeJSON(w, code, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("删除开发机成功", "name", name)
	writeJSON(w, http.StatusOK, s.probeMachines(r.Context()))
}
