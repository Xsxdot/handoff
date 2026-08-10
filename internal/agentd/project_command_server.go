// agentd peer 项目目录命令 HTTP adapter。
//
// 职责：
//   - 接收配对 agentd 的 InspectPath/Clone 请求并调用本机 machineauthority
//   - 返回稳定、版本化的 peer DTO 与可行动 Problem
//   - 记录 operation/target/path 阶段，不记录 Git URL 或凭证
//
// 边界：
//   - 不使用 SSH，不运行平台探针；owner agentd 直接操作自己的文件系统
//   - 项目 Location 数量、role/机器匹配由发起端 ProjectService 校验
package agentd

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/desktopapi"
	"github.com/xushixin/handoff/internal/machineauthority"
	"github.com/xushixin/handoff/internal/peer"
)

type projectMachineAuthority interface {
	InspectPath(context.Context, string) (controlplane.PathInspection, error)
	Clone(context.Context, machineauthority.CloneCommand) (controlplane.PathInspection, error)
}

func (s *Server) handlePeerProjectInspectPath(w http.ResponseWriter, r *http.Request) {
	if !s.requireMachineAuthority(w) {
		return
	}
	var request peer.ProjectInspectRequest
	if !decodeProjectCommand(w, r, &request) {
		return
	}
	if request.OperationID == "" || request.TargetID == "" || request.Path == "" {
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "operation_id、target_id、path 必填",
		})
		return
	}
	inspection, err := s.machineAuthority.InspectPath(r.Context(), request.Path)
	if err != nil {
		s.log.Error("peer 项目目录检查失败", "operation_id", request.OperationID,
			"target_id", request.TargetID, "path", request.Path, "cause", err)
		desktopapi.WriteProblem(w, http.StatusConflict, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "目标目录不可访问或无效",
		})
		return
	}
	s.log.Info("peer 项目目录检查完成", "operation_id", request.OperationID,
		"target_id", request.TargetID, "path", inspection.Path, "is_repo", inspection.IsRepo)
	writeJSON(w, http.StatusOK, toPeerPathInspection(inspection))
}

func (s *Server) handlePeerProjectClone(w http.ResponseWriter, r *http.Request) {
	if !s.requireMachineAuthority(w) {
		return
	}
	var request peer.ProjectCloneRequest
	if !decodeProjectCommand(w, r, &request) {
		return
	}
	if request.OperationID == "" || request.TargetID == "" || request.GitURL == "" || request.ClonePath == "" {
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code:    desktopapi.ProblemResourceNotFound,
			Message: "operation_id、target_id、git_url、clone_path 必填",
		})
		return
	}
	inspection, err := s.machineAuthority.Clone(r.Context(), machineauthority.CloneCommand{
		GitURL: request.GitURL, ClonePath: request.ClonePath,
	})
	if err != nil {
		// Git URL 可能包含 userinfo/token，日志只记录目标 path 与不透明 command ID。
		s.log.Error("peer 项目仓库 clone 失败", "operation_id", request.OperationID,
			"target_id", request.TargetID, "clone_path", request.ClonePath, "cause", err)
		desktopapi.WriteProblem(w, http.StatusConflict, desktopapi.Problem{
			Code: desktopapi.ProblemVersionConflict, Message: "clone 失败或目标目录冲突",
		})
		return
	}
	s.log.Info("peer 项目仓库 clone 完成", "operation_id", request.OperationID,
		"target_id", request.TargetID, "clone_path", inspection.Path, "is_repo", inspection.IsRepo)
	writeJSON(w, http.StatusOK, toPeerPathInspection(inspection))
}

func (s *Server) requireMachineAuthority(w http.ResponseWriter) bool {
	if s.machineAuthority != nil {
		return true
	}
	desktopapi.WriteProblem(w, http.StatusServiceUnavailable, desktopapi.Problem{
		Code: desktopapi.ProblemLocalAgentdUnavailable, Message: "项目目录命令服务未就绪", Retryable: true,
	})
	return false
}

func decodeProjectCommand(w http.ResponseWriter, r *http.Request, out any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		desktopapi.WriteProblem(w, http.StatusBadRequest, desktopapi.Problem{
			Code: desktopapi.ProblemResourceNotFound, Message: "项目命令请求体无效",
		})
		return false
	}
	return true
}

func toPeerPathInspection(value controlplane.PathInspection) peer.ProjectPathInspection {
	return peer.ProjectPathInspection{
		Path: value.Path, CanonicalPath: value.CanonicalPath, IsRepo: value.IsRepo,
		RepoIdentity: value.RepoIdentity, GitCommonDir: value.GitCommonDir,
		Branch: value.Branch, HeadOID: value.HeadOID,
	}
}
