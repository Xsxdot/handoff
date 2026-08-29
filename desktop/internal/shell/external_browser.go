// 本文件负责把控制台外链页面发来的 raw message 安全地交给系统浏览器。
//
// 职责：识别 handoff:open-browser: 协议、校验目标与发送 frame 的 http(s) 同源，
//
//	再调用注入的 opener。
//
// 边界：不 import Wails、不启动浏览器、不导航 webview；main.go 只负责把
//
//	application.OriginInfo 与 app.Browser.OpenURL 接进来。
package shell

import (
	"errors"
	"log/slog"
	"net/url"
	"strings"
)

// ExternalBrowserMessagePrefix 是前端 desktopShell.ts 与 Wails raw handler 共用的
// 协议前缀。不要改成 wails: 开头，否则 Wails 会先走内置窗口消息分发。
const ExternalBrowserMessagePrefix = "handoff:open-browser:"

// HandleExternalBrowserMessage 消费一条从外链控制台送来的系统浏览器请求。
//
// 参数：
//   - log：结构化日志入口；传 nil 时使用 slog.Default()
//   - message：WebKit external handler 传入的原始字符串
//   - sourceFrameURL：Wails OriginInfo.Origin，可能是带 path/query 的完整 frame URL
//   - issue：认证票据签发器；接收受限的相对 next，返回带票据的 console URL
//   - open：系统浏览器 opener；生产实现为 app.Browser.OpenURL，测试注入 spy
//
// 返回：非本协议消息为 false；本协议消息无论校验拒绝或 opener 失败均为 true。
// 注意：只允许目标和 sourceFrameURL 的 scheme、hostname、有效端口相同，且目标必须
//
//	是 http(s) URL；来源/目标 URL 的完整内容不得写日志，避免泄露 ticket。
func HandleExternalBrowserMessage(
	log *slog.Logger,
	message, sourceFrameURL string,
	issue func(next string) (string, error),
	open func(string) error,
) bool {
	if !strings.HasPrefix(message, ExternalBrowserMessagePrefix) {
		return false
	}
	if log == nil {
		log = slog.Default()
	}
	log.Info("收到从控制台打开系统浏览器请求", "message_bytes", len(message))

	rawTarget := strings.TrimPrefix(message, ExternalBrowserMessagePrefix)
	target, err := validateExternalBrowserURL(rawTarget, sourceFrameURL)
	if err != nil {
		log.Warn("拒绝从控制台打开系统浏览器请求",
			"result", "invalid_target", "cause", err)
		return true
	}
	next, err := nextFromTarget(target)
	if err != nil {
		log.Warn("拒绝从控制台打开系统浏览器请求",
			"result", "invalid_next", "cause", err)
		return true
	}
	if issue == nil {
		log.Error("打开系统浏览器失败",
			"result", "issuer_unconfigured", "path", target.EscapedPath())
		return true
	}
	if open == nil {
		log.Error("打开系统浏览器失败",
			"result", "opener_unconfigured", "path", target.EscapedPath())
		return true
	}

	browserURL, err := issue(next)
	if err != nil || browserURL == "" {
		log.Error("打开系统浏览器失败",
			"result", "issue_auth_ticket_failed", "path", target.EscapedPath())
		return true
	}
	log.Debug("调用系统浏览器", "result", "opening", "path", target.EscapedPath())
	if err := open(browserURL); err != nil {
		log.Error("调用系统浏览器失败",
			"result", "open_failed", "path", target.EscapedPath())
		return true
	}
	log.Info("已调用系统浏览器", "result", "opened", "path", target.EscapedPath())
	return true
}

func nextFromTarget(target *url.URL) (string, error) {
	if target.Fragment != "" {
		return "", errors.New("目标 URL 不允许 fragment")
	}
	next := target.EscapedPath()
	if next == "" {
		next = "/"
	}
	if target.RawQuery != "" {
		next += "?" + target.RawQuery
	}
	if err := validateCardsNext(next); err != nil {
		return "", err
	}
	return next, nil
}

func validateCardsNext(next string) error {
	if next == "" {
		return errors.New("next 为空")
	}
	if strings.IndexFunc(next, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("next 含控制字符")
	}
	if strings.Contains(next, "\\") || strings.HasPrefix(next, "//") {
		return errors.New("next 含禁止字符")
	}
	parsed, err := url.Parse(next)
	if err != nil || parsed.IsAbs() || parsed.Scheme != "" ||
		parsed.Host != "" || parsed.User != nil || parsed.Fragment != "" {
		return errors.New("next 必须是无 host 的相对 path")
	}
	if strings.Contains(parsed.Path, "\\") ||
		strings.IndexFunc(parsed.Path, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return errors.New("next path 含禁止字符")
	}
	for _, values := range parsed.Query() {
		for _, value := range values {
			if strings.Contains(value, "\\") ||
				strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
				return errors.New("next query 含禁止字符")
			}
		}
	}
	if parsed.Path != "/cards" && !strings.HasPrefix(parsed.Path, "/cards/") {
		return errors.New("next 不是 /cards 前缀")
	}
	return nil
}

// validateExternalBrowserURL 解析并校验目标与来源 frame 的安全边界。
// 来源 URL 可能带一次性 ticket 的 path/query，所以同源判断只比较 scheme、hostname
// 与有效端口，不比较完整 URL；最终仅返回通过校验的目标 URL。
func validateExternalBrowserURL(rawTarget, sourceFrameURL string) (*url.URL, error) {
	if rawTarget == "" {
		return nil, errors.New("目标 URL 为空")
	}
	if strings.IndexFunc(rawTarget, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0 {
		return nil, errors.New("目标 URL 含控制字符")
	}
	target, err := url.Parse(rawTarget)
	if err != nil {
		return nil, errors.New("目标 URL 无法解析")
	}
	source, err := url.Parse(sourceFrameURL)
	if err != nil {
		return nil, errors.New("来源 frame URL 无法解析")
	}
	if !isHTTPURL(target) {
		return nil, errors.New("目标 URL 必须是带 host 的 http(s) 地址")
	}
	if !isHTTPURL(source) {
		return nil, errors.New("来源 frame URL 必须是带 host 的 http(s) 地址")
	}
	if target.User != nil || source.User != nil {
		return nil, errors.New("URL 不允许 userinfo")
	}
	if !sameOrigin(target, source) {
		return nil, errors.New("目标 URL 与来源 frame 不同源")
	}
	target.Scheme = strings.ToLower(target.Scheme)
	return target, nil
}

func isHTTPURL(candidate *url.URL) bool {
	scheme := strings.ToLower(candidate.Scheme)
	return (scheme == "http" || scheme == "https") && candidate.Hostname() != ""
}

func sameOrigin(left, right *url.URL) bool {
	return strings.EqualFold(left.Scheme, right.Scheme) &&
		strings.EqualFold(left.Hostname(), right.Hostname()) &&
		effectivePort(left) == effectivePort(right)
}

func effectivePort(candidate *url.URL) string {
	if port := candidate.Port(); port != "" {
		return port
	}
	switch strings.ToLower(candidate.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}
