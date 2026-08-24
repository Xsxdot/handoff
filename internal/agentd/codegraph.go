// 本文件实现代码图的两个只读端点：整图数据与按 file:line 的源码窗口。
//
// 职责：
//   - handleProjectCodegraph: 一次性返回基线 + 全部视图 diff + 保鲜报告，
//     合并渲染在前端做（数据契约见 spec 2026-08-19-codegraph-design §3）
//   - handleProjectCodegraphSource: 详情面板「源码」区按 file:line 实时读
//
// 边界：
//   - 只读，不触发扫描、不写任何文件
//   - source 的路径校验是参数校验不是安全边界（同 workspacefiles.go 的论证）：
//     挡打错的路径，不防有心人——控制台会话本就等价主令牌
package agentd

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Xsxdot/charter/graph/codegraph"
	"github.com/Xsxdot/handoff/internal/store"
)

// handleProjectCodegraph 处理 GET /api/projects/{name}/codegraph[?machine=]。
func (s *Server) handleProjectCodegraph(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.PathValue("name")
	s.log.Info("代码图请求", "name", name, "machine", r.URL.Query().Get("machine"))
	loc, err := s.st.GetProjectLocationByName(name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("代码图被拒：项目不存在", "name", name, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未登记"})
			return
		}
		s.log.Error("代码图失败：查询位置表", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	g, err := codegraph.LoadGraph(loc.Path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || strings.Contains(err.Error(), "no such file") {
			s.log.Warn("代码图缺失", "name", name, "repo", loc.Path, "cause", err)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未生成代码图（无 codegraph/baseline.json）"})
			return
		}
		s.log.Error("代码图加载失败", "name", name, "repo", loc.Path, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	names, err := codegraph.ListViews(loc.Path)
	if err != nil {
		s.log.Error("代码图列视图失败", "name", name, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": truncateRunes(err.Error(), 200)})
		return
	}
	views := map[string]*codegraph.Diff{}
	for _, vn := range names {
		d, err := codegraph.LoadDiff(loc.Path, vn)
		if err != nil {
			// 单个坏视图不拖垮整页：跳过并告警，前端照常渲染其余视图
			s.log.Warn("代码图视图解析失败，跳过", "name", name, "view", vn, "cause", err)
			continue
		}
		views[vn] = d
	}
	stale := codegraph.CheckStale(loc.Path, g)
	if stale == nil {
		stale = []codegraph.StaleNode{}
	}
	response := map[string]any{"baseline": g, "views": views, "stale": stale}
	best, err := codegraph.LoadBest(loc.Path)
	if err != nil {
		s.log.Warn("代码图最优图加载失败，跳过对照数据", "name", name, "repo", loc.Path, "cause", err)
	} else if best != nil {
		response["best"] = best
		target, err := codegraph.LoadTarget(loc.Path)
		if err != nil {
			s.log.Warn("代码图目标图加载失败，跳过目标与报告", "name", name, "repo", loc.Path, "cause", err)
		} else {
			response["target"] = target
			decls, err := codegraph.LoadDomainDecls(loc.Path)
			if err != nil {
				s.log.Warn("代码图领域声明加载失败，跳过报告", "name", name, "repo", loc.Path, "cause", err)
			} else {
				report := codegraph.Check(target, best, codegraph.Merge(g, nil), decls)
				if report.Fails == nil {
					report.Fails = []codegraph.Finding{}
				}
				if report.Warns == nil {
					report.Warns = []codegraph.Finding{}
				}
				response["report"] = report
			}
		}
	}
	s.log.Info("代码图完成", "name", name, "nodes", len(g.Nodes),
		"edges", len(g.Edges), "domains", len(g.Domains), "views", len(views), "stale", len(stale))
	writeJSON(w, http.StatusOK, response)
}

// handleProjectCodegraphSource 处理 GET /api/projects/{name}/codegraph/source。
// 窗口规则：from = max(1, line-3)，取 span 行（默认 40，上限 200）——函数定义行
// 上方带 3 行上下文，详情面板不用再拼请求。行号越界不报错，截到文件边界。
func (s *Server) handleProjectCodegraphSource(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	name := r.PathValue("name")
	file := r.URL.Query().Get("file")
	line, _ := strconv.Atoi(r.URL.Query().Get("line"))
	span, _ := strconv.Atoi(r.URL.Query().Get("span"))
	if span <= 0 {
		span = 40
	}
	if span > 200 {
		span = 200
	}
	s.log.Info("代码图源码请求", "name", name, "file", file, "line", line, "span", span)
	clean := filepath.Clean(file)
	if file == "" || filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		s.log.Warn("代码图源码被拒：路径逃逸", "name", name, "file", file, "cause", "路径不是仓库内相对路径")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "file 必须是仓库内相对路径"})
		return
	}
	loc, err := s.st.GetProjectLocationByName(name)
	if err != nil {
		s.log.Warn("代码图源码被拒：项目不存在", "name", name, "cause", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "项目 " + name + " 未登记"})
		return
	}
	raw, err := os.ReadFile(filepath.Join(loc.Path, clean))
	if err != nil {
		s.log.Warn("代码图源码读取失败", "name", name, "file", clean, "cause", err)
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "读取 " + clean + " 失败: " + truncateRunes(err.Error(), 120)})
		return
	}
	lines := strings.Split(string(raw), "\n")
	from := line - 3
	if from < 1 {
		from = 1
	}
	to := from + span
	if to > len(lines)+1 {
		to = len(lines) + 1
	}
	if from > len(lines) {
		from = len(lines)
	}
	s.log.Info("代码图源码完成", "name", name, "file", clean, "from", from, "count", to-from)
	writeJSON(w, http.StatusOK, map[string]any{"file": clean, "from": from, "lines": lines[from-1 : to-1]})
}
