// desktop_coordinator.go —— Claude 桌面端信箱与 continue 深链的跨进程 wire 词表（B434 引入）。
//
// 职责：集中定义控制面、CLI 与 Claude Code mod 之间共用的字面量（信箱目录、来信标记、
// continue 深链前缀、桌面端会话 id 前缀与形状校验）。契约见 docs/superpowers/specs/b434-contract.md
// §3 保留条；B435 X 已删 new 深链字面量、拉起来源词表与四个拉起器/材料 DTO（b435-contract.md §7）。
//
// 边界：本文件只有常量与校验函数；写信箱、发深链等行为在 internal/agentd 落码。mod 是独立
// 交付物，不 import 本包——两侧字面量一致性由金样本测试锁定。
package proto

import (
	"fmt"
	"strings"
)

// DesktopWakeMarker 是 handoff 发给桌面端会话的一切文本（信箱来信）的
// 首个标记。mod 只对以它开头的预填草稿代按发送；项目 CLAUDE.md 的
// 常设授权文本也以它识别 handoff 来信。
const DesktopWakeMarker = "[handoff]"

// DesktopWakeDirRel 是唤醒信箱目录相对 $HOME 的路径（本机文件目录）。
const DesktopWakeDirRel = ".handoff/desktop-wake"

// DesktopSessionPrefix 是 Claude 桌面端会话 id 的固定前缀（形如 local_<uuid>），
// 与 CLI 会话 id（CLAUDE_CODE_SESSION_ID）是两个不同的值。
const DesktopSessionPrefix = "local_"

// ClaudeContinueURLPrefix 是 Claude 桌面端 continue 深链的固定前缀（外部现实，见 spec
// 「深链 wire」）；只带 session 一个参数。
const ClaudeContinueURLPrefix = "claude://code/continue?session="

// ValidateDesktopSessionID 校验桌面端会话 id 形状：DesktopSessionPrefix 开头，
// 余部非空且只含 [A-Za-z0-9-]。
//
// 参数：id 待校验值（原样比较，不裁剪空白；前缀大小写敏感）。
// 返回：nil = 合形；否则返回描述首个违例的错误（不包任何哨兵，调用方按需包装）。
// 注意：只校验形状，不证明该会话在桌面端真实存在。
func ValidateDesktopSessionID(id string) error {
	if !strings.HasPrefix(id, DesktopSessionPrefix) {
		return fmt.Errorf("桌面端会话 id 必须以 %q 开头: %q", DesktopSessionPrefix, id)
	}
	rest := id[len(DesktopSessionPrefix):]
	if rest == "" {
		return fmt.Errorf("桌面端会话 id 在 %q 之后为空", DesktopSessionPrefix)
	}
	for _, r := range rest {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return fmt.Errorf("桌面端会话 id 含非法字符 %q（只允许 [A-Za-z0-9-]）: %q", r, id)
		}
	}
	return nil
}
