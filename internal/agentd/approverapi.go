// approverapi.go —— B437「代他机裁决」端点与远程裁决口适配器。
//
// 职责：
//   - 注册并实现 POST /api/approver/decide：收到请求即用**本机**的执行器环境
//     跑一次单环 OneShot 裁决，回结构化结果（approve/escalate/error + 理由 + 模型 + 耗时）
//   - 提供 orchestration.RemoteDecider 适配器：发起方经 target 客户端池取对端 client，
//     施加裁决时限，把失败归类为 §4.6 五分类
//
// 边界：
//   - 端点不碰任务、不建工单、不写事件（审计归发起方）；不挂 withLedger（无 DSN
//     机器也能当他机环）；鉴权沿用外层 Bearer token
//   - 端点只执行「请求点名的执行器」这一环，不递归走对端自己的审批链
//   - 请求体无 prompt 字段（冻结 F1）——不把对端变成通用 LLM 代跑口
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/envfile"
	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/proto"
)

// SetApproverEnv 注入「代他机裁决」端点跑 OneShot 时使用的 env 文件解析器。
// 与本机 Manager 的审批者共用同一份（组装点调用）。
func (s *Server) SetApproverEnv(env *envfile.Resolver) { s.approverEnv = env }

// registerApproverRoutes 注册代他机裁决端点（独立注册函数，避免 Handler 主表膨胀）。
//
// 不挂 withLedger：本端点只跑本机 OneShot，不依赖账本，无 DSN 的机器也能当他机环。
// 在 s.auth 之内（Bearer token），无需新鉴权。
func (s *Server) registerApproverRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/approver/decide", s.handleApproverDecide)
}

// handleApproverDecide 是 POST /api/approver/decide（B437 冻结 F1/F2）。
//
// 响应：200 ApproverDecideResp（decision ∈ {approve,escalate,error}）；400 体解码失败
// 或 permission/executor 为空。端点全程零 AppendEvent、不碰任务。
func (s *Server) handleApproverDecide(w http.ResponseWriter, r *http.Request) {
	var req proto.ApproverDecideReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, forwardBodyLimit)).Decode(&req); err != nil {
		s.log.Warn("代他机裁决请求体解码失败", "cause", err)
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if strings.TrimSpace(req.Permission) == "" {
		s.log.Warn("代他机裁决请求被拒：permission 为空")
		writeErr(w, http.StatusBadRequest, errors.New("permission 不能为空"))
		return
	}
	if strings.TrimSpace(req.Executor) == "" {
		s.log.Warn("代他机裁决请求被拒：executor 为空")
		writeErr(w, http.StatusBadRequest, errors.New("executor 不能为空"))
		return
	}
	s.log.Info("收到代他机裁决请求", "executor", req.Executor, "model", req.Model)

	shot, ok := s.approverShot(req.Executor)
	if !ok {
		s.log.Warn("代他机裁决：本机无此执行器的 OneShot", "executor", req.Executor)
		writeJSON(w, http.StatusOK, proto.ApproverDecideResp{
			Decision:  proto.ApproverDecisionError,
			Reason:    fmt.Sprintf("本机执行器 %q 不可用", req.Executor),
			ErrorKind: proto.ApproverErrorNoExecutor,
		})
		return
	}
	// timeout=0：端点不施加自身裁决封顶，只依赖 r.Context()——发起方把链级时限
	// 施加在 HTTP 请求上（DecideRemote 的 context.WithTimeout），截止经 HTTP 取消
	// 传播到此处的 r.Context()。若这里再硬编码一个封顶，就会掐断用户配置的链级时限
	// （本次补充 (2)）。
	out := orchestration.RunLocalRing(r.Context(), shot, req.Executor, req.Model,
		req.Permission, req.TaskSummary, 0, s.approverEnv, s.log)
	resp := proto.ApproverDecideResp{
		Decision:  out.Decision,
		Reason:    out.Reason,
		Model:     out.Model,
		ElapsedMS: out.ElapsedMS,
	}
	if out.Decision == proto.ApproverDecisionError {
		resp.ErrorKind = orchestration.NormalizeApproverErrorKind(out.ErrorKind)
	}
	s.log.Info("代他机裁决完成", "executor", req.Executor,
		"decision", resp.Decision, "elapsed_ms", resp.ElapsedMS)
	writeJSON(w, http.StatusOK, resp)
}

// approverShot 取本机某执行器的一次性调用能力。
func (s *Server) approverShot(name string) (executor.OneShot, bool) {
	if s.providers == nil {
		return nil, false
	}
	provider, err := s.providers.Get(name)
	if err != nil {
		return nil, false
	}
	shot, ok := provider.(executor.OneShot)
	return shot, ok
}

// ApproverRemoteDecider 返回发起方侧的远程裁决口适配器。
//
// 选路经 s.clientForTarget（本机名短路由编排侧 S4 前置拦截，正常不会命中本机分支）；
// 池取不到客户端（设备表/配置无此名）归「不可达」，Reason 写明原因。
func (s *Server) ApproverRemoteDecider() orchestration.RemoteDecider {
	return &remoteApproverDecider{srv: s, log: s.log}
}

// approverDecideCaller 是 DecideRemote 依赖的对端裁决调用面。
//
// 生产实现是 *client.Client；测试注入故障替身，以确定性地钉住失败分类
// （尤其「拨号超时形态的 net.Error」这类难以在秒级内真实复现的传输错误）。
type approverDecideCaller interface {
	ApproverDecide(ctx context.Context, req proto.ApproverDecideReq) (*proto.ApproverDecideResp, error)
}

type remoteApproverDecider struct {
	srv *Server
	log *slog.Logger
	// clientFor 是取对端裁决调用面的缝：nil = 经 srv.clientForTarget（生产路径）；
	// 测试注入故障替身（见 approverapi_test.go 的失败分类表驱动）。
	clientFor func(machine string) (approverDecideCaller, error)
}

// deciderFor 解析某台机器的对端裁决调用面。
func (r *remoteApproverDecider) deciderFor(machine string) (approverDecideCaller, error) {
	if r.clientFor != nil {
		return r.clientFor(machine)
	}
	c, err := r.srv.clientForTarget(machine)
	if err != nil {
		return nil, err
	}
	return c, nil
}

// DecideRemote 请一台他机的 agentd 代跑一个环（B437 §4.3）。
//
// 失败归类（§4.6）：404 → 不支持；整体截止（链 timeout）到点 → 超时；其余传输
// 错误（含拨号超时）→ 不可达；应答 decision=error → 归一 error_kind（未知 →
// internal）；应答无法解码 → 裁决无效。判「超时」只看 cctx.Err()，不看错误链——
// net 拨号超时错误也会满足 errors.Is(err, context.DeadlineExceeded)（本次补充 (3)）。
func (r *remoteApproverDecider) DecideRemote(ctx context.Context, ring orchestration.ApproverRing,
	permission, taskSummary string, timeout time.Duration) orchestration.RemoteOutcome {
	start := time.Now()
	c, err := r.deciderFor(ring.Machine)
	if err != nil {
		r.log.Warn("代他机裁决：取对端客户端失败", "machine", ring.Machine, "cause", err)
		return orchestration.RemoteOutcome{Decision: proto.ApproverDecisionError,
			ErrorKind: orchestration.ApproverFailUnreachable, Reason: err.Error(),
			ElapsedMS: time.Since(start).Milliseconds()}
	}
	cctx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		cctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	resp, err := c.ApproverDecide(cctx, proto.ApproverDecideReq{
		Permission: permission, Executor: ring.Executor, TaskSummary: taskSummary, Model: ring.Model,
	})
	if err != nil {
		kind := orchestration.ApproverFailUnreachable
		switch {
		case errors.Is(err, client.ErrApproverUnsupported):
			kind = orchestration.ApproverFailUnsupported
		case cctx.Err() == context.DeadlineExceeded:
			// 只以整体截止（链 timeout 到点）判「超时」（§4.6）。不能用
			// errors.Is(err, context.DeadlineExceeded)：net 拨号超时错误在部分路径上
			// 也满足它，会把「拨号 10s 超时但链截止未到」误判成「超时」——真机上同一
			// 黑洞环并发 4 次曾出现 2 次判超时、2 次判不可达的非确定分类（本次补充 (3)）。
			// 其余传输错误（含拨号超时）一律判「不可达」。
			kind = orchestration.ApproverFailTimeout
		}
		r.log.Warn("代他机裁决请求失败", "machine", ring.Machine, "executor", ring.Executor,
			"kind", kind, "cause", err)
		return orchestration.RemoteOutcome{Decision: proto.ApproverDecisionError,
			ErrorKind: kind, Reason: err.Error(), ElapsedMS: time.Since(start).Milliseconds()}
	}
	switch resp.Decision {
	case proto.ApproverDecisionApprove, proto.ApproverDecisionEscalate:
		return orchestration.RemoteOutcome{Decision: resp.Decision, Reason: resp.Reason,
			Model: resp.Model, ElapsedMS: resp.ElapsedMS}
	case proto.ApproverDecisionError:
		return orchestration.RemoteOutcome{Decision: proto.ApproverDecisionError,
			ErrorKind: orchestration.NormalizeApproverErrorKind(resp.ErrorKind),
			Reason:    resp.Reason, Model: resp.Model, ElapsedMS: resp.ElapsedMS}
	default:
		r.log.Warn("代他机裁决应答取值非法", "machine", ring.Machine, "decision", resp.Decision)
		return orchestration.RemoteOutcome{Decision: proto.ApproverDecisionError,
			ErrorKind: orchestration.ApproverFailInvalidOutput, Reason: "对端裁决取值非法",
			ElapsedMS: resp.ElapsedMS}
	}
}
