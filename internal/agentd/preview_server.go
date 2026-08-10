// agentd Preview 资源 HTTP adapter。
//
// 职责：
//   - POST /v1/workspaces/{workspace_id}/previews 幂等创建 owner-loopback Preview 会话
//   - GET/DELETE /v1/previews/{preview_session_id} 读取/关闭会话
//   - /v1/preview-proxy/{nonce}/{path...} 把 desktop 流量代理到本机 loopback 端口，
//     或按 machine_id 转发给远端 owner agentd（peer connector）
//
// 边界：
//   - 不启动或终止 Preview 进程；代理本体由 preview.Service/proxyLoopback 持有
//   - 代理命中路由不套 Bearer 鉴权：Electron <webview> 无法携带 Authorization 头，
//     访问控制由不可预测的短期 nonce + owner-loopback 约束承担（URL 用 nonce 而非 agent token）
//   - 日志只记录 machine/workspace/session/port 摘要，绝不记录 nonce 全文或凭证
package agentd

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/store"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// previewPeerConnector 是 agentd 对远端 preview 代理与关闭的窄端口（由 peer.AuthorityRegistry 实现）。
type previewPeerConnector interface {
	ForwardPreviewProxy(w http.ResponseWriter, r *http.Request, machineID, nonce string)
	ClosePreviewSession(ctx context.Context, machineID, previewSessionID string) error
}

// handleCreatePreview 幂等创建 Preview 会话并把 URL 重写到本机 agentd loopback。
func (s *Server) handleCreatePreview(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	workspaceID := r.PathValue("workspace_id")
	var req desktopapi.CreatePreviewRequest
	if err := decodeResourceJSON(w, r, &req); err != nil {
		return
	}
	assembler := &desktopapi.ResourceAssembler{}
	session, err := s.resources.CreatePreview(r.Context(), workspaceID,
		assembler.ToCreatePreviewCommand(workspaceID, req))
	if err != nil {
		s.writeResourceError(w, workspaceID, err)
		return
	}
	// 无论 owner 是本机还是远端，desktop 一律拿到「指向本机 agentd」的 nonce URL；
	// 远端会话也由本机按 machine_id 转发，desktop 永不接触远端 endpoint/token。
	if base := s.localPreviewBase(); base != "" {
		session.URL = base + "/v1/preview-proxy/" + previewNonceFromURL(session.URL) + "/"
	}
	if err := s.st.UpsertPreviewSession(r.Context(), session.MachineID, session); err != nil {
		s.log.Error("Preview session 摘要缓存失败", "workspace_id", workspaceID,
			"preview_session_id", session.PreviewSessionID, "cause", err)
	}
	s.log.Info("Preview 创建 API 完成", "machine_id", session.MachineID, "workspace_id", workspaceID,
		"preview_session_id", session.PreviewSessionID, "port", session.Port, "state", session.State)
	writeJSON(w, http.StatusCreated, assembler.ToPreviewSession(session))
}

// handleGetPreview 按 preview_session_id 读取会话；缺失或过期返回 404。
func (s *Server) handleGetPreview(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	previewSessionID := r.PathValue("preview_session_id")
	session, err := s.st.GetPreviewSession(r.Context(), previewSessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			desktopapi.WriteProblem(w, http.StatusNotFound, desktopapi.Problem{
				Code: desktopapi.ProblemResourceNotFound, Message: "Preview 会话不存在",
			})
			return
		}
		s.log.Error("Preview 会话定位失败", "preview_session_id", previewSessionID, "cause", err)
		s.writeResourceError(w, "", err)
		return
	}
	if s.previewExpired(session) {
		desktopapi.WriteProblem(w, http.StatusNotFound, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "Preview 会话已过期",
		})
		return
	}
	s.log.Info("Preview 读取 API 完成", "machine_id", session.MachineID, "workspace_id", session.WorkspaceID,
		"preview_session_id", previewSessionID, "port", session.Port, "state", session.State)
	writeJSON(w, http.StatusOK, (&desktopapi.ResourceAssembler{}).ToPreviewSession(session))
}

// handleClosePreview 显式关闭会话：本机走 owner service，远端经 peer connector，
// 无论哪条路径都标记本地 closed 并持久化。
func (s *Server) handleClosePreview(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	previewSessionID := r.PathValue("preview_session_id")
	session, err := s.st.GetPreviewSession(r.Context(), previewSessionID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			desktopapi.WriteProblem(w, http.StatusNotFound, desktopapi.Problem{
				Code: desktopapi.ProblemResourceNotFound, Message: "Preview 会话不存在",
			})
			return
		}
		s.log.Error("Preview 会话定位失败", "preview_session_id", previewSessionID, "cause", err)
		s.writeResourceError(w, "", err)
		return
	}
	if session.MachineID == s.localMachineID() {
		if s.previewOwner == nil {
			desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
				Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "Preview 服务未就绪", Retryable: true,
			})
			return
		}
		if _, err := s.previewOwner.Close(r.Context(), previewSessionID); err != nil {
			s.log.Error("Preview 关闭失败", "workspace_id", session.WorkspaceID,
				"preview_session_id", previewSessionID, "cause", err)
		}
	} else {
		if s.previewPeers == nil {
			desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
				Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "远端 Preview 服务未就绪", Retryable: true,
			})
			return
		}
		if err := s.previewPeers.ClosePreviewSession(r.Context(), session.MachineID, previewSessionID); err != nil {
			s.log.Warn("远端 Preview 会话关闭失败（本地仍标记 closed）", "machine_id", session.MachineID,
				"preview_session_id", previewSessionID, "cause", err)
		}
	}
	session.State = workspaceapi.PreviewStateClosed
	if err := s.st.UpsertPreviewSession(r.Context(), session.MachineID, session); err != nil {
		s.log.Error("Preview 关闭后状态持久化失败", "workspace_id", session.WorkspaceID,
			"preview_session_id", previewSessionID, "cause", err)
		s.writeResourceError(w, session.WorkspaceID, err)
		return
	}
	s.log.Info("Preview 关闭 API 完成", "machine_id", session.MachineID, "workspace_id", session.WorkspaceID,
		"preview_session_id", previewSessionID, "port", session.Port, "state", session.State)
	writeJSON(w, http.StatusOK, (&desktopapi.ResourceAssembler{}).ToPreviewSession(session))
}

// handlePreviewProxy 处理 /v1/preview-proxy/{nonce}/{path...}。
//
// 注意：本路由注册在 s.auth 之外，因此**不做** requireResources，也不要求
// Bearer token。访问控制依赖不可猜测的短期 nonce 与 owner-loopback 约束；
// 本函数绝不把 nonce 全文写入日志。
func (s *Server) handlePreviewProxy(w http.ResponseWriter, r *http.Request) {
	nonce := r.PathValue("nonce")
	session, err := s.st.GetPreviewSessionByNonce(r.Context(), nonce)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			desktopapi.WriteProblem(w, http.StatusNotFound, desktopapi.Problem{
				Code: desktopapi.ProblemResourceNotFound, Message: "Preview 会话不存在",
			})
			return
		}
		s.log.Error("Preview 代理按 nonce 解析失败", "cause", err)
		desktopapi.WriteProblem(w, http.StatusInternalServerError, desktopapi.Problem{
			Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "Preview 会话解析失败", Retryable: true,
		})
		return
	}
	if s.previewExpired(session) {
		desktopapi.WriteProblem(w, http.StatusNotFound, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "Preview 会话已过期",
		})
		return
	}
	// 可用性门禁：Machine 断开立即失效（与 resourcegateway 的语义一致）。
	machine, machineErr := s.st.GetMachine(r.Context(), session.MachineID)
	workspace, wsErr := s.st.GetWorkspace(r.Context(), session.WorkspaceID)
	if machineErr != nil || wsErr != nil ||
		machine.Status != controlplane.MachineStatusConnected ||
		workspace.Availability != controlplane.AvailabilityAvailable {
		desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
			Code: desktopapi.ProblemMachineOffline, Message: "开发机当前不可用", Retryable: true,
			MachineID: session.MachineID, WorkspaceID: session.WorkspaceID,
		})
		return
	}
	// 剥离 /v1/preview-proxy/{nonce} 前缀，只留上游应用路径与查询串。
	r.URL.Path = "/" + r.PathValue("path")
	if session.MachineID == s.localMachineID() {
		if s.previewOwner == nil {
			desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
				Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "Preview 服务未就绪", Retryable: true,
			})
			return
		}
		s.previewOwner.HandleProxy(w, r, session)
	} else {
		if s.previewPeers == nil {
			desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
				Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "远端 Preview 代理未就绪", Retryable: true,
			})
			return
		}
		s.previewPeers.ForwardPreviewProxy(w, r, session.MachineID, nonce)
	}
	s.log.Info("Preview 代理转发完成", "machine_id", session.MachineID, "workspace_id", session.WorkspaceID,
		"preview_session_id", session.PreviewSessionID, "port", session.Port)
}

// localPreviewBase 从本机监听地址推导 desktop 始终可访问的 loopback 基地址。
//
// 规则：取 Listen 最后一个冒号后的端口段，拼成 http://127.0.0.1:<port>；
// Listen 为空或格式非法时返回空串（调用方跳过 URL 重写）。
func (s *Server) localPreviewBase() string {
	return localPreviewURL(s.cfg.Listen)
}

// localPreviewURL 从监听地址提取端口并拼出 loopback 基地址（无端口时返回空串）。
func localPreviewURL(listen string) string {
	idx := strings.LastIndex(listen, ":")
	if idx < 0 || idx == len(listen)-1 {
		return ""
	}
	return "http://127.0.0.1:" + listen[idx+1:]
}

// previewExpired 判断 Preview 会话是否已过 expires_at（零值视为未过期）。
func (s *Server) previewExpired(session workspaceapi.PreviewSession) bool {
	return !session.ExpiresAt.IsZero() && time.Now().After(session.ExpiresAt)
}

// previewNonceFromURL 从 owner-loopback URL 中提取代理 nonce（与 internal/store 同款逻辑）。
//
// PreviewSession 不携带 nonce 字段，nonce 是 URL 的路径段；保持本地实现，
// 不引入对 store 的 nonce 解析依赖。
func previewNonceFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i, part := range parts {
		if part == "preview-proxy" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	return ""
}
