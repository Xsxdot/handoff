// discipline.go —— 控制台的纪律配置 HTTP 面（B157）。
//
// 职责：
//   - GET  /api/discipline            列出内置两版、该机纪律块文件、每个 executor 的档位
//   - GET  /api/discipline/file       读单个纪律块文件正文
//   - PUT  /api/discipline/file       写单个纪律块文件（带前置哈希）
//   - PUT  /api/discipline/mapping    整段替换该机的 discipline 配置段
//
// 边界：
//   - 文件判断力全在 internal/discipline（名字校验、大小上限、冲突判定），
//     本层只做 HTTP 编解码与错误映射，**中文错误原文原样透传**
//   - 跨机由 forwardIfRequested 处理（?machine=），本文件只管本机
//   - 不理解纪律内容；不碰任务与派发
package agentd

import (
	"net/http"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/discipline"
	"github.com/Xsxdot/handoff/internal/proto"
)

// handleDisciplineGet 处理 GET /api/discipline[?machine=]。
//
// 响应：
//   - 200 proto.DisciplineResp
//   - 503：manager 未就绪（与 dispatch 等路由同款：executor 名单来自 manager）
func (s *Server) handleDisciplineGet(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	s.log.Info("纪律配置查询请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Warn("纪律配置查询：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	dir := discipline.Dir(s.conf().DataDir)
	files, err := discipline.List(dir)
	if err != nil {
		s.log.Error("纪律配置查询：列举文件失败", "dir", dir, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := proto.DisciplineResp{
		Dir:      dir,
		Builtins: make([]proto.DisciplineBuiltin, 0, 2),
		Files:    make([]proto.DisciplineFile, 0, len(files)),
		Bindings: s.disciplineBindings(),
	}
	for _, b := range discipline.Builtins() {
		resp.Builtins = append(resp.Builtins, proto.DisciplineBuiltin{Tier: b.Tier, Content: b.Content})
	}
	for _, f := range files {
		resp.Files = append(resp.Files, proto.DisciplineFile{Name: f.Name, Size: f.Size, SHA256: f.SHA256})
	}
	s.log.Info("纪律配置查询完成", "dir", dir,
		"files", len(resp.Files), "bindings", len(resp.Bindings))
	writeJSON(w, http.StatusOK, resp)
}

// disciplineBindings 把「已注册的 executor ∪ 配置里已出现的键」折成档位列表，按名字升序。
//
// 三档映射：键不存在 → default；值为空串 → off；否则 → file。
func (s *Server) disciplineBindings() []proto.DisciplineBinding {
	m := s.conf().Discipline
	seen := map[string]bool{}
	names := []string{}
	for _, n := range s.mgr.ExecutorNames() {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for n := range m {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	sort.Strings(names)

	out := make([]proto.DisciplineBinding, 0, len(names))
	for _, n := range names {
		b := proto.DisciplineBinding{Executor: n, DefaultTier: discipline.DefaultTierFor(n)}
		v, configured := m[n]
		switch {
		case !configured:
			b.Mode = proto.DisciplineModeDefault
		case strings.TrimSpace(v) == "":
			b.Mode = proto.DisciplineModeOff
		default:
			b.Mode, b.File = proto.DisciplineModeFile, strings.TrimSpace(v)
		}
		out = append(out, b)
	}
	return out
}
