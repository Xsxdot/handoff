// claudeDeepLink —— 把控制面产出的拉起材料编成 Claude 桌面端 new 深链（B434，边界型接缝）。
//
// 边界：
//   - 只编码，不拼简报：folder 与 brief 原样来自 GET /coordinator/desktop-launch；
//   - 两个参数都用 encodeURIComponent（空格是 %20，不是 +）；
//   - brief 超过 CLAUDE_Q_MAX_CHARS（JS 字符串 length）即拒绝，不截断；
//   - folder 必须是绝对路径（以 / 开头）。
// 承重：CLAUDE_Q_MAX_CHARS 与 internal/proto/desktop_coordinator.go#ClaudeNewQMaxChars 同值，
// CLAUDE_NEW_SESSION_URL_PREFIX 与 #ClaudeNewSessionURLPrefix 同值。
// TODO(B434)：Ticket 0 空壳，buildClaudeNewSessionURL 恒返回 not_implemented。

/** 桌面端 new 深链 q 参数上限（app 包读数 14,336 字符）。 */
export const CLAUDE_Q_MAX_CHARS = 14336

/** new 深链固定前缀；其后依次为 folder=…&q=…。 */
export const CLAUDE_NEW_SESSION_URL_PREFIX = 'claude://code/new?'

/** 编码失败原因：folder 非绝对路径 / brief 为空 / brief 超限 / 空壳未实现。 */
export type ClaudeDeepLinkRejectReason = 'folder_not_absolute' | 'brief_empty' | 'brief_too_long' | 'not_implemented'

/** 编码结果：成功给 url；失败给原因，调用方据此退化为「复制简报」。 */
export type ClaudeDeepLinkResult = { ok: true; url: string } | { ok: false; reason: ClaudeDeepLinkRejectReason }

/**
 * 参数：folder 项目根目录（绝对路径）、brief 简报正文；
 * 返回：`claude://code/new?folder=<enc>&q=<enc>` 或拒绝原因。
 */
export function buildClaudeNewSessionURL(folder: string, brief: string): ClaudeDeepLinkResult {
  // TODO(B434)：Ticket 0 空壳，参数在实现节点使用；void 仅为让 lint 不把它们当未用形参。
  void folder
  void brief
  return { ok: false, reason: 'not_implemented' }
}
