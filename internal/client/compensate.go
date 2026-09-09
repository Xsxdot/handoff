package client

import (
	"context"
	"errors"
)

var errStopAndReclaimUnwired = errors.New("client: stop and reclaim not wired")

// StopAndReclaim 是远端孤儿 task 的共享补偿接缝：由实现票按 Stop/Reclaim 的既有
// HTTP 语义完成主动中止与 force 回收。Ticket 0 只提供签名，不发请求。
func (c *Client) StopAndReclaim(ctx context.Context, taskID string) error {
	return errStopAndReclaimUnwired
}
