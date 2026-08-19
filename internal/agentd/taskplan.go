// 本文件实现 GET /api/tasks/{id}/plan：交出派发当刻给 executor 的指令原文。
//
// 为什么需要它：派发内容此前只以两种形态存在——磁盘上任务目录里的那份文件，
// 和库里截断过的 plan_summary。控制台因此答不出「这个任务当初被要求做什么」，
// 而这恰恰是审阅一个回合时最先要对照的东西。
//
// 边界：
//   - 只读**归档在任务目录里的那一份**（Task.PlanPath），不读仓库里的原始 plan
//     文件：后者可能已被改写或删除，而审阅要对照的是「当时发下去的那份」
//   - 不做渲染、不切段：原文进原文出，markdown 怎么显示是前端的事
//   - 跨机由 byTask 中间件透明转发（路由注册在 byTask 之下），本函数只管本机
package agentd

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// maxPlanBytes 是响应里 content 的上限（256 KiB）。
//
// 超过就截断并置 Truncated：plan 是人写的指令，正常在几十 KB 以内；真有人派发
// 一个几 MB 的文件时，把它整个塞进一次 JSON 会让控制台卡住，而看开头 256 KiB
// 已经足够回答「这任务被要求做什么」。
const maxPlanBytes = 256 << 10

// handleTaskPlan 处理 GET /api/tasks/{id}/plan。
//
// 响应：
//   - 200 proto.TaskPlan：指令原文（超过 maxPlanBytes 时截断并置 truncated）
//   - 404：任务不存在 / 该任务没有归档指令（老任务）/ 归档文件已被删除
//
// 注意：文件缺失返回 404 而不是 200 空串——「没有这份东西」与「这份东西是空的」
// 对界面是两件事，后者会被画成一个空气泡。
func (s *Server) handleTaskPlan(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	s.log.Info("派发指令请求", "method", r.Method, "path", r.URL.Path, "task", taskID)

	task, err := s.st.GetTask(taskID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("派发指令请求：任务不存在", "task", taskID)
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "任务不存在"})
			return
		}
		s.log.Error("派发指令请求：查任务失败", "task", taskID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	if task.PlanPath == "" {
		s.log.Warn("派发指令请求：任务没有归档指令", "task", taskID)
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "该任务没有归档派发指令（派发于本功能上线之前）"})
		return
	}

	fi, err := os.Stat(task.PlanPath)
	if err != nil {
		// 文件被人删了 / 数据目录搬过家：如实说是哪条路径没了，别缩略成「内部错误」
		s.log.Warn("派发指令请求：归档文件不可读", "task", taskID, "path", task.PlanPath, "cause", err)
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "派发指令文件已不存在：" + task.PlanPath})
		return
	}
	data, err := os.ReadFile(task.PlanPath)
	if err != nil {
		s.log.Error("派发指令请求：读取失败", "task", taskID, "path", task.PlanPath, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取派发指令失败"})
		return
	}
	truncated := false
	if len(data) > maxPlanBytes {
		data = data[:maxPlanBytes]
		truncated = true
	}
	s.log.Info("派发指令完成", "task", taskID, "name", filepath.Base(task.PlanPath),
		"size", fi.Size(), "truncated", truncated)
	writeJSON(w, http.StatusOK, proto.TaskPlan{
		Name:      filepath.Base(task.PlanPath),
		Content:   string(data),
		Size:      fi.Size(),
		Truncated: truncated,
	})
}
