// coordinator.go —— 协调者控制面拨号方法。
//
// 协调者的 launch 拨号位于 squads.go；本文件提供 launch-only rebind、跨机唤醒转交、
// 内存驱逐、B434 拉起器代开（DesktopLauncherOpen）与 agentd→agentd 拉起器投递
// （DesktopDeliver），保持 HTTP body 与服务端契约一致，
// 不把浏览器或 CLI 提供的 identity 当作可信席位。
package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrApproverUnsupported 表示对端 agentd 太旧，没有「代他机裁决」端点。
//
// 与 ErrStatusUnsupported / ErrFootprintUnsupported / ErrReclaimUnsupported 同款
// 哨兵：能收到 404 说明 TCP 通、HTTP 正常、Bearer 已过，是一条「对端不支持」的
// 结论而非异常。审批链据此把该环归为「对端不支持」失败、转下一环（冻结 F3）。
var ErrApproverUnsupported = errors.New("对端 agentd 不支持代他机裁决")

// ApproverDecide 请对端 agentd 用它自己的执行器环境代跑一个审批环
// （POST /api/approver/decide，B437 冻结 F1/F2）。
//
// 参数：ctx 控制请求（调用方给单环裁决时限）；req 只含审批语义参数，不含 prompt。
// 返回：200 → 解码后的裁决应答；404 → ErrApproverUnsupported（对端不支持）；
// 其余非 2xx → httpError 包装错误；连接失败 → ErrUnreachable 链。
// 注意：只发一次、不重试——重试会让一次权限裁决变成多次 OneShot 调用。
func (c *Client) ApproverDecide(ctx context.Context, req proto.ApproverDecideReq) (*proto.ApproverDecideResp, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/approver/decide", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		c.log().Debug("对端 agentd 不支持 /api/approver/decide，按版本过旧处理")
		return nil, ErrApproverUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("approver decide", resp)
	}
	var out proto.ApproverDecideResp
	if err := decodeWire(resp, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

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

// DesktopDeliver 把一次拉起器投递交给拉起器所在机器的 agentd（POST /api/desktop/deliver，
// 契约 §3.9、§3.9.1，P3=A）：收件机按 req.Card 经共享账本读卡、用**它自己**登记的项目
// 位置渲染拉起器来信，写进 req.CLISession 的信箱并对 req.DesktopSession 发 continue。
//
// 参数：ctx 控制请求（调用方给单次时限）；req 只搬运卡号与两个会话 id，不含来信正文（条 211–212）。
// 返回：200 → 解码后的回执（Written=true；Opened 报告 continue 是否发出）；非 2xx →
// httpError 包装的错误，状态码用 StatusCode 取、`error` 原文用 ServerErrorText 取；
// 连接失败 → ErrUnreachable 链。
// 注意：出站方负责加防环头（调用方用 MarkForwarded，条 153）；只发一次、不重试——
// 重试会让「信已落盘但响应丢了」变成两封拉起信、开出两场会话。
func (c *Client) DesktopDeliver(ctx context.Context, req proto.DesktopDeliverReq) (*proto.DesktopDeliverResp, error) {
	c.log().Info("拉起器投递转交到收件机", "card", req.Card, "cli_session", req.CLISession)
	resp, err := c.do(ctx, http.MethodPost, "/api/desktop/deliver", req)
	if err != nil {
		c.log().Warn("拉起器投递未拿到收件机响应", "card", req.Card, "cause", err)
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("desktop deliver", resp)
	}
	var out proto.DesktopDeliverResp
	if err := decodeWire(resp, &out); err != nil {
		c.log().Warn("拉起器投递回执解码失败", "card", req.Card, "cause", err)
		return nil, err
	}
	c.log().Info("拉起器投递已由收件机受理", "card", req.Card,
		"handled_by", out.HandledBy, "written", out.Written, "opened", out.Opened)
	return &out, nil
}
