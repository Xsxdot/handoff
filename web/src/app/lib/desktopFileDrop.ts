// desktopFileDrop —— 把桌面薄壳的原生拖放交来的**真实文件路径**分发给页面。
//
// 职责：在薄壳里接住「用户把文件从访达拖进窗口」这个动作，按落点找到愿意接收的
//       区域（目前只有终端），把路径交给它。浏览器里整个模块是空转。
// 边界：
//   - 不决定拿到路径之后干什么。插到哪、怎么转义，是接收方（TerminalTab）的事
//   - 不碰 HTML5 的 drag/drop 事件。那条路在这里**根本没用**，原因见下
//
// ── 为什么必须走原生通道 ──
// WKWebView 里 HTML5 拖放本身是通的：`dataTransfer.files` 能拿到 File 对象，
// name 与 size 都对。但**拿不到文件系统路径**——实测 `file.path` 是 undefined、
// `text/uri-list` 是空串（Electron 才有 `File.path` 那个非标准扩展）。而终端要的
// 恰恰是路径，不是文件内容。所以网页层无论怎么写都办不到这件事，只能由原生层给。
//
// ── 这条通道长什么样 ──
// Wails 在窗口上注册了一个 NSDraggingDestination 覆盖层（需要窗口选项
// `EnableFileDrop: true`，见 desktop/main.go），落盘后由 Go 侧执行一句 JS：
//     window._wails.handlePlatformFileDrop(filenames, x, y)
// 官方设计里这个函数由前端 bundle 的 `@wailsio/runtime` npm 包提供，它会找
// `data-file-drop-target` 元素、再把结果**发回 Go**。这套对控制台不成立：
//   - 控制台是 agentd 伺服的**外链页面**，Wails 注入的只有 runtime core，
//     实测 `window._wails` 是 object 但 `handlePlatformFileDrop` 是 undefined；
//   - 就算把 npm 包引进来，它回发 Go 的请求打的是页面自己的 origin（agentd），
//     根本到不了 Wails 的运行时端点。
// 所以这里**自己实现那个回调**：路径已经在参数里了，压根不需要回 Go 一趟。
//
// 承重：`window._wails` 是导航完成之后才注入的（实测：load 时 undefined，
// 约 1s 后变成 object），所以安装要轮询等它出现，不能只在模块加载时试一次。
import { isDesktopShell } from './desktopShell'

// FileDropTarget 是一个愿意接收拖入文件的区域。
export interface FileDropTarget {
  // host 用来判定落点：只有落在这个元素（或其后代）上的拖放才交给它。
  host: HTMLElement
  // accept 收到该区域上的一次拖放，参数是绝对路径列表（至少一个）。
  accept: (paths: string[]) => void
}

// WailsInternal 是 Wails 注入的内部对象。这里只用到一个我们自己写进去的键。
interface WailsInternal {
  handlePlatformFileDrop?: (filenames: string[], x: number, y: number) => void
}

// targets 是当前挂载着的接收区。用数组而不是单值：将来若有第二种接收区
//（比如把文件拖进某个表单），落点判定天然就能区分，不必改协议。
const targets: FileDropTarget[] = []

// installed 防止重复安装。轮询每次都要检查它——`window._wails` 被重新注入时
//（重新导航）我们的函数会被连带冲掉，那时需要重新装一次。
let installTimer: ReturnType<typeof setInterval> | null = null

// dispatch 是我们塞进 Wails 内部对象的那个回调。
//
// 参数：filenames 是绝对路径；x/y 是落点在页面坐标系里的位置（原生层已经把
// AppKit 的左下原点换算成左上原点，单位是点，与 CSS px 一致）。
function dispatch(filenames: string[], x: number, y: number): void {
  if (filenames.length === 0) return
  const el = document.elementFromPoint(x, y)
  if (el === null) return
  const hit = targets.find((t) => t.host === el || t.host.contains(el))
  if (!hit) return
  hit.accept(filenames)
}

// ensureInstalled 把 dispatch 挂到 `window._wails` 上，挂不上就继续等。
//
// 注意：这里**不**创建 `window._wails`。Wails 注入 runtime core 时会整体赋值
// 那个对象，我们先建一个只会被覆盖掉；等它出现再往里加键才是稳的。
function ensureInstalled(): void {
  const w = (window as unknown as { _wails?: WailsInternal })._wails
  if (!w) return
  if (w.handlePlatformFileDrop === dispatch) return
  w.handlePlatformFileDrop = dispatch
}

// registerFileDropTarget 登记一个接收区，返回注销函数。
//
// 在浏览器里调用是安全的空操作（返回的注销函数同样可调），调用方不必自己
// 分辨运行环境。
export function registerFileDropTarget(target: FileDropTarget): () => void {
  if (!isDesktopShell()) return () => {}
  targets.push(target)
  if (installTimer === null) {
    ensureInstalled()
    // 200ms 一次地盯着：既覆盖「运行时还没注入」的启动窗口，也覆盖
    // 「重新导航后运行时被重新注入、我们的键被冲掉」。代价可以忽略。
    installTimer = setInterval(ensureInstalled, 200)
  }
  return () => {
    const i = targets.indexOf(target)
    if (i >= 0) targets.splice(i, 1)
    if (targets.length === 0 && installTimer !== null) {
      clearInterval(installTimer)
      installTimer = null
    }
  }
}

// SAFE_UNQUOTED 是无需转义即可直接放进 shell 命令行的字符集。
// 与 POSIX 的惯例一致（Python shlex.quote 用的也是这一套）。
const SAFE_UNQUOTED = /^[A-Za-z0-9_@%+=:,./-]+$/

// shellQuote 把一个路径转成可以安全粘进 shell 命令行的形式。
//
// 参数：path 为绝对路径原文。
// 返回：可直接拼进命令行的串。
//
// 用单引号而不是双引号：单引号里 shell 不做任何展开，`$`、反引号、`\` 都是
// 字面量——把带 `$(...)` 的文件名拖进终端不该变成一次命令执行。单引号本身
// 无法在单引号里转义，只能断开再补一个转义过的单引号，即 `'\''`。
export function shellQuote(path: string): string {
  if (path !== '' && SAFE_UNQUOTED.test(path)) return path
  return `'${path.split("'").join(`'\\''`)}'`
}
