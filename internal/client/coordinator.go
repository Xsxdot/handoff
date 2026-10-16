// coordinator.go —— 协调者控制面拨号方法。
//
// 协调者的 launch 拨号位于 squads.go；本文件提供 launch-only rebind、跨机唤醒转交、
// 内存驱逐与 B434 拉起器代开（DesktopLauncherOpen），保持 HTTP body 与服务端契约一致，
// 不把浏览器或 CLI 提供的 identity 当作可信席位。
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

// CoordinatorWake 把本批唤醒事件转交给承载席位所在机器的 agentd 执行（B389
// §3.3）。出站方负责加防环头（调用方用 MarkForwarded）；本方法只发一次请求、
// 原样透出非 2xx 错误正文。请求/返回/错误边界见 proto.CoordinatorWakeReq/Resp。
func (c *Client) CoordinatorWake(ctx context.Context, cardID string,
	req proto.CoordinatorWakeReq) (*proto.CoordinatorWakeResp, error) {
	resp, err := c.do(ctx, http.MethodPost,
		"/api/cards/"+url.PathEscape(cardID)+"/coordinator/wake", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("coordinator wake", resp)
	}
	var out proto.CoordinatorWakeResp
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

// DesktopLauncherOpen 请 agentd 让卡所属项目的拉起器代开一场 Claude 桌面端协调者会话
// （POST /api/cards/{id}/coordinator/desktop-launch，体 {}；契约 §3.9、§3.10，P7=d）。
//
// 参数：ctx 控制请求；cardID 卡号（作路径段转义）。
// 返回：200 → 解码后的回执（拉起器桌面端会话 id、拉起器登记机器、实际写信的机器）；
// 非 2xx → httpError 包装的错误，服务端 `error` 原文用 ServerErrorText 取；连接失败 →
// ErrUnreachable 链。
// 注意：只发一次、不重试；不打 needs_human——无拉起器的等人标记由端点落（条 145），
// 调用方也不得补打（条 260）。不经协调者小队识别与准入（与 CoordinatorRebind 不同路由）。
func (c *Client) DesktopLauncherOpen(ctx context.Context, cardID string) (*proto.DesktopLauncherOpenResp, error) {
	c.log().Info("请求拉起器代开桌面端协调者", "card", cardID)
	resp, err := c.do(ctx, http.MethodPost,
		"/api/cards/"+url.PathEscape(cardID)+"/coordinator/desktop-launch", struct{}{})
	if err != nil {
		c.log().Warn("拉起器代开请求未拿到响应", "card", cardID, "cause", err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("desktop launcher open", resp)
	}
	var out proto.DesktopLauncherOpenResp
	if err := decodeWire(resp, &out); err != nil {
		c.log().Warn("拉起器代开回执解码失败", "card", cardID, "cause", err)
		return nil, err
	}
	c.log().Info("拉起器代开已受理", "card", cardID,
		"launcher", out.Launcher, "machine", out.Machine, "handled_by", out.HandledBy)
	return &out, nil
}
