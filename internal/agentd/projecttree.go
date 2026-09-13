// 本文件实现项目树的 gateway 消费面：GET /api/projects/tree 端点入口。
//
// 职责：
//   - handleProjectTree：端点入口，含 ?scope=all 的分流
//
// 边界：
//   - 树构建与跨机汇总是域逻辑，已随 B233.19 迁往 internal/workspace
//     （BuildLocalTree / FanoutProjectTree）；本文件只保留 handler 与两处
//     注入胶水（本机 store 位置表 + target 池适配器）
//   - 不改 B62 的 GET /api/projects：那条端点返回的就是位置表本身，语义本分。
//     项目树是另一种表示（嵌套、带探测、可跨机），塞进同一个端点会让一个端点
//     有两种响应形状，因此另开子路径
//   - spec §5.3 提到的 “projects?scope=all” 落在**本端点**上：§3 明文
//     「W3a 不动 /api/projects 那三条」，扁平端点保持单机
package agentd

import (
	"context"
	"net/http"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/workspace"
)

// buildLocalTree 构建本机项目树，实现见 workspace.BuildLocalTree。
func (s *Server) buildLocalTree(ctx context.Context) (proto.ProjectTreeResp, error) {
	return workspace.BuildLocalTree(ctx, s.st,
		filepath.Join(s.conf().DataDir, "worktrees"), s.log)
}

// buildTreeAll 汇总本机与全部 target 的项目树，实现见 workspace.FanoutProjectTree。
func (s *Server) buildTreeAll(ctx context.Context) proto.ProjectTreeResp {
	return workspace.FanoutProjectTree(ctx, s.buildLocalTree,
		NewProjectTreeSource(s.pool), s.log)
}

// handleProjectTree 处理 GET /api/projects/tree[?scope=all]。
//
// 路由说明：net/http ServeMux 的字面段优先于通配段，本路由与
// DELETE /api/projects/{name} 方法与形态都不冲突；即便将来补
// GET /api/projects/{name}，字面段仍然优先。
func (s *Server) handleProjectTree(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	s.log.Info("项目树请求", "scope", scope, "remote_addr", r.RemoteAddr)
	if scope == "all" && !isForwarded(r) {
		// 带转发头时降级为仅本机（防环优先于范围）
		writeJSON(w, http.StatusOK, s.buildTreeAll(r.Context()))
		return
	}
	tree, err := s.buildLocalTree(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	writeJSON(w, http.StatusOK, tree)
}
