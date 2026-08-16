// 本文件负责鉴权握手：拿主令牌向 agentd 换一张一次性 ticket，得到可直接打开的控制台 URL。
//
// 职责：调 internal/client 的 IssueAuthTicket，把结果整理成一个 URL。
// 边界：
//   - **不实现任何鉴权逻辑**。凭据的签发与校验全在 agentd 侧
//   - **不 shell out 调 handoff console**。同一个进程里有 Go API，
//     多起一个进程只是多一层错误面
//   - 不判断 URL 打开后页面上有什么。那由 agentd 伺服的前端决定
package shell

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/Xsxdot/handoff/internal/client"
)

// DefaultDeviceName 返回展示在 agentd 会话列表里的设备名。
//
// 带上 handoff-desktop 前缀是有意的：同一台机器上既可能有 CLI 换的会话、
// 也可能有薄壳换的会话，只写主机名会让吊销时分不清该吊哪个。
func DefaultDeviceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		// 取不到主机名不是失败：服务端会补，这里给个仍然可辨认的名字
		slog.Warn("取主机名失败，设备名退化为不含主机名的形式", "cause", err)
		return "handoff-desktop"
	}
	return fmt.Sprintf("handoff-desktop (%s)", host)
}

// ConsoleURL 换一张 ticket，返回可直接交给 webview 打开的控制台 URL。
//
// 参数：
//   - ctx: 取消与超时
//   - ep: Task 2 的 Resolve 产出的地址与主令牌
//   - deviceName: 展示名；传空则用 DefaultDeviceName()
//
// 返回：
//   - 控制台 URL。**有效期很短（60 秒）**，拿到就该立刻加载，不要缓存复用
//   - error：连不上 agentd、令牌不对、或响应无法解析
//
// 注意：
//   - 返回的 URL 里带着一次性凭据，**不得写进日志**
func ConsoleURL(ctx context.Context, ep Endpoint, deviceName string) (string, error) {
	if deviceName == "" {
		deviceName = DefaultDeviceName()
	}
	slog.Info("开始鉴权握手", "addr", ep.Addr, "device_name", deviceName)

	tk, err := client.New(ep.Addr, ep.Token).IssueAuthTicket(ctx, deviceName)
	if err != nil {
		// client 的报文已经含「连接 agentd … 失败（它在运行吗？）」，这里补上地址维度
		slog.Error("鉴权握手失败", "addr", ep.Addr, "cause", err)
		return "", fmt.Errorf("向 agentd %s 换取控制台入场券失败: %w", ep.Addr, err)
	}
	// 只记过期时间，不记 URL：URL 里带一次性凭据
	slog.Info("鉴权握手成功", "addr", ep.Addr, "expires_at", tk.ExpiresAt)
	return tk.URL, nil
}
