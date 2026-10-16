// 本文件负责把控制台外链页面发来的 Claude 桌面端深链请求交给系统 opener（B434）。
//
// 职责：识别 handoff:open-deeplink: 协议；只放行 claude://code/new 与
//
//	claude://code/continue 两种深链，再调用注入的 opener。
//
// 边界：不 import Wails、不导航 webview；main.go 只负责把 OriginInfo 与 opener
//
//	接进来。白名单只有 claude 这一个 scheme（spec：薄壳不放行任意 scheme）。
//	Ticket 0 空壳：校验与调用在实现节点落码（TODO(B434)）。
package shell

import (
	"errors"
	"log/slog"
	"net/url"
)

// ExternalDeepLinkMessagePrefix 是前端 desktopShell.ts 与 Wails raw handler 共用的
// 深链协议前缀。与 ExternalBrowserMessagePrefix 同理，不得以 wails: 开头。
const ExternalDeepLinkMessagePrefix = "handoff:open-deeplink:"

// HandleExternalDeepLinkMessage 消费一条从外链控制台送来的 Claude 桌面端深链请求。
//
// 参数：
//   - log：结构化日志入口；nil 时使用 slog.Default()
//   - message：WebKit external handler 传入的原始字符串
//   - sourceFrameURL：Wails OriginInfo.Origin；必须是带 host 的 http(s) 地址
//   - open：系统 opener；测试注入 spy
//
// 返回：非本协议消息为 false；本协议消息无论校验拒绝或 opener 失败均为 true。
// 注意：深链 q 内含简报全文，完整 URL 不得写日志。
func HandleExternalDeepLinkMessage(
	log *slog.Logger,
	message, sourceFrameURL string,
	open func(string) error,
) bool {
	_, _, _, _ = log, message, sourceFrameURL, open
	return false // TODO(B434)
}

// validateClaudeDeepLink 校验深链：scheme 恰为 claude、host 恰为 code、path 为
// /new 或 /continue、无 userinfo/fragment/控制字符。
func validateClaudeDeepLink(raw string) (*url.URL, error) {
	_ = raw
	return nil, errors.New("B434 深链校验尚未实现") // TODO(B434)
}
