// agentd peer_server：peer v1 机器同步路由（本机 agentd 被远端访问的入口）。
//
// 职责：
//   - GET /v1/peer/hello：协议版本 + capability map
//   - GET /v1/machine/snapshot：本机全量快照
//   - GET /v1/machine/events?machine_id=&after=&limit=：本机 outbox 事件
//   - 声明项目目录命令 capability；具体 adapter 在 project_command_server.go
//
// 边界：
//   - 与现有 /api、/ws 路由并存，互不影响
//   - 全部走统一 Bearer 鉴权（server.auth）
//   - 非 loopback 明文 HTTP 的 fail-closed 门控在 server.go 统一处理
package agentd

import (
	"context"
	"net/http"
	"strconv"

	"github.com/xushixin/handoff/internal/controlplane"
	"github.com/xushixin/handoff/internal/peer"
	"github.com/xushixin/handoff/internal/ptyservice"
)

// peerCapabilities 是本机 agentd 声明的 peer capability。
var peerCapabilities = func() map[string]int {
	capabilities := map[string]int{
		"catalog": 1, "machine_events": 1,
		peer.CapabilityFiles:           1,
		peer.CapabilityGit:             1,
		peer.CapabilityProjectCommands: 1,
		// Preview 与 PTY 不同：只依赖 owner 侧 platform-independent 的 loopback 代理，
		// 不依赖真实平台 adapter，因此无条件声明（同 files/git）。
		peer.CapabilityPreview: 1,
	}
	if ptyservice.Supported() {
		capabilities[peer.CapabilityPty] = 1
	}
	return capabilities
}()

// handlePeerHello 返回协议版本与 capability。
func (s *Server) handlePeerHello(w http.ResponseWriter, r *http.Request) {
	s.log.Info("peer hello 请求", "method", r.Method, "path", r.URL.Path)
	writeJSON(w, http.StatusOK, peer.Hello{
		ProtocolVersion: peer.ProtocolVersion,
		Capabilities:    peerCapabilities,
	})
}

// handlePeerMachineSnapshot 返回本机全量快照。
func (s *Server) handlePeerMachineSnapshot(w http.ResponseWriter, r *http.Request) {
	s.log.Info("peer snapshot 请求", "method", r.Method, "path", r.URL.Path)
	workspaces, err := s.st.ListAllWorkspaces(r.Context())
	if err != nil {
		s.log.Error("peer snapshot 读 workspaces 失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	refs, err := s.st.ListAllGitRefs(r.Context())
	if err != nil {
		s.log.Error("peer snapshot 读 git refs 失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	summaries, err := s.st.ListTaskSummaries()
	if err != nil {
		s.log.Error("peer snapshot 读 task summaries 失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	cursor, err := s.st.CurrentCursor(r.Context(), s.localMachineID())
	if err != nil {
		s.log.Error("peer snapshot 读 cursor 失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	writeJSON(w, http.StatusOK, peer.MachineSnapshot{
		ThroughMachineSeq: cursor,
		WorkspaceCount:    len(workspaces),
		GitRefCount:       len(refs),
		TaskCount:         len(summaries),
	})
}

// handlePeerMachineEvents 返回本机 outbox 事件（供远端 catch-up）。
func (s *Server) handlePeerMachineEvents(w http.ResponseWriter, r *http.Request) {
	machineID := r.URL.Query().Get("machine_id")
	if machineID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "缺少 machine_id 参数"})
		return
	}
	after, err := parseAfter(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	limit := 200
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, lerr := strconv.Atoi(q); lerr == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	events, err := s.st.MachineEventsAfter(r.Context(), machineID, after, limit)
	if err != nil {
		s.log.Error("peer events 读取失败", "machine_id", machineID, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
		return
	}
	writeJSON(w, http.StatusOK, toPeerEvents(events))
}

// toPeerEvents 把 controlplane.MachineEvent 转为 peer.MachineEvent 线格式。
func toPeerEvents(events []controlplane.MachineEvent) []peer.MachineEvent {
	out := make([]peer.MachineEvent, 0, len(events))
	for _, ev := range events {
		out = append(out, peer.MachineEvent{
			MachineID: ev.MachineID, MachineSeq: ev.MachineSeq, EventID: ev.EventID,
			Kind: string(ev.Kind), ResourceID: ev.ResourceID,
			Payload: ev.Payload, CreatedAt: ev.CreatedAt,
		})
	}
	return out
}

// parseAfter 解析 after 查询参数（>=0 整数）。
func parseAfter(r *http.Request) (int64, error) {
	q := r.URL.Query().Get("after")
	if q == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(q, 10, 64)
	if err != nil || n < 0 {
		return 0, strconv.ErrSyntax
	}
	return n, nil
}

// localMachineID 返回本机 Machine ID（用于 snapshot 的 through_machine_seq）。
func (s *Server) localMachineID() string {
	m, err := s.st.EnsureLocalMachine(context.Background(), "本机")
	if err != nil {
		return ""
	}
	return m.ID
}
