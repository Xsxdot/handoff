// coordinator.go —— 协调者控制面拨号方法。
//
// 协调者的 launch 拨号位于 squads.go；本文件提供 launch-only rebind、跨机唤醒转交与
// 内存驱逐（B434 拉起器代开与投递两方法已随 B435 X 退役），保持 HTTP body 与服务端契约一致，
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
