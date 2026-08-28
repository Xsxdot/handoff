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
//   - 不由本 hook 直接做持久化。tab 组存内存，落盘与恢复由外部同步层处理；
//     这样容器不需要认识 HTTP 或坏 payload 的降级策略
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
  resizeGroups,
  setTabContent,
  isAlreadyOpen,
  splitGroup,
  splitGroupAt,
  type TabContent,
  type Workbench,
} from './tabs'

// BaseDir 是一个 tab 组的基准目录。
//
// key 是它在 Map 里的身份：工作树用 path 或 path@machine，home 用 '~' / '~@machine'。
// label 是面包屑与 tab 标题里的短名——工作树优先用分支名（原型显示的是
// `integration/b2-b3` 这样的分支），没有分支（detached）时退回目录名。
export interface BaseDir {
  key: string
  // kind 三种：workspace 是 git 工作树，home 只用于浮窗终端，scratch 是 agentd
  // 的草稿区，只被浮窗里的 file tab 用来发文件请求。
  //
  // scratch 刻意不伪装成 workspace：它不是 git 工作树，不该被左栏选中、也不该
  // 有右栏文件树。把它标成 workspace 是一句半年后会骗到人的谎。
  kind: 'workspace' | 'home' | 'scratch'
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

// scratchBase 把一台机器的草稿区路径做成浮窗文件 tab 使用的基准。
//
// 参数：
//   - root: agentd 上报的草稿区绝对路径
//   - machine: 机器名；空串表示本机
//
// 返回：不进入 byBase 的草稿区基准，只供 FileTab 读取路径与机器名发文件请求。
// 注意：草稿区不是可选中的基准目录，左栏和面包屑都不会展示它。
export function scratchBase(root: string, machine: string): BaseDir {
  return {
    key: `scratch:${machine}:${root}`,
    kind: 'scratch',
    path: root,
    label: '临时',
    projectName: '',
    machine,
  }
}

export interface WorkbenchApi {
  base: BaseDir | null
  wb: Workbench
  select: (b: BaseDir) => void
  // open / openTerminal 的第三个参数是**开在哪一组**。省略 = 开在当前焦点组。
  //
  // 为什么必须能显式指定：分屏后两组各有自己的 `+`，点左边的 `+` 就该开在左边，
  // 哪怕焦点在右边。不传组号时 openTab 会退回 `wb.active`，于是「点哪个 + 都开在
  // 焦点组」——这是走查里真实撞到的偏差，不是理论问题。
  open: (c: TabContent, b?: BaseDir, group?: number) => void
  openTerminal: (b?: BaseDir, group?: number, rel?: string) => void
  // attach 等边界入口使用服务端提供的首次命令；命令原样进入 terminal tab。
  openTerminalWithCommand: (command: string, b?: BaseDir, group?: number) => void
  close: (group: number, tabId: string) => void
  activate: (group: number, tabId: string) => void
  setContent: (group: number, tabId: string, c: TabContent) => void
  split: () => void
  // splitAt 在指定位置插入一栏（0 = 最左）。⌘D 与面包屑按钮仍走 split（末尾追加）。
  splitAt: (index: number) => void
  // openInNewPane 在 index 处插入一栏并把内容开在其中——「分屏并打开」这个
  // 复合动作的唯一实现（拖放投放到边缘走它）。
  //
  // 内容已经在别处开着时**不分屏**，只激活已有的那个。为什么这条必须做在这里
  // 而不是让调用方先 splitAt 再 open：openTab 的跨组去重会把已有的那个在它
  // **原来那一栏**激活，于是刚分出来的新栏空在那儿——用户要的是「分屏并打开」，
  // 拿到一个空栏比不分屏更糟（实测走查发现）。两步分开写就必然漏掉这一支，
  // 因为调用方手上没有目标基准的 Workbench（跨基准拖放时尤其如此）。
  //
  // index 越界或已到 MAX_GROUPS 时退化为「在焦点栏打开」，不抛错。
  openInNewPane: (c: TabContent, index: number, b?: BaseDir) => void
  // closeById 按 tab id 关闭，自己反查它在哪一组。
  //
  // 为什么要有它：组下标只在一次事件内可靠。确认弹层打开期间用户可能分屏、
  // 关栏，等他点「确认」时存下来的下标已经指向别的栏了——那会关掉另一栏的
  // tab。tabId 在整个 workbench 内唯一（nextTabId 保证），反查是确定的。
  closeById: (tabId: string) => void
  // resize 调整第 dividerIndex 条分隔条两侧的栏宽。三个参数逐字透传给
  // tabs.ts 的 resizeGroups——这里不做夹紧也不认识像素，只负责把它接到当前基准的
  // Workbench 上。
  resize: (dividerIndex: number, delta: number, minRatio: number) => void
  // restoreTerminal 把一个**已存在于服务端**的会话恢复成 tab。
  //
  // 与 openTerminal 的关键差别：它**不切换当前基准**。页面加载时可能一次恢复
  // 好几个目录下的会话，逐个 select 过去会让用户的选中态落在最后一条上——
  // 那是把「后台恢复」变成了「替用户点了一下左栏」。
  restoreTerminal: (b: BaseDir, sessionId: string, incompatible?: boolean) => void
  // byBase 是全部基准目录的 tab 组，**只读**。持久化层要监听它整体做差分，
  // 只盯当前基准是不够的——restoreTerminal 会写非当前基准的行。
  byBase: Record<string, Workbench>
  // baseDirs 是每个 key 对应的基准元数据。
  //
  // 为什么必须单独存一张：byBase 只有 key → Workbench，而落盘时要把 label /
  // projectName / machine / path 一起编码进 payload（恢复时 key 还原不出它们）。
  // 当前基准之外的那些目录，除了这张表没有别的地方留着它们的 BaseDir。
  baseDirs: Record<string, BaseDir>
  // hydrate 一次性灌入落盘恢复出来的全部 tab 组。
  //
  // **刻意不管选中**：selected 的恢复要等项目树加载完（spec §6），在这里 select
  // 会把用户丢进一个树还没到、面包屑是空的界面。
  hydrate: (entries: Array<{ base: BaseDir; wb: Workbench }>) => void
}

export function useWorkbench(): WorkbenchApi {
  const [base, setBase] = useState<BaseDir | null>(null)
  const [byBase, setByBase] = useState<Record<string, Workbench>>({})
  // baseDirs 与 byBase 同生命周期：凡是写进 byBase 的 key，元数据都在这里
  const [baseDirs, setBaseDirs] = useState<Record<string, BaseDir>>({})
  // baseRef 让 open/openTerminal 在同一个事件里「先切基准再写它的 tab 组」时
  // 读到刚切过去的那个，而不是本次渲染闭包里的旧值
  const baseRef = useRef<BaseDir | null>(null)
  baseRef.current = base

  const wb = base ? (byBase[base.key] ?? EMPTY_WORKBENCH) : EMPTY_WORKBENCH

  const select = useCallback((b: BaseDir) => {
    baseRef.current = b
    setBase(b)
    // 登记元数据：落盘时要用它编码 payload。用 updater 里的浅比较避免
    // 每次 select 都产生一个新对象引发无谓的重渲染
    setBaseDirs((prev) => (prev[b.key] === b ? prev : { ...prev, [b.key]: b }))
  }, [])

  // mutate 是所有写入的唯一通道：确定目标基准 → 取它的 Workbench → 应用纯函数。
  // 未选中任何基准且调用方也没给一个时，是空操作——静默造一个基准出来会让
  // 「tab 开在哪个目录下」变得不可解释。
  const mutate = useCallback(
    (fn: (w: Workbench) => Workbench, b?: BaseDir) => {
      const target = b ?? baseRef.current
      if (!target) return
      if (b && b.key !== baseRef.current?.key) select(b)
      setBaseDirs((prev) => (prev[target.key] ? prev : { ...prev, [target.key]: target }))
      setByBase((prev) => ({ ...prev, [target.key]: fn(prev[target.key] ?? EMPTY_WORKBENCH) }))
    },
    [select],
  )

  const open = useCallback(
    (c: TabContent, b?: BaseDir, group?: number) => mutate((w) => openTab(w, c, group), b),
    [mutate],
  )

  const openTerminal = useCallback(
    (b?: BaseDir, group?: number, rel?: string) =>
      mutate((w) => {
        const seq = nextTerminalSeq(w)
        // rel 只在显式给时写进 tab 内容，缺省与旧形态逐字节一致（去重键、标题都不看它）
        return openTab(w, rel ? { kind: 'terminal', seq, rel } : { kind: 'terminal', seq }, group)
      }, b),
    [mutate],
  )

  const openTerminalWithCommand = useCallback(
    (command: string, b?: BaseDir, group?: number) => mutate((w) => {
      const seq = nextTerminalSeq(w)
      return openTab(w, { kind: 'terminal', seq, initCommand: command }, group)
    }, b),
    [mutate],
  )

  const close = useCallback((g: number, id: string) => mutate((w) => closeTab(w, g, id)), [mutate])
  const activate = useCallback((g: number, id: string) => mutate((w) => activateTab(w, g, id)), [mutate])
  const setContent = useCallback(
    (g: number, id: string, c: TabContent) => mutate((w) => setTabContent(w, g, id, c)),
    [mutate],
  )
  const split = useCallback(() => mutate(splitGroup), [mutate])
  const splitAt = useCallback((index: number) => mutate((w) => splitGroupAt(w, index)), [mutate])
  const openInNewPane = useCallback(
    (c: TabContent, index: number, b?: BaseDir) =>
      mutate((w) => {
        // 已经开着就只激活它，不分屏——见接口注释里那条「空栏比不分屏更糟」
        if (isAlreadyOpen(w, c)) return openTab(w, c)
        const split = splitGroupAt(w, index)
        // splitGroupAt 到上限时原样返回同一个对象，此时没有新栏可开，
        // 退回焦点栏（传 undefined 让 openTab 用 w.active）
        if (split === w) return openTab(split, c)
        return openTab(split, c, Math.max(0, Math.min(index, split.groups.length - 1)))
      }, b),
    [mutate],
  )
  const closeById = useCallback(
    (tabId: string) =>
      mutate((w) => {
        const gi = w.groups.findIndex((g) => g.tabs.some((t) => t.id === tabId))
        // 找不到是正常情形：确认弹层还开着时这个 tab 被别的路径关掉了。
        // 空操作，不抛错——弹层的「确认」按钮不该因此炸掉
        if (gi === -1) return w
        return closeTab(w, gi, tabId)
      }),
    [mutate],
  )
  const resize = useCallback(
    (dividerIndex: number, delta: number, minRatio: number) =>
      mutate((w) => resizeGroups(w, dividerIndex, delta, minRatio)),
    [mutate],
  )

  // restoreTerminal 不走 mutate：mutate 在给了显式基准时会 select 过去，而恢复
  // 是后台动作，不该把用户的选中态拽走。它只在 byBase 里按目标基准写入。
  const restoreTerminal = useCallback((b: BaseDir, sessionId: string, incompatible?: boolean) => {
    setBaseDirs((prev) => (prev[b.key] ? prev : { ...prev, [b.key]: b }))
    setByBase((prev) => {
      const w = prev[b.key] ?? EMPTY_WORKBENCH
      // seq 在 updater 里算：连着恢复多个会话时，闭包外算出来的序号全是旧的
      const content = incompatible
        ? { kind: 'terminal' as const, seq: nextTerminalSeq(w), sessionId, incompatible: true }
        : { kind: 'terminal' as const, seq: nextTerminalSeq(w), sessionId }
      return { ...prev, [b.key]: openTab(w, content) }
    })
  }, [])

  // hydrate 一次性灌入恢复出来的全部 tab 组。见接口注释：不碰选中态。
  //
  // 用整体替换而不是逐条合并：水合只在应用启动时发生一次，那时 byBase 必然是空的。
  // 写成合并会让「重复调用」看起来是安全的，而它其实会把用户启动后新开的 tab
  // 与一份陈旧快照混在一起——那种状态没人能解释。
  const hydrate = useCallback((entries: Array<{ base: BaseDir; wb: Workbench }>) => {
    const nextBases: Record<string, Workbench> = {}
    const nextDirs: Record<string, BaseDir> = {}
    for (const e of entries) {
      nextBases[e.base.key] = e.wb
      nextDirs[e.base.key] = e.base
    }
    setByBase(nextBases)
    setBaseDirs(nextDirs)
  }, [])

  return {
    base,
    wb,
    byBase,
    baseDirs,
    select,
    open,
    openTerminal,
    openTerminalWithCommand,
    close,
    closeById,
    activate,
    setContent,
    split,
    splitAt,
    openInNewPane,
    resize,
    restoreTerminal,
    hydrate,
  }
}
