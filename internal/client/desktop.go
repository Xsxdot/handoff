// 本文件实现桌面薄壳向 agentd 单向上报自身状态的客户端方法。
//
// 职责：封装 PUT /api/desktop/state 的线请求。
// 边界：不接收任何来自 agentd 的指令；agentd 只持有并转发状态，反向控制通道
// 已在设计上排除（spec §5）。
package client

import (
	"context"
	"net/http"

	"github.com/Xsxdot/handoff/internal/proto"
)

// PutDesktopState 上报薄壳自身状态（PUT /api/desktop/state）。
//
// 参数：ctx 控制请求生命周期；st 是薄壳当前版本与同步结论。
// 返回：agentd 返回 200 时为 nil；连接、鉴权或 HTTP 错误原样返回。
// 注意：这是单向通道。agentd 只持有并转发给控制台，不会有任何指令回来——
// 让控制台点得动薄壳需要一条反向通道，设计上已排除（spec §5）。
func (c *Client) PutDesktopState(ctx context.Context, st proto.DesktopState) error {
	resp, err := c.do(ctx, http.MethodPut, "/api/desktop/state", st)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("上报薄壳状态", resp)
	}
	c.log().Debug("薄壳状态上报成功", "app_version", st.AppVersion, "sync_plan", st.SyncPlan)
	return nil
}
