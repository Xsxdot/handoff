// 本文件实现「按任务 id 的透明路由」：任务 id 是 UUID、全网唯一，因此
// /api/tasks/{id}/... 不需要调用方指定机器。
//
// 判定顺序（§5.1）：
//  1. 本机 tasks 表有该 id → 本机处理（现状不变）
//  2. 否则查镜像索引 mirror_tasks 得所属机器 → 原样转发
//  3. 两处都没有 → 交给本机处理，由它给出与今天一致的 404
//
// 边界：
//   - 不改任何被包住的 handler 的行为
//   - 带 X-Handoff-Forwarded 的请求一律本机处理（防环优先于路由）
//   - 不缓存判定结果：一次本机主键查询的成本远低于「任务刚归档但缓存说它在
//     远端」这类失真
package agentd

import (
	"errors"
	"net/http"

	"github.com/Xsxdot/handoff/internal/store"
)

// byTask 包住 /api/tasks/{id}/... 系列 handler，按任务归属决定本机处理还是转发。
//
// 注意：render 是流式响应，也走同一条搬运——forwardTo 用 io.Copy 直通，
// 客户端断开时 r.Context() 取消、上游连接随之断开，无需特殊处理。
func (s *Server) byTask(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" || isForwarded(r) {
			next(w, r)
			return
		}
		if _, err := s.st.GetTask(id); err == nil {
			next(w, r) // 本机的活
			return
		} else if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("任务路由：查本机任务失败", "task", id, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
			return
		}
		target, ok, err := s.st.MirrorTaskTarget(id)
		if err != nil {
			s.log.Error("任务路由：查镜像索引失败", "task", id, "cause", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
			return
		}
		if !ok {
			next(w, r) // 两处都没有：让 handler 给出与今天一致的 404
			return
		}
		t, defined := s.conf().Targets[target]
		if !defined {
			// 镜像里记着一台配置里已经没有的机器：如实报告，别假装 404
			s.log.Warn("任务路由：镜像指向的机器已不在配置中",
				"task", id, "machine", target)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "任务在机器 " + target + " 上，但它已不在本机配置的 targets 中"})
			return
		}
		// relay 机器没有 addr，传输必须复用池里的选路结果（与各扇出同一条纪律）
		c, err := s.pool.For(target)
		if err != nil {
			s.log.Error("任务路由：取目标客户端失败", "task", id, "machine", target, "cause", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "转发到 " + target + " 失败: " + err.Error()})
			return
		}
		s.log.Info("任务路由：转发到远端", "task", id, "machine", target,
			"method", r.Method, "path", r.URL.Path)
		s.forwardTo(w, r, target, c, t.Token)
	}
}
