// claudeDeepLink —— 把控制面产出的拉起材料编成 Claude 桌面端 new 深链（B434，边界型接缝）。
//
// 职责：folder + brief → `claude://code/new?folder=<enc>&q=<enc>`，或给出拒绝原因。
//
// 边界：
//   - 只编码，不拼简报：folder 与 brief 原样来自 GET /coordinator/desktop-launch；
//   - 两个参数都用 encodeURIComponent（空格是 %20，不是 +；契约条 16–18）；
//   - 校验顺序固定：folder 非绝对 → brief 为空 → brief 超限，先报最上游的材料错误；
//   - brief 超过 CLAUDE_Q_MAX_CHARS（JS 字符串 length，即 UTF-16 码元数）即拒绝，**不截断**
//     ——截断的简报会让坐下的会话拿到半截指令，宁可让调用方退到复制兜底；
//   - 纯函数：不做 I/O、不打日志，日志由调用方（CoordinatorPanel）拿到结果后打。
// 承重：CLAUDE_Q_MAX_CHARS 与 internal/proto/desktop_coordinator.go#ClaudeNewQMaxChars 同值，
// CLAUDE_NEW_SESSION_URL_PREFIX 与 #ClaudeNewSessionURLPrefix 同值（两侧各有字面量金样本）。
// 注意：encodeURIComponent 遇孤立代理项抛 URIError。服务端 JSON 只产合法 UTF-8（Go encoding/json
// 把非法字节写成 �），真实材料走不到这条；调用方仍以 try/catch 兜底，不让点击静默失效。

/** 桌面端 new 深链 q 参数上限（app 包读数 14,336 字符）。 */
export const CLAUDE_Q_MAX_CHARS = 14336

/** new 深链固定前缀；其后依次为 folder=…&q=…。 */
export const CLAUDE_NEW_SESSION_URL_PREFIX = 'claude://code/new?'

/**
 * 编码失败原因：folder 非绝对路径 / brief 为空 / brief 超限 / not_implemented。
 * not_implemented 是 Ticket 0 空壳期的值，契约 §3.12 把它冻结在联合类型里；实现后本函数不再产出它。
 */
export type ClaudeDeepLinkRejectReason = 'folder_not_absolute' | 'brief_empty' | 'brief_too_long' | 'not_implemented'

/** 编码结果：成功给 url；失败给原因，调用方据此退化为复制兜底。 */
export type ClaudeDeepLinkResult = { ok: true; url: string } | { ok: false; reason: ClaudeDeepLinkRejectReason }

/**
 * 参数：folder 项目根目录（必须以 / 开头）、brief 简报正文（非空、length ≤ CLAUDE_Q_MAX_CHARS）；
 * 返回：`claude://code/new?folder=<enc>&q=<enc>` 或拒绝原因；
 * 注意：超限只拒绝不截断；孤立代理项会让 encodeURIComponent 抛 URIError，由调用方兜底。
 */
export function buildClaudeNewSessionURL(folder: string, brief: string): ClaudeDeepLinkResult {
  if (!folder.startsWith('/')) return { ok: false, reason: 'folder_not_absolute' }
  if (brief === '') return { ok: false, reason: 'brief_empty' }
  if (brief.length > CLAUDE_Q_MAX_CHARS) return { ok: false, reason: 'brief_too_long' }
  return {
    ok: true,
    url: `${CLAUDE_NEW_SESSION_URL_PREFIX}folder=${encodeURIComponent(folder)}&q=${encodeURIComponent(brief)}`,
  }
}
