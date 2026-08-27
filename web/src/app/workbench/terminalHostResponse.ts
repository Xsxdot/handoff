// terminalHostResponse —— 识别 xterm 解析器对「终端查询」的自动回包。
//
// 职责：判断一段 onData 是不是 xterm 在解析输出时自动生成的设备回包
//       （DA / CPR / OSC 颜色 / DECRPM），而不是用户按键。
// 边界：
//   - 不认识 PTY、不发字节。只做字符串判定，给 TerminalTab 决定要不要上送
//   - 不处理鼠标报告（那是用户动作，必须上送）
//
// 为什么必须拦：handoff 的 PTY 在独立进程里，查询要绕一圈 WebSocket。
// 切 tab / 重连会把环形缓冲整段重放进新的 xterm，历史里的 CSI/OSC 查询
// 会被再答一遍；答包落到当前前台进程（常常是 zsh）就被当成「用户敲的字」。
// 真机形态是提示符上突然出现 `>0;276;0c`（xterm.js Secondary DA）和
// `rgb:0b0b/0b0b/0c0c`（OSC 11 按主题底色 #0b0b0c 答的）。
//
// 即便不重放，往返也经常慢过 starship / TUI 的查询超时，答包迟到同样泄漏。
// 所以这条路上的回包一律不上送——应用拿不到颜色探测结果会走默认值，
// 比把设备回包打进 shell 输入轻得多。

// consumeOne 吃掉 s 开头的一条设备回包，返回消耗的字符数；不是回包则 0。
function consumeOne(s: string): number {
  if (!s.startsWith('\x1b') || s.length < 2) return 0

  // OSC：ESC ] … BEL 或 ST（ESC \）。两种收尾都合法，取先到的那个。
  if (s[1] === ']') {
    const bel = s.indexOf('\x07')
    const st = s.indexOf('\x1b\\')
    const belEnd = bel >= 0 ? bel + 1 : Number.POSITIVE_INFINITY
    const stEnd = st >= 0 ? st + 2 : Number.POSITIVE_INFINITY
    const end = Math.min(belEnd, stEnd)
    return Number.isFinite(end) ? end : 0
  }

  // DCS：ESC P … ST。DECRQSS 失败形态是 `ESC P 2 $ y ST`
  if (s[1] === 'P') {
    const st = s.indexOf('\x1b\\')
    if (st >= 0) return st + 2
    const bel = s.indexOf('\x07')
    return bel >= 0 ? bel + 1 : 0
  }

  if (s[1] !== '[') return 0

  // CSI 设备回包：DA (`c`) / CPR (`R`) / DECRPM (`$y`) / 窗口报告 (`t`)
  // 不能写成「任意 CSI」——方向键是 ESC [ A，必须放行。
  const daCpr = s.match(/^\x1b\[(?:\?|>)?[\d;]*[cR]/)
  if (daCpr) return daCpr[0].length
  const decrpm = s.match(/^\x1b\[(?:\?|>)?[\d;]*\$y/)
  if (decrpm) return decrpm[0].length
  const win = s.match(/^\x1b\[\d+(?:;\d+)*t/)
  if (win) return win[0].length
  return 0
}

// isTerminalHostResponse 判断 data 是否完全由一条或多条设备回包组成。
//
// 参数：data 为 xterm onData 交出的原文（含 ESC）。
// 返回：true = 整段都是回包，调用方不应写入 PTY。
export function isTerminalHostResponse(data: string): boolean {
  if (data.length === 0) return false
  let i = 0
  let n = 0
  while (i < data.length) {
    const c = consumeOne(data.slice(i))
    if (c <= 0) return false
    i += c
    n++
  }
  return n > 0
}
