package client

import "github.com/coder/websocket"

// ExportWSDialOptions 把包内 wsDialOptions 暴露给契约测试：锁的是「必须交出本 Client
// 的 http.Client」，不是拨号行为。
func ExportWSDialOptions(c *Client) *websocket.DialOptions {
	return c.wsDialOptions()
}
