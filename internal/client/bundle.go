// 本文件是 agentd 的 GET /api/tasks/{id}/bundle 端点的客户端侧封装。
//
// 职责：
//   - 把 HTTP 状态码翻成调用方能分诊的三种结果：拿到包 / 区间为空 / 对端过旧
//
// 边界：
//   - 不落盘、不调 git：把字节流原样交给调用方（cmd/pull.go 负责落临时文件再 fetch）
//   - 不做回落决策：回落与否是 cmd 层的事，本层只提供可判别的哨兵
package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
)

// ErrBundleUnsupported 表示对端 agentd 不认识 /api/tasks/{id}/bundle（版本早于该端点引入）。
//
// why（必须是可判别的哨兵）：与 ErrStatusUnsupported 同一条纪律——**404 是结论，
// 其它是故障**。能收到 404 说明 TCP 通、HTTP 正常、Bearer 已经通过。调用方据此
// 退回 ssh 老路；换成对任何错误都回落，就会把一次真失败伪装成「老路也能跑」。
//
// 为什么 404 不会与「任务不存在」混淆：byTask 对不存在的任务也回 404，但
// pull 的第一步 client.Attach(taskID) 成功返回才轮到 Bundle——**任务存在已被
// 上一次请求证明**，所以这里的 404 只能来自路由缺失。实现不去比对响应体文案，
// 那同样是把判据建在字符串上。
var ErrBundleUnsupported = errors.New("对端 agentd 不支持 /api/tasks/{id}/bundle")

// Bundle 取任务分支的 git bundle 字节流。
//
// 参数：
//   - taskID: 完整任务 UUID
//   - have:   协调者本地已有的基线提交；空串请求全量包
//
// 返回：
//   - rc:  bundle 字节流，**调用方负责 Close**
//   - err: 404 → ErrBundleUnsupported（对端过旧，调用方应回落）；其余为故障
//
// 注意：
//   - 成功路径**不能** defer resp.Body.Close()：Body 就是返回给调用方的 rc
func (c *Client) Bundle(ctx context.Context, taskID, have string) (io.ReadCloser, error) {
	path := "/api/tasks/" + url.PathEscape(taskID) + "/bundle"
	if have != "" {
		path += "?have=" + url.QueryEscape(have)
	}
	c.log().Debug("请求任务 bundle", "task", taskID, "have", have)
	resp, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, fmt.Errorf("bundle 请求: %w", err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		c.log().Debug("bundle 响应就绪", "task", taskID, "content_length", resp.ContentLength)
		return resp.Body, nil
	case http.StatusNoContent:
		// 防御分支：服务端保证区间永不为空（放宽到 <branch>~1..<branch>），所以
		// 这里不该被走到。真收到 204 说明对端是那个短命的中间版本——如实报错，
		// **不要**沉默地当成「已是最新」：那正是 B143 真机验收抓到的静默倒退，
		// 客户端会在本地根本没有该分支引用的情况下报成功
		resp.Body.Close()
		c.log().Warn("对端返回 204（空区间），本实现的服务端不应产生它", "task", taskID, "have", have)
		return nil, fmt.Errorf("对端返回 204 空区间响应：该版本不产出带 ref 的包，本地分支引用无法建立")
	case http.StatusNotFound:
		// 不走 httpError：它会打 Warn 并造出一个普通错误，而这里的 404 是一条
		// 有用的结论，不是异常（与 Status 的 ErrStatusUnsupported 同款处置）
		resp.Body.Close()
		c.log().Debug("对端 agentd 不支持 bundle 端点，按版本过旧处理", "task", taskID)
		return nil, ErrBundleUnsupported
	default:
		defer resp.Body.Close()
		return nil, c.httpError("取 bundle", resp)
	}
}
