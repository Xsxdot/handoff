// useHomeDock —— home 基准终端的状态。
//
// 职责：持有一组 home 浮窗 tab（终端或临时文件）、当前激活项、浮窗的开合与几何。
//
// 边界（这是它存在的全部理由）：
//   - **与 useWorkbench 完全独立**。home 终端不挂在任何目录上，而中央工作区的
//     tab 组是按「当前选中目录」组织的（byBase 那张 Map）。把 home 塞进去，
//     它就会跟着目录切换走——「我刚才那个 ssh 到 devbox 的终端去哪了」正是
//     这么来的
//   - 不碰 DOM、不发任何请求。建会话是 TerminalTab 的事，杀会话由调用方在
//     closeTab 前后自己做（见计划 Global 的第 3 条既有事实）

import { useCallback, useRef, useState } from 'react'

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
  adopt: (t: HomeTab) => void // 恢复既有会话时把它收进来
}

const INITIAL_GEOM = { x: 320, y: 140, w: 620, h: 340 }

export function useHomeDock(): HomeDockApi {
  const [tabs, setTabs] = useState<HomeTab[]>([])
  const [activeId, setActiveId] = useState<string | null>(null)
  const [windowOpen, setWindowOpen] = useState(false)
  const [geom, setGeomState] = useState(INITIAL_GEOM)

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
    setWindowOpen(true)
  }, [])

  const newFile = useCallback((rel: string) => {
    const id = `h${++tabIdCounter.current}`
    const tab: HomeTab = { id, kind: 'file', rel, seq: ++seqCounter.current, machine: '' }
    setTabs((prev) => [...prev, tab])
    setActiveId(id)
    setWindowOpen(true)
  }, [])

  const activate = useCallback((id: string) => {
    setActiveId(id)
    setWindowOpen(true)
  }, [])

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
    setGeomState((prev) => {
      const next = { ...prev, ...g }
      // 下界钳制：浮窗缩到比内容还小就没意义了
      next.w = Math.max(360, next.w)
      next.h = Math.max(200, next.h)
      next.x = Math.max(8, next.x)
      next.y = Math.max(8, next.y)
      return next
    })
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
    adopt,
  }
}
