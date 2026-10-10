// pushapi.go —— iOS 推送设备登记 REST（B432 spec §契约面 1）。
//
// 职责：POST /api/push/devices 登记、DELETE /api/push/devices 注销；成员由
// requireConsoleIdentity 服务端注入（决策 D3），请求体不携带 member。
//
// 边界：
//   - 只回「已登记」，绝不回「已送达」——APNs 是否真收到由 fanout 投递侧
//     决定（410 → 删设备），登记接口不校验 token 真伪（spec 验收③ 防假送达）
//   - platform 白名单仅 ios（本 MVP 唯一平台；安卓/厂商通道不进本卡）
//   - 日志只落 device_id/member，禁止落 apns_token 明文
//   - 登记本身不触发推送；事件→通知的扇出在 pushfanout.go
package agentd

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/store"
)

// registerPushRoutes 注册推送设备登记端点（spec §契约面 1）。
// 两者都走 s.auth（Bearer 主令牌，iOS 壳经 Go 核持有）+ requireConsoleIdentity。
func (s *Server) registerPushRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/push/devices", s.handlePushDeviceRegister)
	api.HandleFunc("DELETE /api/push/devices", s.handlePushDeviceDelete)
}

// handlePushDeviceRegister POST /api/push/devices → 登记或覆盖一台设备。
//
// 入参：proto.PushDeviceRegisterReq（device_id/platform/apns_token 必填，
// auth_session_id 可选）。member 服务端注入（决策 D3）。
// 出参：200 {"ok":true} / 400 入参不合 / 403 未配 console_user / 500 写库失败。
func (s *Server) handlePushDeviceRegister(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	var req proto.PushDeviceRegisterReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("推送设备登记请求体解析失败", "method", r.Method, "path", r.URL.Path, "cause", err)
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	req.APNSToken = strings.TrimSpace(req.APNSToken)
	s.log.Debug("推送设备登记请求", "method", r.Method, "path", r.URL.Path,
		"member", id.Member, "device", req.DeviceID, "platform", req.Platform)
	if req.DeviceID == "" || req.APNSToken == "" || req.Platform != proto.PushPlatformIOS {
		writeErr(w, http.StatusBadRequest,
			errors.New("device_id/apns_token 必填，platform 必须为 ios"))
		return
	}
	dev := proto.PushDevice{
		Member: id.Member, DeviceID: req.DeviceID, Platform: req.Platform,
		APNSToken: req.APNSToken, AuthSessionID: req.AuthSessionID, UpdatedAt: time.Now(),
	}
	if err := s.st.UpsertPushDevice(&dev); err != nil {
		s.log.Warn("推送设备登记失败", "member", id.Member, "device", req.DeviceID, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("推送设备已登记", "member", id.Member, "device", req.DeviceID, "platform", req.Platform)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handlePushDeviceDelete DELETE /api/push/devices → 注销一台设备。
//
// 入参：proto.PushDeviceDeleteReq（device_id 必填）。member 同样服务端注入。
// 出参：200 {"ok":true} / 400 入参为空 / 403 未配 console_user /
//
//	404 该成员名下无此设备 / 500 写库失败
func (s *Server) handlePushDeviceDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := s.requireConsoleIdentity(w, r)
	if !ok {
		return
	}
	var req proto.PushDeviceDeleteReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.log.Warn("推送设备注销请求体解析失败", "method", r.Method, "path", r.URL.Path, "cause", err)
		writeErr(w, http.StatusBadRequest, errors.New("bad json"))
		return
	}
	req.DeviceID = strings.TrimSpace(req.DeviceID)
	if req.DeviceID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("device_id 必填"))
		return
	}
	if err := s.st.DeletePushDevice(id.Member, req.DeviceID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("推送设备注销未命中", "member", id.Member, "device", req.DeviceID)
			writeErr(w, http.StatusNotFound, errors.New("设备未登记"))
			return
		}
		s.log.Warn("推送设备注销失败", "member", id.Member, "device", req.DeviceID, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.log.Info("推送设备已注销", "member", id.Member, "device", req.DeviceID)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
