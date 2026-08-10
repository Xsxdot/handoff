// Package resourcegateway 把稳定 Workspace ID 路由到所属机器的资源 Authority。
//
// 职责：
//   - 从控制面解析 Workspace 与 Machine
//   - 统一执行 Machine 状态、Workspace availability 与 capability 门禁
//   - local 走 machineauthority，remote 仅按 machine_id 走 peer resolver
//
// 边界：
//   - 不执行文件、Git、PTY、Preview I/O；实际授权由 owner Authority 二次完成
//   - 不向 peer resolver 或调用方暴露 Machine endpoint、SecretRef 或 token
//   - 所有拒绝返回 typed desktopapi.ProblemError，adapter 不解析错误文本
package resourcegateway

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/peer"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

// CatalogReader 是 Router 所需的最小控制面读取端口。
type CatalogReader interface {
	GetWorkspace(context.Context, string) (controlplane.Workspace, error)
	GetMachine(context.Context, string) (controlplane.Machine, error)
}

// PeerAuthorityResolver 只按稳定 machine_id 返回远端 authority。
//
// 为什么不传 Machine：credential/endpoint 解析属于本机 agentd 的 peer registry，
// Router 若传整台 Machine 会扩大 secret metadata 的传播面。
type PeerAuthorityResolver interface {
	AuthorityForMachine(context.Context, string) (workspaceapi.Authority, error)
}

// Router 是 Workspace 资源统一路由器。
type Router struct {
	catalog CatalogReader
	local   workspaceapi.Authority
	peers   PeerAuthorityResolver
	log     *slog.Logger
}

// NewRouter 创建资源路由器。
func NewRouter(catalog CatalogReader, local workspaceapi.Authority, peers PeerAuthorityResolver, log *slog.Logger) *Router {
	if log == nil {
		log = slog.Default()
	}
	return &Router{catalog: catalog, local: local, peers: peers, log: log}
}

type route struct {
	authority workspaceapi.Authority
	workspace workspaceapi.WorkspaceRef
	owner     string
}

func (r *Router) resolve(ctx context.Context, operation, workspaceID, capability string) (route, error) {
	started := time.Now()
	r.log.Debug("Workspace 资源路由开始", "operation", operation, "workspace_id", workspaceID, "capability", capability)
	ws, err := r.catalog.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return route{}, r.reject(http.StatusNotFound, desktopapi.ProblemResourceNotFound, "工作区不存在", "", workspaceID, err)
	}
	machine, err := r.catalog.GetMachine(ctx, ws.MachineID)
	if err != nil {
		return route{}, r.reject(http.StatusNotFound, desktopapi.ProblemResourceNotFound, "工作区所属机器不存在", ws.MachineID, workspaceID, err)
	}
	if machine.Status == controlplane.MachineStatusIncompatible {
		return route{}, r.reject(http.StatusConflict, desktopapi.ProblemCapabilityUnsupported, "开发机协议不兼容，请升级 agentd", machine.ID, workspaceID, nil)
	}
	if machine.Status != controlplane.MachineStatusConnected || ws.Availability != controlplane.AvailabilityAvailable {
		return route{}, r.reject(http.StatusServiceUnavailable, desktopapi.ProblemMachineOffline, "开发机当前不可用", machine.ID, workspaceID, nil)
	}
	if machine.Capabilities[capability] < 1 {
		return route{}, r.reject(http.StatusConflict, desktopapi.ProblemCapabilityUnsupported, "开发机不支持该资源能力", machine.ID, workspaceID, nil)
	}

	var authority workspaceapi.Authority
	owner := "local"
	switch machine.Kind {
	case controlplane.MachineKindLocal:
		authority = r.local
	case controlplane.MachineKindRemote:
		owner = "peer"
		authority, err = r.peers.AuthorityForMachine(ctx, machine.ID)
		if err != nil {
			return route{}, r.reject(http.StatusServiceUnavailable, desktopapi.ProblemMachineOffline, "远端开发机当前不可用", machine.ID, workspaceID, err)
		}
	default:
		return route{}, r.reject(http.StatusConflict, desktopapi.ProblemCapabilityUnsupported, "未知机器类型", machine.ID, workspaceID, nil)
	}
	if authority == nil {
		return route{}, r.reject(http.StatusServiceUnavailable, desktopapi.ProblemMachineOffline, "资源执行者未就绪", machine.ID, workspaceID, nil)
	}
	r.log.Info("Workspace 资源路由成功", "operation", operation, "workspace_id", workspaceID,
		"machine_id", machine.ID, "capability", capability, "owner", owner,
		"elapsed_ms", time.Since(started).Milliseconds())
	return route{authority: authority, owner: owner, workspace: workspaceapi.WorkspaceRef{
		WorkspaceID: ws.ID, MachineID: machine.ID, RootPath: ws.Path,
	}}, nil
}

func (r *Router) reject(status int, code desktopapi.ProblemCode, message, machineID, workspaceID string, cause error) error {
	r.log.Warn("Workspace 资源路由拒绝", "problem_code", code, "machine_id", machineID, "workspace_id", workspaceID, "cause", cause)
	return &desktopapi.ProblemError{Status: status, Problem: desktopapi.Problem{
		Code: code, Message: message, Retryable: code == desktopapi.ProblemMachineOffline,
		MachineID: machineID, WorkspaceID: workspaceID,
	}, Cause: cause}
}

func (r *Router) operationError(route route, workspaceID string, cause error) error {
	var problemErr *desktopapi.ProblemError
	if errors.As(cause, &problemErr) {
		return cause
	}
	var resourceErr *workspaceapi.Error
	if errors.As(cause, &resourceErr) {
		status := http.StatusConflict
		switch resourceErr.Code {
		case workspaceapi.ErrorResourceNotFound:
			status = http.StatusNotFound
		case workspaceapi.ErrorPathOutsideWorkspace:
			status = http.StatusBadRequest
		case workspaceapi.ErrorVersionConflict, workspaceapi.ErrorCommandConflict, workspaceapi.ErrorCursorExpired, workspaceapi.ErrorCapabilityUnsupported:
			status = http.StatusConflict
		case workspaceapi.ErrorUnavailable:
			status = http.StatusServiceUnavailable
		}
		return r.reject(status, desktopapi.ProblemCode(resourceErr.Code), resourceErr.Message,
			route.workspace.MachineID, workspaceID, cause)
	}
	if route.owner == "peer" {
		return r.reject(http.StatusServiceUnavailable, desktopapi.ProblemMachineOffline,
			"远端资源服务暂不可用", route.workspace.MachineID, workspaceID, cause)
	}
	return r.reject(http.StatusInternalServerError, desktopapi.ProblemLocalAgentdUnavailable,
		"本机资源服务暂不可用", route.workspace.MachineID, workspaceID, cause)
}

// ListDirectory 路由 Workspace-relative 目录浏览。
func (r *Router) ListDirectory(ctx context.Context, workspaceID, relativePath string) ([]workspaceapi.FileEntry, error) {
	route, err := r.resolve(ctx, "files.list", workspaceID, peer.CapabilityFiles)
	if err != nil {
		return nil, err
	}
	entries, err := route.authority.ListDirectory(ctx, route.workspace, relativePath)
	if err != nil {
		r.log.Error("目录浏览失败", "workspace_id", workspaceID, "relative_path", relativePath, "cause", err)
		return nil, r.operationError(route, workspaceID, err)
	}
	r.log.Info("目录浏览完成", "workspace_id", workspaceID, "relative_path", relativePath, "entry_count", len(entries))
	return entries, nil
}

// ReadFile 路由 Workspace-relative 文件读取。
func (r *Router) ReadFile(ctx context.Context, workspaceID, relativePath string) (workspaceapi.FileDocument, error) {
	route, err := r.resolve(ctx, "files.read", workspaceID, peer.CapabilityFiles)
	if err != nil {
		return workspaceapi.FileDocument{}, err
	}
	doc, err := route.authority.ReadFile(ctx, route.workspace, relativePath)
	if err != nil {
		r.log.Error("文件读取失败", "workspace_id", workspaceID, "relative_path", relativePath, "cause", err)
		return workspaceapi.FileDocument{}, r.operationError(route, workspaceID, err)
	}
	r.log.Info("文件读取完成", "workspace_id", workspaceID, "relative_path", relativePath, "version", doc.Version, "size", doc.Size)
	return doc, nil
}

// WriteFile 路由带 version 的 Workspace-relative 原子写。
func (r *Router) WriteFile(ctx context.Context, workspaceID string, command workspaceapi.WriteFileCommand) (workspaceapi.FileDocument, error) {
	route, err := r.resolve(ctx, "files.write", workspaceID, peer.CapabilityFiles)
	if err != nil {
		return workspaceapi.FileDocument{}, err
	}
	command.WorkspaceID = workspaceID
	doc, err := route.authority.WriteFile(ctx, route.workspace, command)
	if err != nil {
		r.log.Error("文件写入失败", "workspace_id", workspaceID, "relative_path", command.Path, "command_id", command.CommandID, "cause", err)
		return workspaceapi.FileDocument{}, r.operationError(route, workspaceID, err)
	}
	r.log.Info("文件写入完成", "workspace_id", workspaceID, "relative_path", command.Path, "command_id", command.CommandID, "version", doc.Version, "size", doc.Size)
	return doc, nil
}

// SearchFiles 路由有界 literal 文件搜索。
func (r *Router) SearchFiles(ctx context.Context, workspaceID string, command workspaceapi.SearchFilesCommand) (workspaceapi.FileSearchResult, error) {
	route, err := r.resolve(ctx, "files.search", workspaceID, peer.CapabilityFiles)
	if err != nil {
		return workspaceapi.FileSearchResult{}, err
	}
	command.WorkspaceID = workspaceID
	result, err := route.authority.SearchFiles(ctx, route.workspace, command)
	if err != nil {
		r.log.Error("文件搜索失败", "workspace_id", workspaceID, "relative_path", command.Path, "cause", err)
		return workspaceapi.FileSearchResult{}, r.operationError(route, workspaceID, err)
	}
	r.log.Info("文件搜索完成", "workspace_id", workspaceID, "relative_path", command.Path, "match_count", len(result.Matches), "scanned_files", result.ScannedFiles, "scanned_bytes", result.ScannedBytes, "truncated", result.Truncated)
	return result, nil
}

// GitStatus 路由 Workspace Git 基础状态读取。
func (r *Router) GitStatus(ctx context.Context, workspaceID string) (workspaceapi.GitStatusSnapshot, error) {
	route, err := r.resolve(ctx, "git.status", workspaceID, peer.CapabilityGit)
	if err != nil {
		return workspaceapi.GitStatusSnapshot{}, err
	}
	status, err := route.authority.GitStatus(ctx, route.workspace)
	if err != nil {
		r.log.Error("Git 状态读取失败", "workspace_id", workspaceID, "cause", err)
		return workspaceapi.GitStatusSnapshot{}, r.operationError(route, workspaceID, err)
	}
	r.log.Info("Git 状态读取完成", "workspace_id", workspaceID, "branch", status.Branch, "head_oid", status.HeadOID, "entry_count", len(status.Entries))
	return status, nil
}

// CreateTerminal 路由幂等 PTY 创建。
func (r *Router) CreateTerminal(ctx context.Context, workspaceID string, command workspaceapi.CreateTerminalCommand) (workspaceapi.PtySession, error) {
	route, err := r.resolve(ctx, "pty.create", workspaceID, peer.CapabilityPty)
	if err != nil {
		return workspaceapi.PtySession{}, err
	}
	command.WorkspaceID = workspaceID
	session, err := route.authority.CreateTerminal(ctx, route.workspace, command)
	if err != nil {
		r.log.Error("PTY 创建失败", "workspace_id", workspaceID, "command_id", command.CommandID, "cause", err)
		return workspaceapi.PtySession{}, r.operationError(route, workspaceID, err)
	}
	if session.WorkspaceID != workspaceID {
		return workspaceapi.PtySession{}, r.reject(http.StatusNotFound, desktopapi.ProblemResourceNotFound,
			"终端会话不属于该工作区", route.workspace.MachineID, workspaceID, errors.New("terminal workspace mismatch"))
	}
	r.log.Info("PTY 创建完成", "workspace_id", workspaceID, "terminal_session_id", session.TerminalSessionID, "incarnation", session.Incarnation)
	return session, nil
}

// GetTerminal 按 Workspace owner 路由已有 PTY 元数据读取。
func (r *Router) GetTerminal(ctx context.Context, workspaceID, terminalSessionID string) (workspaceapi.PtySession, error) {
	route, err := r.resolve(ctx, "pty.get", workspaceID, peer.CapabilityPty)
	if err != nil {
		return workspaceapi.PtySession{}, err
	}
	session, err := route.authority.GetTerminal(ctx, terminalSessionID)
	if err != nil {
		r.log.Error("PTY 读取失败", "workspace_id", workspaceID, "terminal_session_id", terminalSessionID, "cause", err)
		return workspaceapi.PtySession{}, r.operationError(route, workspaceID, err)
	}
	if session.WorkspaceID != workspaceID {
		return workspaceapi.PtySession{}, r.reject(http.StatusNotFound, desktopapi.ProblemResourceNotFound, "终端会话不属于该工作区", route.workspace.MachineID, workspaceID, errors.New("terminal workspace mismatch"))
	}
	r.log.Info("PTY 读取完成", "workspace_id", workspaceID, "terminal_session_id", terminalSessionID, "incarnation", session.Incarnation, "state", session.State)
	return session, nil
}

// ConnectTerminal 按 Workspace owner 路由 PTY replay、live 与控制帧。
func (r *Router) ConnectTerminal(ctx context.Context, workspaceID, terminalSessionID, incarnation string,
	after int64) (*workspaceapi.PtySubscription, error) {
	route, err := r.resolve(ctx, "pty.connect", workspaceID, peer.CapabilityPty)
	if err != nil {
		return nil, err
	}
	subscription, err := route.authority.ConnectTerminal(ctx, terminalSessionID, incarnation, after)
	if err != nil {
		r.log.Error("PTY 连接失败", "workspace_id", workspaceID, "terminal_session_id", terminalSessionID, "cause", err)
		return nil, r.operationError(route, workspaceID, err)
	}
	if subscription == nil || subscription.Session.WorkspaceID != workspaceID {
		if subscription != nil {
			subscription.Cancel()
		}
		return nil, r.reject(http.StatusNotFound, desktopapi.ProblemResourceNotFound,
			"终端会话不属于该工作区", route.workspace.MachineID, workspaceID, errors.New("terminal workspace mismatch"))
	}
	r.log.Info("PTY 连接完成", "workspace_id", workspaceID, "terminal_session_id", terminalSessionID,
		"incarnation", incarnation, "after_seq", after, "replay_count", len(subscription.Replay))
	return subscription, nil
}

// CloseTerminal 按 Workspace owner 路由显式 PTY 终止。
func (r *Router) CloseTerminal(ctx context.Context, workspaceID, terminalSessionID, incarnation string) (workspaceapi.PtySession, error) {
	route, err := r.resolve(ctx, "pty.close", workspaceID, peer.CapabilityPty)
	if err != nil {
		return workspaceapi.PtySession{}, err
	}
	session, err := route.authority.CloseTerminal(ctx, terminalSessionID, incarnation)
	if err != nil {
		r.log.Error("PTY 终止失败", "workspace_id", workspaceID, "terminal_session_id", terminalSessionID, "cause", err)
		return workspaceapi.PtySession{}, r.operationError(route, workspaceID, err)
	}
	if session.WorkspaceID != workspaceID {
		return workspaceapi.PtySession{}, r.reject(http.StatusNotFound, desktopapi.ProblemResourceNotFound,
			"终端会话不属于该工作区", route.workspace.MachineID, workspaceID, errors.New("terminal workspace mismatch"))
	}
	r.log.Info("PTY 终止完成", "workspace_id", workspaceID, "terminal_session_id", terminalSessionID,
		"incarnation", incarnation, "state", session.State)
	return session, nil
}

// CreatePreview 路由幂等 owner-loopback Preview 创建。
func (r *Router) CreatePreview(ctx context.Context, workspaceID string, command workspaceapi.CreatePreviewCommand) (workspaceapi.PreviewSession, error) {
	route, err := r.resolve(ctx, "preview.create", workspaceID, peer.CapabilityPreview)
	if err != nil {
		return workspaceapi.PreviewSession{}, err
	}
	command.WorkspaceID = workspaceID
	session, err := route.authority.CreatePreview(ctx, route.workspace, command)
	if err != nil {
		r.log.Error("Preview 创建失败", "workspace_id", workspaceID, "command_id", command.CommandID, "port", command.Port, "cause", err)
		return workspaceapi.PreviewSession{}, r.operationError(route, workspaceID, err)
	}
	r.log.Info("Preview 创建完成", "workspace_id", workspaceID, "preview_session_id", session.PreviewSessionID, "port", session.Port, "state", session.State)
	return session, nil
}

// SubscribeFiles 路由文件失效提示流；local/peer 必须实现同一 FileStreamer 端口。
func (r *Router) SubscribeFiles(ctx context.Context, workspaceID string, after int64) (*workspaceapi.FileSubscription, error) {
	route, err := r.resolve(ctx, "files.stream", workspaceID, peer.CapabilityFiles)
	if err != nil {
		return nil, err
	}
	streamer, ok := route.authority.(workspaceapi.FileStreamer)
	if !ok {
		return nil, r.reject(http.StatusConflict, desktopapi.ProblemCapabilityUnsupported,
			"资源执行者未提供文件事件流", route.workspace.MachineID, workspaceID, nil)
	}
	subscription, err := streamer.SubscribeFiles(ctx, route.workspace, after)
	if err != nil {
		return nil, r.operationError(route, workspaceID, err)
	}
	r.log.Info("文件事件流路由完成", "workspace_id", workspaceID, "machine_id", route.workspace.MachineID,
		"owner", route.owner, "after_seq", after, "replay_count", len(subscription.Replay))
	return subscription, nil
}
