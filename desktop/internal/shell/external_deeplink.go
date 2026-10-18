// 本文件负责把控制台外链页面发来的 Claude 桌面端深链请求交给系统 opener（B434）。
//
// 职责：识别 handoff:open-deeplink: 协议；校验来源 frame 是带 host 的 http(s) 页面；
//
//	只放行 claude://code/continue 深链，再把原始 URL 逐字交给注入的 opener（B434 的 new-session
//	形态随 B435 X 退役：契约 b435-contract.md §7，b434 条 31 改为被拒）。
//
// 边界：不 import Wails、不导航 webview；main.go 只负责把 OriginInfo 与 opener
//
//	（app.Browser.OpenURL）接进来。白名单只有 claude 这一个 scheme、code 这一个 host、
//	/continue 这一个 path（spec：薄壳不放行任意 scheme）。与系统浏览器、剪贴板
//	通道是三道独立的门，只共用同包谓词 isHTTPURL 判来源。
//
// 已知限制：生产 opener 在 darwin 上是 `open <url>` 的 Start()，不回报 open 的退出码
//
//	（wails/v3@v3.0.0-beta.8 internal/browser/browser_darwin.go）。桌面端未安装、深链
//	被桌面端拒绝时本处理器看不到失败——补救在控制台侧的复制兜底（契约 §3.12）。
package shell

import (
	"errors"
	"log/slog"
	"net/url"
	"strings"
)

// ExternalDeepLinkMessagePrefix 是前端 desktopShell.ts 与 Wails raw handler 共用的
// 深链协议前缀。与 ExternalBrowserMessagePrefix 同理，不得以 wails: 开头。
const ExternalDeepLinkMessagePrefix = "handoff:open-deeplink:"

// claudeDeepLinkScheme / claudeDeepLinkHost / claudeDeepLinkPaths 是深链白名单的
// 三段；放宽任何一段都等于给控制台页面开了一条通往本机任意 URL handler 的路。
const (
	claudeDeepLinkScheme = "claude"
	claudeDeepLinkHost   = "code"
)

var claudeDeepLinkPaths = map[string]bool{
	"/continue": true,
}

// HandleExternalDeepLinkMessage 消费一条从外链控制台送来的 Claude 桌面端深链请求。
//
// 参数：
//   - log：结构化日志入口；nil 时使用 slog.Default()
//   - message：WebKit external handler 传入的原始字符串（前缀 + 完整 claude:// URL）
//   - sourceFrameURL：Wails OriginInfo.Origin；必须是带 host、无 userinfo 的 http(s) 地址
//   - open：系统 opener；生产实现为 app.Browser.OpenURL，测试注入 spy。收到的参数
//     与消息里的 URL 逐字相同（不经 url.URL.String() 重新编码）
//
// 返回：非本协议消息为 false；本协议消息无论校验拒绝、opener 未配置或 opener 失败
// 均为 true（已处理，调用方不得再交给下一个处理器）。
// 注意：完整 URL、query、来源 URL 与 opener 错误文本都不写日志（query 可能带会话标识）；
// 日志只记字节数、path 与 query 字节数。
func HandleExternalDeepLinkMessage(
	log *slog.Logger,
	message, sourceFrameURL string,
	open func(string) error,
) bool {
	if !strings.HasPrefix(message, ExternalDeepLinkMessagePrefix) {
		return false
	}
	if log == nil {
		log = slog.Default()
	}
	log.Info("收到从控制台打开 Claude 桌面端深链请求", "message_bytes", len(message))

	if err := validateDeepLinkSource(sourceFrameURL); err != nil {
		log.Warn("拒绝从控制台打开 Claude 桌面端深链请求",
			"result", "invalid_source", "cause", err)
		return true
	}
	raw := strings.TrimPrefix(message, ExternalDeepLinkMessagePrefix)
	target, err := validateClaudeDeepLink(raw)
	if err != nil {
		log.Warn("拒绝从控制台打开 Claude 桌面端深链请求",
			"result", "invalid_target", "cause", err)
		return true
	}
	if open == nil {
		log.Error("打开 Claude 桌面端深链失败",
			"result", "opener_unconfigured", "path", target.Path)
		return true
	}

	log.Debug("调用系统 opener 打开 Claude 桌面端深链",
		"result", "opening", "path", target.Path, "query_bytes", len(target.RawQuery))
	// 为什么交 raw 而不是 target.String()：契约 §4.3 条 31 要求 opener 参数与原 URL
	// 逐字相等。对通过白名单的 URL，String() 今天恰好也相同（net/url 原样保留
	// RawQuery），但交 raw 让逐字性不依赖 net/url 的序列化细节。
	if err := open(raw); err != nil {
		// opener 错误文本可能回显 URL（exec 错误带参数时），故不记 cause。
		log.Error("调用系统 opener 打开 Claude 桌面端深链失败",
			"result", "open_failed", "path", target.Path)
		return true
	}
	log.Info("已调用系统 opener 打开 Claude 桌面端深链",
		"result", "opened", "path", target.Path, "query_bytes", len(target.RawQuery))
	return true
}

// validateDeepLinkSource 确保请求来自桌面壳当前加载的 Web 控制台（带 host 的 http(s)
// 页面），而不是被导航进来的 file/about/wails 页面。判据与 validateClipboardSource、
// validateExternalBrowserURL 的来源部分一致（三通道一致性由测试锁住）。
func validateDeepLinkSource(sourceFrameURL string) error {
	if sourceFrameURL == "" {
		return errors.New("来源 frame URL 为空")
	}
	source, err := url.Parse(sourceFrameURL)
	if err != nil || !isHTTPURL(source) {
		return errors.New("来源 frame URL 必须是带 host 的 http(s) 地址")
	}
	if source.User != nil {
		return errors.New("来源 frame URL 不允许 userinfo")
	}
	return nil
}

// validateClaudeDeepLink 校验深链：scheme 恰为 claude（字面小写）、host 恰为 code、
// path 恰为 /continue（不接受百分号变体与尾斜杠）、无 userinfo/fragment、
// 原始字符串不含控制字符（< 0x20 或 0x7f）。
//
// 返回：通过校验的解析结果，供调用方取 path 写日志；错误文案固定，不回显 URL
// （url.Parse 的原始错误含完整 URL，不得透传）。
// 注意：控制字符只查原始字符串；百分号编码的换行（%0A）不在拒绝之列；query 的键与值
// 不在本白名单内校验。
func validateClaudeDeepLink(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, errors.New("深链为空")
	}
	// 为什么自查：net/url 的 parse 今天也拒控制字符，但条 39 是承重安全属性，
	// 不寄托于库的内部实现。
	if strings.IndexFunc(raw, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return nil, errors.New("深链含控制字符")
	}
	// net/url 会把 scheme 转成小写；先按字面前缀卡住，避免 CLAUDE:// 之类变体放行。
	if !strings.HasPrefix(raw, claudeDeepLinkScheme+"://") {
		return nil, errors.New("深链 scheme 必须是 claude://")
	}
	target, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("深链无法解析")
	}
	if target.Scheme != claudeDeepLinkScheme || target.Opaque != "" {
		return nil, errors.New("深链 scheme 必须是 claude://")
	}
	if target.User != nil {
		return nil, errors.New("深链不允许 userinfo")
	}
	if target.Host != claudeDeepLinkHost {
		return nil, errors.New("深链 host 必须是 code")
	}
	if !claudeDeepLinkPaths[target.Path] || target.EscapedPath() != target.Path {
		return nil, errors.New("深链 path 必须是 /continue")
	}
	if target.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errors.New("深链不允许 fragment")
	}
	return target, nil
}
