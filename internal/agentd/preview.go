package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/relay"
	"github.com/Xsxdot/handoff/internal/store"
)

// previewUnwired is the Ticket 0 boundary. The route and wire shape are
// present so callers compile; owner persistence, tunnel projection and local
// Chromium launching belong to the implementation node.
const previewUnwired = "预览会话尚未接线"

func (s *Server) handlePreviewCreate(w http.ResponseWriter, r *http.Request) {
	if s.previewOwner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
		return
	}
	var req proto.PreviewOpenReq
	if err := decodePreviewJSON(r, &req); err != nil {
		s.log.Warn("预览创建请求体非法", "operation", "create", "path", r.URL.Path, "cause", err)
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "preview create: " + err.Error()})
		return
	}
	session, err := s.previewOwner.Create(r.Context(), req)
	if err != nil {
		s.writePreviewError(w, "create", "", err)
		return
	}
	writeJSON(w, http.StatusOK, session)
}

func (s *Server) handlePreviewList(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("scope") == "all" {
		if s.previewMirror == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "preview list_all: mirror 未配置"})
			return
		}
		resp, err := s.previewMirror.ListAll(r.Context())
		if err != nil {
			s.writePreviewError(w, "list_all", "", err)
			return
		}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	if s.previewOwner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
		return
	}
	if scope := r.URL.Query().Get("scope"); scope != "" && scope != "all" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "preview list: scope 只接受 all"})
		return
	}
	resp, err := s.previewOwner.List(r.Context())
	if err != nil {
		s.writePreviewError(w, "list", "", err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePreviewClose(w http.ResponseWriter, r *http.Request) {
	if s.previewOwner == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "preview close: id 不能为空"})
		return
	}
	resp, err := s.previewOwner.Close(r.Context(), id)
	if err != nil {
		s.writePreviewError(w, "close", id, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePreviewOpen(w http.ResponseWriter, r *http.Request) {
	if s.previewOpener == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
		return
	}
	id := r.PathValue("id")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "preview open: id 不能为空"})
		return
	}
	resp, err := s.previewOpener.OpenPreview(r.Context(), id, r.URL.Query().Get("machine"))
	if err != nil {
		s.writePreviewError(w, "open", id, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handlePreviewRawWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("预览 raw WS 握手失败", "operation", "preview_raw", "path", r.URL.Path, "cause", err)
		return
	}
	defer conn.CloseNow()
	// Pipe lifetime is this handler (Bridge + defer CloseNow), not the HTTP
	// request ctx — that can fire independently of the hijacked WS.
	raw := websocket.NetConn(context.Background(), conn, websocket.MessageBinary)
	network, addr, err := relay.ReadPreviewRawRequest(raw)
	if err != nil {
		s.log.Warn("预览 raw 请求读取失败", "operation", "preview_raw", "cause", err)
		_ = relay.WritePreviewRawResponse(raw, err)
		return
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		err = fmt.Errorf("不支持 raw network=%q", network)
		s.log.Warn("预览 raw network 拒绝", "operation", "preview_raw", "network", network, "addr", addr, "cause", err)
		_ = relay.WritePreviewRawResponse(raw, err)
		return
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		s.log.Warn("预览 raw 地址非法", "operation", "preview_raw", "addr", addr, "cause", err)
		_ = relay.WritePreviewRawResponse(raw, err)
		return
	}
	if strings.EqualFold(host, "localhost") || host == "::1" {
		addr = net.JoinHostPort("127.0.0.1", port)
	}
	s.log.Info("预览 raw owner 拨号开始", "operation", "preview_raw", "network", network, "addr", addr)
	upstream, err := (&net.Dialer{}).DialContext(r.Context(), network, addr)
	if err != nil {
		s.log.Warn("预览 raw owner 拨号失败", "operation", "preview_raw", "network", network, "addr", addr, "cause", err)
		_ = relay.WritePreviewRawResponse(raw, err)
		return
	}
	defer upstream.Close()
	if err := relay.WritePreviewRawResponse(raw, nil); err != nil {
		s.log.Warn("预览 raw 成功响应失败", "operation", "preview_raw", "addr", addr, "cause", err)
		return
	}
	s.log.Info("预览 raw owner 拨号成功", "operation", "preview_raw", "network", network, "addr", addr)
	relay.BridgePreviewRaw(raw, upstream)
	s.log.Info("预览 raw 会话结束", "operation", "preview_raw", "addr", addr)
}

func (s *Server) handlePreviewWS(w http.ResponseWriter, r *http.Request) {
	if s.previewOwner == nil || s.previewOwner.hub == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": previewUnwired})
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.log.Warn("预览 WS 握手失败", "operation", "stream", "path", r.URL.Path, "cause", err)
		return
	}
	defer conn.CloseNow()
	ctx := conn.CloseRead(r.Context())
	ch, cancel := s.previewOwner.hub.Subscribe()
	defer cancel()
	sent := 0
	for {
		select {
		case <-ctx.Done():
			s.log.Info("预览 WS 连接结束", "operation", "stream", "sent", sent, "cause", ctx.Err())
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			body, err := json.Marshal(event)
			if err != nil {
				s.log.Error("预览事件编码失败", "operation", "stream", "session", event.Session.ID, "cause", err)
				return
			}
			if err := conn.Write(ctx, websocket.MessageText, body); err != nil {
				s.log.Warn("预览事件发送失败", "operation", "stream", "session", event.Session.ID, "cause", err)
				return
			}
			sent++
			s.log.Info("预览事件发送成功", "operation", "stream", "type", event.Type, "session", event.Session.ID)
		}
	}
}

func decodePreviewJSON(r *http.Request, dst any) error {
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("请求体不允许尾随 JSON")
		}
		return err
	}
	return nil
}

func (s *Server) writePreviewError(w http.ResponseWriter, operation, id string, err error) {
	status := http.StatusInternalServerError
	var inputErr *previewInputError
	switch {
	case errors.As(err, &inputErr):
		status = http.StatusBadRequest
	case errors.Is(err, store.ErrNotFound), errors.Is(err, errPreviewClosed):
		status = http.StatusNotFound
	}
	s.log.Warn("预览操作失败", "operation", operation, "session", id, "status", status, "cause", err)
	writeJSON(w, status, map[string]string{"error": err.Error()})
}
