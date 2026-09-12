// gc.go —— 机器级 handoff gc 的 HTTP 接缝。
//
// 职责：GET /api/gc 只预览，POST /api/gc 才执行；两条路由继续走 Server.Handler 的 auth。
//
// 边界：编排实现在 internal/orchestration（B233.13 迁出）；本文件只做 HTTP 解码、
// 未就绪判据与响应写回。
package agentd

import (
	"encoding/json"
	"net/http"

	"github.com/Xsxdot/handoff/internal/proto"
)

// handleGC 处理 GET/POST /api/gc。GET 仅预览，POST 才执行；两条路由都受 Server.Handler 的 auth 包裹。
// POST 解码失败时 force=false。
func (s *Server) handleGC(w http.ResponseWriter, r *http.Request) {
	force := r.URL.Query().Get("force") == "true"
	if r.Method == http.MethodPost {
		var req proto.GCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			s.log.Warn("gc 请求体解码失败，按默认 force=false 处理", "cause", err)
		}
		force = req.Force
	}
	execute := r.Method == http.MethodPost
	s.log.Info("gc HTTP 进入", "method", r.Method, "path", r.URL.Path, "force", force, "execute", execute)
	if s.mgr == nil {
		s.log.Warn("gc 请求到达但 manager 未注入", "method", r.Method, "path", r.URL.Path)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp, err := s.mgr.GC(r.Context(), force, execute)
	if err != nil {
		s.log.Error("gc 请求失败", "method", r.Method, "force", force, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	s.log.Info("gc HTTP 完成", "method", r.Method, "force", force, "execute", execute,
		"scanned", resp.Scanned, "failures", resp.Failures)
	writeJSON(w, http.StatusOK, resp)
}
