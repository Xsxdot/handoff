// 本文件实现薄壳状态的中转：PUT/GET /api/desktop/state。
//
// 职责：把薄壳上报的自身状态（版本 + 本次开机同步的结论）在内存里持有一小段
// 时间，供控制台读取。
//
// 边界（承重）：
//   - 只在内存，不落盘。壳没在跑就等于没有壳。落盘会让「上次开过桌面端」的
//     痕迹在纯浏览器会话里伪装成「现在有个壳」，渲染出一个点了没反应的按钮。
//   - 不解释内容。SyncPlan 的取值语义属于薄壳，这里不做校验、不做映射。
//   - 不含反向通道：薄壳只上报、不接指令（spec §5）。
package agentd

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// desktopStateTTL 是薄壳状态的有效期。
//
// 取 30s = 薄壳上报间隔（10s）的三倍：容得下两次丢包，又不会在薄壳退出后
// 让控制台继续显示半分钟以上的幻影。
const desktopStateTTL = 30 * time.Second

// handleDesktopStatePut 接收薄壳自身状态并刷新内存中的 TTL。
//
// 参数：w/r 是标准 HTTP 响应与请求；请求体必须是 proto.DesktopState JSON。
// 返回：成功 200 空体；JSON 无法解码时返回 400 与 error JSON。
// 注意：状态不落盘，也不校验 SyncPlan，薄壳负责解释自己的同步结论。
func (s *Server) handleDesktopStatePut(w http.ResponseWriter, r *http.Request) {
	var st proto.DesktopState
	if err := json.NewDecoder(r.Body).Decode(&st); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	now := time.Now()
	if s.desktopNow != nil {
		now = s.desktopNow()
	}
	s.desktopMu.Lock()
	s.desktopState = &st
	s.desktopAt = now
	s.desktopMu.Unlock()
	s.log.Info("薄壳状态已上报", "app_version", st.AppVersion, "sync_plan", st.SyncPlan, "busy", st.SyncBusy)
	w.WriteHeader(http.StatusOK)
}

// handleDesktopStateGet 返回仍在 TTL 内的薄壳状态。
//
// 参数：w/r 是标准 HTTP 响应与请求。
// 返回：有状态时 200 + DesktopState；无状态或已过期时 204 空体。
// 注意：复制状态后再解锁，避免把受保护的指针交给 JSON 编码器。
func (s *Server) handleDesktopStateGet(w http.ResponseWriter, r *http.Request) {
	now := time.Now()
	if s.desktopNow != nil {
		now = s.desktopNow()
	}
	s.desktopMu.Lock()
	st := s.desktopState
	at := s.desktopAt
	if st == nil || now.Sub(at) >= desktopStateTTL {
		s.desktopMu.Unlock()
		s.log.Debug("薄壳状态缺席或已过期")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	copyState := *st
	s.desktopMu.Unlock()
	writeJSON(w, http.StatusOK, copyState)
}
