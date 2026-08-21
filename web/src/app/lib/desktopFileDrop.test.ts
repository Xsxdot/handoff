import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { registerFileDropTarget, shellQuote } from './desktopFileDrop'

// 薄壳的判定只看 UA，测试里直接把 UA 换掉即可进入桌面分支。
const DESKTOP_UA = 'Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 handoff-desktop'
const BROWSER_UA = 'Mozilla/5.0 (Macintosh) AppleWebKit/605.1.15 Safari/605.1.15'

function setUA(ua: string): void {
  Object.defineProperty(navigator, 'userAgent', { value: ua, configurable: true })
}

// wails 造一个「Wails 已经注入 runtime core」的现场。真实注入是导航完成
// 之后才发生的，所以默认不建，由用例自己决定什么时候建。
function injectWails(): Record<string, unknown> {
  const w: Record<string, unknown> = {}
  ;(window as unknown as { _wails?: unknown })._wails = w
  return w
}

// drop 模拟原生层那一下：Wails 的 Go 侧就是这样调进来的。
function drop(w: Record<string, unknown>, paths: string[], x: number, y: number): void {
  ;(w.handlePlatformFileDrop as (p: string[], x: number, y: number) => void)(paths, x, y)
}

let cleanup: Array<() => void> = []

beforeEach(() => {
  vi.useFakeTimers()
  delete (window as unknown as { _wails?: unknown })._wails
})

afterEach(() => {
  cleanup.forEach((fn) => fn())
  cleanup = []
  vi.useRealTimers()
  setUA(BROWSER_UA)
  document.body.innerHTML = ''
})

// 落点判定靠 elementFromPoint，jsdom 不做真实布局，桩掉它即可——
// 本模块要测的是「按落点选中哪个接收区」，不是浏览器的命中测试。
function stubElementFromPoint(el: Element | null): void {
  document.elementFromPoint = () => el
}

describe('registerFileDropTarget', () => {
  it('浏览器里是空操作：不碰 window._wails，也不登记任何东西', () => {
    setUA(BROWSER_UA)
    const w = injectWails()
    const accept = vi.fn()
    cleanup.push(registerFileDropTarget({ host: document.createElement('div'), accept }))
    vi.advanceTimersByTime(1000)
    expect(w.handlePlatformFileDrop).toBeUndefined()
  })

  it('运行时晚到也能装上：轮询会等到 window._wails 出现', () => {
    setUA(DESKTOP_UA)
    const host = document.createElement('div')
    document.body.appendChild(host)
    const accept = vi.fn()
    cleanup.push(registerFileDropTarget({ host, accept }))

    // 注入还没发生时，什么都没得挂——这正是「只试一次就放弃」会漏掉的窗口
    vi.advanceTimersByTime(400)
    const w = injectWails()
    expect(w.handlePlatformFileDrop).toBeUndefined()

    vi.advanceTimersByTime(400)
    expect(typeof w.handlePlatformFileDrop).toBe('function')

    stubElementFromPoint(host)
    drop(w, ['/tmp/a.txt'], 10, 20)
    expect(accept).toHaveBeenCalledWith(['/tmp/a.txt'])
  })

  it('运行时被重新注入后会自动补挂回去', () => {
    setUA(DESKTOP_UA)
    const host = document.createElement('div')
    document.body.appendChild(host)
    const accept = vi.fn()
    cleanup.push(registerFileDropTarget({ host, accept }))
    injectWails()
    vi.advanceTimersByTime(300)

    // 重新导航：Wails 整体重新赋值 _wails，我们那个键被冲掉
    const w2 = injectWails()
    expect(w2.handlePlatformFileDrop).toBeUndefined()
    vi.advanceTimersByTime(300)
    expect(typeof w2.handlePlatformFileDrop).toBe('function')

    stubElementFromPoint(host)
    drop(w2, ['/tmp/b.txt'], 1, 1)
    expect(accept).toHaveBeenCalledWith(['/tmp/b.txt'])
  })

  it('落在接收区之外的拖放不交给任何人', () => {
    setUA(DESKTOP_UA)
    const host = document.createElement('div')
    const elsewhere = document.createElement('div')
    document.body.append(host, elsewhere)
    const accept = vi.fn()
    cleanup.push(registerFileDropTarget({ host, accept }))
    const w = injectWails()
    vi.advanceTimersByTime(300)

    stubElementFromPoint(elsewhere)
    drop(w, ['/tmp/a.txt'], 5, 5)
    expect(accept).not.toHaveBeenCalled()
  })

  it('落在接收区的后代元素上也算命中（终端内部是嵌套的）', () => {
    setUA(DESKTOP_UA)
    const host = document.createElement('div')
    const inner = document.createElement('canvas')
    host.appendChild(inner)
    document.body.appendChild(host)
    const accept = vi.fn()
    cleanup.push(registerFileDropTarget({ host, accept }))
    const w = injectWails()
    vi.advanceTimersByTime(300)

    stubElementFromPoint(inner)
    drop(w, ['/tmp/a.txt', '/tmp/b.txt'], 5, 5)
    expect(accept).toHaveBeenCalledWith(['/tmp/a.txt', '/tmp/b.txt'])
  })

  it('注销之后不再收到拖放', () => {
    setUA(DESKTOP_UA)
    const host = document.createElement('div')
    document.body.appendChild(host)
    const accept = vi.fn()
    const off = registerFileDropTarget({ host, accept })
    const w = injectWails()
    vi.advanceTimersByTime(300)
    off()

    stubElementFromPoint(host)
    drop(w, ['/tmp/a.txt'], 5, 5)
    expect(accept).not.toHaveBeenCalled()
  })

  it('空文件列表不触发接收方', () => {
    setUA(DESKTOP_UA)
    const host = document.createElement('div')
    document.body.appendChild(host)
    const accept = vi.fn()
    cleanup.push(registerFileDropTarget({ host, accept }))
    const w = injectWails()
    vi.advanceTimersByTime(300)

    stubElementFromPoint(host)
    drop(w, [], 5, 5)
    expect(accept).not.toHaveBeenCalled()
  })
})

describe('shellQuote', () => {
  it('干净的路径原样不动，不给命令行添噪音', () => {
    expect(shellQuote('/Users/dev/handoff/README.md')).toBe('/Users/dev/handoff/README.md')
  })

  it('带空格的路径加单引号', () => {
    expect(shellQuote('/Users/dev/My Docs/a.txt')).toBe("'/Users/dev/My Docs/a.txt'")
  })

  it('带单引号的路径断开再补一个转义过的单引号', () => {
    expect(shellQuote("/tmp/it's.txt")).toBe("'/tmp/it'\\''s.txt'")
  })

  // 这条是安全断言而不是格式断言：文件名里的 $(...)、反引号、$VAR 落进
  // 命令行后绝不能被 shell 展开——单引号里 shell 不做任何展开。
  it('文件名里的命令替换与变量被单引号封死，不会变成一次执行', () => {
    expect(shellQuote('/tmp/$(rm -rf ~).txt')).toBe("'/tmp/$(rm -rf ~).txt'")
    expect(shellQuote('/tmp/`id`.txt')).toBe("'/tmp/`id`.txt'")
    expect(shellQuote('/tmp/$HOME.txt')).toBe("'/tmp/$HOME.txt'")
  })

  it('空串也要引起来，否则会在命令行上凭空消失', () => {
    expect(shellQuote('')).toBe("''")
  })
})
