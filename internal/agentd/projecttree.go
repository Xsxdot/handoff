// 本文件实现项目树：把扁平的位置表折成 project → location → workspace 三层，
// 并对每个 location 现场探测工作树。
//
// 职责：
//   - buildLocalTree：本机那一棵（GET /api/projects/tree）
//   - handleProjectTree：端点入口，含 ?scope=all 的分流（汇总实现见 projectfanout.go）
//
// 边界：
//   - 不改 B62 的 GET /api/projects：那条端点返回的就是位置表本身，语义本分。
//     项目树是另一种表示（嵌套、带探测、可跨机），塞进同一个端点会让一个端点
//     有两种响应形状，因此另开子路径
//   - spec §5.3 提到的 “projects?scope=all” 落在**本端点**上：§3 明文
//     「W3a 不动 /api/projects 那三条」，扁平端点保持单机
//   - 不写库：树是读出来的，一个字节都不落盘
package agentd

import (
	"context"
	"net/http"
	"path/filepath"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// buildLocalTree 构建本机项目树。
//
// 返回：
//   - 项目树（Projects/Unowned 永不为 nil）
//   - 错误：只有「位置表都读不出来」才算错误；单个 location 探测失败是数据
//
// 注意：
//   - 同一 project_id 的多行会被合并到一个 ProjectNode 下。本机理论上不可能
//     出现（project_id 是主键），但代码按可能处理——真出现了就是库被手改过，
//     此时合并展示比崩掉强
func (s *Server) buildLocalTree(ctx context.Context) (proto.ProjectTreeResp, error) {
	start := time.Now()
	locs, err := s.st.ListProjectLocations()
	if err != nil {
		s.log.Error("项目树：查询位置表失败", "cause", err)
		return proto.ProjectTreeResp{}, err
	}
	managedRoot := filepath.Join(s.cfg.DataDir, "worktrees")

	resp := proto.ProjectTreeResp{Projects: []proto.ProjectNode{}, Unowned: []string{}}
	byID := map[string]int{} // project_id → resp.Projects 下标
	broken := 0
	for _, l := range locs {
		if l.ProjectID == "" {
			// 算不出 project_id 的脏行：诚实列出，不吞、也不塞进某个项目里
			resp.Unowned = append(resp.Unowned, l.Name)
			continue
		}
		ws, probeErr := probeWorkspaces(ctx, l.Path, managedRoot)
		if probeErr != "" {
			broken++
		}
		node := proto.ProjectLocationNode{
			Machine:    "", // 本机恒空串，与 tasks.target 的空串语义一致
			Name:       l.Name,
			Path:       l.Path,
			Workspaces: ws,
			ProbeError: probeErr,
		}
		if i, ok := byID[l.ProjectID]; ok {
			resp.Projects[i].Locations = append(resp.Projects[i].Locations, node)
			continue
		}
		byID[l.ProjectID] = len(resp.Projects)
		resp.Projects = append(resp.Projects, proto.ProjectNode{
			ProjectID: l.ProjectID,
			OriginURL: l.OriginURL,
			Name:      l.Name, // 取该项目下首条登记的 name
			Locations: []proto.ProjectLocationNode{node},
		})
	}
	s.log.Info("项目树构建完成", "projects", len(resp.Projects),
		"locations", len(locs), "broken", broken, "unowned", len(resp.Unowned),
		"elapsed_ms", time.Since(start).Milliseconds())
	return resp, nil
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
