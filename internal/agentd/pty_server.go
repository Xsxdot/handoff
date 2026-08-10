// agentd 普通 PTY HTTP/WebSocket adapter。
//
// 职责：
//   - 创建、读取和终止稳定 PTY session
//   - 以 subscribed -> replay/snapshot -> live 顺序代理双向终端流
//   - 缓存远端 owner session 摘要，供后续无 Workspace 参数的 attach 路由定位
//
// 边界：
//   - 不启动 shell、不访问 Workspace 路径；全部委托 resourcegateway
//   - 不记录或持久化 input/output 字节，只记录 session 身份、状态和计数
//   - 不使用 SSH/tmux；远端流由 peer Authority 代理同一协议
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/store"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

const ptyClientFrameReadLimit = (maxPtyInputBytes*4)/3 + (64 << 10)

const maxPtyInputBytes = 1 << 20

func (s *Server) handleCreateTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	workspaceID := r.PathValue("workspace_id")
	var request desktopapi.CreateTerminalRequest
	if err := decodeResourceJSON(w, r, &request); err != nil {
		return
	}
	assembler := &desktopapi.ResourceAssembler{}
	session, err := s.resources.CreateTerminal(r.Context(), workspaceID,
		assembler.ToCreateTerminalCommand(workspaceID, request))
	if err != nil {
		s.writeResourceError(w, workspaceID, err)
		return
	}
	if err := s.cacheTerminalSession(r.Context(), workspaceID, session); err != nil {
		s.log.Error("PTY session 摘要缓存失败", "workspace_id", workspaceID,
			"terminal_session_id", session.TerminalSessionID, "cause", err)
		desktopapi.WriteProblem(w, http.StatusInternalServerError, desktopapi.Problem{
			Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "PTY session 摘要保存失败", Retryable: true,
			WorkspaceID: workspaceID,
		})
		return
	}
	s.log.Info("PTY 创建 API 完成", "workspace_id", workspaceID,
		"terminal_session_id", session.TerminalSessionID, "incarnation", session.Incarnation, "state", session.State)
	writeJSON(w, http.StatusCreated, assembler.ToPtySession(session))
}

func (s *Server) handleGetTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	terminalSessionID := r.PathValue("terminal_session_id")
	cached, err := s.st.GetPtySession(r.Context(), terminalSessionID)
	if err != nil {
		s.writeTerminalLookupError(w, terminalSessionID, err)
		return
	}
	session, err := s.resources.GetTerminal(r.Context(), cached.WorkspaceID, terminalSessionID)
	if err != nil {
		s.writeResourceError(w, cached.WorkspaceID, err)
		return
	}
	if err := s.cacheTerminalSession(r.Context(), cached.WorkspaceID, session); err != nil {
		s.log.Warn("PTY 读取后摘要缓存失败", "workspace_id", cached.WorkspaceID,
			"terminal_session_id", terminalSessionID, "cause", err)
	}
	writeJSON(w, http.StatusOK, (&desktopapi.ResourceAssembler{}).ToPtySession(session))
}

func (s *Server) handleCloseTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	terminalSessionID := r.PathValue("terminal_session_id")
	incarnation := r.URL.Query().Get("incarnation")
	if incarnation == "" {
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemCommandConflict, Message: "缺少 incarnation",
		})
		return
	}
	cached, err := s.st.GetPtySession(r.Context(), terminalSessionID)
	if err != nil {
		s.writeTerminalLookupError(w, terminalSessionID, err)
		return
	}
	session, err := s.resources.CloseTerminal(r.Context(), cached.WorkspaceID, terminalSessionID, incarnation)
	if err != nil {
		s.writeResourceError(w, cached.WorkspaceID, err)
		return
	}
	if err := s.cacheTerminalSession(r.Context(), cached.WorkspaceID, session); err != nil {
		s.log.Error("PTY 终止后摘要缓存失败", "workspace_id", cached.WorkspaceID,
			"terminal_session_id", terminalSessionID, "cause", err)
		desktopapi.WriteProblem(w, http.StatusInternalServerError, desktopapi.Problem{
			Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "PTY session 摘要保存失败", Retryable: true,
			WorkspaceID: cached.WorkspaceID,
		})
		return
	}
	s.log.Info("PTY 终止 API 完成", "workspace_id", cached.WorkspaceID,
		"terminal_session_id", terminalSessionID, "incarnation", incarnation, "state", session.State)
	writeJSON(w, http.StatusOK, (&desktopapi.ResourceAssembler{}).ToPtySession(session))
}

func (s *Server) handleTerminalStream(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	terminalSessionID := r.PathValue("terminal_session_id")
	incarnation := r.URL.Query().Get("incarnation")
	if incarnation == "" {
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemCommandConflict, Message: "缺少 incarnation",
		})
		return
	}
	after, err := parseAfter(r)
	if err != nil {
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemCursorExpired, Message: "after 必须是大于等于 0 的整数",
		})
		return
	}
	cached, err := s.st.GetPtySession(r.Context(), terminalSessionID)
	if err != nil {
		s.writeTerminalLookupError(w, terminalSessionID, err)
		return
	}
	subscription, err := s.resources.ConnectTerminal(r.Context(), cached.WorkspaceID,
		terminalSessionID, incarnation, after)
	if err != nil {
		s.writeResourceError(w, cached.WorkspaceID, err)
		return
	}
	defer subscription.Cancel()
	defer subscription.ReleaseRecoveryPayload()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("PTY WebSocket 接受失败", "workspace_id", cached.WorkspaceID,
			"terminal_session_id", terminalSessionID, "cause", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "pty stream closed")
	// input 使用 base64，wire frame 会比原始 1 MiB 上限更大；必须显式设置
	// 与 service 输入边界一致的 WebSocket read limit，避免默认 32 KiB 假拒绝。
	conn.SetReadLimit(ptyClientFrameReadLimit)
	assembler := &desktopapi.ResourceAssembler{}
	subscribed := workspaceapi.PtyServerFrame{Version: 1, Kind: workspaceapi.PtyFrameSubscribed,
		TerminalSessionID: terminalSessionID, Incarnation: incarnation, WorkspaceID: cached.WorkspaceID,
		Capabilities: subscription.Capabilities,
		ThroughSeq:   subscription.Session.ThroughSeq, State: subscription.Session.State,
		ExitCode: subscription.Session.ExitCode}
	if err := writePtyStreamFrame(r.Context(), conn, assembler.ToPtyServerFrame(subscribed)); err != nil {
		return
	}
	replayCount := len(subscription.Replay)
	if subscription.CursorExpired {
		problem := workspaceapi.PtyServerFrame{Version: 1, Kind: workspaceapi.PtyFrameProblem,
			TerminalSessionID: terminalSessionID, Incarnation: incarnation, WorkspaceID: cached.WorkspaceID,
			ThroughSeq: subscription.Session.ThroughSeq,
			Problem: &workspaceapi.ResourceProblem{Code: string(workspaceapi.ErrorCursorExpired),
				Message: "PTY 输出游标已过期，已发送有界快照"}}
		if err := writePtyStreamFrame(r.Context(), conn, assembler.ToPtyServerFrame(problem)); err != nil {
			return
		}
		if subscription.Snapshot != nil {
			if err := writePtyStreamFrame(r.Context(), conn, assembler.ToPtyServerFrame(*subscription.Snapshot)); err != nil {
				return
			}
		}
	} else {
		for _, frame := range subscription.Replay {
			if err := writePtyStreamFrame(r.Context(), conn, assembler.ToPtyServerFrame(frame)); err != nil {
				return
			}
		}
	}
	// WebSocket 之后可能保持数小时；恢复载荷一旦写入 wire 就不再需要，不能
	// 让每条 attach 长期保留一份最多数 MiB 的 replay/snapshot。
	subscription.ReleaseRecoveryPayload()
	s.log.Info("PTY WebSocket 已订阅", "workspace_id", cached.WorkspaceID,
		"terminal_session_id", terminalSessionID, "incarnation", incarnation,
		"after_seq", after, "through_seq", subscription.Session.ThroughSeq,
		"replay_count", replayCount, "cursor_expired", subscription.CursorExpired)

	streamCtx, cancel := context.WithCancel(r.Context())
	defer cancel()
	clientErrors := make(chan error, 1)
	go readPtyClientFrames(streamCtx, conn, assembler, subscription, clientErrors)
	events := subscription.Events
	for {
		select {
		case <-r.Context().Done():
			return
		case clientErr := <-clientErrors:
			if clientErr != nil {
				problem := resourceProblem(clientErr, cached.WorkspaceID)
				_ = writePtyStreamFrame(context.Background(), conn, desktopapi.PtyServerFrameDTO{
					Version: 1, Kind: string(workspaceapi.PtyFrameProblem), TerminalSessionID: terminalSessionID,
					Incarnation: incarnation, WorkspaceID: cached.WorkspaceID, ThroughSeq: subscription.Session.ThroughSeq, Problem: &problem,
				})
			}
			return
		case streamErr, ok := <-subscription.Done:
			// provider 在关闭 Done 前先关闭 Events；先排空可保证 exit frame 不被
			// select 随机性越过，客户端总能看到最终状态。
			for frame := range subscription.Events {
				if err := writePtyStreamFrame(r.Context(), conn, assembler.ToPtyServerFrame(frame)); err != nil {
					return
				}
			}
			if ok && streamErr != nil {
				problem := resourceProblem(streamErr, cached.WorkspaceID)
				_ = writePtyStreamFrame(context.Background(), conn, desktopapi.PtyServerFrameDTO{
					Version: 1, Kind: string(workspaceapi.PtyFrameProblem), TerminalSessionID: terminalSessionID,
					Incarnation: incarnation, WorkspaceID: cached.WorkspaceID, ThroughSeq: subscription.Session.ThroughSeq, Problem: &problem,
				})
			}
			return
		case frame, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if err := writePtyStreamFrame(r.Context(), conn, assembler.ToPtyServerFrame(frame)); err != nil {
				return
			}
		}
	}
}

func readPtyClientFrames(ctx context.Context, conn *websocket.Conn, assembler *desktopapi.ResourceAssembler,
	subscription *workspaceapi.PtySubscription, result chan<- error) {
	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			if websocket.CloseStatus(err) == websocket.StatusNormalClosure || ctx.Err() != nil {
				result <- nil
			} else {
				result <- fmt.Errorf("PTY 客户端流中断: %w", err)
			}
			return
		}
		var dto desktopapi.PtyClientFrameDTO
		if err := json.Unmarshal(raw, &dto); err != nil {
			result <- &workspaceapi.Error{Code: workspaceapi.ErrorCommandConflict, Message: "PTY 客户端 frame 必须是有效 JSON"}
			return
		}
		if err := subscription.Send(ctx, assembler.FromPtyClientFrame(dto)); err != nil {
			result <- err
			return
		}
	}
}

func (s *Server) cacheTerminalSession(ctx context.Context, workspaceID string, session workspaceapi.PtySession) error {
	workspace, err := s.st.GetWorkspace(ctx, workspaceID)
	if err != nil {
		return fmt.Errorf("读取 PTY Workspace: %w", err)
	}
	return s.st.CachePtySession(ctx, workspace.MachineID, session)
}

func (s *Server) writeTerminalLookupError(w http.ResponseWriter, terminalSessionID string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		desktopapi.WriteProblem(w, http.StatusNotFound, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "PTY session 不存在",
		})
		return
	}
	s.log.Error("PTY session 定位失败", "terminal_session_id", terminalSessionID, "cause", err)
	desktopapi.WriteProblem(w, http.StatusInternalServerError, desktopapi.Problem{
		Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "PTY session 定位失败", Retryable: true,
	})
}

func writePtyStreamFrame(parent context.Context, conn *websocket.Conn, frame desktopapi.PtyServerFrameDTO) error {
	raw, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, raw)
}
