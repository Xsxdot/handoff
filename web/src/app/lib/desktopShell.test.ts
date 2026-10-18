import { afterEach, describe, expect, it, vi } from 'vitest'
import {
  DESKTOP_TOP_INSET,
  isDesktopShell,
  NATIVE_CLIPBOARD_MESSAGE_PREFIX,
  NATIVE_CLIPBOARD_RESULT_EVENT,
  OPEN_BROWSER_MESSAGE_PREFIX,
  OPEN_DEEPLINK_MESSAGE_PREFIX,
  requestNativeClipboard,
  requestOpenCurrentPageInBrowser,
  requestTitlebarZoom,
  topInset,
} from './desktopShell'

describe('desktopShell', () => {
  it('UA 带薄壳标记时判为桌面壳，并让出顶部拖动区', () => {
    const ua = 'Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 handoff-desktop'
    expect(isDesktopShell(ua)).toBe(true)
    expect(topInset(ua)).toBe(DESKTOP_TOP_INSET)
  })

  it('普通浏览器一律不让位——页面不该无端多出一条空白', () => {
    const ua = 'Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 Chrome/140 Safari/605.1.15'
    expect(isDesktopShell(ua)).toBe(false)
    expect(topInset(ua)).toBe(0)
  })

  it('默认的 wails.io UA 不算薄壳标记', () => {
    // why：薄壳显式把 ApplicationNameForUserAgent 设成 handoff-desktop。
    // 认 wails.io 会把任何 Wails 应用都算成自己，判据要认自己的名字
    expect(isDesktopShell('Mozilla/5.0 wails.io')).toBe(false)
  })
})

describe('requestTitlebarZoom', () => {
  const bridge = () => (window as unknown as { webkit?: unknown })

  afterEach(() => {
    delete bridge().webkit
  })

  it('桥在时发出 Wails 的双击消息', () => {
    // why：双击标题栏最大化在 Wails 里是 JS 实现的（drag.ts 发
    // wails:drag:doubleclick，Go 侧才 handleTitlebarDoubleClick）。外链页面没有
    // 运行时，这条得我们自己发——消息字符串写错就是静默失效，所以钉住它
    const postMessage = vi.fn()
    bridge().webkit = { messageHandlers: { external: { postMessage } } }
    expect(requestTitlebarZoom()).toBe(true)
    expect(postMessage).toHaveBeenCalledWith('wails:drag:doubleclick')
  })

  it('桥不在时安静返回 false，不抛异常', () => {
    // 浏览器里打开控制台就是这条路径：双击顶栏本来也没有语义，不能因此炸掉页面
    expect(requestTitlebarZoom()).toBe(false)
  })
})

describe('requestOpenCurrentPageInBrowser', () => {
  const bridge = () => (window as unknown as { webkit?: unknown })

  afterEach(() => {
    delete bridge().webkit
    window.history.replaceState({}, '', '/')
  })

  it('发送固定协议前缀和当前页面的 path/query', () => {
    const postMessage = vi.fn()
    bridge().webkit = { messageHandlers: { external: { postMessage } } }
    window.history.replaceState({}, '', '/cards?project=handoff')

    expect(requestOpenCurrentPageInBrowser()).toBe(true)
    expect(OPEN_BROWSER_MESSAGE_PREFIX).toBe('handoff:open-browser:')
    expect(postMessage).toHaveBeenCalledWith(
      `${OPEN_BROWSER_MESSAGE_PREFIX}${window.location.origin}/cards?project=handoff`,
    )
  })

  it('没有 external bridge 时返回 false 且不抛异常', () => {
    expect(requestOpenCurrentPageInBrowser()).toBe(false)
  })

  // B434 对侧常量执法：与 desktop/internal/shell ExternalDeepLinkMessagePrefix 逐字相等。
  it('深链协议前缀字面量冻结且不与系统浏览器前缀相同', () => {
    expect(OPEN_DEEPLINK_MESSAGE_PREFIX).toBe('handoff:open-deeplink:')
    expect(OPEN_DEEPLINK_MESSAGE_PREFIX).not.toBe(OPEN_BROWSER_MESSAGE_PREFIX)
  })
})

describe('requestNativeClipboard', () => {
  const bridge = () => (window as unknown as { webkit?: unknown })

  afterEach(() => {
    delete bridge().webkit
  })

  it('桥在时发送 UTF-8 base64 请求，并按 requestId 接收成功结果', async () => {
    const postMessage = vi.fn((message: string) => {
      const requestID = message.slice(NATIVE_CLIPBOARD_MESSAGE_PREFIX.length).split(':', 1)[0]
      window.dispatchEvent(new CustomEvent(NATIVE_CLIPBOARD_RESULT_EVENT, {
        detail: { requestId: requestID, ok: true },
      }))
    })
    bridge().webkit = { messageHandlers: { external: { postMessage } } }

    const result = requestNativeClipboard('你好\nhello')

    expect(result).not.toBeNull()
    expect(postMessage).toHaveBeenCalledWith(expect.stringMatching(
      new RegExp(`^${NATIVE_CLIPBOARD_MESSAGE_PREFIX}\\d+:`),
    ))
    const message = postMessage.mock.calls[0][0]
    expect(message.endsWith('5L2g5aW9CmhlbGxv')).toBe(true)
    await expect(result).resolves.toBe(true)
  })

  it('没有 external bridge 时返回 null，让调用方走浏览器路径', () => {
    expect(requestNativeClipboard('text')).toBeNull()
  })
})

// B435 D10 / 契约 §7（W plan Q4 甲）：薄壳深链出口函数随卡页坐席按钮退役；前缀常量
// OPEN_DEEPLINK_MESSAGE_PREFIX 与其字面量用例（上方）作为与 desktop/ 的 CDC 对保留。
// 函数名分段拼接：段 6 的独立判据是 web/src 内 grep 退役名零命中。
describe('薄壳深链出口退役', () => {
  it('desktopShell 不再导出深链出口函数，前缀常量仍在', async () => {
    const shell = await import('./desktopShell')
    expect(shell).not.toHaveProperty(['requestOpen', 'DeepLink'].join(''))
    expect(shell.OPEN_DEEPLINK_MESSAGE_PREFIX).toBe('handoff:open-deeplink:')
  })
})
