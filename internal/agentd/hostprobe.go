// hostprobe.go —— 本机隔离 HOME 探测/唤起的 HTTP 面（B293）。
//
// 职责：解码 → 调 hostapi.Host.ProbeHome / WakeHome → 编码。本机能力，经
// forwardIfRequested 按 ?machine= 一跳转发到目标机同名端点。
//
// 边界：不写编制域状态（检测写状态在 schedapi 的 detect 端点）；不发明第二套
// 转发。Ticket 0 空壳：hostapi 方法恒 ErrUnavailable → 本层 503。
package agentd

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/Xsxdot/handoff/internal/hostapi"
	"github.com/Xsxdot/handoff/internal/proto"
)

func (s *Server) registerHostProbeRoutes(api *http.ServeMux) {
	api.HandleFunc("POST /api/host/probe", s.withHostAPI(s.handleHomeProbe))
	api.HandleFunc("POST /api/host/wake", s.withHostAPI(s.handleHomeWake))
}

func (s *Server) withHostAPI(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.hostAPI == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"error": "进程承载门面未装配（SetupAutomation 未执行）",
			})
			return
		}
		h(w, r)
	}
}

// handleHomeProbe POST /api/host/probe?machine=：只读探测。?machine= 先转发。
func (s *Server) handleHomeProbe(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	var in proto.HomeProbeReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("解析请求体: %v", err)})
		return
	}
	if in.CLI == "" || in.Path == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "cli 与 path 必填"})
		return
	}
	s.log.Info("探测隔离 HOME", "cli", in.CLI, "credential", in.Credential)
	reply, err := s.hostAPI.ProbeHome(r.Context(), hostapi.ProbeRequest{
		Path: in.Path, CLI: in.CLI, Credential: in.Credential,
	})
	if err != nil {
		s.hostProbeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proto.HomeProbeResp{Kind: string(reply.Kind), Detail: reply.Detail})
}

// handleHomeWake POST /api/host/wake?machine=：本机有时限唤起。?machine= 先转发。
func (s *Server) handleHomeWake(w http.ResponseWriter, r *http.Request) {
	if s.forwardIfRequested(w, r) {
		return
	}
	var in proto.HomeWakeReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("解析请求体: %v", err)})
		return
	}
	if in.CLI == "" || in.HomeDir == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "cli 与 home_dir 必填"})
		return
	}
	s.log.Info("唤起隔离 HOME", "cli", in.CLI)
	reply, err := s.hostAPI.WakeHome(r.Context(), hostapi.WakeRequest{
		CLI: in.CLI, HomeDir: in.HomeDir, Model: in.Model,
	})
	if err != nil {
		s.hostProbeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proto.HomeWakeResp{Outcome: string(reply.Outcome), Detail: reply.Detail})
}

func (s *Server) hostProbeErr(w http.ResponseWriter, err error) {
	if errors.Is(err, hostapi.ErrUnavailable) {
		s.log.Warn("本机 HOME 探测/唤起尚未接线", "cause", err)
		writeErr(w, http.StatusServiceUnavailable, err)
		return
	}
	s.log.Error("本机 HOME 探测/唤起失败", "cause", err)
	writeErr(w, http.StatusInternalServerError, err)
}
