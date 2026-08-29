package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/coder/websocket"
)

// CreatePreview creates one owner session (POST /api/previews).
func (c *Client) CreatePreview(ctx context.Context, req proto.PreviewOpenReq) (*proto.PreviewSession, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/previews", req)
	if err != nil {
		return nil, fmt.Errorf("创建预览会话请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("创建预览会话", resp)
	}
	var out proto.PreviewSession
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析预览会话响应: %w", err)
	}
	return &out, nil
}

// ListPreviews returns sessions owned by the target (GET /api/previews).
func (c *Client) ListPreviews(ctx context.Context) (*proto.PreviewListResp, error) {
	return c.listPreviews(ctx, "/api/previews", "预览会话列表")
}

// ListPreviewsAll returns the coordinator's merged snapshot
// (GET /api/previews?scope=all).
func (c *Client) ListPreviewsAll(ctx context.Context) (*proto.PreviewListResp, error) {
	return c.listPreviews(ctx, "/api/previews?scope=all", "预览会话汇总")
}

func (c *Client) listPreviews(ctx context.Context, path, op string) (*proto.PreviewListResp, error) {
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("%s请求: %w", op, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError(op, resp)
	}
	var out proto.PreviewListResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析%s响应: %w", op, err)
	}
	return &out, nil
}

// ClosePreview closes one owner session (DELETE /api/previews/{id}).
func (c *Client) ClosePreview(ctx context.Context, id string) (*proto.PreviewCloseResp, error) {
	resp, err := c.do(ctx, http.MethodDelete, "/api/previews/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("关闭预览会话请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("关闭预览会话", resp)
	}
	var out proto.PreviewCloseResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析关闭预览会话响应: %w", err)
	}
	return &out, nil
}

// OpenPreview opens or focuses a session in the desktop running this client.
// machine is the owner machine name; it remains a query parameter so the
// request body stays the owner create DTO and the local click is not forwarded.
func (c *Client) OpenPreview(ctx context.Context, id, machine string) (*proto.PreviewOpenResp, error) {
	path := "/api/previews/" + id + "/open"
	if machine != "" {
		path += "?machine=" + encodeQuery(machine)
	}
	resp, err := c.do(ctx, http.MethodPost, path, struct{}{})
	if err != nil {
		return nil, fmt.Errorf("打开预览会话请求: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("打开预览会话", resp)
	}
	var out proto.PreviewOpenResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析打开预览会话响应: %w", err)
	}
	return &out, nil
}

// StreamPreviewEventsOnce consumes one live owner/coordinator WS connection.
// Catch-up is deliberately not cursor based: callers list first, then
// reconnect by listing again before opening a fresh stream.
func (c *Client) StreamPreviewEventsOnce(ctx context.Context, onEvent func(proto.PreviewEvent) error) error {
	if err := c.checkInit(); err != nil {
		return err
	}
	wsScheme := "ws"
	if strings.HasPrefix(c.baseURL, "https://") {
		wsScheme = "wss"
	}
	host := strings.TrimPrefix(strings.TrimPrefix(c.baseURL, "http://"), "https://")
	wsURL := wsScheme + "://" + host + "/ws/previews"
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	conn, resp, err := websocket.Dial(dialCtx, wsURL, c.wsDialOptions())
	cancel()
	if err != nil {
		if resp != nil {
			return fmt.Errorf("预览 WS 拨号失败 status=%d: %w", resp.StatusCode, err)
		}
		return fmt.Errorf("预览 WS 拨号失败: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(512 << 10)
	for {
		_, body, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("预览 WS 读取: %w", err)
		}
		var event proto.PreviewEvent
		if err := json.Unmarshal(body, &event); err != nil {
			return fmt.Errorf("预览 WS 事件反序列化: %w", err)
		}
		if err := onEvent(event); err != nil {
			return err
		}
	}
}

func encodeQuery(value string) string {
	return url.QueryEscape(value)
}
