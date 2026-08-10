// preview owner-side Preview 会话生命周期服务。
//
// 职责：
//   - 幂等创建 preview session（command_id 去重），URL 携带不可猜测 nonce
//   - 按 preview_session_id 读取、显式关闭、按 nonce 反向解析
//   - 过期会话视为已不存在，统一返回 ResourceNotFound
//
// 边界：
//   - 不启动或终止 preview 代理进程；HTTP 代理由 proxy.go 持有
//   - 无 HTTP server、无 wire adapter，只暴露强类型端口
//   - 日志只记录 machine/workspace/session/port 摘要，绝不记录 nonce 全文
package preview

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// Repository 是 Preview 服务所需的 provider-owned durable 事实端口。
type Repository interface {
	CreatePreviewSession(context.Context, string, string, workspaceapi.PreviewSession) (workspaceapi.PreviewSession, bool, error)
	GetPreviewSession(context.Context, string) (workspaceapi.PreviewSession, error)
	GetPreviewSessionByNonce(context.Context, string) (workspaceapi.PreviewSession, error)
	UpsertPreviewSession(context.Context, string, workspaceapi.PreviewSession) error
	ExpirePreviewSessions(context.Context) (int, error)
}

const defaultPreviewTTL = 15 * time.Minute

// Service 持有当前 agentd 进程 Preview 会话的生命周期状态机。
type Service struct {
	repo      Repository
	machineID string
	listenURL string
	log       *slog.Logger
	ttl       time.Duration
	newID     func() string
	newNonce  func() string
}

// NewService 创建 Preview 服务。
func NewService(repo Repository, machineID, listenURL string, log *slog.Logger) (*Service, error) {
	if repo == nil || machineID == "" || listenURL == "" {
		return nil, fmt.Errorf("preview service 缺 repository/machine_id/listen_url")
	}
	if log == nil {
		log = slog.Default()
	}
	return newService(repo, machineID, listenURL, log, defaultPreviewTTL, uuid.NewString, uuid.NewString), nil
}

// newService 是注入边界的构造器，供测试覆盖 ttl 与 ID/nonce 生成。
func newService(repo Repository, machineID, listenURL string, log *slog.Logger, ttl time.Duration,
	newID, newNonce func() string) *Service {
	return &Service{repo: repo, machineID: machineID, listenURL: listenURL, log: log, ttl: ttl,
		newID: newID, newNonce: newNonce}
}

// ValidatePort 校验 Preview 代理目标端口；越界返回 COMMAND_CONFLICT。
func ValidatePort(port int) error {
	if port < 1 || port > 65535 {
		return &workspaceapi.Error{Code: workspaceapi.ErrorCommandConflict,
			Message: fmt.Sprintf("preview 端口必须在 1-65535 之间，got %d", port)}
	}
	return nil
}

// Create 幂等创建 Preview 会话；相同 command_id 永远返回首个会话。
func (s *Service) Create(ctx context.Context, ws workspaceapi.WorkspaceRef,
	command workspaceapi.CreatePreviewCommand) (workspaceapi.PreviewSession, error) {
	if err := ValidatePort(command.Port); err != nil {
		return workspaceapi.PreviewSession{}, err
	}
	session := workspaceapi.PreviewSession{
		PreviewSessionID: s.newID(),
		WorkspaceID:      ws.WorkspaceID,
		MachineID:        s.machineID,
		State:            workspaceapi.PreviewStatePending,
		URL:              s.listenURL + "/v1/preview-proxy/" + s.newNonce() + "/",
		Port:             command.Port,
		ExpiresAt:        time.Now().Add(s.ttl),
	}
	created, inserted, err := s.repo.CreatePreviewSession(ctx, s.machineID, command.CommandID, session)
	if err != nil {
		return workspaceapi.PreviewSession{}, err
	}
	if !inserted {
		s.log.Debug("preview 会话已存在，返回既有会话", "machine_id", s.machineID,
			"workspace_id", created.WorkspaceID, "preview_session_id", created.PreviewSessionID)
		return created, nil
	}
	s.log.Info("preview 会话已创建", "machine_id", s.machineID, "workspace_id", session.WorkspaceID,
		"preview_session_id", session.PreviewSessionID, "port", session.Port, "expires_at", session.ExpiresAt)
	return session, nil
}

// Get 按 preview_session_id 读取；过期会话视为不存在。
func (s *Service) Get(ctx context.Context, sessionID string) (workspaceapi.PreviewSession, error) {
	session, err := s.repo.GetPreviewSession(ctx, sessionID)
	if err != nil {
		return workspaceapi.PreviewSession{}, err
	}
	if previewExpired(session) {
		return workspaceapi.PreviewSession{}, &workspaceapi.Error{Code: workspaceapi.ErrorResourceNotFound,
			Message: "Preview 会话已过期"}
	}
	return session, nil
}

// Close 显式关闭会话并持久化 closed 状态。
func (s *Service) Close(ctx context.Context, sessionID string) (workspaceapi.PreviewSession, error) {
	session, err := s.repo.GetPreviewSession(ctx, sessionID)
	if err != nil {
		return workspaceapi.PreviewSession{}, err
	}
	session.State = workspaceapi.PreviewStateClosed
	if err := s.repo.UpsertPreviewSession(ctx, s.machineID, session); err != nil {
		return workspaceapi.PreviewSession{}, err
	}
	s.log.Info("preview 会话已关闭", "machine_id", s.machineID, "workspace_id", session.WorkspaceID,
		"preview_session_id", session.PreviewSessionID, "port", session.Port)
	return session, nil
}

// LookupNonce 按 nonce 反向解析会话（代理命中入口用）；过期视为不存在。
func (s *Service) LookupNonce(ctx context.Context, nonce string) (workspaceapi.PreviewSession, error) {
	session, err := s.repo.GetPreviewSessionByNonce(ctx, nonce)
	if err != nil {
		return workspaceapi.PreviewSession{}, err
	}
	if previewExpired(session) {
		return workspaceapi.PreviewSession{}, &workspaceapi.Error{Code: workspaceapi.ErrorResourceNotFound,
			Message: "Preview 会话已过期"}
	}
	return session, nil
}

// Shutdown 释放服务资源；Preview service 无后台资源，幂等返回 nil。
func (s *Service) Shutdown() error {
	return nil
}

// HandleProxy 是本机 owner 的 Preview 代理命中入口：校验会话归属本机且未过期，
// 把 pending 提升为 active（best-effort）后委托 proxyLoopback 反向代理到 loopback 端口。
//
// 守卫（与 store 层的 unexported 判空不同，这里面向 HTTP）：
//   - 会话不属于本机 owner：返回 404 RESOURCE_NOT_FOUND（不泄露远端身份）
//   - 会话已过期：返回 404 RESOURCE_NOT_FOUND
//   - 其余状态（closed/expired）由 proxyLoopback 之后的代理行为决定，不额外拦截
//
// 日志只记录 machine/workspace/session/port 摘要，绝不记录 nonce 全文。
func (s *Service) HandleProxy(w http.ResponseWriter, r *http.Request, session workspaceapi.PreviewSession) {
	if session.MachineID != s.machineID {
		desktopapi.WriteProblem(w, http.StatusNotFound, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "Preview 会话不存在",
			MachineID: session.MachineID, WorkspaceID: session.WorkspaceID,
		})
		return
	}
	if previewExpired(session) {
		desktopapi.WriteProblem(w, http.StatusNotFound, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "Preview 会话已过期",
			MachineID: session.MachineID, WorkspaceID: session.WorkspaceID,
		})
		return
	}
	if session.State == workspaceapi.PreviewStatePending {
		session.State = workspaceapi.PreviewStateActive
		if err := s.repo.UpsertPreviewSession(r.Context(), s.machineID, session); err != nil {
			s.log.Warn("preview 会话状态提升失败（忽略，继续代理）", "machine_id", s.machineID,
				"workspace_id", session.WorkspaceID, "preview_session_id", session.PreviewSessionID, "cause", err)
		}
	}
	s.log.Info("preview 代理打开", "machine_id", s.machineID, "workspace_id", session.WorkspaceID,
		"preview_session_id", session.PreviewSessionID, "port", session.Port, "state", session.State)
	proxyLoopback(w, r, session, s.log)
}

func previewExpired(session workspaceapi.PreviewSession) bool {
	return !session.ExpiresAt.IsZero() && session.ExpiresAt.Before(time.Now())
}
