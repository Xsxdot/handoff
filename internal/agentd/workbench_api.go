// workbench_api.go —— 控制台工作台状态的 HTTP 面（2026-08-20 状态同步 spec §4.2）。
//
// 职责：
//   - GET  /api/workbench/state          一次读出全部基准行与两个单例
//   - PUT  /api/workbench/state/base     写/删一行基准状态
//   - PUT  /api/workbench/state/selected 写当前选中目录
//   - PUT  /api/workbench/state/dock     写/清空悬浮窗现场
//
// 边界：
//   - **不解释 payload**：它是前端序列化好的 JSON 字符串，本层只做长度校验。
//     后端不认识什么叫「分屏」，布局形状改了这里一行都不用动（spec §3）
//   - **不接 forwardIfRequested**：工作台状态是协调者本机的东西，不按 ?machine= 转发。
//     布局里可以有远程机器的目录，但那只是 payload 里的一个字段
//   - 不做鉴权：走 /api 既有的那一套
package agentd

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// maxWorkbenchPayload 是单个 payload 的字节上限。
//
// 这不是防攻击——控制台会话在能力上本就等价于主令牌（POST /api/tasks/{id}/run 就是 sh -c）。
// 它防的是前端 bug：万一哪天有人把文件草稿塞进 TabContent，希望它当场 400，
// 而不是把库悄悄撑大到几百 MB。正常一行布局是 1–2 KiB，256 KiB 有两个数量级余量。
const maxWorkbenchPayload = 256 * 1024

// nowMilli 返回当前毫秒时间戳。单独抽出来是为了让将来注入假时钟只改一处。
func nowMilli() int64 { return time.Now().UnixMilli() }

// handleWorkbenchStateGet 处理 GET /api/workbench/state。
//
// 响应：200 proto.WorkbenchStateResp / 500 读库失败。
//
// 注意：Selected 与 Dock 缺席时返回**空串**而不是缺键——两者都是「当前没有」
// 这个明确结论，缺键会让前端分不清它和「这版服务端还不认识这个字段」。
// Bases 恒为数组（可能为空），不返回 null，省掉前端一条判空分支。
func (s *Server) handleWorkbenchStateGet(w http.ResponseWriter, r *http.Request) {
	bases, singles, err := s.st.ListWorkbench()
	if err != nil {
		s.log.Error("读取工作台状态失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	resp := proto.WorkbenchStateResp{
		Selected: singles[store.WorkbenchKeySelected],
		Dock:     singles[store.WorkbenchKeyDock],
		Bases:    bases,
	}
	payloadBytes := len(resp.Selected) + len(resp.Dock)
	for _, base := range resp.Bases {
		payloadBytes += len(base.Payload)
	}
	s.log.Debug("工作台状态查询完成",
		"key", "state", "bases", len(resp.Bases), "payload_bytes", payloadBytes)
	writeJSON(w, http.StatusOK, resp)
}

// handleWorkbenchBasePut 处理 PUT /api/workbench/state/base。
//
// 请求体 proto.WorkbenchBaseReq：Payload 取 null 表示删除该行。
// 响应：200 空对象 / 400 参数错（坏 JSON、空 base_key、payload 超长）/ 500 写库失败。
func (s *Server) handleWorkbenchBasePut(w http.ResponseWriter, r *http.Request) {
	var req proto.WorkbenchBaseReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("工作台基准行写入：请求体无法解码", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.BaseKey == "" {
		s.log.Warn("工作台基准行写入：base_key 为空")
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_key 不能为空"})
		return
	}
	if req.Payload == nil {
		if err := s.st.DeleteWorkbenchBase(req.BaseKey); err != nil {
			s.log.Error("删除工作台基准行失败", "base_key", req.BaseKey, "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.log.Debug("工作台基准行已删除", "key", req.BaseKey, "payload_bytes", 0)
		writeJSON(w, http.StatusOK, map[string]string{})
		return
	}
	if len(*req.Payload) > maxWorkbenchPayload {
		s.log.Warn("工作台基准行写入：payload 超长",
			"base_key", req.BaseKey, "bytes", len(*req.Payload), "limit", maxWorkbenchPayload)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload 超过长度上限"})
		return
	}
	if err := s.st.PutWorkbenchBase(req.BaseKey, *req.Payload, nowMilli()); err != nil {
		s.log.Error("写工作台基准行失败", "base_key", req.BaseKey, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Debug("工作台基准行已写入", "base_key", req.BaseKey, "bytes", len(*req.Payload))
	writeJSON(w, http.StatusOK, map[string]string{})
}

// handleWorkbenchSelectedPut 处理 PUT /api/workbench/state/selected。
//
// 请求体 proto.WorkbenchSelectedReq。BaseKey 为空串是**合法状态**（当前没选中任何目录），
// 落库成空串而不是删行——删行与存空串在读取端等价，但存空串让「用户确实取消了选中」
// 与「从来没写过」在库里可区分，排障时有用。
//
// 响应：200 空对象 / 400 坏 JSON / 500 写库失败。
func (s *Server) handleWorkbenchSelectedPut(w http.ResponseWriter, r *http.Request) {
	var req proto.WorkbenchSelectedReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("选中目录写入：请求体无法解码", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if len(req.BaseKey) > maxWorkbenchPayload {
		s.log.Warn("选中目录写入：base_key 超长", "bytes", len(req.BaseKey))
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "base_key 超过长度上限"})
		return
	}
	if err := s.st.PutWorkbenchSingleton(store.WorkbenchKeySelected, req.BaseKey, nowMilli()); err != nil {
		s.log.Error("写选中目录失败", "base_key", req.BaseKey, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Debug("选中目录已写入", "key", store.WorkbenchKeySelected, "base_key", req.BaseKey,
		"payload_bytes", len(req.BaseKey))
	writeJSON(w, http.StatusOK, map[string]string{})
}

// handleWorkbenchDockPut 处理 PUT /api/workbench/state/dock。
//
// 请求体 proto.WorkbenchDockReq：Payload 取 null 表示清空悬浮窗现场。
// 响应：200 空对象 / 400 参数错 / 500 写库失败。
func (s *Server) handleWorkbenchDockPut(w http.ResponseWriter, r *http.Request) {
	var req proto.WorkbenchDockReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("悬浮窗现场写入：请求体无法解码", "err", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.Payload == nil {
		if err := s.st.DeleteWorkbenchSingleton(store.WorkbenchKeyDock); err != nil {
			s.log.Error("清空悬浮窗现场失败", "err", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		s.log.Debug("悬浮窗现场已清空", "key", store.WorkbenchKeyDock, "payload_bytes", 0)
		writeJSON(w, http.StatusOK, map[string]string{})
		return
	}
	if len(*req.Payload) > maxWorkbenchPayload {
		s.log.Warn("悬浮窗现场写入：payload 超长", "bytes", len(*req.Payload), "limit", maxWorkbenchPayload)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "payload 超过长度上限"})
		return
	}
	if err := s.st.PutWorkbenchSingleton(store.WorkbenchKeyDock, *req.Payload, nowMilli()); err != nil {
		s.log.Error("写悬浮窗现场失败", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	s.log.Debug("悬浮窗现场已写入", "key", store.WorkbenchKeyDock,
		"payload_bytes", len(*req.Payload))
	writeJSON(w, http.StatusOK, map[string]string{})
}
