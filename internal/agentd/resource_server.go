// agentd Workspace 文件资源 HTTP/WS adapter。
//
// 职责：
//   - decode Workspace ID/relative path 请求并调用 resourcegateway.Router
//   - 使用 ResourceAssembler 编解码文件 DTO 与 typed Problem
//   - 输出 subscribed replay + live event 的版本化 WebSocket frame
//
// 边界：
//   - 不读写文件、不解析 owner root/endpoint/token；这些只存在于 gateway/peer 内部
//   - 不记录 content_base64、搜索 query、preview 或 remote token
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/workspaceapi"
)

const resourceRequestLimit = 32 << 20

func (s *Server) handleResourceEntries(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	workspaceID := r.PathValue("workspace_id")
	entries, err := s.resources.ListDirectory(r.Context(), workspaceID, r.URL.Query().Get("path"))
	if err != nil {
		s.writeResourceError(w, workspaceID, err)
		return
	}
	dto := (&desktopapi.ResourceAssembler{}).ToFileEntries(entries)
	s.log.Info("文件目录 API 完成", "workspace_id", workspaceID, "entry_count", len(dto))
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleResourceFile(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	workspaceID := r.PathValue("workspace_id")
	doc, err := s.resources.ReadFile(r.Context(), workspaceID, r.URL.Query().Get("path"))
	if err != nil {
		s.writeResourceError(w, workspaceID, err)
		return
	}
	dto := (&desktopapi.ResourceAssembler{}).ToFileDocument(doc)
	s.log.Info("文件读取 API 完成", "workspace_id", workspaceID, "relative_path", dto.Path,
		"version", dto.Version, "size", dto.Size)
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleResourceWriteFile(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	workspaceID := r.PathValue("workspace_id")
	var req desktopapi.WriteFileRequest
	if err := decodeResourceJSON(w, r, &req); err != nil {
		return
	}
	assembler := &desktopapi.ResourceAssembler{}
	command, err := assembler.ToWriteFileCommand(workspaceID, req)
	if err != nil {
		s.writeResourceError(w, workspaceID, &workspaceapi.Error{Code: workspaceapi.ErrorCommandConflict, Message: err.Error()})
		return
	}
	doc, err := s.resources.WriteFile(r.Context(), workspaceID, command)
	if err != nil {
		s.writeResourceError(w, workspaceID, err)
		return
	}
	dto := assembler.ToFileDocument(doc)
	s.log.Info("文件写入 API 完成", "workspace_id", workspaceID, "relative_path", dto.Path,
		"command_id", command.CommandID, "version", dto.Version, "size", dto.Size)
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleResourceSearchFiles(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	workspaceID := r.PathValue("workspace_id")
	var req desktopapi.SearchFilesRequest
	if err := decodeResourceJSON(w, r, &req); err != nil {
		return
	}
	assembler := &desktopapi.ResourceAssembler{}
	result, err := s.resources.SearchFiles(r.Context(), workspaceID, assembler.ToSearchFilesCommand(workspaceID, req))
	if err != nil {
		s.writeResourceError(w, workspaceID, err)
		return
	}
	dto := assembler.ToFileSearchResult(result)
	s.log.Info("文件搜索 API 完成", "workspace_id", workspaceID, "match_count", len(dto.Matches),
		"scanned_files", dto.ScannedFiles, "scanned_bytes", dto.ScannedBytes, "truncated", dto.Truncated)
	writeJSON(w, http.StatusOK, dto)
}

func (s *Server) handleResourceFileStream(w http.ResponseWriter, r *http.Request) {
	if !s.requireResources(w) {
		return
	}
	workspaceID := r.PathValue("workspace_id")
	after, err := parseAfter(r)
	if err != nil {
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{Code: desktopapi.ProblemCursorExpired, Message: "after 必须是大于等于 0 的整数", WorkspaceID: workspaceID})
		return
	}
	subscription, err := s.resources.SubscribeFiles(r.Context(), workspaceID, after)
	if err != nil {
		s.writeResourceError(w, workspaceID, err)
		return
	}
	defer subscription.Cancel()
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("文件事件 WebSocket 接受失败", "workspace_id", workspaceID, "cause", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "file stream closed")
	assembler := &desktopapi.ResourceAssembler{}
	through := after
	if len(subscription.Replay) > 0 {
		through = subscription.Replay[len(subscription.Replay)-1].Seq
	}
	if err := writeFileStreamFrame(r.Context(), conn, desktopapi.FileStreamFrameDTO{
		Version: 1, Kind: "subscribed", WorkspaceID: workspaceID, ThroughSeq: through,
		Replay: assembler.ToFileEvents(subscription.Replay),
	}); err != nil {
		return
	}
	s.log.Info("文件事件 WebSocket 已订阅", "workspace_id", workspaceID, "after_seq", after, "through_seq", through,
		"replay_count", len(subscription.Replay))
	for {
		select {
		case <-r.Context().Done():
			return
		case streamErr, ok := <-subscription.Done:
			if ok && streamErr != nil {
				problem := resourceProblem(streamErr, workspaceID)
				_ = writeFileStreamFrame(context.Background(), conn, desktopapi.FileStreamFrameDTO{
					Version: 1, Kind: "problem", WorkspaceID: workspaceID, ThroughSeq: through, Problem: &problem,
				})
			}
			return
		case event, ok := <-subscription.Events:
			if !ok {
				return
			}
			through = event.Seq
			dto := assembler.ToFileEvent(event)
			if err := writeFileStreamFrame(r.Context(), conn, desktopapi.FileStreamFrameDTO{
				Version: 1, Kind: "event", WorkspaceID: workspaceID, ThroughSeq: through, Event: &dto,
			}); err != nil {
				return
			}
		}
	}
}

func (s *Server) requireResources(w http.ResponseWriter) bool {
	if s.resources != nil {
		return true
	}
	desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
		Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "Workspace 资源服务未就绪", Retryable: true,
	})
	return false
}

func decodeResourceJSON(w http.ResponseWriter, r *http.Request, out any) error {
	err := json.NewDecoder(io.LimitReader(r.Body, resourceRequestLimit)).Decode(out)
	if err != nil {
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{Code: desktopapi.ProblemCommandConflict, Message: "请求体必须是有效 JSON"})
	}
	return err
}

func (s *Server) writeResourceError(w http.ResponseWriter, workspaceID string, err error) {
	problem := resourceProblem(err, workspaceID)
	status := http.StatusInternalServerError
	var problemErr *desktopapi.ProblemError
	if errors.As(err, &problemErr) {
		status = problemErr.Status
	}
	s.log.Error("Workspace 资源 API 失败", "workspace_id", workspaceID, "problem_code", problem.Code, "cause", err)
	desktopapi.WriteProblem(w, status, problem)
}

func resourceProblem(err error, workspaceID string) desktopapi.Problem {
	var problemErr *desktopapi.ProblemError
	if errors.As(err, &problemErr) {
		return problemErr.Problem
	}
	var resourceErr *workspaceapi.Error
	if errors.As(err, &resourceErr) {
		return desktopapi.Problem{Code: desktopapi.ProblemCode(resourceErr.Code), Message: resourceErr.Message,
			Retryable: resourceErr.Retryable, WorkspaceID: workspaceID}
	}
	return desktopapi.Problem{Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "Workspace 资源服务暂不可用", Retryable: true, WorkspaceID: workspaceID}
}

func writeFileStreamFrame(parent context.Context, conn *websocket.Conn, frame desktopapi.FileStreamFrameDTO) error {
	raw, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(parent, 10*time.Second)
	defer cancel()
	return conn.Write(ctx, websocket.MessageText, raw)
}
