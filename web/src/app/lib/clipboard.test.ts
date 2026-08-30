// copyToClipboard 的行为契约：
//   - execCommand('copy') 可用时同步走它（桌面壳 WKWebView 里 writeText 会被拒，
//     且拒绝是异步的、届时手势已过期，兜底必须同步先行）
//   - execCommand 不可用（jsdom / 未来被移除）或返回 false 时退回 writeText
//   - writeText 拒绝时不抛未处理 rejection——复制保持尽力而为
import { afterEach, describe, expect, it, vi } from 'vitest'
import { copyToClipboard } from './clipboard'
import { NATIVE_CLIPBOARD_MESSAGE_PREFIX, NATIVE_CLIPBOARD_RESULT_EVENT } from './desktopShell'

afterEach(() => {
  vi.restoreAllMocks()
  // jsdom 的 document.execCommand 本来不存在，测试里挂上去的要清掉
  delete (document as { execCommand?: unknown }).execCommand
  delete (window as unknown as { webkit?: unknown }).webkit
})

describe('copyToClipboard', () => {
  it('桌面宿主桥成功时使用原生剪贴板，不碰浏览器 API', async () => {
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })
    const postMessage = vi.fn((message: string) => {
      const requestID = message.slice(NATIVE_CLIPBOARD_MESSAGE_PREFIX.length).split(':', 1)[0]
      window.dispatchEvent(new CustomEvent(NATIVE_CLIPBOARD_RESULT_EVENT, {
        detail: { requestId: requestID, ok: true },
      }))
    })
    ;(window as unknown as { webkit?: unknown }).webkit = { messageHandlers: { external: { postMessage } } }

    await expect(copyToClipboard('native text')).resolves.toBe(true)

    expect(postMessage).toHaveBeenCalledTimes(1)
    expect(writeText).not.toHaveBeenCalled()
  })

  it('execCommand 成功时同步复制，不再调用 writeText', async () => {
    let selected = ''
    ;(document as { execCommand?: unknown }).execCommand = vi.fn(() => {
      // execCommand('copy') 复制的是当前选区；jsdom 里 select() 不动 activeElement，
      // 用「此刻文档里的临时 textarea」代替选区来断言内容
      const el = document.querySelector('textarea')
      if (el) selected = el.value
      return true
    })
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

    await expect(copyToClipboard('/some/abs/path')).resolves.toBe(true)

    expect(selected).toBe('/some/abs/path')
    expect(writeText).not.toHaveBeenCalled()
    // 临时 textarea 不能残留在文档里
    expect(document.querySelector('textarea')).toBeNull()
  })

  it('execCommand 不可用时退回 writeText', () => {
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

    void copyToClipboard('rel/path')

    expect(writeText).toHaveBeenCalledWith('rel/path')
  })

  it('execCommand 返回 false 时退回 writeText', () => {
    ;(document as { execCommand?: unknown }).execCommand = vi.fn(() => false)
    const writeText = vi.fn(() => Promise.resolve())
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

    void copyToClipboard('x')

    expect(writeText).toHaveBeenCalledWith('x')
  })

  it('writeText 拒绝时返回失败且无未处理 rejection', async () => {
    const writeText = vi.fn(() => Promise.reject(new DOMException('denied', 'NotAllowedError')))
    vi.stubGlobal('navigator', { ...navigator, clipboard: { writeText } })

    await expect(copyToClipboard('y')).resolves.toBe(false)
    // 让微任务里的 rejection 落定；未捕获的话 vitest 会把它记为测试失败
    await new Promise((r) => setTimeout(r, 0))
  })

  it('navigator.clipboard 不存在时返回失败而不抛错', async () => {
    vi.stubGlobal('navigator', { ...navigator, clipboard: undefined })
    await expect(copyToClipboard('z')).resolves.toBe(false)
  })
})
