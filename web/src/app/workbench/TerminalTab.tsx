// TerminalTab —— 中央区的真终端（W4 PTY spec §6）。
//
// 职责：
//   - 挂 xterm，把一个服务端 PTY 会话的字节流画出来
//   - 没有会话时先建一个，并把 id 回报给 tab（onSession）
//   - 按键上送、尺寸上送、断线重连（重连逻辑在 api/pty.ts，这里只消费）
//   - shell 退出后在下方显示退出码，tab 留着等用户自己关
//
// 边界：
//   - **不删会话**。卸载只断 WS——切 tab、切基准目录、关页面都不该杀掉
//     跑了一晚上的 build（spec §6.2）。删会话是 × 按钮的事，在 Shell 里
//   - 不做重连退避、不认识 WS 帧格式：那都在 api/pty.ts
//   - 不判断这台机器支不支持 PTY：那是 Shell 的降级门（Task 14）。
//     这里只兜住「真发了请求才知道不支持」的那一路（501）
//
// 关于「切 tab 就重放整段回放」：WorkbenchPage 只渲染激活 tab，切走即卸载，
// 游标随之丢失，切回来从 since=0 起重放整个环形缓冲。这是**有意**的——
// 环形缓冲的存在就是为了让任何一次重新接入都能重建屏幕，256 KiB 的重放
// 远比维护一份前端的「上次看到哪」更可靠。
import { useEffect, useRef, useState } from 'react'
import { TerminalSquare } from 'lucide-react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import { WebglAddon } from '@xterm/addon-webgl'
import '@xterm/xterm/css/xterm.css'
import { createPtySession, deletePtySession } from '../../api/client'
import { connectPty, type PtyHandle } from '../../api/pty'
import type { BaseDir } from './useWorkbench'

export interface TerminalTabProps {
  base: BaseDir
  seq: number
  // sessionId 缺席 = 这个 tab 还没有会话，挂载时建一个。
  sessionId?: string
  // rel 是终端要起的工作树子目录；空串/缺席 = 工作树根。
  rel?: string
  // onSession 把新建会话的 id 交回上层写进 TabContent。必须回报：
  // 不回报的话切一次 tab 就会再建一个会话，用户每切一次多留一个 shell。
  onSession: (id: string) => void
}

// ptyBase 把一个基准目录翻译成建会话请求的两个字段。
//
// home 基准的 path 是字面量 '~'，**不是**服务端认识的路径（useWorkbench 里
// 早有这条纪律）。base_kind=home 时服务端用它自己的 $HOME，所以这里发空串，
// 免得将来有人把 '~' 当路径去 stat。
//
// rel 只在 workspace 基准确有语义：home 的 cwd 由服务端决定，不往上带。
// rel 为空/undefined 时返回的对象与历史形态**逐字节一致**（不加 rel 键），
// 建会话的既有断言与行为不得受影响。
function ptyBase(base: BaseDir, rel?: string): { base_kind: string; base_path: string; rel?: string } {
  const out: { base_kind: string; base_path: string; rel?: string } = {
    base_kind: base.kind,
    base_path: base.kind === 'home' ? '' : base.path,
  }
  if (rel && base.kind === 'workspace') out.rel = rel
  return out
}

export function TerminalTab({ base, seq, sessionId, rel, onSession }: TerminalTabProps) {
  const hostRef = useRef<HTMLDivElement>(null)
  const [error, setError] = useState<string | null>(null)
  // exit 为 undefined 表示还活着；已退出时它是退出码（对端没给退出码时是 null）
  const [exit, setExit] = useState<number | null | undefined>(undefined)
  const [status, setStatus] = useState<'connecting' | 'open' | 'closed'>('connecting')

  useEffect(() => {
    const host = hostRef.current
    if (!host) return
    let disposed = false
    let handle: PtyHandle | null = null

    // 终端底色固定为深色：xterm 的 WebGL 渲染器不支持透明背景，跟着页面主题
    // 走会在浅色主题下拿到一块透不过去的白底。终端惯例本就是深色，不折腾。
    const term = new Terminal({
      fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace',
      fontSize: 12,
      cursorBlink: true,
      scrollback: 5000,
      theme: { background: '#0b0b0c' },
    })
    const fit = new FitAddon()
    term.loadAddon(fit)
    term.open(host)
    // WebGL 渲染器有两条失效路径，两条都不能白屏（spec §6.3）：
    //   1. 构造期不可用（远程桌面、禁用硬件加速、老显卡）——构造直接抛，
    //      catch 住即可，xterm 用内建 DOM 渲染器继续。
    //   2. 运行期上下文丢失（GPU 复位、驱动重启、浏览器驱逐 WebGL 上下文——
    //      终端 tab 开多了就够得着）。这条 try/catch **管不着**：addon 已经
    //      挂上去了，不 dispose 它就留下一个死渲染器，画面永久停住，控制台
    //      刷 `dimensions` 的 TypeError。所以必须注册 onContextLoss 主动摘除。
    try {
      const webgl = new WebglAddon()
      webgl.onContextLoss(() => {
        console.warn('WebGL 上下文丢失，已摘除 WebGL 渲染器回退到 DOM 渲染（会慢一些，但画面继续）')
        webgl.dispose()
      })
      term.loadAddon(webgl)
    } catch (err) {
      console.warn('WebGL 渲染器不可用，已回退到 DOM 渲染', err)
    }
    fit.fit()

    const start = async () => {
      let id = sessionId
      if (!id) {
        const created = await createPtySession(
          { ...ptyBase(base, rel), cols: term.cols, rows: term.rows },
          base.machine,
        )
        if (disposed) {
          // 会话已在服务端建成（shell 已 fork），但 id 从没回报给上层——
          // 界面上没有任何入口能连上它或杀掉它，而 ptyhost 只在 shell 退出时
          // 回收、没有空闲清扫，不删就是一个永远挂着的孤儿。
          //
          // 这跟「卸载不删会话」的纪律不冲突：那条护的是**已回报**的会话
          // （tab 里记着 id，切回来还能接上）。这里的会话没人知道它存在。
          void deletePtySession(created.id, base.machine).catch((err: unknown) => {
            console.warn('回收孤儿终端会话失败，服务端可能残留一个 shell', created.id, err)
          })
          return
        }
        id = created.id
        onSession(id)
      }
      if (disposed) return
      handle = connectPty({
        sessionId: id,
        machine: base.machine,
        onAttached: ({ truncated }) => {
          // 服务端说中间丢了一段：屏幕上现有的内容与即将到来的回放接不上，
          // 不清就会把同一段输出画两遍
          if (truncated) term.clear()
        },
        onData: (bytes) => term.write(bytes),
        onExit: (code) => {
          setExit(code ?? null)
          setStatus('closed')
        },
        onStatus: setStatus,
        onError: (message) => setError(message),
        onTerminal: ({ message }) => setError(message),
      })
      term.onData((d) => handle?.send(new TextEncoder().encode(d)))
      term.onResize(({ cols, rows }) => handle?.resize(cols, rows))
      term.focus()
    }

    start().catch((err: unknown) => {
      if (disposed) return
      setError(err instanceof Error ? err.message : String(err))
    })

    const ro = new ResizeObserver(() => fit.fit())
    ro.observe(host)

    return () => {
      disposed = true
      ro.disconnect()
      // 只断连接，不发 DELETE：服务端会话继续跑
      handle?.close()
      term.dispose()
    }
    // 依赖故意只有会话身份与基准：base.label 之类的展示字段变化不该重建终端。
    // rel 参与身份：改 rel 就该在新的子目录里重建会话。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [sessionId, base.key, base.machine, rel])

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
        <TerminalSquare className="size-3.5" />
        <span className="font-mono">
          {base.label}
          {seq > 1 && ` (${seq})`}
        </span>
        {status === 'connecting' && exit === undefined && <span>连接中…</span>}
        <span className="ml-auto font-mono">{base.path}</span>
      </div>
      <div ref={hostRef} className="min-h-0 flex-1 bg-[#0b0b0c]" />
      {error !== null && (
        <div className="border-t px-3 py-1.5 text-xs text-destructive">{error}</div>
      )}
      {exit !== undefined && (
        <div className="border-t px-3 py-1.5 text-xs text-muted-foreground">
          {exit === null ? 'shell 已退出（对端未给出退出码）' : `shell 已退出，退出码 ${exit}`}
          ．关闭这个 tab 即可清理
        </div>
      )}
    </div>
  )
}
