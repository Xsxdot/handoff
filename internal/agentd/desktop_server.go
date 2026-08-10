// agentd desktop_server：桌面 /v1 控制面路由。
//
// 职责：
//   - GET /v1/bootstrap：一致性读事务返回快照与 revision
//   - GET /v1/control/events?after=&limit=：重放 control events
//   - POST /v1/projects/operations：项目创建（202 + Operation）
//   - GET /v1/operations/{id}：查询 Operation
//
// 边界：
//   - handler 只做 decode → CatalogAssembler → application service →
//     CatalogAssembler → encode，不直接写 store、不手拼 DTO、不承载业务校验
//   - 错误按 Problem code 映射；用户可修复错误保留具体 message，
//     内部错误只返回安全摘要并在日志记录 cause
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/store"
)

// handleDesktopBootstrap 返回控制面全量快照与 revision。
func (s *Server) handleDesktopBootstrap(w http.ResponseWriter, r *http.Request) {
	s.log.Info("desktop bootstrap 请求", "method", r.Method, "path", r.URL.Path)
	snap, err := s.st.Snapshot(r.Context())
	if err != nil {
		s.log.Error("desktop bootstrap 读快照失败", "cause", err)
		desktopapi.WriteProblem(w, http.StatusInternalServerError, desktopapi.Problem{
			Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "本机 agentd 暂不可用",
		})
		return
	}
	body := (&desktopapi.CatalogAssembler{}).ToBootstrap(snap)
	s.log.Info("desktop bootstrap 完成", "control_revision", snap.ControlRevision)
	writeJSON(w, http.StatusOK, body)
}

// handleDesktopControlEvents 返回 revision 之后的 control events（重放）。
func (s *Server) handleDesktopControlEvents(w http.ResponseWriter, r *http.Request) {
	after, err := parseAfter(r)
	if err != nil {
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "after 必须是大于等于 0 的整数",
		})
		return
	}
	limit := 200
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, lerr := strconv.Atoi(q); lerr == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	events, err := s.st.ControlEventsAfter(r.Context(), after, limit)
	if err != nil {
		s.log.Error("desktop control events 读取失败", "after", after, "cause", err)
		desktopapi.WriteProblem(w, http.StatusInternalServerError, desktopapi.Problem{
			Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "本机 agentd 暂不可用",
		})
		return
	}
	a := &desktopapi.CatalogAssembler{}
	envelopes := make([]desktopapi.ControlEventEnvelope, 0, len(events))
	for _, ev := range events {
		env, err := a.ToControlEvent(ev)
		if err != nil {
			s.log.Error("desktop control event 转换失败", "revision", ev.ControlRevision, "cause", err)
			continue
		}
		envelopes = append(envelopes, env)
	}
	s.log.Info("desktop control events 完成", "after", after, "events", len(envelopes))
	writeJSON(w, http.StatusOK, envelopes)
}

// handleDesktopCreateProject 创建项目 Operation（202）。
//
// 同 operation ID 返回现有权威 Operation，不重复执行（幂等由 ProjectService 保证）。
func (s *Server) handleDesktopCreateProject(w http.ResponseWriter, r *http.Request) {
	if s.projects == nil {
		s.log.Warn("desktop 项目创建请求到达但 ProjectService 未注入")
		desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
			Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "本机 agentd 写服务未就绪",
		})
		return
	}
	var req desktopapi.CreateProjectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("desktop 项目创建请求体解析失败", "cause", err)
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "请求体必须是 JSON {operation_id, name, locations}",
		})
		return
	}
	a := &desktopapi.CatalogAssembler{}
	command, err := a.ToCreateProjectCommand(req)
	if err != nil {
		s.log.Warn("desktop 项目创建命令转换失败", "operation_id", req.OperationID, "cause", err)
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: err.Error(),
		})
		return
	}
	// Operation 是 durable 长操作：桌面关闭、刷新或 HTTP 连接中断都不能取消
	// owner 机器上的 Inspect/Clone。WithoutCancel 仅脱离客户端取消信号；服务端
	// 各 Git/peer 命令仍有自己的超时与断线门禁。
	op, err := s.projects.Create(context.WithoutCancel(r.Context()), command)
	if err != nil {
		s.log.Error("desktop 项目创建失败", "operation_id", req.OperationID, "cause", err)
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: err.Error(),
		})
		return
	}
	s.log.Info("desktop 项目创建已接受", "operation_id", op.OperationID, "state", op.State)
	writeJSON(w, http.StatusAccepted, a.ToOperation(op))
}

// handleDesktopGetOperation 查询 Operation。
func (s *Server) handleDesktopGetOperation(w http.ResponseWriter, r *http.Request) {
	operationID := r.PathValue("operation_id")
	s.log.Info("desktop operation 查询", "operation_id", operationID)
	op, err := s.st.GetOperation(r.Context(), operationID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			desktopapi.WriteProblem(w, http.StatusNotFound, desktopapi.Problem{
				Code: desktopapi.ProblemResourceNotFound, Message: "操作不存在",
			})
			return
		}
		s.log.Error("desktop operation 查询失败", "operation_id", operationID, "cause", err)
		desktopapi.WriteProblem(w, http.StatusInternalServerError, desktopapi.Problem{
			Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "本机 agentd 暂不可用",
		})
		return
	}
	writeJSON(w, http.StatusOK, (&desktopapi.CatalogAssembler{}).ToOperation(op))
}
