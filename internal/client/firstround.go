// firstround.go —— B448 协调者首轮转交的出站拨号（agentd → 载体机 agentd）。
//
// 职责：两个只发一次的类型化方法（卡首轮、驻场者首轮），把对端 404 归成「对端版本过旧」哨兵，把非 2xx
// 错误体解码成带 kind 的 FirstRoundError，供受理机还原成既有的类型化错误。
// 边界：不加防环头（调用方 MarkForwarded）、不设超时（调用方 ctx）、不重试（plan §8 F36）。
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/Xsxdot/handoff/internal/proto"
)

// ErrFirstRoundUnsupported 表示对端 agentd 太旧，没有首轮转交端点（对新路径回 404）。
//
// 与 ErrApproverUnsupported 同款：能收到 404 说明 TCP 通、Bearer 已过，结论是「对端不支持」而非异常。
// 新版收件端业务上绝不回 404（卡不存在回 409 not_found，F20），所以 404 判据单一。
var ErrFirstRoundUnsupported = errors.New("对端 agentd 不支持协调者首轮转交")

// firstRoundErrorBodyLimit 是错误体读取上限：比 httpError 的 4096 宽，保证 kind 解码先于截断。
const firstRoundErrorBodyLimit = 64 << 10

// FirstRoundError 是首轮转交端点的非 2xx 回执（已拿到 HTTP 响应）。
//
// Kind 为空表示对端没给 kind（如 503 未装配、或错误体不是 JSON），此时 Message 是原文。
type FirstRoundError struct {
	Status    int
	Kind      string
	Message   string
	SessionID string
	Pointed   bool
}

func (e *FirstRoundError) Error() string {
	if e.Kind == "" {
		return fmt.Sprintf("首轮转交被拒（HTTP %d）：%s", e.Status, e.Message)
	}
	return fmt.Sprintf("首轮转交被拒（HTTP %d，%s）：%s", e.Status, e.Kind, e.Message)
}

// CoordinatorFirstRound 把卡的协调者首轮转交给载体机执行（POST /api/cards/{id}/coordinator/first-round）。
//
// 参数：ctx 是唯一超时来源（调用方给 ≥ 回合上界的截止）；req 带冻结的 squad/carrier（换绑时带 expect）。
// 返回：200 → 回执；404 → ErrFirstRoundUnsupported；其余非 2xx → *FirstRoundError；没拿到响应 →
// ErrUnreachable / ErrTunnelDisconnected 链（ctx 取消/超时原样）。只发一次。
func (c *Client) CoordinatorFirstRound(ctx context.Context, cardID string,
	req proto.FirstRoundTransferReq) (*proto.CoordinatorFirstRoundResp, error) {
	var out proto.CoordinatorFirstRoundResp
	if err := c.firstRound(ctx, "/api/cards/"+url.PathEscape(cardID)+"/coordinator/first-round", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ResidentFirstRound 把会话驻场者首轮转交给载体机执行（POST /api/sessions/{id}/resident/first-round）。
// 返回与错误同 CoordinatorFirstRound；*FirstRoundError.Pointed 报告对端是否已在会话落失败指针行。
func (c *Client) ResidentFirstRound(ctx context.Context, sessionID string,
	req proto.FirstRoundTransferReq) (*proto.ResidentFirstRoundResp, error) {
	var out proto.ResidentFirstRoundResp
	if err := c.firstRound(ctx, "/api/sessions/"+url.PathEscape(sessionID)+"/resident/first-round", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) firstRound(ctx context.Context, path string, req proto.FirstRoundTransferReq, out any) error {
	resp, err := c.do(ctx, http.MethodPost, path, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		c.log().Warn("对端 agentd 不认首轮转交端点，按版本过旧处理", "path", path, "url", c.baseURL)
		return ErrFirstRoundUnsupported
	}
	if resp.StatusCode != http.StatusOK {
		return c.firstRoundError(path, resp)
	}
	return decodeWire(resp, out)
}

// firstRoundError 解码首轮转交错误体；不是 JSON 或无 kind 时保留原文（F19：受理端按未知 kind 透出）。
func (c *Client) firstRoundError(path string, resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, firstRoundErrorBodyLimit))
	out := &FirstRoundError{Status: resp.StatusCode, Message: strings.TrimSpace(string(raw))}
	var body proto.FirstRoundTransferError
	if err := json.Unmarshal(raw, &body); err == nil {
		out.Kind, out.SessionID, out.Pointed = body.Kind, body.SessionID, body.Pointed
		if body.Error != "" {
			out.Message = body.Error
		}
	}
	if resp.StatusCode >= 500 {
		c.log().Error("首轮转交对端失败", "path", path, "status", resp.StatusCode, "kind", out.Kind, "body", out.Message)
	} else {
		c.log().Warn("首轮转交被对端拒绝", "path", path, "status", resp.StatusCode, "kind", out.Kind, "body", out.Message)
	}
	return out
}
