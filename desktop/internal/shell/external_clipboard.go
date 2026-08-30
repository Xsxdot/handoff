// 本文件负责把控制台外链页面发来的剪贴板请求交给桌面宿主。
//
// 职责：识别并校验 handoff:clipboard-write: 协议、解码 UTF-8 文本、调用注入的
// 系统剪贴板写入器，并把成功/失败结果交给注入的回传器。
//
// 边界：不 import Wails、不直接操作 NSPasteboard；main.go 只负责把 Wails 的
// application.Clipboard 与 window.ExecJS 接进来。
package shell

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ExternalClipboardMessagePrefix 是前端 desktopShell.ts 与 Wails raw handler 共用的
// 协议前缀。不要改成 wails: 开头，否则 Wails 会先走内置窗口消息分发。
const ExternalClipboardMessagePrefix = "handoff:clipboard-write:"

// ExternalClipboardResultEvent 是桌面宿主通过 ExecJS 回传写入结果的事件名。
const ExternalClipboardResultEvent = "handoff-native-clipboard-result"

// HandleExternalClipboardMessage 消费一条从控制台外链页面送来的原生剪贴板请求。
//
// 参数：
//   - log：结构化日志入口；传 nil 时使用 slog.Default()
//   - message：WebKit external handler 传入的原始字符串
//   - sourceFrameURL：Wails OriginInfo 的来源 URL；必须是 http(s) 且带 host
//   - setText：注入的系统剪贴板写入器；生产实现为 application.Clipboard.SetText
//   - reply：把 requestID 与写入结果回传给前端；传 nil 时不回传
//
// 返回：非本协议消息为 false；本协议消息无论校验拒绝或写入失败均为 true。
// 注意：日志只记录字节数与结果，不记录剪贴板内容，避免把用户文本写进日志。
func HandleExternalClipboardMessage(
	log *slog.Logger,
	message, sourceFrameURL string,
	setText func(string) bool,
	reply func(requestID string, ok bool),
) bool {
	if !strings.HasPrefix(message, ExternalClipboardMessagePrefix) {
		return false
	}
	if log == nil {
		log = slog.Default()
	}
	log.Info("收到控制台原生剪贴板请求", "message_bytes", len(message))

	payload := strings.TrimPrefix(message, ExternalClipboardMessagePrefix)
	requestID, encoded, ok := strings.Cut(payload, ":")
	if !validClipboardRequestID(requestID) {
		log.Warn("拒绝原生剪贴板请求", "result", "invalid_request_id")
		return true
	}
	if !ok || encoded == "" {
		log.Warn("拒绝原生剪贴板请求", "result", "invalid_payload", "request_id", requestID)
		replyClipboard(reply, requestID, false)
		return true
	}
	if err := validateClipboardSource(sourceFrameURL); err != nil {
		log.Warn("拒绝原生剪贴板请求", "result", "invalid_source", "request_id", requestID, "cause", err)
		replyClipboard(reply, requestID, false)
		return true
	}

	text, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !utf8.Valid(text) {
		log.Warn("拒绝原生剪贴板请求", "result", "invalid_encoding", "request_id", requestID)
		replyClipboard(reply, requestID, false)
		return true
	}
	if setText == nil {
		log.Error("原生剪贴板写入器未配置", "request_id", requestID)
		replyClipboard(reply, requestID, false)
		return true
	}

	success := setText(string(text))
	log.Info("原生剪贴板请求完成", "request_id", requestID, "bytes", len(text), "ok", success)
	replyClipboard(reply, requestID, success)
	return true
}

// validClipboardRequestID 只允许前端生成的短 ASCII 标识进入回传脚本，
// 避免把 raw message 的内容拼进 ExecJS 时形成脚本注入面。
func validClipboardRequestID(requestID string) bool {
	if requestID == "" || len(requestID) > 64 {
		return false
	}
	for _, r := range requestID {
		if (r < '0' || r > '9') && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}

// validateClipboardSource 确保请求来自桌面壳当前加载的 Web 控制台，而不是
// 一个被意外导航进来的 file/javascript 页面。具体 agentd host 已在握手 URL 中
// 固定，raw handler 本身只接收当前 webview 的来源。
func validateClipboardSource(sourceFrameURL string) error {
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

func replyClipboard(reply func(requestID string, ok bool), requestID string, ok bool) {
	if reply != nil {
		reply(requestID, ok)
	}
}
