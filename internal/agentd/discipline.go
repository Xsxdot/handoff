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
	"encoding/json"
	"errors"
	"io/fs"
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

// handleDisciplineFileRead 处理 GET /api/discipline/file?name=[&machine=]。
//
// 响应：200 proto.FileRead / 400 名字非法 / 404 文件不存在。
// 注意：内置两版**不走这条**——它们的全文已随 GET /api/discipline 一并交出。
func (s *Server) handleDisciplineFileRead(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := discipline.Dir(s.conf().DataDir)
	s.log.Info("纪律块读文件请求", "dir", dir, "name", name)

	content, sha, size, err := discipline.Read(dir, name)
	if err != nil {
		switch {
		case errors.Is(err, discipline.ErrBadName):
			s.log.Warn("纪律块读文件被拒：名字非法", "name", name, "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("纪律块读文件：目标不存在", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "纪律块文件不存在"})
		default:
			s.log.Error("纪律块读文件失败", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取纪律块文件失败"})
		}
		return
	}
	s.log.Info("纪律块读文件完成", "dir", dir, "name", name, "bytes", size, "sha256", shortHash(sha))
	writeJSON(w, http.StatusOK, proto.FileRead{Content: content, Size: size, SHA256: sha})
}

// handleDisciplineFileWrite 处理 PUT /api/discipline/file?name=[&machine=]。
//
// 请求体 proto.FileWriteReq：base_sha256 为空串 = 新建（目标必须不存在）。
//
// 响应：200 FileWriteResp / 400 名字非法或超限 / 409 撞名或冲突（带磁盘现状）。
//
// 注意：**中文错误原文原样透传**，不吞成「操作失败」——用户看到「不能含路径
// 分隔符：只支持 …/discipline 下的纯文件名」能立刻改对（沿工作树写文件的纪律）。
func (s *Server) handleDisciplineFileWrite(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.URL.Query().Get("name")
	dir := discipline.Dir(s.conf().DataDir)

	var req proto.FileWriteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("纪律块写文件请求体解析失败", "name", name, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 正文可能有几十 KB，日志只记长度不记内容。
	s.log.Info("纪律块写文件请求", "dir", dir, "name", name,
		"bytes", len(req.Content), "base", shortHash(req.BaseSHA256))

	sha, size, err := discipline.Write(dir, name, req.Content, req.BaseSHA256)
	if err != nil {
		switch {
		case errors.Is(err, discipline.ErrBadName), errors.Is(err, discipline.ErrTooLarge):
			s.log.Warn("纪律块写文件被拒", "name", name, "cause", err)
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, discipline.ErrExists):
			s.log.Warn("纪律块写文件被拒：撞名", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		case errors.Is(err, discipline.ErrBaseMismatch):
			// 409 的 body 带磁盘现状：界面据此提供「重新加载」，绝不静默覆盖。
			cur, curSHA, curSize, rerr := discipline.Read(dir, name)
			if rerr != nil {
				s.log.Error("纪律块写文件冲突后读现状失败", "name", name, "cause", rerr)
				writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
				return
			}
			s.log.Warn("纪律块写文件冲突", "dir", dir, "name", name,
				"base", shortHash(req.BaseSHA256), "current", shortHash(curSHA), "cause", err)
			writeJSON(w, http.StatusConflict, proto.FileConflictResp{
				Error:   "纪律块文件已被改动",
				Current: proto.FileRead{Content: cur, Size: curSize, SHA256: curSHA},
			})
		case errors.Is(err, fs.ErrNotExist):
			s.log.Warn("纪律块写文件：目标在编辑期间被删", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "纪律块文件不存在"})
		default:
			s.log.Error("纪律块写文件失败", "dir", dir, "name", name, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "写入纪律块文件失败"})
		}
		return
	}
	s.log.Info("纪律块写文件完成", "dir", dir, "name", name, "bytes", size, "sha256", shortHash(sha))
	writeJSON(w, http.StatusOK, proto.FileWriteResp{SHA256: sha, Size: size})
}
