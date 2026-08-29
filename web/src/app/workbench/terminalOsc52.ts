// terminalOsc52 —— OSC 52 载荷的解析与解码（B300）。
//
// 职责：把 xterm parser 交来的 OSC 52 data（`<selection>;<payload>`）翻译成
//       「要写入本机剪贴板的文本」；不该写的输入一律返回 null。
// 边界：
//   - 不碰剪贴板、不挂 xterm——那是 TerminalTab 的事；本文件是可表驱动的纯函数
//   - 「不该写」的输入：载荷为 `?`（读查询，远端不得读本机剪贴板）、空载荷
//     （「清剪贴板」语义，远端不得清本机剪贴板）、解码失败（坏 base64 / 坏 UTF-8）

export interface Osc52Copy {
  // TUI 声明的目标选择区：c（clipboard）/ s / p（primary）。浏览器只有一个
  // 剪贴板，三种都写它——本文件只透传，不据此拦截。
  selection: string
  text: string
}

// parseOsc52 解析 OSC 52 载荷。data 是 registerOscHandler(52, cb) 交给 cb 的
// 参数（`52;` 之后的原文）。返回 null 表示本条不产生剪贴板写入。
export function parseOsc52(data: string): Osc52Copy | null {
  const semi = data.indexOf(';')
  if (semi < 0) return null
  const selection = data.slice(0, semi)
  const payload = data.slice(semi + 1).replace(/\s+/g, '')
  // `?` 是「读剪贴板」查询：绝不回应——本功能的安全边界（spec 实现决定 2）。
  if (payload === '?') return null
  // 空载荷是「清空剪贴板」：忽略——远端不应能清掉本机剪贴板（spec 实现决定 3）。
  if (payload === '') return null
  const text = decodeBase64Utf8(payload)
  return text === null ? null : { selection, text }
}

// decodeBase64Utf8 容忍缺 padding 与换行包裹的 base64，按 UTF-8 严格解码。
// 任何一步失败都返回 null，由调用方决定丢弃。
function decodeBase64Utf8(b64: string): string | null {
  try {
    const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4)
    const bin = atob(padded)
    const bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0))
    return new TextDecoder('utf-8', { fatal: true }).decode(bytes)
  } catch {
    return null
  }
}
