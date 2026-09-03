// coordinator.go —— 协调者换绑拨号方法。
//
// 协调者的 launch 拨号位于 squads.go；本文件只提供 launch-only rebind，保持
// HTTP body 与服务端契约一致，不把浏览器或 CLI 提供的 identity 当作可信席位。
package client

import (
	"context"
	"net/http"
	"net/url"

	"github.com/Xsxdot/handoff/internal/proto"
)

// CoordinatorRebind 通过协调者控制面请求机器人接班；调用方只应提供 mode=launch。
func (c *Client) CoordinatorRebind(ctx context.Context, cardID string, req proto.CoordinatorRebindReq) (*proto.CoordinatorLaunchResp, error) {
	resp, err := c.do(ctx, http.MethodPost,
		"/api/cards/"+url.PathEscape(cardID)+"/coordinator/rebind", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("coordinator rebind", resp)
	}
	var out proto.CoordinatorLaunchResp
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CoordinatorForget 通知本机 agentd 丢弃卡的旧协调者会话引用。
// 参数：ctx 控制请求；cardID 是卡号。返回：agentd 返回 200 才为 nil。
// 注意：该方法不写账本；席位 CAS 由 CLI 或 agentd 控制面先完成。
func (c *Client) CoordinatorForget(ctx context.Context, cardID string) error {
	resp, err := c.do(ctx, http.MethodPost,
		"/api/cards/"+url.PathEscape(cardID)+"/coordinator/forget", struct{}{})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("coordinator forget", resp)
	}
	return nil
}
