// executor_default.go —— 控制台配置机器级缺省执行者的 HTTP 面（B160）。
//
// 职责：
//   - GET  /api/executor/default   该机的缺省执行者、它的默认模型、可选名单
//   - PUT  /api/executor/default   保存这两项（整体替换）
//
// 边界：
//   - 只碰 config 的 executor 段。approver / proc_fence / listen 等机器级配置
//     一律不给写，理由逐条见 spec §1.2
//   - 跨机由 forwardIfRequested 处理（?machine=），本文件只管本机
//   - **Model 不校验**：agentd 不认识任何执行器的模型名单，没有可判据
//   - 落盘走 swapConf。**不要**为 Executor 补深拷：ExecutorConfig 是两个 string
//     的值类型，结构体浅拷即完整拷贝
package agentd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/proto"
)

// handleExecutorDefaultGet 处理 GET /api/executor/default[?machine=]。
//
// 响应：200 proto.ExecutorDefaultResp / 503 manager 未就绪。
//
// 为什么 manager 未就绪要 503 而不是回一个空名单：空名单会让界面画出一个
// 选无可选的下拉框，用户会以为配置丢了。诚实地说「现在答不上来」更好。
func (s *Server) handleExecutorDefaultGet(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	s.log.Info("缺省执行者查询请求", "method", r.Method, "path", r.URL.Path)
	if s.mgr == nil {
		s.log.Warn("缺省执行者查询：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	resp := s.executorDefaultResp()
	s.log.Info("缺省执行者查询完成",
		"default", resp.Default, "has_model", resp.Model != "", "available", len(resp.Available))
	writeJSON(w, http.StatusOK, resp)
}

// executorDefaultResp 组装当前状态（GET 与 PUT 的成功响应共用，保证两处一致）。
func (s *Server) executorDefaultResp() proto.ExecutorDefaultResp {
	c := s.conf()
	return proto.ExecutorDefaultResp{
		Default:   c.Executor.Default,
		Model:     c.Executor.Model,
		Available: s.mgr.ExecutorNames(), // 已按名字升序（registeredNames）
	}
}

// handleExecutorDefaultPut 处理 PUT /api/executor/default[?machine=]。
//
// 请求体 proto.ExecutorDefaultReq：**整体替换** executor 段的两个字段。
//
// 响应：200 proto.ExecutorDefaultResp（保存后的最新状态，界面直接拿它刷新）
//
//	400 default 为空或未注册
//	503 manager 未就绪
//
// 为什么 default 必须校验：它是 resolveExecutor 的兜底值。写进一个该机没有的
// 名字，此后**每一次**不带 --executor 的派发都会失败——一个下拉框搞挂一台机。
//
// 为什么 model 不校验：agentd 不认识任何执行器的模型名单（模型名按执行器、
// 也按机器不同），没有可判据。它的失败面也小得多：只影响缺省执行者、只影响
// 不带 --model 的派发，且失败是当场的（第一个事件就是 400 或秒退）。
func (s *Server) handleExecutorDefaultPut(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	if s.mgr == nil {
		s.log.Warn("缺省执行者保存：manager 未就绪")
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "manager 未就绪"})
		return
	}
	var req proto.ExecutorDefaultReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("缺省执行者保存：请求体无法解析", "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体不是合法 JSON"})
		return
	}
	// 粘贴模型名时带空格是常事，而带空格的名字会被 provider 当成另一个名字直接 400。
	def := strings.TrimSpace(req.Default)
	model := strings.TrimSpace(req.Model)
	s.log.Info("缺省执行者保存请求", "default", def, "has_model", model != "")

	names := s.mgr.ExecutorNames()
	if def == "" {
		s.log.Warn("缺省执行者保存被拒：为空", "cause", "缺省执行者不能为空")
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("缺省执行者不能为空（可选: %s）", strings.Join(names, ", "))})
		return
	}
	if !containsString(names, def) {
		s.log.Warn("缺省执行者保存被拒：未注册", "default", def, "registered", names,
			"cause", "该机没有这个执行者")
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("未知 executor %q（可选: %s）", def, strings.Join(names, ", "))})
		return
	}

	if err := s.swapConf(func(c *config.Config) error {
		// ExecutorConfig 是值类型，整体赋值即完整替换——不需要也不该补深拷。
		c.Executor = config.ExecutorConfig{Default: def, Model: model}
		return nil
	}); err != nil {
		s.log.Error("缺省执行者落盘失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Info("缺省执行者已保存", "default", def, "has_model", model != "")
	writeJSON(w, http.StatusOK, s.executorDefaultResp())
}

// containsString 判断名单里有没有某个名字（名单只有个位数长度，线性扫足够）。
func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}
