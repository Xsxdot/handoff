// 本文件验证 preview client 的 HTTP/WS 传输接缝。
//
// 职责：穿过真实 httptest HTTP handler 与 websocket 文本帧，锁定路径、查询参数、
// Bearer 鉴权和回调错误回传；不测试 owner/store 业务规则。
package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

func TestPreviewClientTransportSeams(t *testing.T) {
	const token = "preview-client-token"
	var openedMachine string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer "+token {
			t.Errorf("authorization=%q", got)
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/previews":
			if r.URL.Query().Get("scope") == "all" {
				writePreviewClientJSON(t, w, proto.PreviewListResp{Sessions: []proto.PreviewSession{{ID: "all"}}})
				return
			}
			writePreviewClientJSON(t, w, proto.PreviewListResp{Sessions: []proto.PreviewSession{{ID: "owner"}}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/previews/owner/open":
			openedMachine = r.URL.Query().Get("machine")
			writePreviewClientJSON(t, w, proto.PreviewOpenResp{Opened: true})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/previews/owner":
			writePreviewClientJSON(t, w, proto.PreviewCloseResp{OK: true})
		case r.Method == http.MethodGet && r.URL.Path == "/ws/previews":
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept preview ws: %v", err)
				return
			}
			defer conn.CloseNow()
			body, _ := json.Marshal(proto.PreviewEvent{Type: proto.PreviewEventCreated, Session: proto.PreviewSession{ID: "ws"}})
			if err := conn.Write(r.Context(), websocket.MessageText, body); err != nil {
				t.Errorf("write preview ws: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	c := New(server.URL, token)
	owner, err := c.ListPreviews(context.Background())
	if err != nil || len(owner.Sessions) != 1 || owner.Sessions[0].ID != "owner" {
		t.Fatalf("ListPreviews=%+v err=%v", owner, err)
	}
	all, err := c.ListPreviewsAll(context.Background())
	if err != nil || len(all.Sessions) != 1 || all.Sessions[0].ID != "all" {
		t.Fatalf("ListPreviewsAll=%+v err=%v", all, err)
	}
	if _, err := c.OpenPreview(context.Background(), "owner", "dev box"); err != nil {
		t.Fatalf("OpenPreview: %v", err)
	}
	if openedMachine != "dev box" {
		t.Fatalf("open machine=%q", openedMachine)
	}
	if _, err := c.ClosePreview(context.Background(), "owner"); err != nil {
		t.Fatalf("ClosePreview: %v", err)
	}
	wantErr := errors.New("stop after preview event")
	err = c.StreamPreviewEventsOnce(context.Background(), func(event proto.PreviewEvent) error {
		if event.Type != proto.PreviewEventCreated || event.Session.ID != "ws" {
			t.Fatalf("preview event=%+v", event)
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("StreamPreviewEventsOnce err=%v", err)
	}
}

func writePreviewClientJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Errorf("encode preview response: %v", err)
	}
}
