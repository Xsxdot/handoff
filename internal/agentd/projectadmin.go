// 本文件是 agentd 侧「项目 × 本机位置」的 HTTP 面。
//
// 职责：
//   - PATCH /api/projects/{name}：改引用名与/或本机路径
//   - GET /api/projects/{name}/branches：列本地分支与占用
//   - POST /api/projects/{name}/worktrees：开一棵手工工作树并挂卡基线
//
// 边界：
//   - Manager 侧的项目位置实现（登记/列出/注销/磁盘校验）已随 B233.13 迁往
//     internal/orchestration；登记域共享词汇（哨兵/校验/派名）已随 B233.19
//     迁往 internal/workspace（ValidateProjectName / SameLocation /
//     ErrProjectAlreadyExists / ProjectStatus* 等，本文件经 workspace.* 引用）。
//     本文件只保留 handler 与其校验助手（DTO 留 gateway，D1 拍板）。
//   - 不做解析：派发时「这个请求指哪个项目」由 workspace.ResolveProject 决定
//   - 不做持久化细节：SQL 在 internal/store/projects.go
package agentd

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/projectid"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// projectPatchRequest 是 PATCH /api/projects/{name} 的请求体。
//
// 两个字段都可选，但不能都为空：new_name 改引用名，path 改本机路径（两个改动
// 可以同时发生，也可各自独立）。
type projectPatchRequest struct {
	NewName string `json:"new_name"`
	Path    string `json:"path"`
}

// handleProjectPatch 改一条项目位置的引用名与/或 path。
//
// 顺序（handler 级判定，**不许调换**）：
//  1. 解析 body；两个字段都空 → 400
//  2. 按 name 取当前记录；不存在 → 404
//  3. 若要改 name：复用登记时那套 ValidateProjectName 校验；不合法 → 400。
//     new_name 与当前 name 相同 → 当作没改这个字段（spec §3.3），传给 store 空串
//  4. 若要改 path：对新目录做与登记同款的检查（InspectRepoDir 现读 origin），
//     算 projectid.FromOrigin(origin)：
//     - 与当前 project_id 不同 → 400（该目录是另一个项目）
//     - 相同且 new path 与当前 path 是同一位置（SameLocation）→ 当作没改这个字段
//  5. store.UpdateProjectLocation；ErrProjectDuplicate → 409（writeProjectError）、
//     ErrNotFound → 404
//  6. 返回更新后的记录
//
// 注意：
//   - 本机与远程都能改：先 forwardIfRequested，显式 ?machine= 的请求本机只做搬运
//   - 改 path 的两条校验（origin 一致 + 非同一位置）保证「编辑 path」永远不把登记
//     静默指向另一个仓库：project_id 由 origin 派生，磁盘上换了仓库而身份不变是
//     比不给编辑危险得多的脏状态（本 handler 的正身）
func (s *Server) handleProjectPatch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if s.forwardIfRequested(w, r) {
		return // 显式指名了别的机器：本机只做搬运（W3a §5.1.1）
	}
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req projectPatchRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("project patch 请求体解析失败", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "请求体必须是 JSON {new_name, path}"})
		return
	}
	// 进入处理：明确本次要改哪些字段
	s.log.Info("project patch 请求", "name", name,
		"change_name", req.NewName != "", "new_name", req.NewName,
		"change_path", req.Path != "", "path", req.Path)
	if req.NewName == "" && req.Path == "" {
		s.log.Warn("project patch 被拒：两个字段都为空", "name", name,
			"cause", "new_name 与 path 不能都为空")
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "new_name 与 path 不能都为空"})
		return
	}
	cur, err := s.mgr.GetProjectLocationByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("project patch 被拒：项目不存在", "name", name, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		s.log.Error("project patch 读取项目失败", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	// 改 name：复用登记时那套校验；new_name 与当前相同视为没改
	newName := req.NewName
	if newName != "" {
		if newName == cur.Name {
			newName = ""
		} else if err := workspace.ValidateProjectName(newName); err != nil {
			s.log.Warn("project patch 被拒：新名字非法", "name", name,
				"new_name", req.NewName, "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
	}
	// 改 path：与登记同款的三步检查（InspectRepoDir 内部 EnsureRepoUsable →
	// MainWorktreeRoot → 现读 origin），project_id 由 origin 派生，必须与当前一致
	newPath := req.Path
	if newPath != "" {
		root, origin, ierr := s.mgr.InspectRepoDir(r.Context(), newPath)
		if ierr != nil {
			s.log.Warn("project patch 被拒：新路径不是可用的仓库",
				"name", name, "path", newPath, "cause", ierr)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": ierr.Error()})
			return
		}
		if pid := projectid.FromOrigin(origin); pid != cur.ProjectID {
			s.log.Warn("project patch 被拒：新路径属于另一个项目",
				"name", name, "path", root, "origin", origin,
				"current_project_id", cur.ProjectID, "cause", "该目录是另一个项目（origin 不同）")
			writeJSON(w, http.StatusBadRequest,
				map[string]string{"error": "该目录是另一个项目（origin 不同），请注销后重新添加"})
			return
		}
		// 同一位置（归并后）视为没改这个字段——与登记幂等的语义一致
		if workspace.SameLocation(cur.Path, root) {
			newPath = ""
		} else {
			newPath = root
		}
	}
	loc, err := s.mgr.UpdateProjectLocation(name, newName, newPath)
	if err != nil {
		s.writeProjectError(w, name, err)
		return
	}
	s.log.Info("project patch 完成", "old_name", name, "new_name", loc.Name,
		"old_path", cur.Path, "new_path", loc.Path)
	// 与 handleProjectAdd/persistProject 对齐：登记返回前也填「有效」，两个端点
	// 都返回 proto.ProjectLocation，行为应一致（改 path 场景新目录刚过
	// EnsureRepoUsable，填「有效」语义成立）。
	loc.Status = workspace.ProjectStatusOK
	writeJSON(w, http.StatusOK, loc)
}

// handleProjectBranches 处理 GET /api/projects/{name}/branches[?machine=]。
//
// 列出该项目位置的本地分支，并标出每个分支是否已被某棵工作树检出——建树弹层
// 的两个下拉都吃这份数据。
//
// 参数：
//   - name（路径）: 项目登记名
//   - machine（查询）: 可选，转发到指定机器
//
// 响应：200 proto.ProjectBranchesResp；项目不存在 404；列分支失败 500（原文透出）
func (s *Server) handleProjectBranches(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.PathValue("name")
	s.log.Info("列项目分支请求", "name", name, "machine", r.URL.Query().Get("machine"))
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	loc, err := s.mgr.GetProjectLocationByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("列项目分支被拒：项目不存在", "name", name, "status", http.StatusNotFound, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未登记"})
			return
		}
		s.log.Error("列项目分支失败：查询位置表", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	names, err := workspace.Branches(loc.Path)
	if err != nil {
		s.log.Error("列项目分支失败", "name", name, "repo", loc.Path, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	worktreesDir := filepath.Join(s.conf().DataDir, "worktrees")
	// 占用表复用项目树那次探测的同一个函数，不另跑一遍 worktree list：
	// 两处口径分叉时，界面置灰的分支与真能建的分支会对不上
	existing, probeErr := workspace.ProbeWorkspaces(r.Context(), loc.Path, worktreesDir)
	if probeErr != "" {
		s.log.Warn("列项目分支：探测工作树失败，占用信息缺失", "name", name, "cause", probeErr)
	}
	byBranch := make(map[string]string, len(existing))
	for _, ws := range existing {
		if ws.Branch != "" {
			byBranch[ws.Branch] = ws.Path
		}
	}
	resp := proto.ProjectBranchesResp{
		Branches:     make([]proto.ProjectBranch, 0, len(names)),
		Default:      workspace.ResolveBaseBranch(loc.Path),
		WorktreeRoot: workspace.ManualWorktreeRoot(worktreesDir),
	}
	for _, b := range names {
		resp.Branches = append(resp.Branches, proto.ProjectBranch{Name: b, Worktree: byBranch[b]})
	}
	s.log.Info("列项目分支完成", "name", name, "count", len(resp.Branches),
		"default", resp.Default, "occupied", len(byBranch))
	writeJSON(w, http.StatusOK, resp)
}

// handleProjectWorktreeCreate 处理 POST /api/projects/{name}/worktrees[?machine=]。
//
// 在该项目位置上开一棵不属于任何任务的工作树（spec §3.2）。
//
// 参数：
//   - name（路径）: 项目登记名
//   - machine（查询）: 可选，转发到指定机器
//   - 请求体: proto.CreateWorktreeReq
//
// 响应：200 proto.Workspace；项目不存在 404；请求不合法 400；git 失败 500（原文透出）
func (s *Server) handleProjectWorktreeCreate(w http.ResponseWriter, r *http.Request) {
	handled, forwarded, cardIDs := s.forwardWorktreeIfRequested(w, r)
	if handled {
		if forwarded == nil {
			return
		}
		if len(cardIDs) > 0 && s.ledger != nil {
			*forwarded = s.attachCardBaseBranches(*forwarded, cardIDs, s.ledgerActor(r))
		}
		writeJSON(w, http.StatusOK, *forwarded)
		s.log.Info("跨机建树完成并由本机挂卡", "machine", r.URL.Query().Get("machine"),
			"branch", forwarded.Branch, "card_result_count", len(forwarded.CardResults))
		return
	}
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.PathValue("name")
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req proto.CreateWorktreeReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("建树请求体解析失败", "name", name, "status", http.StatusBadRequest, "cause", err)
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "请求体必须是 JSON {mode, branch, base}"})
		return
	}
	s.log.Info("建树请求", "name", name, "machine", r.URL.Query().Get("machine"),
		"mode", req.Mode, "branch", req.Branch, "base", req.Base, "card_count", len(req.CardIDs))
	loc, err := s.mgr.GetProjectLocationByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("建树被拒：项目不存在", "name", name, "status", http.StatusNotFound, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未登记"})
			return
		}
		s.log.Error("建树失败：查询位置表", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	if err := s.mgr.RequireWorkspace(); err != nil {
		s.log.Error("建树失败：工作区能力未注入", "name", name, "cause", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("准备通过工作区能力创建手工树", "name", name, "repo", loc.Path,
		"mode", req.Mode, "branch", req.Branch)
	tree, err := s.mgr.Workspace().CreateManual(r.Context(), loc.Path,
		filepath.Join(s.conf().DataDir, "worktrees"),
		workspace.ManualReq{Mode: req.Mode, Branch: req.Branch, Base: req.Base})
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, workspace.ErrBadWorktreeReq) {
			status = http.StatusBadRequest
		}
		s.log.Error("建树失败", "name", name, "repo", loc.Path, "mode", req.Mode,
			"branch", req.Branch, "status", status, "cause", err)
		writeJSON(w, status, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	s.log.Info("手工树创建成功", "name", name, "path", tree.Path, "branch", tree.Branch)
	ws := proto.Workspace{Path: tree.Path, Branch: tree.Branch, Head: tree.Head, Managed: tree.Managed}
	if len(req.CardIDs) > 0 && s.ledger != nil {
		ws = s.attachCardBaseBranches(ws, req.CardIDs, s.ledgerActor(r))
	}
	s.log.Info("建树完成", "name", name, "dir", ws.Path, "branch", ws.Branch,
		"card_result_count", len(ws.CardResults))
	writeJSON(w, http.StatusOK, ws)
}

// attachCardBaseBranches 在 Git 工作树已经成功后，按请求顺序逐张设置卡基线。
// 单张失败只进入该项结果，不删除已经存在的工作树，也不回滚此前成功的卡。
func (s *Server) attachCardBaseBranches(ws proto.Workspace, ids []string, actor string) proto.Workspace {
	results := make([]proto.CardBaseBranchResult, 0, len(ids))
	s.log.Info("建树后开始逐卡挂基线", "branch", ws.Branch, "card_count", len(ids), "actor", actor)
	for _, id := range ids {
		result := proto.CardBaseBranchResult{ID: id}
		if err := s.ledger.SetCardBaseBranch(id, ws.Branch, actor); err != nil {
			result.Error = err.Error()
			s.log.Warn("建树后挂卡失败：工作树保留", "card", id, "branch", ws.Branch, "cause", err)
		} else {
			result.OK = true
			s.log.Info("建树后挂卡完成", "card", id, "branch", ws.Branch, "actor", actor)
		}
		results = append(results, result)
	}
	ws.CardResults = results
	s.log.Info("建树后逐卡挂基线完成", "branch", ws.Branch, "card_count", len(results))
	return ws
}
