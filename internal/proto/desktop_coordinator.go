// desktop_coordinator.go —— B434 Claude 桌面端会话当协调者的跨进程 wire 词表与 DTO。
//
// 职责：集中定义控制面、CLI、控制台与 Claude Code mod 之间共用的字面量
// （信箱目录、来信标记、深链形状、桌面端会话 id 前缀、拉起来源词表）与
// 两个 HTTP DTO。契约见 docs/superpowers/specs/b434-contract.md §3。
//
// 边界：本文件只有常量、类型与空壳；写信箱、发深链、拼简报等行为在
// internal/agentd 与 cmd 实现节点落码（TODO(B434)）。mod 是独立交付物，
// 不 import 本包——两侧字面量一致性由金样本测试锁定。
package proto

import "errors"

// DesktopWakeMarker 是 handoff 发给桌面端会话的一切文本（深链 q 简报、信箱
// 来信）的首个标记。mod 只对以它开头的预填草稿代按发送；项目 CLAUDE.md 的
// 常设授权文本也以它识别 handoff 来信。
const DesktopWakeMarker = "[handoff]"

// DesktopWakeDirRel 是唤醒信箱目录相对 $HOME 的路径（本机文件目录）。
const DesktopWakeDirRel = ".handoff/desktop-wake"

// DesktopSessionPrefix 是 Claude 桌面端会话 id 的固定前缀（形如 local_<uuid>），
// 与 CLI 会话 id（CLAUDE_CODE_SESSION_ID）是两个不同的值。
const DesktopSessionPrefix = "local_"

// ClaudeNewSessionURLPrefix / ClaudeContinueURLPrefix 是 Claude 桌面端两条深链
// 的固定前缀（外部现实，见 spec「深链 wire」）。new 带 folder 与 q 两个
// encodeURIComponent 编码的参数；continue 只带 session。
const (
	ClaudeNewSessionURLPrefix = "claude://code/new?"
	ClaudeContinueURLPrefix   = "claude://code/continue?session="
)

// ClaudeNewQMaxChars 是 new 深链 q 参数的上限（桌面端 app 包读数 14,336 字符）。
// 超限拒绝，不截断。
const ClaudeNewQMaxChars = 14336

// SeatDesktopVia* 是桌面端席位的拉起来源词表，进 seat_desktop_set 事件载荷，
// 供卡 timeline 区分「卡页拉起 / 拉起器代开 / 手填」。
const (
	SeatDesktopViaPage     = "page"
	SeatDesktopViaLauncher = "launcher"
	SeatDesktopViaManual   = "manual"
)

// ErrDesktopNotImplemented 是 Ticket 0 空壳的统一返回值；实现节点删除。
var ErrDesktopNotImplemented = errors.New("B434 桌面端协调者：尚未实现") // TODO(B434)

// DesktopLaunchMaterial 是「用 Claude Code 坐下」的拉起材料
// （GET /api/cards/{id}/coordinator/desktop-launch 的 200 响应体）。同一份材料
// 服务深链、复制兜底与拉起器来信，前端不得自行拼简报。
type DesktopLaunchMaterial struct {
	Card   string `json:"card"`   // 卡号
	Folder string `json:"folder"` // 项目在本 agentd 登记的绝对根目录
	Brief  string `json:"brief"`  // 人尺度简报正文，以 DesktopWakeMarker 开头
}

// DesktopLauncherOpenResp 是 POST /api/cards/{id}/coordinator/desktop-launch
// （拉起器代开）的 200 响应体：信已投进拉起器信箱且 continue 深链已发出。
type DesktopLauncherOpenResp struct {
	Launcher  string `json:"launcher"`   // 拉起器的桌面端会话 id（local_…）
	Machine   string `json:"machine"`    // 拉起器登记的机器；空串 = 本机
	HandledBy string `json:"handled_by"` // 实际写信、发深链的机器名
}

// DesktopDeliverReq 是 agentd→agentd 的桌面端投递请求（POST /api/desktop/deliver）：
// 收件机在本机信箱为 CLISession 写一封信并对 DesktopSession 发 continue 深链。
// 仅服务拉起器异机投递，且以契约待裁决 P3 取 A 为前提；取 B 则删除本类型与回执。
type DesktopDeliverReq struct {
	CLISession     string `json:"cli_session"`
	DesktopSession string `json:"desktop_session"`
	Letter         string `json:"letter"`
}

// DesktopDeliverResp 是投递回执：Written 表示信已原子落盘，Opened 表示 continue
// 深链已成功交给本机 opener。Written=true 且 Opened=false 是「信留在信箱」态。
type DesktopDeliverResp struct {
	HandledBy string `json:"handled_by"`
	Written   bool   `json:"written"`
	Opened    bool   `json:"opened"`
}

// ValidateDesktopSessionID 校验桌面端会话 id 形状：DesktopSessionPrefix 开头，
// 余部非空且只含 [A-Za-z0-9-]。
func ValidateDesktopSessionID(id string) error {
	_ = id
	return ErrDesktopNotImplemented // TODO(B434)
}
