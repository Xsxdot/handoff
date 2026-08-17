// 本文件实现「按工作树寻址」的文件接口：目录列举、读文件、写文件、条目
// 操作（建/复制/改名/删）与文件夹内查找。
//
// 职责：
//   - resolveWorkspace：白名单闸门——只有 GET /api/projects/tree 探测得出的
//     工作树路径才被接受
//   - handleWorkspaceDir / handleWorkspaceFile / handleWorkspaceFileWrite /
//     handleWorkspaceEntryCreate / handleWorkspaceEntryCopy /
//     handleWorkspaceEntryRename / handleWorkspaceEntryDelete /
//     handleWorkspaceSearch：八个端点的 HTTP 层（写文件只解请求体 + 映射错误，
//     判断力全在 WriteFile；条目操作与搜索同样只做解参 + 映射错误，判断力全在
//     CreateEntry/CopyEntry/RenameEntry/DeleteEntry/SearchInDir）
//
// 边界：
//   - 写文件只在单个已存在文件上做原子替换，不建目录、不删任何东西；条目
//     操作能建、能删、能改名、能复制（单层名，不出当前目录），查找在目录内
//     扫文本行——全部只落在本机**已探测到**的工作树内部，越过边界一律 4xx
//   - 不接受任意路径。**这是参数校验，不是安全边界**：控制台会话在能力上
//     等价于主令牌（auth 中间件让两者落在同一个 mux 上，其中包含
//     POST /api/tasks/{id}/run 的 sh -c），白名单挡不住任何有心人。它存在的
//     理由是防止前端传一个打错的路径把 agentd 变成任意目录浏览器。
//     完整论证见 docs/superpowers/specs/2026-08-12-w4-pty-terminal-design.md §1。
//   - 不改 /api/tasks/{id}/file：那是 CLI `handoff fetch` 在用的既有契约。
//     另开一条是因为工作树可以没有任务（人手开的、任务已 done 的），
//     文件浏览不能依赖任务存在
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"strconv"

	"github.com/Xsxdot/handoff/internal/proto"
)

// resolveWorkspace 判定 path 是否是本机**已探测到**的某个工作树。
//
// 判定口径与 GET /api/projects/tree 完全一致：先比登记表里的位置路径（一次
// 数据库读，绝大多数请求命中主目录时到此为止），未命中再对每个 location 跑一次
// git worktree list 现场探测。两段式是为了让最常见的请求不付探测成本。
//
// 为什么不缓存探测结果：worktree 会在 agentd 背后被 git worktree add/remove
// 改动，缓存必然产生「已经删掉的工作树还能浏览」的失真窗口——而这条闸门是
// **参数校验**，失真窗口会让用户浏览到一个已经不存在的工作树，报错莫名其妙。
// 真变慢了再谈带短 TTL 的缓存。
//
// 参数：
//   - ctx: 上下文，透传给 probeWorkspaces（其内部另有 5s 兜底超时）
//   - path: 调用方上送的工作树绝对路径
//
// 返回：
//   - 归一化（filepath.Clean）后的路径，仅在 ok 为真时有意义
//   - ok: 命中白名单为真
func (s *Server) resolveWorkspace(ctx context.Context, path string) (string, bool) {
	want := filepath.Clean(path)
	locs, err := s.st.ListProjectLocations()
	if err != nil {
		// 读不出位置表时**拒绝**而不是放行：闸门坏了要关上，不能敞开
		s.log.Error("工作树白名单：查询位置表失败，按拒绝处理", "cause", err)
		return "", false
	}
	for _, l := range locs {
		if l.ProjectID == "" {
			continue
		}
		if filepath.Clean(l.Path) == want {
			return want, true
		}
	}
	managedRoot := filepath.Join(s.conf().DataDir, "worktrees")
	for _, l := range locs {
		if l.ProjectID == "" {
			continue
		}
		ws, probeErr := probeWorkspaces(ctx, l.Path, managedRoot)
		if probeErr != "" {
			continue
		}
		for _, w := range ws {
			if filepath.Clean(w.Path) == want {
				return want, true
			}
		}
	}
	return "", false
}

// workspaceRootOrErr 取出并校验 path 查询参数，失败时已写好响应。
//
// 返回 ok=false 时调用方必须直接 return。
func (s *Server) workspaceRootOrErr(w http.ResponseWriter, r *http.Request) (string, bool) {
	path := r.URL.Query().Get("path")
	if path == "" {
		s.log.Warn("工作树请求缺 path 参数", "url_path", r.URL.Path)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 path 参数"})
		return "", false
	}
	root, ok := s.resolveWorkspace(r.Context(), path)
	if !ok {
		// 400 而不是 403：白名单是**参数校验**，不是安全边界。控制台会话在
		// 能力上等价于主令牌（auth 中间件让两者落在同一个 mux 上，其中包含
		// POST /api/tasks/{id}/run 的 sh -c），所以这里挡不住任何有心人，
		// 它挡的是「前端传了个打错的路径，把 agentd 变成任意目录浏览器」。
		// 403 会宣称一个不存在的权限模型，误导排障的人往鉴权方向找。
		// 完整论证见 docs/superpowers/specs/2026-08-12-w4-pty-terminal-design.md §1
		s.log.Warn("工作树白名单拒绝", "path", path, "url_path", r.URL.Path)
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "路径不是本机已探测到的工作树，拒绝访问"})
		return "", false
	}
	return root, true
}

// handleWorkspaceDir 处理 GET /api/workspaces/dir?path=&rel=[&machine=]。
//
// 参数：
//   - path: 工作树绝对路径（必须，且必须命中白名单，否则 400）
//   - rel: 工作树内的相对目录路径；省略或空串表示工作树根
//   - machine: 可选，转发到指定机器（复用 forwardIfRequested）
func (s *Server) handleWorkspaceDir(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	s.log.Info("工作树目录列举请求", "path", r.URL.Query().Get("path"), "rel", rel)
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	entries, err := ListDir(root, rel)
	if err != nil {
		switch {
		case errors.Is(err, ErrPathEscape):
			s.log.Warn("目录列举路径逃逸被拒绝", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不合法（不允许逃出工作树）"})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("目录列举目标不存在", "root", root, "rel", rel)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "目录不存在"})
		case errors.Is(err, ErrPathNotDir):
			s.log.Warn("目录列举目标不是目录", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不是目录"})
		default:
			s.log.Error("目录列举失败", "root", root, "rel", rel, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "目录列举失败"})
		}
		return
	}
	// 忽略标注在列举之后单独做：ListDir 是纯文件系统操作（还被建/删/改名复用），
	// 而「归不归 git 管」要问 git。失败只降级不影响这次列举（见 markIgnored）
	markIgnored(r.Context(), root, rel, entries)
	s.log.Info("工作树目录列举完成", "root", root, "rel", rel, "entries", len(entries))
	writeJSON(w, http.StatusOK, proto.DirListResult{Entries: entries})
}

// handleWorkspaceFile 处理 GET /api/workspaces/file?path=&rel=[&machine=]。
//
// 语义与 GET /api/tasks/{id}/file 完全一致（同一个 ReadFile、同一套错误映射），
// 只是寻址从任务改为工作树。
func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	s.log.Info("工作树读文件请求", "path", r.URL.Query().Get("path"), "rel", rel)
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	if rel == "" {
		s.log.Warn("工作树读文件缺 rel 参数", "root", root)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 rel 参数"})
		return
	}
	res, err := ReadFile(root, rel)
	if err != nil {
		switch {
		case errors.Is(err, ErrPathEscape):
			s.log.Warn("读文件路径逃逸被拒绝", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不合法（不允许逃出工作树）"})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("读文件目标不存在", "root", root, "rel", rel)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		case errors.Is(err, ErrPathIsDir):
			s.log.Warn("读文件目标是目录", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径是目录，不是文件"})
		case errors.Is(err, ErrNotRegularFile):
			s.log.Warn("读文件目标不是普通文件", "root", root, "rel", rel)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不是普通文件"})
		default:
			s.log.Error("读取文件失败", "root", root, "rel", rel, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取文件失败"})
		}
		return
	}
	// 二进制的正文在**端点层**置空，不在 ReadFile 里置空：ReadFile 是
	// handoff fetch 与在线编辑共用的一段代码，在那里抹掉内容会把 CLI 的
	// 既有行为一起改掉（fetch 一个 PNG 现在返回原始字节，那是已发布契约）。
	// 而对浏览器，返回一串被 UTF-8 替换字符打烂的内容既没有展示价值，
	// 又会诱使人把它存回去
	if res.Binary {
		res.Content = ""
	}
	s.log.Info("工作树读文件完成", "root", root, "rel", rel,
		"bytes", len(res.Content), "size", res.Size,
		"truncated", res.Truncated, "binary", res.Binary)
	writeJSON(w, http.StatusOK, res)
}

// handleWorkspaceFileWrite 处理 PUT /api/workspaces/file?path=&rel=[&machine=]。
//
// 判断力全在 WriteFile 里，本函数只做三件事：解请求体、调它、把哨兵错误映射成
// 状态码。**中文错误原文原样透传**，不吞成「操作失败」——用户看到「不允许写入
// .git 目录」能立刻明白，看到「操作失败」只能来问。
//
// 参数（查询串）：
//   - path: 工作树绝对路径（必须命中白名单，否则 400）
//   - rel: 工作树内的相对路径（必须）
//   - machine: 可选，转发到指定机器（复用 forwardIfRequested，与两个读端点同一条路）
func (s *Server) handleWorkspaceFileWrite(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	if rel == "" {
		s.log.Warn("工作树写文件缺 rel 参数", "root", root)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 rel 参数"})
		return
	}
	var req proto.FileWriteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("工作树写文件请求体解析失败", "root", root, "rel", rel, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 请求体里的 content 可能有几百 KB，日志只记长度不记内容
	s.log.Info("工作树写文件请求", "root", root, "rel", rel,
		"bytes", len(req.Content), "base", shortHash(req.BaseSHA256))

	res, err := WriteFile(root, rel, req.Content, req.BaseSHA256)
	if err != nil {
		switch {
		case errors.Is(err, ErrBaseMismatch):
			// 409 的 body 带磁盘现状：冲突界面的两个出口都要用它
			s.log.Warn("工作树写文件冲突", "root", root, "rel", rel,
				"base", shortHash(req.BaseSHA256), "current", shortHash(res.SHA256))
			writeJSON(w, http.StatusConflict, proto.FileConflictResp{
				Error: "文件已被改动", Current: res})
		case errors.Is(err, ErrPathEscape):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不合法（不允许逃出工作树）"})
		case errors.Is(err, ErrGitDirWrite):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "不允许写入 .git 目录"})
		case errors.Is(err, ErrSymlinkTarget):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "目标是符号链接，不支持在线编辑"})
		case errors.Is(err, ErrPathIsDir):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径是目录，不是文件"})
		case errors.Is(err, ErrNotRegularFile):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "路径不是普通文件"})
		case errors.Is(err, ErrBinaryFile):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "二进制文件不支持在线编辑"})
		case errors.Is(err, ErrFileTooLarge):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件超过 1 MB，不支持在线编辑"})
		case errors.Is(err, fs.ErrNotExist):
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "文件不存在"})
		default:
			s.log.Error("工作树写文件失败", "root", root, "rel", rel, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入文件失败"})
		}
		return
	}
	s.log.Info("工作树写文件完成", "root", root, "rel", rel,
		"bytes", res.Size, "sha256", shortHash(res.SHA256))
	writeJSON(w, http.StatusOK, proto.FileWriteResp{SHA256: res.SHA256, Size: res.Size})
}

// writeEntryError 把条目操作/查找的错误映射成 HTTP 应答（四个 entry 端点 +
// search 端点共用）。
//
// 文案策略与写文件端点同一纪律：被拒的哨兵错误（ErrEntryExists /
// ErrEntryNotFound / ErrBadEntryName / ErrPathEscape / ErrGitDirWrite）**原样透传
// err.Error()**——这些哨兵文案本身就带目标细节（如「不允许写入 .git 目录:
// ".git"」），吞成「操作失败」只会让用户回来问。4xx 一律 Warn（被拒不是 agentd
// 出故障），5xx 才 Error。
func (s *Server) writeEntryError(w http.ResponseWriter, root, rel string, err error) {
	switch {
	case errors.Is(err, ErrEntryExists):
		s.log.Warn("工作树条目操作被拒：目标已存在", "root", root, "rel", rel, "status", http.StatusConflict)
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrEntryNotFound), errors.Is(err, fs.ErrNotExist):
		s.log.Warn("工作树条目操作被拒：目标不存在", "root", root, "rel", rel, "status", http.StatusNotFound)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrBadEntryName):
		s.log.Warn("工作树条目操作被拒：名字不合法", "root", root, "rel", rel, "status", http.StatusBadRequest)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrPathEscape):
		s.log.Warn("工作树条目操作被拒：路径逃逸", "root", root, "rel", rel, "status", http.StatusBadRequest)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	case errors.Is(err, ErrGitDirWrite):
		s.log.Warn("工作树条目操作被拒：命中 .git", "root", root, "rel", rel, "status", http.StatusBadRequest)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		s.log.Error("工作树条目操作失败", "root", root, "rel", rel, "status", http.StatusInternalServerError, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "操作失败"})
	}
}

// handleWorkspaceEntryCreate 处理 POST /api/workspaces/entry?path=&rel=[&machine=]。
//
// 在工作树的 rel 目录下新建一个空文件或空目录（B107 文件树右键菜单「新建」）。
//
// 参数（查询串）：
//   - path: 工作树绝对路径（必须命中白名单，否则 400）
//   - rel: 新条目所在父目录的相对路径；省略或空串表示工作树根
//   - machine: 可选，转发到指定机器（复用 forwardIfRequested）
//
// 请求体：proto.CreateWorkspaceEntryReq（name 为单层名，kind 为 "file" 或 "dir"）。
// 响应：200 返回新建条目的 proto.DirEntry。
func (s *Server) handleWorkspaceEntryCreate(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	s.log.Info("工作树新建条目请求", "method", r.Method, "path", r.URL.Query().Get("path"), "rel", rel, "machine", r.URL.Query().Get("machine"))
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	var req proto.CreateWorkspaceEntryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("工作树新建条目请求体解析失败", "root", root, "rel", rel, "status", http.StatusBadRequest, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	entry, err := CreateEntry(root, rel, req.Name, req.Kind)
	if err != nil {
		s.writeEntryError(w, root, rel, err)
		return
	}
	s.log.Info("工作树新建条目完成", "root", root, "rel", rel, "name", req.Name, "kind", req.Kind)
	writeJSON(w, http.StatusOK, entry)
}

// handleWorkspaceEntryCopy 处理 POST /api/workspaces/entry/copy?path=&rel=[&machine=]。
//
// 在工作树内复制 rel 条目（B107 文件树右键菜单「复制」），副本按
// "foo copy 1" / "foo copy 2" 计数命名，目录连同其内容递归复制。
//
// 参数（查询串）：
//   - path: 工作树绝对路径（必须命中白名单，否则 400）
//   - rel: 待复制条目的相对路径（空串即工作树根，按非法名拒绝）
//   - machine: 可选，转发到指定机器（复用 forwardIfRequested）
//
// 响应：200 返回副本的 proto.DirEntry。
func (s *Server) handleWorkspaceEntryCopy(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	s.log.Info("工作树复制条目请求", "method", r.Method, "path", r.URL.Query().Get("path"), "rel", rel, "machine", r.URL.Query().Get("machine"))
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	entry, err := CopyEntry(root, rel)
	if err != nil {
		s.writeEntryError(w, root, rel, err)
		return
	}
	s.log.Info("工作树复制条目完成", "root", root, "rel", rel, "copy", entry.Name)
	writeJSON(w, http.StatusOK, entry)
}

// handleWorkspaceEntryRename 处理 PATCH /api/workspaces/entry?path=&rel=[&machine=]。
//
// 把工作树内的 rel 条目改名为请求体里的 new_name（B107 文件树右键菜单「重命名」，
// 单层名，本期不做跨目录移动）。
//
// 参数（查询串）：
//   - path: 工作树绝对路径（必须命中白名单，否则 400）
//   - rel: 待改名条目的相对路径
//   - machine: 可选，转发到指定机器（复用 forwardIfRequested）
//
// 请求体：proto.RenameWorkspaceEntryReq。响应：200 返回改名后条目的 proto.DirEntry。
func (s *Server) handleWorkspaceEntryRename(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	s.log.Info("工作树改名条目请求", "method", r.Method, "path", r.URL.Query().Get("path"), "rel", rel, "machine", r.URL.Query().Get("machine"))
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	var req proto.RenameWorkspaceEntryReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("工作树改名条目请求体解析失败", "root", root, "rel", rel, "status", http.StatusBadRequest, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	entry, err := RenameEntry(root, rel, req.NewName)
	if err != nil {
		s.writeEntryError(w, root, rel, err)
		return
	}
	s.log.Info("工作树改名条目完成", "root", root, "rel", rel, "new_name", req.NewName)
	writeJSON(w, http.StatusOK, entry)
}

// handleWorkspaceEntryDelete 处理 DELETE /api/workspaces/entry?path=&rel=[&machine=]。
//
// 删除工作树内的 rel 条目（B107 文件树右键菜单「删除」），目录连同其内容一并删；
// 不做回收站，理由见 DeleteEntry 函数头。
//
// 参数（查询串）：
//   - path: 工作树绝对路径（必须命中白名单，否则 400）
//   - rel: 待删除条目的相对路径（空串即工作树根，按非法名拒绝）
//   - machine: 可选，转发到指定机器（复用 forwardIfRequested）
//
// 响应：200 返回 {"ok": true}。
func (s *Server) handleWorkspaceEntryDelete(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	rel := r.URL.Query().Get("rel")
	s.log.Info("工作树删除条目请求", "method", r.Method, "path", r.URL.Query().Get("path"), "rel", rel, "machine", r.URL.Query().Get("machine"))
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	if err := DeleteEntry(root, rel); err != nil {
		s.writeEntryError(w, root, rel, err)
		return
	}
	s.log.Info("工作树删除条目完成", "root", root, "rel", rel)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleWorkspaceSearch 处理 GET /api/workspaces/search?path=&rel=&q=&limit=[&machine=]。
//
// 在工作树内（默认整棵）按关键词全文搜索命中行（B107 文件树右键菜单「在文件夹
// 内查找」），三条护栏（命中数上限/超时/跳过生成物目录）都在 SearchInDir 内部。
//
// 参数（查询串）：
//   - path: 工作树绝对路径（必须命中白名单，否则 400）
//   - rel: 搜索范围（相对工作树根的目录）；省略或空串表示整棵工作树
//   - q: 关键词（必须非空，否则 400）
//   - limit: 命中数上限；省略取默认，非法数字 400，超出上限收敛
//   - machine: 可选，转发到指定机器（复用 forwardIfRequested）
//
// 响应：200 返回 proto.SearchResult。
func (s *Server) handleWorkspaceSearch(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	q := r.URL.Query().Get("q")
	rel := r.URL.Query().Get("rel")
	s.log.Info("工作树搜索请求", "method", r.Method, "path", r.URL.Query().Get("path"), "rel", rel, "q", q, "machine", r.URL.Query().Get("machine"))
	root, ok := s.workspaceRootOrErr(w, r)
	if !ok {
		return
	}
	if q == "" {
		s.log.Warn("工作树搜索缺关键词", "root", root, "rel", rel, "status", http.StatusBadRequest)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 q 参数"})
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			s.log.Warn("工作树搜索 limit 不是数字", "root", root, "rel", rel, "status", http.StatusBadRequest, "limit", raw)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit 必须是数字"})
			return
		}
		limit = n
	}
	res, err := SearchInDir(r.Context(), root, rel, q, limit)
	if err != nil {
		s.writeEntryError(w, root, rel, err)
		return
	}
	s.log.Info("工作树搜索完成", "root", root, "rel", rel, "q", q, "hits", len(res.Hits), "truncated", res.Truncated)
	writeJSON(w, http.StatusOK, res)
}
