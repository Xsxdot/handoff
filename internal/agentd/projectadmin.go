// 本文件是 agentd 侧「项目 × 本机位置」的 HTTP 面。
//
// 职责：
//   - PATCH /api/projects/{name}：改引用名与/或本机路径
//   - GET /api/projects/{name}/branches：列本地分支与占用
//   - POST /api/projects/{name}/worktrees：开一棵手工工作树并挂卡基线
//
// 边界：
//   - Manager 侧的项目位置实现（登记/列出/注销/磁盘校验）已随 B233.13 迁往
//     internal/orchestration；本文件只保留 handler 与其校验助手（DTO 留 gateway，
//     D1 拍板）。
//   - 不做解析：派发时「这个请求指哪个项目」由 projectresolve.go 的纯函数决定
//   - 不做持久化细节：SQL 在 internal/store/projects.go
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/projectid"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// ErrProjectAlreadyExists 表示位置冲突或克隆落点已被占用，映射 409。
//
// 与 ErrRepoUnusable（400）的区别：那是「请求本身有问题，改了再来」，
// 这是「当前状态与请求冲突」——和 ErrDirtyWorktree / ErrWorkdirBusy 同层级。
var ErrProjectAlreadyExists = errors.New("项目位置冲突或克隆落点已存在")

// ErrProjectOriginMismatch 表示调用方声称的项目与该路径上实际仓库的 origin 不符，映射 400。
//
// 为什么必须单列一个哨兵：这是自动化最容易造出的脏登记——路径敲错但恰好指到
// 另一个真实仓库。若并进 ErrRepoUnusable，报文就变成含糊的「仓库不可用」，
// 而人需要的是「你说的是 A，那儿实际是 B」。
var ErrProjectOriginMismatch = errors.New("路径上的仓库与请求的项目不是同一个")

// project ls 的状态取值。不落库，每次列出时现场探得。
const (
	ProjectStatusOK      = "有效"
	ProjectStatusMissing = "路径不存在"
	ProjectStatusNotRepo = "不是 git 仓库"
)

// NameFallbackLimit 是名字冲突时的最大退让次数（handoff-2 … handoff-50）。
//
// 为什么要有上限：退让是个循环，没有上限时一张被写坏的表能让登记请求空转。
// 50 远超「一台机器上有 50 个同末段名的不同项目」的现实量级。
const NameFallbackLimit = 50

// RegisterProjectReq 是登记一个项目位置的请求。
//
// 形态由 Path 是否给出 / 路径是否存在 / OriginURL 是否非空共同决定（三态决策表）：
//   - Path 空 + OriginURL 空 → 400：既无身份也无落点
//   - Path 空 + OriginURL 有 → 由本机 clone 到 cfg.RepoRoot/<Name>（或认领已有落点）
//   - Path 非空且目录存在 → 登记已有仓（OriginURL 可省，省则现读 origin）
//   - Path 非空且目录不存在 + OriginURL 空 → 400：无 URL 无法创建
//   - Path 非空且目录不存在 + OriginURL 有 → clone 到该 Path 再登记
//   - 其余非法组合 → 400
//
// Path 的形态约束：必须是**绝对路径**。相对路径与 ~ 一律 400——clone 落点与
// 落库路径的解析基准不同，猜错的代价是一条指向不存在路径的死记录。
//
// 为什么没有 Clone 布尔位：形态已被 Path + 文件系统状态 + OriginURL 是否为空
// 完全决定，多一个布尔位只会多出一组无意义的非法组合。
//
// Name 可省，此时由 OriginURL（请求给出或现读的实际 origin）末段派生；
// 它只是人可读引用，不参与身份判定。
type RegisterProjectReq struct {
	OriginURL string
	Name      string
	Path      string
}

// ProjectOriginURL 读取仓库的 origin 地址。
//
// 参数：
//   - ctx: 控制 git 调用生命周期
//   - repo: 仓库路径
//
// 返回：
//   - origin 地址；仓库不可用或没有 origin 时返回包装 ErrRepoUnusable 的错误
//
// 注意：
//   - 没有 origin 的仓库拒绝登记：project_id 由 origin 派生，没有 origin 就
//     算不出身份，登记进来只会是一条永远引用不到的死记录
//
// B233.13：保留声明于 gateway（编排包经 agentd.ProjectOriginURL 引用）。
func ProjectOriginURL(ctx context.Context, repo string) (string, error) {
	return workspace.OriginURL(ctx, repo)
}

// ProjectNameFromURL 从 git URL 末段派生缺省引用名（去掉 .git 后缀）。
//
// 例：git@github.com:Xsxdot/handoff.git → handoff
//
// why 分隔符集合里有反斜杠：origin 可以是 Windows 本地路径（`C:\work\x.git`）。
// git URL 的四种形态（https/ssh/scp 简写/file）都不含反斜杠，把它加进集合
// 对既有形态零影响，只有本地路径 origin 会走到这一支。
//
// B233.13：保留声明于 gateway（编排包经 agentd.ProjectNameFromURL 引用）。
func ProjectNameFromURL(url string) string {
	s := strings.TrimRight(strings.TrimSpace(url), `/\`)
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, `/:\`); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// ValidateProjectName 校验引用名的合法性，返回包装 ErrBadDispatchRequest 的错误。
//
// 规则：
//   - 空名或纯空白 → 拒
//   - 名字含 / \ : → 拒：clone 落点是 repo_root/<名字>，这三个字符会让它跑到别处
//   - 名字为 . / .. 或含 .. 路径段 → 拒：会让落点逃出 repo_root
//
// 为什么必须入口拦：名字由 origin 末段派生或人工指定，没人保证它干净；
// 而它会被直接拼进文件系统路径。
//
// B233.13：保留声明于 gateway（handler 与编排包共用）。
func ValidateProjectName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%w: 项目名不能为空", ErrBadDispatchRequest)
	}
	if strings.ContainsAny(name, `/\:`) {
		return fmt.Errorf("%w: 项目名 %q 含路径特征字符（/ \\ :），会让克隆落点跑到 repo_root 之外",
			ErrBadDispatchRequest, name)
	}
	for _, seg := range strings.FieldsFunc(name, func(r rune) bool { return r == '/' || r == '\\' }) {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%w: 项目名 %q 含 . 或 .. 路径段，会让克隆落点逃出 repo_root",
				ErrBadDispatchRequest, name)
		}
	}
	return nil
}

// SameLocation 比较两个路径是否指向同一位置。
//
// 为什么不能直接比字符串：git 在 linked worktree 里返回的 common-dir 是
// 主仓 .git 的**符号链接解析后**绝对路径（macOS 上 /var → /private/var），
// 而首次登记存进表的路径来自调用方目录（未解析符号链接）。两个都解析再比，
// 才能让「linked worktree 归并后幂等命中」在 macOS 上成立。
//
// 路径已不存在时（EvalSymlinks 失败）退回直接比较 Clean 后的串。
//
// B233.13：保留声明于 gateway（handler 与编排包共用）。
func SameLocation(a, b string) bool {
	if ra, errA := filepath.EvalSymlinks(a); errA == nil {
		if rb, errB := filepath.EvalSymlinks(b); errB == nil {
			return ra == rb
		}
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

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
	cur, err := s.st.GetProjectLocationByName(name)
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
		} else if err := ValidateProjectName(newName); err != nil {
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
		if SameLocation(cur.Path, root) {
			newPath = ""
		} else {
			newPath = root
		}
	}
	loc, err := s.st.UpdateProjectLocation(name, newName, newPath)
	if err != nil {
		s.writeProjectError(w, name, err)
		return
	}
	s.log.Info("project patch 完成", "old_name", name, "new_name", loc.Name,
		"old_path", cur.Path, "new_path", loc.Path)
	// 与 handleProjectAdd/persistProject 对齐：登记返回前也填「有效」，两个端点
	// 都返回 proto.ProjectLocation，行为应一致（改 path 场景新目录刚过
	// EnsureRepoUsable，填「有效」语义成立）。
	loc.Status = ProjectStatusOK
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
	loc, err := s.st.GetProjectLocationByName(name)
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
	var req proto.CreateWorktreeReq
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		s.log.Warn("建树请求体解析失败", "name", name, "status", http.StatusBadRequest, "cause", err)
		writeJSON(w, http.StatusBadRequest,
			map[string]string{"error": "请求体必须是 JSON {mode, branch, base}"})
		return
	}
	s.log.Info("建树请求", "name", name, "machine", r.URL.Query().Get("machine"),
		"mode", req.Mode, "branch", req.Branch, "base", req.Base, "card_count", len(req.CardIDs))
	loc, err := s.st.GetProjectLocationByName(name)
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
	if s.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
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
