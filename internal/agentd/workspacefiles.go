// 本文件实现「按工作树寻址」的两个只读文件接口：目录列举与读文件。
//
// 职责：
//   - resolveWorkspace：白名单闸门——只有 GET /api/projects/tree 探测得出的
//     工作树路径才被接受
//   - handleWorkspaceDir / handleWorkspaceFile：两个端点的 HTTP 层
//
// 边界：
//   - 不写文件、不建目录、不删任何东西：本期是只读的（spec §7.3）
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
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"

	"github.com/xushixin/handoff/internal/proto"
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
	managedRoot := filepath.Join(s.cfg.DataDir, "worktrees")
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
		// 403 而不是 404：路径存在与否不该从状态码泄露出去，而「你没有权限
		// 浏览这个目录」正是真实原因
		s.log.Warn("工作树白名单拒绝", "path", path, "url_path", r.URL.Path)
		writeJSON(w, http.StatusForbidden, map[string]string{
			"error": "路径不是本机已探测到的工作树，拒绝访问"})
		return "", false
	}
	return root, true
}

// handleWorkspaceDir 处理 GET /api/workspaces/dir?path=&rel=[&machine=]。
//
// 参数：
//   - path: 工作树绝对路径（必须，且必须命中白名单，否则 403）
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
