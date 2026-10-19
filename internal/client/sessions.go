// sessions.go —— 会话域的 agentd HTTP 拨号方法（B444 P2：项目内建会话、改名、叫驻场者）。
//
// 职责：与 squads.go 同一底座（do/httpError/decodeWire）；线格式在 internal/proto（SessionCreateReq/Resp、
// SessionRenameReq、SessionResidentLaunchReq、SessionResidentOutcome）。无业务判断：编排与校验在服务端
// agentd（sessioncreate.go），本包只保证传输与服务端 {"error"} 正文透明（CLI 原样呈现给操作者）。
package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Xsxdot/handoff/internal/proto"
)

// SessionCreate 建会话（POST /api/sessions）。带 project/cards/squad 时服务端走项目内编排（同步拉起驻场者
// 首轮）；返回内嵌 Session 的响应，拒卡与驻场者结果在 RejectedCards/Resident。非 200 原样返回服务端正文。
func (c *Client) SessionCreate(ctx context.Context, req proto.SessionCreateReq) (*proto.SessionCreateResp, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/sessions", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("session create", resp)
	}
	var out proto.SessionCreateResp
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SessionRename 以 agentd 控制台身份改会话名（POST /api/sessions/{id}/rename）；expect 非空作 CAS 前值。
func (c *Client) SessionRename(ctx context.Context, session string, req proto.SessionRenameReq) (*proto.Session, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/sessions/"+url.PathEscape(session)+"/rename", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("session rename", resp)
	}
	var out proto.Session
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SessionResidentLaunch 为会话叫 push 驻场者（POST /api/sessions/{id}/resident/launch，plan Q4 甲）。
func (c *Client) SessionResidentLaunch(ctx context.Context, session string, req proto.SessionResidentLaunchReq) (*proto.SessionResidentOutcome, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/sessions/"+url.PathEscape(session)+"/resident/launch", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("session resident launch", resp)
	}
	var out proto.SessionResidentOutcome
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
