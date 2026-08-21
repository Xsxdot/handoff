// clipboard —— 复制文本到系统剪贴板，桌面壳兼容版。
//
// 职责：给「复制路径 / 复制相对路径」这类点击即复制的场景一个到处都能用的入口。
//
// 为什么不直接用 navigator.clipboard.writeText：桌面薄壳（Wails 的 WKWebView）
// 里，**真实用户手势**触发的 writeText 会被 WebKit 以 NotAllowedError 拒绝
//（2026-08 实测：同一窗口里 evaluateJavaScript 的合成点击反而能过，浏览器里
// 也一切正常，唯独真人点击被拒）。而且拒绝是异步落定的，届时用户手势已过期，
// 「先 writeText、失败再兜底」救不回来——所以兜底必须**在手势内同步先行**。
//
// 边界：
//   - 尽力而为：两条路径都失败只 console.warn，不打扰用户（与既有语义一致）
//   - execCommand 已废弃但 WebKit/Chromium 均仍支持；jsdom 等环境没有它，
//     此时自动退回 writeText

// execCommandCopy 用临时 textarea + execCommand('copy') 同步复制。
// 返回是否成功。必须在用户手势的同步调用栈内执行。
function execCommandCopy(text: string): boolean {
  if (typeof document.execCommand !== 'function') return false
  const ta = document.createElement('textarea')
  ta.value = text
  // 不能 display:none——选区落不到隐藏元素上；用 fixed + 透明放在视口外
  ta.style.position = 'fixed'
  ta.style.top = '0'
  ta.style.left = '-9999px'
  ta.style.opacity = '0'
  ta.setAttribute('readonly', '') // 防移动端弹键盘
  document.body.appendChild(ta)
  ta.select()
  let ok = false
  try {
    ok = document.execCommand('copy')
  } catch {
    ok = false
  }
  ta.remove()
  return ok
}

// copyToClipboard 把 text 写入系统剪贴板：同步 execCommand 优先，
// 不可用或失败时退回 navigator.clipboard.writeText。
export function copyToClipboard(text: string): void {
  if (execCommandCopy(text)) return
  const p = navigator.clipboard?.writeText(text)
  if (!p) {
    console.warn('复制失败：execCommand 与 navigator.clipboard 均不可用')
    return
  }
  p.catch((err) => {
    console.warn('复制失败：', err)
  })
}
