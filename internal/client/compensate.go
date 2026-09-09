// 本文件负责把远端孤儿任务的 Stop 与强制 Reclaim 串成一个补偿操作。
// 边界：只调用 client 已有 HTTP 方法，不新增路由、超时、重试或 branch 删除逻辑。
package client

import (
	"context"
	"errors"
	"net/http"
)

func hasHTTPStatus(err error, code int) bool {
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && statusErr.code == code
}

// StopAndReclaim 先停止远端任务，再以 force=true 回收其孤儿资源。
// 参数 taskID 是远端任务 ID；返回 nil 表示已完成或命中契约规定的软成功。
// 注意：Stop 404 不再发 Reclaim；Stop 409 继续 Reclaim；Reclaim 404/不支持视为软成功。
func (c *Client) StopAndReclaim(ctx context.Context, taskID string) error {
	c.log().Info("孤儿派发补偿进入", "task", taskID)
	_, err := c.Stop(ctx, taskID)
	if err != nil {
		if hasHTTPStatus(err, http.StatusNotFound) {
			c.log().Info("Stop 返回 404，孤儿补偿视为完成", "task", taskID)
			return nil
		}
		if !hasHTTPStatus(err, http.StatusConflict) {
			c.log().Error("Stop 失败，未进入 Reclaim", "task", taskID, "cause", err)
			return err
		}
		c.log().Warn("Stop 返回 409，继续强制回收", "task", taskID, "cause", err)
	} else {
		c.log().Info("Stop 成功，开始强制回收", "task", taskID)
	}
	if _, err := c.Reclaim(ctx, taskID, true); err != nil {
		if hasHTTPStatus(err, http.StatusNotFound) || errors.Is(err, ErrReclaimUnsupported) {
			c.log().Info("Reclaim 不可用或任务已消失，孤儿补偿视为完成", "task", taskID, "cause", err)
			return nil
		}
		c.log().Error("Reclaim 失败", "task", taskID, "cause", err)
		return err
	}
	c.log().Info("孤儿派发补偿完成", "task", taskID)
	return nil
}
