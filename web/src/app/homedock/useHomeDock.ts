// useHomeDock —— home 基准终端的状态。
//
// 职责：持有一组 home 浮窗 tab（终端或临时文件）、当前激活项、浮窗的开合与几何。
//
// 边界（这是它存在的全部理由）：
//   - **与 useWorkbench 完全独立**。home 终端不挂在任何目录上，而中央工作区的
//     tab 组是按「当前选中目录」组织的（byBase 那张 Map）。把 home 塞进去，
//     它就会跟着目录切换走——「我刚才那个 ssh 到 devbox 的终端去哪了」正是
//     这么来的
//   - 不发任何请求。建会话是 TerminalTab 的事，杀会话由调用方在 closeTab
//     前后自己做（见计划 Global 的第 3 条既有事实）
//   - 只在挂载时读一次视口尺寸（window.innerWidth/Height）来摆浮窗初始位置，
//     除此之外不碰 DOM。这一次读是必要的：浮窗要贴着右下角那颗悬浮球开，
//     而「右下角在哪」只有视口知道

import { useCallback, useRef, useState } from 'react'
import { topInset } from '../lib/desktopShell'

export interface HomeTab {
  id: string // 客户端生成的 tab 身份（不是服务端 sessionId）
  kind: 'terminal' | 'file'
  seq: number // 第几个浮窗 tab，用于终端标题 'bash · home N'
  sessionId?: string // 服务端会话 id；建成之前是 undefined
  machine: string // '' = 本机
  rel?: string // file tab 在 scratch 根下的相对路径
  draft?: string // file tab 的未保存内容；不设表示组件尚未改动
  baseSha?: string // draft 对应的服务端版本
}

export interface HomeDockApi {
  tabs: HomeTab[]
  activeId: string | null
  windowOpen: boolean
  geom: { x: number; y: number; w: number; h: number }
  newTerminal: (machine?: string) => void // 建 tab、激活它、打开浮窗
  // newFile 收进一个已经由调用方建好的 scratch 文件，不在 hook 内发请求。
  newFile: (rel: string) => void
  activate: (id: string) => void // 激活并打开浮窗
  collapse: () => void // 收起浮窗，**不动 tabs**
  closeTab: (id: string) => void // 从列表移除；杀会话由调用方负责
  setSession: (id: string, sessionId: string) => void
  // setDraft 把文件 tab 的草稿寄存在 tab 上；null 清除草稿快照。
  setDraft: (id: string, d: { draft: string; baseSha: string } | null) => void
  setGeom: (g: Partial<{ x: number; y: number; w: number; h: number }>) => void
  // maximized = 浮窗正铺满视口。此时 geom 仍是「还原后回到哪」的记忆，不被改写。
  maximized: boolean
  toggleMaximize: () => void
  adopt: (t: HomeTab) => void // 恢复既有会话时把它收进来
}

// 悬浮球（HomeDock 的 FAB）在视口里的位置与尺寸，单位 px。
// 与 HomeDock.tsx 上那三个 Tailwind 类一一对应：right-5 / bottom-11 / size-11。
// 改那边的类要同步改这里，否则浮窗会贴不住球。
const FAB_RIGHT = 20
const FAB_BOTTOM = 44
const FAB_SIZE = 44
// FAB_GAP 是浮窗下沿与悬浮球上沿之间留的缝：贴到 0 会看起来像粘住了。
const FAB_GAP = 8
// MARGIN 是浮窗与视口边缘的最小间距，同时是 x / y 的下界。
const MARGIN = 8
// 尺寸下界：缩到比内容还小就没意义了。
const MIN_W = 360
const MIN_H = 200

// defaultGeom 算浮窗的初始几何：**面积占视口四分之一**（宽高各取一半），
// 右下角贴着悬浮球——右沿与球右沿对齐，下沿在球正上方留 FAB_GAP。
//
// 为什么是「贴着球」而不是屏幕中央：浮窗是从右下角那颗球里长出来的，开在别处
// 会让人找不到自己刚点的东西和它的关系（走查里的原话：「在偏左上区域打开，很奇怪」）。
//
// 参数 vw/vh 是视口宽高；inset 是页面顶部要让出的高度（桌面薄壳的拖动区，
// 浏览器里为 0）——浮窗是 fixed 定位，不受根容器 padding 约束，得自己让。
//
// 边界：视口小到装不下半屏时，宽高各自被 MIN_* 与「视口减两倍边距」夹住；
// 夹紧后可能贴不住球（x/y 被顶到下界），此时保证不出屏优先。
export function defaultGeom(vw: number, vh: number, inset = 0): { x: number; y: number; w: number; h: number } {
  const w = Math.max(MIN_W, Math.min(Math.round(vw / 2), vw - MARGIN * 2))
  const h = Math.max(MIN_H, Math.min(Math.round(vh / 2), vh - inset - MARGIN * 2))
  return {
    x: Math.max(MARGIN, vw - FAB_RIGHT - w),
    y: Math.max(inset + MARGIN, vh - FAB_BOTTOM - FAB_SIZE - FAB_GAP - h),
    w,
    h,
  }
}

// initialGeom 按**此刻**的视口算初始几何。
//
// 承重：必须在「浮窗第一次打开」那一刻调用，不能在 hook 挂载时算。
// 挂载时页面很可能还没定稿——实测控制台首帧的 innerWidth 只有几百 px，
// 于是浮窗被最小尺寸兜成 360×200 钉在左上角，正是要修的那个毛病；用户在
// 打开浮窗之前调整过窗口大小也是同一类。
//
// 只算这一次：浮窗一旦被摆过，位置就是用户的意思了，之后随窗口 resize 弹回去
// 是抢方向盘。视口取不到（SSR）时退回一个常数，不抛错。
function initialGeom(): { x: number; y: number; w: number; h: number } {
  const FALLBACK = { x: 320, y: 140, w: 620, h: 340 }
  if (typeof window === 'undefined') return FALLBACK
  // innerWidth/Height 在页面尚未定稿时可能是 0（实测：内嵌 webview 刚 reload 完
  // 的那一帧就是 0/0），documentElement.clientWidth 是同一时刻更靠谱的读数。
  // 两个都取不到就退回常数——摆错位置也好过摆成一个 360×200 的最小窗
  const vw = window.innerWidth || document.documentElement.clientWidth
  const vh = window.innerHeight || document.documentElement.clientHeight
  if (vw <= 0 || vh <= 0) return FALLBACK
  return defaultGeom(vw, vh, topInset())
}

export function useHomeDock(): HomeDockApi {
  const [tabs, setTabs] = useState<HomeTab[]>([])
  const [activeId, setActiveId] = useState<string | null>(null)
  const [windowOpen, setWindowOpen] = useState(false)
  const [geom, setGeomState] = useState(initialGeom)
  const [maximized, setMaximized] = useState(false)
  // placed = 浮窗已经有过一个「按真实视口算出来的」位置。
  // 首次打开时才置位，之后不再自动摆——见 initialGeom 的承重说明
  const placed = useRef(false)

  // openWindow 打开浮窗，并在**第一次**打开时按当下视口把它摆到悬浮球旁边。
  const openWindow = useCallback(() => {
    if (!placed.current) {
      placed.current = true
      setGeomState(initialGeom())
    }
    setWindowOpen(true)
  }, [])

  // seqCounter 是只增不减的计数器，跨 closeTab 存活。
  //
  // 为什么不用 tabs.length + 1：关掉中间一个再新建就会撞号——列表是 [1, 2, 3]
  // 关掉 2 后长度是 2，新建的 seq 就又是 3，页面里会出现两个 'home 3'。
  const seqCounter = useRef(0)
  // tabIdCounter 生成客户端 tab 身份，同理只增不减保证唯一
  const tabIdCounter = useRef(0)

  const newTerminal = useCallback((machine?: string) => {
    const id = `h${++tabIdCounter.current}`
    const tab: HomeTab = { id, kind: 'terminal', seq: ++seqCounter.current, machine: machine ?? '' }
    setTabs((prev) => [...prev, tab])
    setActiveId(id)
    openWindow()
  }, [openWindow])

  const newFile = useCallback((rel: string) => {
    const id = `h${++tabIdCounter.current}`
    const tab: HomeTab = { id, kind: 'file', rel, seq: ++seqCounter.current, machine: '' }
    setTabs((prev) => [...prev, tab])
    setActiveId(id)
    openWindow()
  }, [openWindow])

  const activate = useCallback((id: string) => {
    setActiveId(id)
    openWindow()
  }, [openWindow])

  // collapse 只收起浮窗，绝不碰 tabs。
  //
  // 为什么不动 tabs：收起 ≠ 关闭，会话在服务端活着。把 tab 从列表拿掉就等于
  // 通知调用方去杀会话，那浮窗的『最小化』就和『关掉』没有区别了。
  const collapse = useCallback(() => {
    setWindowOpen(false)
  }, [])

  const closeTab = useCallback((id: string) => {
    setTabs((prev) => {
      const idx = prev.findIndex((t) => t.id === id)
      if (idx === -1) return prev
      const next = prev.filter((t) => t.id !== id)
      if (next.length === 0) {
        setWindowOpen(false)
        setActiveId(null)
      } else if (prev[idx]?.id === activeId) {
        // 删的是激活项：激活权交给剩下的第一个
        setActiveId(next[0].id)
      }
      return next
    })
  }, [activeId])

  const setSession = useCallback((id: string, sessionId: string) => {
    setTabs((prev) => prev.map((t) => (t.id === id ? { ...t, sessionId } : t)))
  }, [])

  const setDraft = useCallback((id: string, d: { draft: string; baseSha: string } | null) => {
    setTabs((prev) =>
      prev.map((t) =>
        t.id === id
          ? d === null
            ? { ...t, draft: undefined, baseSha: undefined }
            : { ...t, draft: d.draft, baseSha: d.baseSha }
          : t,
      ),
    )
  }, [])

  const setGeom = useCallback((g: Partial<{ x: number; y: number; w: number; h: number }>) => {
    // 用户亲手摆过就算「已定位」：之后再打开不能把他摆的位置冲掉
    placed.current = true
    setGeomState((prev) => {
      const next = { ...prev, ...g }
      // 下界钳制：浮窗缩到比内容还小就没意义了
      next.w = Math.max(MIN_W, next.w)
      next.h = Math.max(MIN_H, next.h)
      next.x = Math.max(MARGIN, next.x)
      // y 的下界要加上顶部让位：薄壳里前 28px 是窗口拖动区，浮窗标题栏拖进去
      // 就再也抓不住了（点击被 AppKit 吃掉）
      next.y = Math.max(topInset() + MARGIN, next.y)
      return next
    })
  }, [])

  // toggleMaximize 在「铺满视口」与「回到 geom」之间切换。
  //
  // 刻意不写 geom：还原时要回到用户自己摆的位置，把 geom 改成全屏几何就回不去了。
  const toggleMaximize = useCallback(() => {
    setMaximized((m) => !m)
  }, [])

  // adopt 把既有会话收进 tabs，但不改 windowOpen、不改 activeId（activeId 为
  // null 时可以设成它）。
  //
  // 为什么不打开浮窗：恢复是后台动作。页面加载时一批会话在后台收编，把它们
  // 逐条弹出来会把用户挡在浮窗外面——恢复不该替用户把浮窗拉起来。
  const adopt = useCallback((t: HomeTab) => {
    setTabs((prev) => {
      if (prev.some((x) => x.id === t.id)) return prev
      return [...prev, t]
    })
    setActiveId((prev) => prev ?? t.id)
  }, [])

  return {
    tabs,
    activeId,
    windowOpen,
    geom,
    newTerminal,
    newFile,
    activate,
    collapse,
    closeTab,
    setSession,
    setDraft,
    setGeom,
    maximized,
    toggleMaximize,
    adopt,
  }
}
