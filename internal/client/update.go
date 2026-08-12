// update.go —— 换版接口的客户端侧：推送二进制、触发重启、轮询确认上线。
//
// 职责：
//   - PushUpdate / RestartAgentd：调 POST /api/update 的两种模式
//   - WaitVersion：换版后轮询 status 直到新版本上线或超时
//
// 边界：
//   - 不下载、不校验资产：那是 internal/release 的职责，本层只搬字节
//   - 不做重试：换版是有副作用的动作，失败了要让操作者看见并决定
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// UpdateRejected 是被两道闸拒绝时的错误。
//
// 为什么要带 Reason 而不只是一句话：busy 与 unmanaged 的处置**完全不同**
// ——前者能 --force 越过，后者不能。把它压成字符串，调用方就只能靠
// strings.Contains 猜，而猜错的代价是给用户一条注定失败的命令。
type UpdateRejected struct {
	Reason string // proto.UpdateReasonBusy / proto.UpdateReasonUnmanaged / ""
	Msg    string
}

func (e *UpdateRejected) Error() string { return e.Msg }

// ErrUpdateUnsupported 表示对端 agentd 不认识 /api/update（v0.1.0 及更早）。
//
// 与 ErrStatusUnsupported 同一条纪律：这是一条**有用的结论**——对端过旧，
// 这一跳必须手工做（spec §8），不是一个含糊的失败。
var ErrUpdateUnsupported = errors.New("对端 agentd 不支持 /api/update")

// PushUpdate 把 tar.gz 资产原文推给对端 agentd 并触发换版重启。
//
// 参数：
//   - tag: 目标版本，agentd 用它做自检比对（新二进制 version 首行必须等于它）
//   - sum: 资产的 sha256（十六进制小写），来自 release 的 checksums.txt
//   - tgz: **tar.gz 原文**，不是解包后的裸二进制——这样三处校验比的是同一个
//     来自 release 的声明，传输两端不会互相背书
//   - force: 越过闸一（活跃任务）。**不越过闸二（非托管）**
//
// 返回：
//   - 成功响应（含 Prev：旧二进制留存路径，回滚要用）
//   - *UpdateRejected（两道闸）/ ErrUpdateUnsupported（对端过旧）/ 其他错误
func (c *Client) PushUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error) {
	return c.postUpdate(ctx, tag, sum, tgz, force)
}

// RestartAgentd 让对端 agentd 重启但不换版（body 为空，spec D8）。
//
// 用于本机：二进制由 CLI 直接换掉了，但正在跑的 agentd 仍是旧进程。
func (c *Client) RestartAgentd(ctx context.Context, force bool) (*proto.UpdateResp, error) {
	return c.postUpdate(ctx, "", "", nil, force)
}

func (c *Client) postUpdate(ctx context.Context, tag, sum string, tgz []byte, force bool) (*proto.UpdateResp, error) {
	q := url.Values{}
	if tag != "" {
		q.Set("tag", tag)
	}
	if sum != "" {
		q.Set("sha256", sum)
	}
	if force {
		q.Set("force", "1")
	}
	u := c.baseURL + "/api/update"
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if len(tgz) > 0 {
		rd = bytes.NewReader(tgz)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, rd)
	if err != nil {
		return nil, fmt.Errorf("构造换版请求: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	c.log().Info("推送换版请求", "tag", tag, "bytes", len(tgz), "force", force)
	resp, err := c.hc.Do(req)
	if err != nil {
		c.log().Error("换版请求失败", "tag", tag, "cause", err)
		return nil, fmt.Errorf("换版请求: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		c.log().Debug("对端 agentd 不支持 /api/update，按版本过旧处理")
		return nil, ErrUpdateUnsupported
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		var e proto.UpdateError
		// 解不出结构化错误就退回原文：一条读得懂的原文好过一句「解析失败」
		if err := json.Unmarshal(body, &e); err != nil || e.Error == "" {
			c.log().Warn("换版被拒", "tag", tag, "status", resp.StatusCode, "body", string(body))
			return nil, fmt.Errorf("换版: 状态码 %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
		}
		c.log().Warn("换版被拒", "tag", tag, "status", resp.StatusCode, "reason", e.Reason, "detail", e.Error)
		return nil, &UpdateRejected{Reason: e.Reason, Msg: e.Error}
	}
	var out proto.UpdateResp
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析换版响应: %w", err)
	}
	c.log().Info("换版已受理，对端将重启", "tag", out.Version, "prev", out.Prev)
	return &out, nil
}

// WaitVersion 轮询 status 直到对端版本变成 want，或超时。
//
// 参数：
//   - want: 期望的版本号（形如 v0.1.1）
//   - timeout / interval: 等待时限与轮询间隔（生产取 60s / 2s）
//
// 注意：
//   - **轮询期间的失败一律忽略继续等**。重启窗口里连接被拒、502、503 都是
//     过程而不是结论；第一次 dial 失败就放弃，等于把每一次正常换版都报成失败
//   - 超时返回错误。不确认就报成功是主张不是事实，而 agentd 起不来恰恰是最
//     需要立刻知道的时刻
func (c *Client) WaitVersion(ctx context.Context, want string, timeout, interval time.Duration) error {
	deadline := time.Now().Add(timeout)
	var last error
	for attempt := 1; ; attempt++ {
		st, err := c.Status(ctx)
		switch {
		case err == nil && st.Version.Version == want:
			c.log().Info("新版本已上线", "want", want, "attempts", attempt)
			return nil
		case err == nil:
			last = fmt.Errorf("对端版本仍是 %q", st.Version.Version)
		default:
			last = err
		}
		if time.Now().After(deadline) {
			c.log().Error("等待新版本上线超时", "want", want, "timeout", timeout, "last", last)
			return fmt.Errorf("等待 %s 上线超时（%s）：%w", want, timeout, last)
		}
		c.log().Debug("等待新版本上线", "want", want, "attempt", attempt, "last", last)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}
