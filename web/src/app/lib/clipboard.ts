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
//   - 尽力而为：两条路径都失败只 console.warn，并把失败交给调用方决定是否提示用户
//   - execCommand 已废弃但 WebKit/Chromium 均仍支持；jsdom 等环境没有它，
//     此时自动退回 writeText

import { requestNativeClipboard } from './desktopShell'

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

// copyWithBrowserAPIs 是非桌面桥路径，以及原生桥失败后的浏览器兜底。
// 必须把 execCommand 放在函数同步部分：它是唯一可能继承当前用户手势的复制方式。
function copyWithBrowserAPIs(text: string): Promise<boolean> {
  if (execCommandCopy(text)) return Promise.resolve(true)

  let p: Promise<void> | undefined
  try {
    p = navigator.clipboard?.writeText(text)
  } catch (err) {
    console.warn('复制失败：调用 navigator.clipboard.writeText 抛错', err)
    return Promise.resolve(false)
  }
  if (!p) {
    console.warn('复制失败：execCommand 与 navigator.clipboard 均不可用')
    return Promise.resolve(false)
  }
  return p.then(
    () => true,
    (err) => {
      console.warn('复制失败：', err)
      return false
    },
  )
}

// copyToClipboard 把 text 写入系统剪贴板：同步 execCommand 优先，
// 不可用或失败时退回 navigator.clipboard.writeText。
//
// 参数：
//   - text: 要写入系统剪贴板的文本
//
// 返回：
//   - Promise<boolean>：true 表示已写入，false 表示两条路径都失败
//
// 注意：
//   - execCommand 必须在调用方的同步调用栈里先执行；WKWebView 的异步
//     writeText 被拒绝后，原来的用户手势已经过期，无法再靠异步兜底。
export function copyToClipboard(text: string): Promise<boolean> {
  const native = requestNativeClipboard(text)
  if (native !== null) {
    return native.then(
      (ok) => (ok ? true : copyWithBrowserAPIs(text)),
      (err) => {
        console.warn('复制失败：桌面宿主桥接抛错', err)
        return copyWithBrowserAPIs(text)
      },
    )
  }
  return copyWithBrowserAPIs(text)
}
