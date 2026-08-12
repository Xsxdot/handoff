// useWorkbench —— 中央工作台的状态容器。
//
// 职责：
//   - 持有「当前基准目录」这一唯一的全局选中态（spec §1.2）
//   - 按基准目录分别持有 tab 组，切目录时整组一起换、切回来原样恢复
//   - 把 tabs.ts 的纯函数包成组件层能直接调的动作
//
// 边界：
//   - 不发请求、不认识 ProjectTree 的数据形状：调用方把选中的目录整理成
//     BaseDir 传进来
//   - 不做持久化。tab 组存内存，刷新即丢（spec §10）——持久化要处理
//     「目录被删了但 tab 还在」这类失效态，本期不值得
//
// 为什么按目录分别持有而不是一份全局 tab 列表：一份全局列表切目录时要么
// 全清空（用户丢工作现场），要么混在一起（左栏选了 A 却看见 B 的文件）。
// 按目录分持是唯一不产生第三种状态的做法。
import { useCallback, useRef, useState } from 'react'
import {
  EMPTY_WORKBENCH,
  activateTab,
  closeTab,
  nextTerminalSeq,
  openTab,
  setTabContent,
  splitGroup,
  type TabContent,
  type Workbench,
} from './tabs'

// BaseDir 是一个 tab 组的基准目录。
//
// key 是它在 Map 里的身份：工作树用绝对路径，home 用 '~'。
// label 是面包屑与 tab 标题里的短名——工作树优先用分支名（原型显示的是
// `integration/b2-b3` 这样的分支），没有分支（detached）时退回目录名。
export interface BaseDir {
  key: string
  kind: 'workspace' | 'home'
  path: string
  label: string
  projectName: string
  machine: string
}

// HOME_BASE 是悬浮按钮的基准：用户 home。
//
// 它进同一套 tab 系统（本计划的决定 1），所以也需要一个 BaseDir。
// path 是 '~' 而不是真实 home 路径：本期它只用于终端 tab 的标题，
// **不会**被发给任何后端接口——目录列举与读文件的白名单不为它放宽（spec §2.6）。
export const HOME_BASE: BaseDir = {
  key: '~',
  kind: 'home',
  path: '~',
  label: 'home',
  projectName: '',
  machine: '',
}

export interface WorkbenchApi {
  base: BaseDir | null
  wb: Workbench
  select: (b: BaseDir) => void
  open: (c: TabContent, b?: BaseDir) => void
  openTerminal: (b?: BaseDir) => void
  close: (group: number, tabId: string) => void
  activate: (group: number, tabId: string) => void
  setContent: (group: number, tabId: string, c: TabContent) => void
  split: () => void
}

export function useWorkbench(): WorkbenchApi {
  const [base, setBase] = useState<BaseDir | null>(null)
  const [byBase, setByBase] = useState<Record<string, Workbench>>({})
  // baseRef 让 open/openTerminal 在同一个事件里「先切基准再写它的 tab 组」时
  // 读到刚切过去的那个，而不是本次渲染闭包里的旧值
  const baseRef = useRef<BaseDir | null>(null)
  baseRef.current = base

  const wb = base ? (byBase[base.key] ?? EMPTY_WORKBENCH) : EMPTY_WORKBENCH

  const select = useCallback((b: BaseDir) => {
    baseRef.current = b
    setBase(b)
  }, [])

  // mutate 是所有写入的唯一通道：确定目标基准 → 取它的 Workbench → 应用纯函数。
  // 未选中任何基准且调用方也没给一个时，是空操作——静默造一个基准出来会让
  // 「tab 开在哪个目录下」变得不可解释。
  const mutate = useCallback(
    (fn: (w: Workbench) => Workbench, b?: BaseDir) => {
      const target = b ?? baseRef.current
      if (!target) return
      if (b && b.key !== baseRef.current?.key) select(b)
      setByBase((prev) => ({ ...prev, [target.key]: fn(prev[target.key] ?? EMPTY_WORKBENCH) }))
    },
    [select],
  )

  const open = useCallback(
    (c: TabContent, b?: BaseDir) => mutate((w) => openTab(w, c), b),
    [mutate],
  )

  const openTerminal = useCallback(
    (b?: BaseDir) => mutate((w) => openTab(w, { kind: 'terminal', seq: nextTerminalSeq(w) }), b),
    [mutate],
  )

  const close = useCallback((g: number, id: string) => mutate((w) => closeTab(w, g, id)), [mutate])
  const activate = useCallback((g: number, id: string) => mutate((w) => activateTab(w, g, id)), [mutate])
  const setContent = useCallback(
    (g: number, id: string, c: TabContent) => mutate((w) => setTabContent(w, g, id, c)),
    [mutate],
  )
  const split = useCallback(() => mutate(splitGroup), [mutate])

  return { base, wb, select, open, openTerminal, close, activate, setContent, split }
}
