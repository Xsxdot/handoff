// Shell —— 控制台的三栏外框：左栏导航树 / 中央 tab 工作台 / 右栏文件树。
//
// 职责：
//   - 持有跨栏共享的数据流（任务流 2.5s、项目树流 30s）与**当前基准目录**这一
//     唯一全局选中态（useWorkbench）
//   - 把三栏接起来：左栏点目录 → 切基准；左栏点任务 → 切基准 + 开 TUI tab；
//     右栏点文件 → 开 file tab；中央按 tab 种类分发渲染
//   - 承载弹出层（看板、工单）、设置页与右下角悬浮按钮
//
// 边界：
//   - 不自己取目录内容（归 FileTree）、不自己取任务会话（归 TuiTab）
//   - 中央 tab 的具体渲染经 renderContent 注入 WorkbenchPage，Shell 只做分发
//   - 机器流只随登记向导开表（useMachines(wizardOpen)）：探活会向每台远程机发
//     GET /api/status，没人看的时候没有理由持续打扰它们（spec §6）
//
// 关于 ShellContext 的移除：W3 用 <Outlet context> 给三个子页面下发共享数据。
// 新 IA 里中央不再是路由页面而是 tab，Outlet 没有了消费者；看板与工单改为弹层，
// 它们要的数据直接由 Shell 以 props 传下去。留一个没人用的 context 只会误导。
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { ApiError, deleteProject, deletePtySession, fetchLaunchers, fetchPtySessions } from '../../api/client'
import { currentUnlinkedTaskIds, fetchCardDetail, fetchCards, fetchDecisions } from '../../api/ledger'
import type { UnlinkedSummary } from '../../api/ledger'
import type { ProjectNode, ProjectTreeResp, Task, Workspace } from '../../api/types'
import type { CoordinatorAttachInfo } from '../../api/scheduling'
import { useMachines } from '../data/useMachines'
import { useProjectTree } from '../data/useProjectTree'
import { useTasks } from '../data/useTasks'
import { usePreviews } from '../data/usePreviews'
import { useMachineCaps } from '../data/useMachineCaps'
import { useLedgerEnabled } from '../data/useLedgerEnabled'
import { usePoll } from '../data/usePoll'
import { useUnlinkedSummaryClock } from '../data/useUnlinkedSummaryClock'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { isDesktopShell } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { AddProjectWizard } from '../projects/AddProjectWizard'
import { ProjectEditDialog } from '../projects/ProjectEditDialog'
import { findBaseByKey, findBaseOfTask, ProjectTree, workspaceBase, type OpenItem } from '../tree/ProjectTree'
import { MobileProjectDetail } from '../tree/MobileProjectDetail'
import { FileTree } from '../files/FileTree'
import { WorkbenchPage } from '../workbench/WorkbenchPage'
import { TerminalTab } from '../workbench/TerminalTab'
import { FileTab } from '../workbench/FileTab'
import { TuiTab } from '../workbench/TuiTab'
import { HomeDock } from '../homedock/HomeDock'
import { useHomeDock } from '../homedock/useHomeDock'
import type { DockSnapshot } from '../homedock/dockPersist'
import { HOME_BASE, scratchBase, sessionBase, useWorkbench, type BaseDir } from '../workbench/useWorkbench'
import { createUntitledFile } from '../workbench/newFile'
import { nextTerminalSeq, spawnTerminalContent, tabTitle, type Tab, type TabContent, type Workbench } from '../workbench/tabs'
import { taskDisplayName } from '../lib/taskName'
import type { StateTone } from '../board/columns'
import { useWorkbenchSync } from '../workbench/useWorkbenchSync'
import { BoardOverlay } from '../overlay/BoardOverlay'
import { TicketsOverlay } from '../overlay/TicketsOverlay'
import { useGlobalTickets } from '../overlay/useGlobalTickets'
import { SettingsPage } from '../settings/SettingsPage'
import { useWebPrefs } from '../settings/useWebPrefs'
import { CodegraphFrame } from '../codegraph/CodegraphFrame'
import { CardsPage } from '../cards/CardsPage'
import { isRunningRow } from '../cards/taskRun'
import { FlowsPage } from '../flows/FlowsPage'
import { fetchSessions, createSession, fetchIdentity } from '../../api/rooms'
import type { SessionSummary } from '../../api/rooms'
import { NewSessionDialog } from '../rooms/NewSessionDialog'
import { SessionSidebar } from '../rooms/SessionSidebar'
import { SessionTab } from '../rooms/SessionTab'
import { totalUnread } from '../rooms/sessionModel'
import { COLLAB_POLL_MS } from '../rooms/constants'
import { needsAttention } from '../cards/columns'
import { Breadcrumb } from './Breadcrumb'
import { DesktopTitleBar } from './DesktopTitleBar'
import { ResizableSidebar } from './ResizableSidebar'
import { MobileTabBar } from './MobileTabBar'
import { TOUCH_BASELINE } from '@/lib/touch'
import { cn } from '@/lib/utils'
import { useMobileNav } from './useMobileNav'
import { isCompactViewport, useShellViewport } from './useShellViewport'

// OverlayKind 是当前打开的弹层。同时只允许一个（spec §0）：两个叠在一起时
// Esc 该关哪个会变得含糊。
type OverlayKind = 'none' | 'board' | 'tickets'

// coordinatorBase 把服务端 attach 定位接回现有工作台基准；树中没有对应目录时，
// 仍保留 machine/path 的身份，避免把服务端给出的 attach 位置静默丢掉。
export function coordinatorBase(tree: ProjectTreeResp | null, info: CoordinatorAttachInfo): BaseDir {
  const match = tree?.projects
    .flatMap((project) => project.locations.flatMap((location) =>
      location.workspaces.map((workspace) => ({ project, location, workspace }))))
    .find(({ location, workspace }) => workspace.path === info.dir && location.machine === info.machine)
  if (match) return workspaceBase(match.project, match.location.machine, match.workspace)
  const label = info.dir.split('/').filter(Boolean).pop() || info.dir
  return {
    key: info.machine === '' ? info.dir : `${info.dir}@${info.machine}`,
    kind: 'workspace',
    path: info.dir,
    label,
    projectName: '',
    machine: info.machine,
  }
}

// Only a complete, timestamped current observation is safe to use as a filter
// source. Unknown status or fields fail closed to the unfiltered task list.
export function unlinkedTaskIdsForSummary(summary?: UnlinkedSummary | null, now = Date.now()): Set<string> | null {
  return currentUnlinkedTaskIds(summary, now)
}

// focusedPaneBase 是顶部展示的单向投影：左树 selected base 仍服务于打开新内容，
// 顶部则必须回答用户正在看的 pane 属于哪个目录。越界或空 pane 代表当前没有内容，
// 不猜测 selected base，避免左栏点击后顶栏与中央画面脱节。
function focusedPaneBase(workbench: Workbench): BaseDir | null {
  const group = workbench.groups.find((candidate) => candidate.id === workbench.activeGroupId)
  if (!group) return null
  const [column, row] = group.focus
  return group.columns[column]?.panes[row]?.base ?? null
}

// focusedTab 是顶部展示的另一个单向投影：焦点窗格里的 tab 本体，
// 面包屑第三段（内容名）与左栏焦点态都要用它。
function focusedTabOf(workbench: Workbench): Tab | null {
  const group = workbench.groups.find((candidate) => candidate.id === workbench.activeGroupId)
  if (!group) return null
  const [column, row] = group.focus
  return group.columns[column]?.panes[row] ?? null
}

export function Shell() {
  const tasksState = useTasks()
  const treeState = useProjectTree()
  const tasks = useMemo(() => tasksState.data ?? [], [tasksState.data])
  const previewsState = usePreviews()
  const previews = useMemo(() => previewsState.data?.sessions ?? [], [previewsState.data])
  const wb = useWorkbench()
  // crumbTaskName 把 taskId 解析成任务原名（与左栏任务行同口径）；解析不到时
  // tabTitle 自己回退 TUI · 前 8 位。标签条、面包屑、左栏已打开行共用同一份口径。
  const taskNameResolver = useCallback((id: string) => {
    const t = tasks.find((x) => x.id === id)
    return t ? taskDisplayName(t) : undefined
  }, [tasks])
  const navigate = useNavigate()
  // 响应式断点（B369.6）：判据与理由见 useShellViewport 文件头。
  const viewport = useShellViewport()
  const compact = isCompactViewport(viewport)
  // 紧凑导航统一模型（B369.7）：mobileTab / mobileDetail / fileDrawer 三个状态源
  // 收编进一个以 URL 为唯一事实源的 hook——任何导航动作 = 写 URL，任何 UI 呈现 =
  // 读 URL。桌面直通（内部 state、URL 不动），行为契约见 useMobileNav 文件头。
  const nav = useMobileNav({ compact })
  // Web 客户端偏好（B369.8 §3.4）：compact 会话打开方式（scene 档解析链）与
  // 底栏角标门控的读取点。桌面不读（两处消费都在 compact 分支内）。
  const [webPrefs] = useWebPrefs()
  // openMobileDetail 在紧凑视口把内容面切到工作台（写 URL：/?tab=<tab>&detail=1）；
  // 桌面是空操作（工作台本就常驻）。
  // 为什么不做成 effect 监听 openedItems：用户点「返回」后底栏首页要能停住，
  // 而 openedItems 依然非空——"有 tab" 推不出 "想看它"。
  const openMobileDetail = useCallback(() => {
    if (compact) nav.enterDetail()
  }, [compact, nav])
  useEffect(() => {
    console.debug('shell.viewport', { viewport, compact })
  }, [viewport, compact])
  const location = useLocation()
  const openCoordinatorTerminal = useCallback((info: CoordinatorAttachInfo) => {
    console.info('coordinator.terminal.open', { machine: info.machine, dir: info.dir, opened: true, cause: 'attach' })
    if (compact) {
      // B369.7：紧凑下抽屉选中已 URL 化，来源卡从当前 location 的 card 参数读，
      // 不需要改 CardsPageProps 回调签名；进任务现场形态 /?tab=cards&detail=1&from=card-<id>。
      const cardParam = new URLSearchParams(location.search).get('card')
      nav.enterDetail({ from: `card-${cardParam ?? ''}`, tab: 'cards' })
    } else {
      openMobileDetail()
    }
    wb.openTerminalWithCommand(info.command, coordinatorBase(treeState.data, info))
  }, [compact, location.search, nav, treeState.data, wb, openMobileDetail])
  const [routeParams] = useSearchParams()

  // B369.7 seam：紧凑下抽屉开关与任务跳转的 URL 收口。只注入给紧凑视口的
  // CardsPage 挂载点；桌面缺省不注入 = CardsPage 既有内部行为零改动。
  const onDrawerCardChange = useCallback((cardId: string | null) => {
    if (cardId === null) nav.closeCard()
    else nav.openCard(cardId)
  }, [nav])

  const taskJumpHref = useCallback((taskId: string) => {
    // 卡到任务的唯一出口仍是 /tasks/:id 深链（B181）；紧凑下把任务现场返回语境
    // 带上：from 取当前 URL 的来源，缺省回落卡来源（紧凑下抽屉选中已 URL 化）。
    const src = nav.from ?? `card-${routeParams.get('card') ?? ''}`
    return `/tasks/${taskId}?from=${encodeURIComponent(src)}&tab=cards`
  }, [nav, routeParams])

  const [overlay, setOverlay] = useState<OverlayKind>('none')
  const [wizardOpen, setWizardOpen] = useState(false)
  // fileDrawer 拆成两半（B369.7）：桌面沿用 useState（写点 openDirectory 与桌面
  // FileTree onClose）；紧凑由 nav.dirKey 从树上反解（URL 是事实源，树未加载或
  // 键失效时为 null——覆盖层不渲染、URL 残参自愈）。下游读点共用合成的同名变量。
  const [desktopFileDrawer, setDesktopFileDrawer] = useState<BaseDir | null>(null)
  const fileDrawer = compact
    ? (nav.dirKey !== null && treeState.data ? findBaseByKey(treeState.data, nav.dirKey) : null)
    : desktopFileDrawer
  // fileTreeNonce 是右栏刷新的触发器。中央区新建文件后递增它。
  // 用计数器而不是把 FileTree 的 refresh 传上来：那会把中央区与右栏焊死，
  // 而它们现在互不认识
  const [fileTreeNonce, setFileTreeNonce] = useState(0)
  // editProject 是正在被编辑的项目（右键菜单「编辑」传入）；null = 弹层关闭。
  const [editProject, setEditProject] = useState<ProjectNode | null>(null)
  const machinesState = useMachines(wizardOpen)
  const tickets = useGlobalTickets(tasks)
  const { enabled: ledgerEnabled, loading: ledgerLoading } = useLedgerEnabled()
  // 账本未启用时会话/卡两个 tab 无内容面，移动首屏退到「项目」，否则首屏是一句
  // 「不可用」——状态门要与桌面门控（ledgerEnabled）一致。归 nav.normalize：
  // 除账本门改写外，它还负责「/ 上 tab=cards 无 detail → /cards」的形状归一与
  // 残参清理（URL 变化会让 normalize 换新身份、effect 重跑；写前比对，幂等）。
  useEffect(() => {
    nav.normalize({ ledgerEnabled, ledgerLoading })
  }, [nav, ledgerEnabled, ledgerLoading])
  const cardsState = usePoll(() => fetchCards(), 2500, { enabled: ledgerEnabled })
  const decisionsState = usePoll(() => fetchDecisions(true), 2500, { enabled: ledgerEnabled })
  const unlinkedSummary = cardsState.data?.unlinked
  const unlinkedNow = useUnlinkedSummaryClock(unlinkedSummary?.observed_at)
  const cardNeedsCount = useMemo(() => {
    // 账本未启用时角标恒 0：轮询已关，cardsState 永远是 null，这里显式返回
    // 比依赖「null 恰好算出 0」可靠
    if (!ledgerEnabled) return 0
    const cards = cardsState.data?.cards ?? []
    const cardCount = cards.filter(needsAttention).length
    const projectDecisionCount = (decisionsState.data ?? []).filter((decision) => decision.card_id === '').length
    return cardCount + projectDecisionCount
  }, [ledgerEnabled, cardsState.data, decisionsState.data])
  // 未挂账 task = 账本里没有卡认领它的那些。任务看板降级为它们的兜底入口
  // （工作项看板是主入口），所以这个集合同时喂给 dock 角标与看板的默认筛选。
  // 账本还没读到时给 null——不过滤，宁可多显示也不能凭空藏任务。
  const unlinkedTaskIds = useMemo(() => {
    return currentUnlinkedTaskIds(unlinkedSummary, unlinkedNow)
  }, [unlinkedSummary, unlinkedNow])
  // 会话流（B361）：徽章要在任务 tab 激活时也活着，数据源在 Shell 持有，
  // 列表组件只渲染。未启用账本时轮询关闭（与旧房间面同门控）。
  const sessionsState = usePoll((signal) => fetchSessions(signal), COLLAB_POLL_MS,
    { enabled: ledgerEnabled, timeoutMs: 15_000 })
  const sessions = useMemo(() => sessionsState.data ?? [], [sessionsState.data])
  // pty 会话流（B369.10）：项目详情面「这台机器上的终端会话」的数据源。
  // 只在紧凑视口开表（fetchPtySessions('all') 是会话恢复的唯一真相源，桌面详情
  // 面不存在，没有理由持续打扰它）；30s 节奏对齐树流——会话列表不是强实时面。
  const ptySessionsState = usePoll(() => fetchPtySessions('all'), 30_000, { enabled: compact })
  const [sidebarTab, setSidebarTab] = useState<'sessions' | 'tasks'>('sessions')
  const [createOpen, setCreateOpen] = useState(false)
  const [createBusy, setCreateBusy] = useState(false)
  const [createError, setCreateError] = useState('')
  // consoleConfigured 身份读缝（B358.9）：未配 console_user 时新建表单给可行动
  // 提示并禁用创建。拉取失败不得误判「未配名」——保持放行（fail-visible），
  // 让创建请求把服务端 403 可行动原文显示在对话框。
  const [consoleConfigured, setConsoleConfigured] = useState(true)
  useEffect(() => {
    let cancelled = false
    fetchIdentity()
      .then((id) => { if (!cancelled) setConsoleConfigured(id.configured) })
      .catch((error: unknown) => {
        console.warn('shell.identity_fetch_failed', { error: errorMessage(error) })
      })
    return () => { cancelled = true }
  }, [])
  // needsOnly 左栏「需要你」筛选态：与筛选钮同层，传给 SessionSidebar 渲染。
  const [needsOnly, setNeedsOnly] = useState(false)
  // 项目筛选（B358.8 #6）：state 与 needsOnly 同层；选项与卡→项目映射都从既有
  // cardsState（2.5s 轮询）投影，零新增数据供给。
  const [projectFilter, setProjectFilter] = useState('')
  const cardProjectById = useMemo(() => {
    const map = new Map<string, string>()
    for (const card of cardsState.data?.cards ?? []) map.set(card.id, card.project)
    return map
  }, [cardsState.data])
  const projectOptions = useMemo(() => [...new Set(cardProjectById.values())].sort(), [cardProjectById])
  const projectOfCard = useCallback((cardId: string) => cardProjectById.get(cardId) ?? '', [cardProjectById])
  const caps = useMachineCaps()
  const launcherMachine = wb.base?.machine ?? ''
  const launchersSupported = caps.launchers(launcherMachine) === true
  const launchersState = usePoll(
    () => fetchLaunchers(launcherMachine),
    30_000,
    { enabled: launchersSupported },
  )
  const { data: launchersData, refresh: refreshLaunchers } = launchersState
  // usePoll 的 fetcher 用 ref 保持稳定；机器切换时 enabled 仍可能同为 true，
  // 所以主动 refresh 一次，避免沿用上一台机器的列表。能力从未知变成 true 时，
  // usePoll 自己会因 enabled 变化首拉，不需要再制造第二次请求。
  const launcherMachineRef = useRef(launcherMachine)
  useEffect(() => {
    if (launcherMachineRef.current !== launcherMachine && launchersSupported) {
      refreshLaunchers()
    }
    launcherMachineRef.current = launcherMachine
  }, [launcherMachine, launchersSupported, refreshLaunchers])
  // scratchRoot 是本机草稿区路径；空串 = 这台 agentd 不支持临时文件，
  // 浮窗里的入口不渲染。
  const scratchRoot = caps.scratchRoot('')
  const [scratchError, setScratchError] = useState('')
  // home 终端的浮窗状态完全独立于 wb：home 终端不挂在任何目录上（见 useHomeDock）
  const dock = useHomeDock()
  // dockSnapshot 把悬浮窗的五份状态收成一个对象，供落盘层做差分。
  // 必须 useMemo：不 memo 的话每次渲染都是新引用，写回 effect 会每帧重排一次去抖
  const dockSnapshot: DockSnapshot = useMemo(
    () => ({ tabs: dock.tabs, activeId: dock.activeId, windowOpen: dock.windowOpen, geom: dock.geom, maximized: dock.maximized }),
    [dock.tabs, dock.activeId, dock.windowOpen, dock.geom, dock.maximized],
  )

  // 工作台状态的水合与写回（2026-08-20 状态同步 spec §5.3）。
  // 它取代了旧的会话恢复入口：布局恢复与会话恢复是同一件事的两半。
  //
  // adoptDockTab 仍用 dock.adopt 而不是别的入口：adopt 不打开浮窗、不抢焦点——
  // 页面一加载就弹出浮窗，等于替用户点了一下
  const sync = useWorkbenchSync({
    workbench: wb.wb,
    selectedKey: wb.base?.key ?? '',
    dockSnapshot,
    hydrateWorkbench: wb.hydrate,
    hydrateDock: dock.hydrate,
    adoptDockTab: dock.adopt,
  })

  // 恢复「上次选中的目录」：要等项目树到位才能校验它还在不在（spec §6 规则三）。
  //
  // 三个条件缺一不可：
  //   - 树已加载（没树就无从校验）
  //   - 服务端确实存了一个（空串 = 上次就没选中）
  //   - 用户还没自己选过（wb.base 非空说明他已经点过左栏了，别抢方向盘）
  // selectedRestoredRef 保证只做一次：树刷新会让这个 effect 重跑，
  // 而用户此时可能已经切到别的目录了
  const selectedRestoredRef = useRef(false)
  useEffect(() => {
    if (selectedRestoredRef.current) return
    if (!treeState.data || sync.restoredSelected === '' || wb.base !== null) return
    selectedRestoredRef.current = true
    const found = findBaseByKey(treeState.data, sync.restoredSelected)
    if (found === null) {
      // 目录已经不在树上了（worktree 被回收、项目被注销）。退回未选中态，
      // 而不是摆出一栏点什么都报错的 tab
      console.debug('上次选中的目录已不在树上，退回未选中态', sync.restoredSelected)
      return
    }
    // 用树上重新构造的那份，而不是 payload 里的快照：树上的 label 会跟着
    // 分支改名一起变，用快照会让面包屑显示一个已经改掉的旧分支名
    wb.select(found)
  }, [treeState.data, sync.restoredSelected, wb.base, wb.select])
  // closingPty 记「哪个终端 tab 正在等确认」。会话 id 与 tab id 都要留着：
  // 确认之后要先删会话、再关那个 tab
  //
  // 为什么连 machine 一起留（B96）：删会话要指名机器，而「该删哪台」是**这个
  // 会话**的属性——它建在哪台机器上就该往哪台删。以前这里在确认时现读
  // `wb.base?.machine`（**当前选中**基准的机器），两者只是因为「工作台按基准
  // 分持、切基准会整组换掉」才恰好相等；那是一条没写下来的隐含前提，一旦弹层
  // 开着时基准被换走就会拿 A 的机器名去删 B 的会话。与下面的 closingHome 对齐：
  // 它一直就是把 machine 存下来的
  const [closingPty, setClosingPty] = useState<
    { tabId: string; sessionId: string; machine: string } | null
  >(null)
  // 只存 tabId 不存组下标：组下标在确认弹层打开期间会因为分屏/关栏而失效，
  // 而 tabId 在整个 workbench 内唯一。关闭走 wb.closeById 自己反查。
  const [closeBusy, setCloseBusy] = useState(false)
  const [closeError, setCloseError] = useState('')
  // closingBusyProc：这个会话里是不是还有前台命令。null = 还没问出来
  const [closingBusyProc, setClosingBusyProc] = useState<boolean | null>(null)
  // closingGone：服务端已经查不到这个会话了（PTY 会话由 ptyhost 持有、跨 agentd
  // 重启存活——查不到说明它真的消失了：机器重启、退出 shell 或显式停止）。只影响
  // 措辞——弹层不能一边说「会终止里面正在运行的命令」，一边关的其实是一个早就
  // 没了的会话。null = 还没问出来，按「可能还活着」说话
  const [closingGone, setClosingGone] = useState<boolean | null>(null)
  // closingDirtyFile 记「哪个有草稿的文件 tab 正在等确认」。只记位置不记草稿：
  // 草稿仍活在 tab 内容里，确认「不保存，关闭」时 wb.close 会把它一起带走
  const [closingDirtyFile, setClosingDirtyFile] = useState<{ tabId: string; rel: string } | null>(null)
  // closingDirtyHome 记「哪个浮窗文件 tab 有草稿、正在等确认」。它不复用
  // closingDirtyFile：浮窗 tab 不在 wb 里，确认后必须调 dock.closeTab。
  const [closingDirtyHome, setClosingDirtyHome] = useState<{ id: string; rel: string } | null>(null)
  // closingHome 记「哪个浮窗 tab 正在等确认」。与 closingPty 同构，只是归
  // 浮窗。为什么也要确认：关闭即终止不可逆，与中央 tab 同一条理由
  const [closingHome, setClosingHome] = useState<{ id: string; sessionId: string; machine: string } | null>(null)

  // ptyNote 把能力三态翻成一句给人看的话；空串 = 可用（或不知道，照常放行）
  const ptyNote = (machine: string): string => {
    if (caps.pty(machine) === false) {
      return machine === ''
        ? '本机 agentd 运行在不支持 PTY 的平台上，终端不可用。'
        : `机器 ${machine} 的 agentd 运行在不支持 PTY 的平台上，终端不可用。`
    }
    // null 一律放行：老 agentd 没上报能力位，很可能是支持的。真不支持时
    // 建会话会返回 501，那句实话由 TerminalTab 显示
    return ''
  }

  // beforeCloseTab 拦下带会话的终端 tab：关它等于终止会话，必须先确认。
  //
  // 与 spec §6.2 的一处收紧（有意为之）：spec 只要求「有前台进程」才确认，
  // 这里只要是带会话的终端 tab 就弹。关闭即终止是不可逆操作，而「有没有前台
  // 进程」这个判据在用户点下 × 的那一瞬间可能刚好过期——宁可多问一句，也不
  // 静默杀掉跑了整个晚上的 build（这正是本设计不做空闲回收的同一条理由）。
  const beforeCloseTab = (c: TabContent, tabId: string, tabBase: BaseDir): boolean => {
    // 有草稿的文件 tab：关掉就是把用户唯一一份未保存的输入丢掉，且没有回收站。
    // 与终端那条分支同一个理由——不可逆操作先问一句。
    //
    // 为什么只拦有草稿的：干净文件关了随时能再开，拦它只会让每次关 tab 都多一次
    // 点击，纯打扰。草稿才是磁盘上没有第二份的东西
    if (c.kind === 'file' && c.draft !== undefined) {
      setClosingDirtyFile({ tabId, rel: c.rel })
      return false
    }
    if (c.kind !== 'terminal' || !c.sessionId) return true
    // machine 在这一刻定下来：此刻显示的正是这个 tab 所属基准的工作台
    setClosingPty({ tabId, sessionId: c.sessionId, machine: tabBase.machine })
    setCloseError('')
    probeClosingSession(c.sessionId)
    return false
  }

  // probeClosingSession 向服务端问一句「这个会话还在吗、忙不忙」，答案只用于
  // 弹层措辞，**不阻塞弹层出现**，也不影响能不能确认。
  //
  // 查不到 = 会话已经不在（会话跨 agentd 重启存活，所以「不在」是真的不在——
  // 机器重启、退出 shell 或显式停止）：那句「会终止正在运行的命令」对它是假话，
  // 而假话会让用户以为自己正在杀掉什么东西。问不出来（请求本身失败）时一律退回
  // null，按「可能还活着」说话——宁可吓一跳，不可骗人说没事
  const probeClosingSession = (sessionId: string) => {
    setClosingBusyProc(null)
    setClosingGone(null)
    fetchPtySessions('all')
      .then((r) => {
        const live = r.sessions.find((s) => s.id === sessionId)
        setClosingGone(live === undefined)
        setClosingBusyProc(live !== undefined && live.foreground)
      })
      .catch(() => {
        setClosingBusyProc(null)
        setClosingGone(null)
      })
  }

  // killPtySession 删一个服务端终端会话，失败时把原文交给 onError 呈现。
  // 中央 tab 的确认关闭与浮窗 tab 的 × 共用：两处都不许吞错误——点了 × 以为
  // 会话关了，服务端却还留着一个 shell。返回是否删成功
  const killPtySession = async (
    sessionId: string,
    machine: string | undefined,
    onError: (msg: string) => void,
  ): Promise<boolean> => {
    try {
      await deletePtySession(sessionId, machine)
      return true
    } catch (err) {
      // 404 是**成功**的一种：服务端根本没有这个会话。PTY 会话跨 agentd 重启存活，
      // 「没有」只能是机器重启、退出 shell 或显式停止之后的真消失。此时「不许吞
      // 错误」那条纪律护的东西（别把还活着的 shell 从视野里抹掉）根本不存在——
      // 已经没有 shell 可留。照旧当失败处理的代价是这个 tab 被焊死：确认弹层每次
      // 都红字报「会话不存在」，关不掉，也没有第二个出口。删除对这一路是幂等的
      if (err instanceof ApiError && err.status === 404) return true
      onError(errorMessage(err))
      return false
    }
  }

  const confirmClosePty = async () => {
    if (!closingPty) return
    setCloseBusy(true)
    setCloseError('')
    if (await killPtySession(closingPty.sessionId, closingPty.machine || undefined, setCloseError)) {
      wb.closeById(closingPty.tabId)
      setClosingPty(null)
    }
    // 删失败不关 tab：关掉就等于把一个还活着的会话从视野里抹掉，
    // 而它仍在占着进程（错误已由 killPtySession 塞进 ConfirmDialog）
    setCloseBusy(false)
  }

  const confirmCloseHome = async () => {
    if (!closingHome) return
    setCloseBusy(true)
    setCloseError('')
    if (await killPtySession(closingHome.sessionId, closingHome.machine || undefined, setCloseError)) {
      dock.closeTab(closingHome.id)
      setClosingHome(null)
    }
    setCloseBusy(false)
  }

  // killHomeSession 是浮窗 tab × 的入口：找到会话、进确认弹层。为什么不吞错误：
  // 失败被吞掉的话，用户以为会话关了、实际服务端还留着一个 shell
  const killHomeSession = (id: string) => {
    const tab = dock.tabs.find((t) => t.id === id)
    if (!tab) return
    if (tab.kind === 'file') {
      if (tab.draft !== undefined) {
        setClosingDirtyHome({ id, rel: tab.rel ?? '未命名' })
      } else {
        // 文件 tab 关闭只卸载编辑器，草稿区里的文件仍保留在磁盘上。
        dock.closeTab(id)
      }
      return
    }
    if (!tab.sessionId) {
      // 会话还没建成（比如刚点完新终端立刻点 ×），没有可删的东西，直接移掉
      dock.closeTab(id)
      return
    }
    setCloseError('')
    setClosingHome({ id, sessionId: tab.sessionId, machine: tab.machine })
    probeClosingSession(tab.sessionId)
  }

  // newScratchFile 建一个草稿区文件并把它收进浮窗。
  // 建文件是一次 POST，所以放在 Shell 而不是 useHomeDock（那个 hook 不发请求）。
  const newScratchFile = () => {
    if (scratchRoot === '') return
    setScratchError('')
    void createUntitledFile(scratchBase(scratchRoot, ''))
      .then((rel) => dock.newFile(rel))
      .catch((err: unknown) => setScratchError(errorMessage(err)))
  }

  const onUnregister = async (name: string, machine: string) => {
    await deleteProject(name, machine)
    treeState.refresh()
  }

  // backToWorkbench 把中央区换回工作台。
  //
  // why 每个「改工作台状态」的入口都得先调它：设置/工作项等是盖在工作台上的
  // 整页，URL 还停在 /cards 时用户看见的仍是那一页。只改状态不换路由的后果
  // 是面包屑跟着变了、中央还是原来那一页，看着像点击没反应（2026-08-19 真机
  // 踩到）。工作台本身常驻不卸（B280），但盖住它的那一层要靠导航拿掉。
  // 已在 / 上时不导航，避免往历史里塞无意义的同址条目。
  // B369.7：紧凑分支改 no-op——pathname 由 nav setter 收口（每个入口动作随后
  // 都落完整 URL），/tasks 跳板由 enterDetail 的 replace 处理；桌面原样。
  const backToWorkbench = () => {
    if (compact) return
    if (location.pathname !== '/') navigate('/')
  }

  // openCardsSurface/openSettingsSurface 是两个「去别处看」入口的移动分支：
  // 紧凑视口下切底栏 tab（写 URL：/cards 或 /?tab=settings），桌面仍走整页路由。
  // 抽成回调解 ProjectTree 的既有 prop 契约不变（它只发信号，不关心去哪）。
  const openCardsSurface = () => {
    if (compact) nav.setTab('cards')
    else navigate('/cards')
  }
  const openSettingsSurface = () => {
    if (compact) nav.setTab('settings')
    else navigate('/settings')
  }

  // openSession 会话行的唯一入口：开工作台 tab（openOrFocus 全局去重——重复
  // 点击聚焦原 tab）；先回工作台（/cards 等整页盖上时会话 tab 看不见）。
  // B369.8（§3.2）：compact 读「会话打开方式」偏好——群聊 tab 照常先开（不等待
  // 解析），scene 档随后尽力解析该会话在跑任务并跳进任务现场；解析不到静默
  // 回落群聊（最坏情况 = 现状）。桌面不读偏好。
  const openSession = (session: SessionSummary) => {
    backToWorkbench()
    openMobileDetail()
    wb.openOrFocus({ kind: 'session', sessionId: session.id, title: session.title }, sessionBase(session.id))
    console.debug('shell.session.open', { sessionId: session.id, title: session.title })
    if (compact && webPrefs.sessionOpenMode === 'scene') void resolveSessionScene(session)
  }

  // onOpenSessionForCard（B369.10 T8）：卡详情「驾驶会话」双跳的落点——会话流
  // 反查（SessionSummary.cards 含 card_id，零新端点），找不到静默返回（行已按
  // driverSession 渲染但无会话可开时点击不动作，不弹错不空转）。
  // Detail can load before the session stream. Unknown/expired lookup is not a missing session.
  const driverSessionReady = sessionsState.data !== null && !sessionsState.sessionExpired
  const onOpenSessionForCard = (cardId: string) => {
    if (!driverSessionReady) {
      console.debug('shell.session.for_card_not_ready', { cardId, expired: sessionsState.sessionExpired, disconnected: sessionsState.disconnected })
      return
    }
    const session = sessions.find((candidate) => (candidate.cards ?? []).some((card) => card.card_id === cardId))
    if (session) openSession(session)
    else console.debug('shell.session.for_card_missing', { cardId })
  }

  // confirmCreateSession 建会话（B358.9）：owner 由服务端按解析人名缺省，前端只交
  // 标题；建后刷新会话流。403「以当前身份加入会话」一键仍在 SessionChat（U7 老屋）。
  const confirmCreateSession = async (title: string) => {
    setCreateBusy(true)
    setCreateError('')
    console.debug('shell.session.create_started', { title })
    try {
      const session = await createSession(title)
      setCreateOpen(false)
      sessionsState.refresh()
      console.debug('shell.session.created', { sessionId: session.id, title })
    } catch (error: unknown) {
      setCreateError(errorMessage(error))
      console.warn('shell.session.create_failed', { title, error: errorMessage(error) })
    } finally {
      setCreateBusy(false)
    }
  }

  // fullPageRoute = 中央区被整页替换掉的那些路由。
  //
  // why 要判它：右栏文件树与面包屑都挂在 <Routes> 外面、只跟 wb.base 走，
  // 于是点了目录再点「工作项」，中央换成了看板、右边那棵文件树却一直挂着，
  // 面包屑也还写着上一个目录（2026-08-19 真机看到）。它们是工作台的一部分，
  // 不属于这些整页。左栏导航树不在此列——它是导航，任何页面都该在。
  // 紧凑视口没有「整页路由」形态：底栏 tab 就是整页的移动对应物。加 !compact
  // 前缀后，移动下 /cards?card=X 这类深链不再触发整页分支，而是由 S9 的
  // CardsPage 覆盖层消费 query（同一组件、同一 useSearchParams）。
  const fullPageRoute = !compact && ['/cards', '/flows', '/settings', '/machines', '/codegraph']
    .some((path) => location.pathname.startsWith(path))
  // B369.8（T7）覆盖层 a11y 硬闸的覆盖判据：compact 首页/目录覆盖层盖住工作台
  //（未下钻），或桌面整页路由盖上（fullPageRoute 恒 desktop）。下钻态工作台是
  // 可见活面，三件套整体摘除（§9.1 反例锁）。
  const workbenchCovered = (compact && !nav.detail) || fullPageRoute
  const cardsRoute = location.pathname.startsWith('/cards')

  // onOpenDirectory 是左栏目录的完整入口：选中基准并打开可关闭的文件抽屉。
  // 抽屉自己的文件点击只开 tab，不清掉 drawer，直到用户明确点 X。
  // B369.7：紧凑下抽屉开关 = 写 URL（/?tab=projects&dir=<key>），桌面沿用 state。
  const openDirectory = (base: BaseDir) => {
    backToWorkbench()
    wb.select(base)
    if (compact) nav.setDir(base.key)
    else setDesktopFileDrawer(base)
    console.debug('shell.directory.open', { project: base.projectName, machine: base.machine, baseKey: base.key, path: base.path })
  }

  // openWorkbenchItem 是左栏「已打开行」的聚焦入口（onFocusOpenItem）。
  // focusTab 而不是 open：无会话终端等内容没有去重键，open 会开出第二个 tab。
  const openWorkbenchItem = (item: OpenItem) => {
    backToWorkbench()
    openMobileDetail()
    wb.focusTab(item.base, item.group, item.tabId)
    console.debug('shell.workbench_item.focus', { project: item.base.projectName, machine: item.base.machine, baseKey: item.base.key, groupId: item.group, tabId: item.tabId })
  }

  // closeOpenItem 是左栏已打开行悬停 × 的关闭入口（onCloseOpenItem）。
  // 必须与窗格 × 走同一条 beforeCloseTab 守卫：终端会话先确认（关闭即终止）、
  // 脏草稿先确认（关掉就没）——左栏的 × 只是另一个入口，不是另一条规则。
  // 放行后 closeById 自己反查坐标收格收组，不依赖 OpenItem 里的 group 快照
  // （悬停期间布局可能已变）。
  const closeOpenItem = (item: OpenItem) => {
    const live = wb.openedItems.find((t) => t.tabId === item.tabId)
    if (!live) return
    if (!beforeCloseTab(live.content, live.tabId, live.base)) return
    wb.closeById(live.tabId)
    console.debug('shell.workbench_item.close', { project: live.base.projectName, machine: live.base.machine, baseKey: live.base.key, groupId: live.groupId, tabId: live.tabId })
  }

  // openTerminalAt 是左栏机器行/工作树子行终端钮的入口（基线语义）：
  // 选中该基准并 openOrFocus 终端——终端无去重键，落进独立新组，不打散当前组。
  const openTerminalAt = (base: BaseDir) => {
    backToWorkbench()
    openMobileDetail()
    wb.select(base)
    wb.openOrFocus(spawnTerminalContent(nextTerminalSeq(wb.wb)), base)
    console.debug('shell.directory.terminal.new_group', {
      project: base.projectName, machine: base.machine, baseKey: base.key, path: base.path,
    })
  }

  // openTaskTui 是「点一个任务 → 在它所在目录开 TUI tab」的唯一实现。
  // 左栏任务行、看板卡片、/tasks/:id 深链、工单弹层的「跳到该任务」都走它。
  // 首参为 null（工单弹层、未归属任务）时先用树解析任务自己的目录；解析不出
  // （任务真的不在树上）才退回「当前选中目录」，一个都没选中则 wb.open 空操作。
  // opts（B369.7）：/tasks 跳板期间把来源与 tab 透传给 nav.enterDetail，修正
  // 瞬态路径上 tab 派生错误；看板弹层/ProjectTree 等既有调用方不传，零感知。
  const openTaskTui = (base: BaseDir | null, taskId: string, opts?: { from?: string | null; tab?: string | null }) => {
    setOverlay('none')
    backToWorkbench()
    if (compact) nav.enterDetail(opts)
    else openMobileDetail()
    let target = base
    if (target === null && treeState.data) {
      target = findBaseOfTask(treeState.data, tasks, taskId)
    }
    if (target !== null) wb.select(target)
    wb.openOrFocus({ kind: 'tui', taskId }, target ?? wb.base ?? undefined)
    console.debug('shell.task.open', {
      project: target?.projectName ?? '', machine: target?.machine ?? '', baseKey: target?.key ?? '', path: target?.path ?? '', taskId,
    })
  }

  // resolveSessionScene 是会话打开方式「任务现场」档的解析链（B369.8 §3.2，
  // 零新增端点）：会话挂卡（SessionSummary.cards，会话列表载荷已有）→ 逐卡
  // fetchCardDetail（既有端点）→ task_states 行 ∩ 任务流取第一个在跑行
  // （isRunningRow 口径同 CardDrawer，共享 taskRun.ts）→ 命中即
  // openTaskTui(null, taskId)（compact 下经 enterDetail 收口 URL，返回条语义
  // 不变）。任何一步失败/无数据静默返回——openSession 已先落群聊态，不弹错
  // 不空转。sceneSeqRef 记「最近一次 openSession 请求序号」：连续点两个会话时
  // 迟到的解析结果序号不匹配即丢弃，防旧解析劫持跳转。
  const sceneSeqRef = useRef(0)
  const resolveSessionScene = async (session: SessionSummary) => {
    const seq = ++sceneSeqRef.current
    for (const card of session.cards ?? []) {
      try {
        const detail = await fetchCardDetail(card.card_id)
        if (seq !== sceneSeqRef.current) return
        const running = (detail?.task_states ?? []).find((row) => isRunningRow(row, tasks))
        if (running) {
          console.debug('shell.session.scene_jump', { sessionId: session.id, card: card.card_id, task: running.TaskID })
          openTaskTui(null, running.TaskID)
          return
        }
      } catch (cause) {
        console.debug('shell.session.scene_resolve_failed', { sessionId: session.id, card: card.card_id, cause })
      }
    }
    console.debug('shell.session.scene_noop', { sessionId: session.id })
  }

  // 紧凑视口：目录覆盖层里点文件/开终端都切进工作台（下钻态）。
  // 桌面沿用既有右栏内联行为，这两个回调用不上。
  const openMobileFile = (base: BaseDir, rel: string) => {
    wb.open({ kind: 'file', rel }, base)
    openMobileDetail()
  }
  const openMobileTerminal = (base: BaseDir, rel: string) => {
    wb.openTerminal(base, undefined, rel)
    openMobileDetail()
  }

  const selectProject = (project: ProjectNode) => {
    const location = project.locations.find((loc) => {
      const machineDown = treeState.data?.machines?.some((machine) => machine.name === loc.machine && !machine.ok) ?? false
      return loc.probe_error === '' && !machineDown && loc.workspaces.some((ws) => ws.is_main)
    })
    const main = location?.workspaces.find((ws) => ws.is_main)
    if (location && main) wb.select(workspaceBase(project, location.machine, main))
  }

  const openProjectCards = (project: ProjectNode) => {
    selectProject(project)
    navigate(`/cards?project=${encodeURIComponent(project.name)}`)
    console.debug('shell.project_route', { project: project.name, route: 'cards' })
  }

  const openProjectCodegraph = (project: ProjectNode) => {
    selectProject(project)
    navigate(`/codegraph?project=${encodeURIComponent(project.name)}`)
    console.debug('shell.project_route', { project: project.name, route: 'codegraph' })
  }

  const openedItems = useMemo(() => wb.openedItems.map((item) => {
    const fresh = treeState.data ? findBaseByKey(treeState.data, item.base.key) : null
    const nextBase = fresh ?? item.base
    return { ...item, base: nextBase, label: tabTitle(item.content, nextBase.label, taskNameResolver) }
  }), [wb.openedItems, treeState.data, taskNameResolver])

  // tabRowStatus 是左栏已打开行圆点的状态表（tabId → 终端连接 / 文件问题）。
  // 数据由各 tab 内容组件经上报缝写入（TerminalTab.onConnection、
  // FileTab.onStatus——它们是连接与冲突/删除这两件事的第一手知情者），
  // Shell 只做聚合投影，不自己发请求。缺值的 tab 按健康显示：会话建立中的
  // 终端不闪红，没读完的文件不闪灰。
  const [tabRowStatus, setTabRowStatus] = useState<Map<string, { pty?: boolean; file?: 'conflict' | 'deleted' | 'ok' }>>(new Map())
  const reportPtyConnection = useCallback((tabId: string, connected: boolean) => {
    setTabRowStatus((prev) => {
      const cur = prev.get(tabId)
      if (cur?.pty === connected) return prev
      const next = new Map(prev)
      next.set(tabId, { ...cur, pty: connected })
      return next
    })
  }, [])
  const reportFileStatus = useCallback((tabId: string, file: 'conflict' | 'deleted' | 'ok') => {
    setTabRowStatus((prev) => {
      const cur = prev.get(tabId)
      if (cur?.file === file) return prev
      const next = new Map(prev)
      next.set(tabId, { ...cur, file })
      return next
    })
  }, [])
  // tab 关掉后残值没有消费者，却会无限累积（长会话一天关几十个 tab）。
  // openedItems 变化时修剪到仍存活的 tabId。
  const liveTabIds = useMemo(
    () => new Set(wb.openedItems.map((item) => item.tabId)),
    [wb.openedItems],
  )
  useEffect(() => {
    setTabRowStatus((prev) => {
      let dropped = false
      const next = new Map()
      for (const [tabId, value] of prev) {
        if (liveTabIds.has(tabId)) next.set(tabId, value)
        else dropped = true
      }
      return dropped ? next : prev
    })
  }, [liveTabIds])

  // openItems 是左栏「已打开行」的投影。顺序 = 组序×列序×格序（即打开顺序），
  // **不做**「当前基准置顶」：打开一个任务会切基准，置顶分区等于每次打开都把
  // 左栏洗一次牌（2026-08-29 裁定：顺序固定）。名字统一经 tabTitle + 任务名
  // resolver——tui 显示任务原名，解析不到（任务已删除）时由 tabTitle 回退
  // TUI · 前 8 位。terminal/file 行带 tone（终端=连接、文件=文件状态），
  // tui 行不带——任务状态圆点由 ProjectTree 从任务流取。
  const openItems: OpenItem[] = useMemo(() => openedItems
    .filter((item) => item.content.kind !== 'blank')
    .map((item): OpenItem => {
      const status = tabRowStatus.get(item.tabId)
      const tone: StateTone | undefined =
        item.content.kind === 'terminal'
          ? (status?.pty === false ? 'failed' : 'active')
          : item.content.kind === 'file'
            ? (status?.file === 'deleted' ? 'done'
              : status?.file === 'conflict' ? 'failed'
                : item.content.draft !== undefined ? 'intervention' : 'active')
            : undefined
      return {
        key: `${item.base.key}\x1f${item.tabId}`,
        kind: item.content.kind === 'tui' ? 'tui' : item.content.kind === 'terminal' ? 'terminal' : 'file',
        name: item.label,
        taskId: item.content.kind === 'tui' ? item.content.taskId : undefined,
        machine: item.base.machine,
        base: item.base,
        group: item.groupId,
        tabId: item.tabId,
        detail: item.content.kind === 'file'
          ? item.content.rel
          : item.content.kind === 'terminal'
            ? item.content.rel
            : item.content.kind === 'tui'
              ? item.content.taskId
              : undefined,
        tone,
      }
    }), [openedItems, tabRowStatus])

  // currentTaskId 是当前目录上「最该看的那个任务」，只用于右栏 M 角标的数据源。
  // 一个目录下可能有多个任务，取第一个正在跑的，没有就取第一个——角标是装饰，
  // 选谁都不影响正确性，但要稳定（不随渲染抖动）。
  const currentTaskId = useMemo(() => {
    const taskBase = fileDrawer ?? wb.base
    if (!taskBase || taskBase.kind !== 'workspace') return null
    const project = treeState.data?.projects.find((candidate) => candidate.name === taskBase.projectName)
    if (!project) return null
    const projectId = project.project_id
    // 与 ProjectTree.tasksOfWorkspace 保持同一归属口径：空 work_dir 只代表主目录的原地任务，
    // 不能按非空路径比较，否则主目录文件抽屉拿不到对应 diff。
    const isMainDirectory = project.locations.some((location) =>
      location.machine === taskBase.machine && location.workspaces.some((workspace) =>
        workspace.is_main && workspace.path === taskBase.path,
      ),
    )
    const under = tasks.filter((t) =>
      t.project_id === projectId && t.machine === taskBase.machine &&
      (t.work_dir === taskBase.path || (isMainDirectory && t.work_dir === '')),
    )
    return under.find((t) => t.state === 'running')?.id ?? under[0]?.id ?? null
  }, [tasks, fileDrawer, wb.base, treeState.data])

  // 薄壳里窗口顶部那 28px 是 AppKit 的隐形拖动区（左键被拿去拖窗口，传不到
  // 页面）。与其空着，不如让它承担面包屑那一行的展示职责——面包屑本来就零
  // 交互，落在吞点击的区域里零代价，页面反而省下原来那一整行。
  // 浏览器里 desktop 为 false，这条不渲染，布局与从前一模一样。
  const desktop = isDesktopShell()
  const focusedBase = focusedPaneBase(wb.wb)
  // focusedTab：焦点窗格里的 tab 本体。面包屑第三段（内容名）与左栏焦点态共用。
  const focusedTab = focusedTabOf(wb.wb)
  // 面包屑第三段跟焦点窗格的内容名（spec §3）：tui=任务原名、file=文件名、
  // terminal=终端标题；空白窗格或没有焦点内容时不传，行里回落目录名。
  const crumbTail = focusedBase && focusedTab && focusedTab.content.kind !== 'blank'
    ? tabTitle(focusedTab.content, focusedBase.label, taskNameResolver)
    : undefined
  // focusedTaskId：焦点窗格是 tui 内容时的 taskId，左栏任务行据此画焦点态。
  const focusedTaskId = focusedTab && focusedTab.content.kind === 'tui' ? focusedTab.content.taskId : null

  // 裁决横幅判据（B369.10 岔口 4）：焦点**组**内存在 tui 窗格、其任务处于
  // waiting_review，或该任务有挂起工单。判据字面取「焦点窗格是 tui」会让
  // 切到终端段横幅即消失，直接违背原型 note ①「钉在顶部，切对话/终端/文件
  // 都不丢」的法定语义——落点在 Shell 层（不进 WorkbenchPage 窗格树，B369.9
  // 两把 pty-host 锁的守恒前提），组内切换只动 wb 焦点、横幅不随之卸载。
  // 时差备注：判据源是任务流（2.5s）与工单聚合，与 TUI 会话流存在 ≤2.5s
  // 时差——横幅是提示面、允许早晚、不二次轮询，不承载状态迁移（spec §6 风险 3）。
  const bannerTask = useMemo(() => {
    if (!compact) return null
    const group = wb.wb.groups.find((g) => g.id === wb.wb.activeGroupId)
    if (!group) return null
    for (const column of group.columns) {
      for (const pane of column.panes) {
        if (pane === null || pane.content.kind !== 'tui') continue
        const taskId = pane.content.taskId
        const task = tasks.find((t) => t.id === taskId)
        if (task === undefined) continue
        const ticketCount = tickets.items.filter((item) => item.task.id === taskId).length
        if (task.state === 'waiting_review' || ticketCount > 0) {
          return { tabId: pane.id, taskId, inReview: task.state === 'waiting_review', ticketCount }
        }
      }
    }
    return null
  }, [compact, wb.wb.groups, wb.wb.activeGroupId, tasks, tickets.items])

  // handleWorktreeCreated 是「建完工作树」的唯一善后：先刷新树再选中。选中只改
  // useWorkbench 的 base，树上那一行要等这次 refresh 回来才会出现，两件事都必须
  // 做。ProjectTree 机器行与移动项目详情面（B369.10）两个入口共用，不得分叉。
  const handleWorktreeCreated = useCallback((project: ProjectNode, machine: string, ws: Workspace) => {
    treeState.refresh()
    wb.select(workspaceBase(project, machine, ws))
  }, [treeState, wb])

  // projectTree 是项目树的唯一实例来源：桌面左栏与紧凑视口「项目」tab 共用同一份
  // JSX（P1=A 一份产物）。两处同时挂载会撞 testid，故用 `!compact` 门保证任一时刻
  // 只挂一个实例（见下面两处消费点）。
  const projectTree = treeState.data === null ? null : (
    <ProjectTree
      tree={treeState.data}
      tasks={tasks}
      selectedKey={fileDrawer?.key ?? wb.base?.key ?? null}
      ticketCount={tickets.count}
      ticketsByDir={tickets.byWorkDir}
      openItems={openItems}
      focusedTaskId={focusedTaskId}
      onFocusOpenItem={openWorkbenchItem}
      onCloseOpenItem={closeOpenItem}
      onOpenTerminalAt={openTerminalAt}
      onOpenDirectory={openDirectory}
      onOpenTask={openTaskTui}
      previews={previews}
      previewMachines={previewsState.data?.machines ?? []}
      previewOpenKeys={previewsState.openKeys}
      previewOpeningKeys={previewsState.openingKeys}
      onOpenPreview={(id, machine) => { void previewsState.open(id, machine).catch(() => {}) }}
      onOpenBoard={() => setOverlay('board')}
      onOpenCards={openCardsSurface}
      onOpenProjectCards={ledgerEnabled ? openProjectCards : undefined}
      // B369.10 T3：compact 项目行的主点击落点 = 移动项目详情面（nav 的 project
      // 参数，岔口 1）。桌面不传：行点击维持折叠 toggle，nav.projectId 桌面恒 null。
      onOpenProjectDetail={compact ? (p) => nav.setProject(p.project_id) : undefined}
      // B369.7 死入口处置：/flows、/codegraph 整页路由只在桌面注册，紧凑下不注入
      // 回调（ProjectTree 据此隐藏按钮并渲染解释文案）；桌面分支原样。
      onOpenFlows={compact ? undefined : () => navigate('/flows')}
      ledgerEnabled={ledgerEnabled}
      cardNeedsCount={cardNeedsCount}
      unlinkedCount={unlinkedTaskIds?.size ?? 0}
      onOpenTickets={() => setOverlay('tickets')}
      onOpenSettings={openSettingsSurface}
      onOpenCodegraph={compact ? undefined : () => navigate('/codegraph')}
      onOpenProjectCodegraph={compact ? undefined : openProjectCodegraph}
      compact={compact}
      onAddProject={() => setWizardOpen(true)}
      onEdit={(p) => setEditProject(p)}
      onUnregister={onUnregister}
      onWorktreeCreated={handleWorktreeCreated}
    />
  )

  // detailProject 是紧凑详情层的反查目标（B369.10 岔口 1）：project_id 是不透明
  // id，树上反查不到（树未到/项目被注销/bogus 深链）时详情层不渲染、列表照常
  // ——与 dir「键失效 → 覆盖层不渲染」同款自愈。桌面 projectId 恒 null。
  const detailProject = compact && nav.projectId !== null
    ? treeState.data?.projects.find((p) => p.project_id === nav.projectId) ?? null
    : null

  return (
    <div className="flex h-dvh flex-col overflow-hidden bg-background">
      {desktop && <DesktopTitleBar base={focusedBase} />}
      <div className="flex min-h-0 flex-1">
      {/* 左栏自身不滚：滚动交给 ProjectTree 内部的树区，好让底部入口钉在底部。
          min-h-0 是必须的——flex 子项默认 min-height:auto，缺它内部的
          overflow-y-auto 不会生效，树会把父容器撑高、footer 照样被顶出去 */}
      {!compact && (<ResizableSidebar>
        {treeState.sessionExpired && <SessionExpiredBanner />}
        {treeState.disconnected && !treeState.sessionExpired && (
          <DisconnectedBanner message={treeState.errorText} compact />
        )}
        {sync.error !== '' && (
          <DisconnectedBanner message={`工作台状态恢复失败，本次不会保存布局：${sync.error}`} compact />
        )}
        {/* 左栏两 tab（B361）：会话 | 任务。双挂载、以 hidden class 切换——
            不用 hidden 属性：attribute 会让 jsdom 的可达性查询排除整个面板，
            既有树交互测试全部失效；class 切换在真机同为 display:none（台账 Task 4）。 */}
        <div className="flex shrink-0 border-b" role="tablist" aria-label="左栏视图">
          {ledgerEnabled && (
            <button type="button" role="tab" aria-selected={sidebarTab === 'sessions'} data-testid="sidebar-tab-sessions"
              onClick={() => setSidebarTab('sessions')}
              className={`flex flex-1 items-center justify-center gap-1.5 py-2 text-[13px] ${sidebarTab === 'sessions' ? 'border-b-2 border-primary font-semibold' : 'text-muted-foreground'}`}>
              会话
              {totalUnread(sessions) > 0 && (
                <span data-testid="sidebar-unread" className="min-w-4 rounded-full bg-red-500 px-1 text-center text-[10px] leading-4 text-white">{totalUnread(sessions)}</span>
              )}
            </button>
          )}
          <button type="button" role="tab" aria-selected={sidebarTab === 'tasks'} data-testid="sidebar-tab-tasks"
            onClick={() => setSidebarTab('tasks')}
            className={`flex flex-1 items-center justify-center py-2 text-[13px] ${sidebarTab === 'tasks' ? 'border-b-2 border-primary font-semibold text-foreground' : 'text-muted-foreground'}`}>
            任务
          </button>
        </div>
        {ledgerEnabled && (
          <div className={`flex min-h-0 flex-1 flex-col ${sidebarTab !== 'sessions' ? 'hidden' : ''}`}>
            {/* B406：401 终止态不算 loading（否则永转圈），过期面交由 expired 渲染 */}
            <SessionSidebar sessions={sessions}
              loading={sessionsState.data === null && !sessionsState.disconnected && !sessionsState.sessionExpired}
              errorText={sessionsState.disconnected ? sessionsState.errorText : ''}
              expired={sessionsState.sessionExpired}
              needsOnly={needsOnly}
              onToggleNeeds={() => setNeedsOnly((current) => !current)}
              projectFilter={projectFilter}
              onProjectFilter={setProjectFilter}
              projectOptions={projectOptions}
              projectOfCard={projectOfCard}
              onOpen={openSession}
              onCreate={() => { setCreateError(''); setCreateOpen(true) }} />
          </div>
        )}
        {/* 账本未启用时会话 tab 不渲染，任务 pane 兜底可见（hidden 恒不挂） */}
        <div className={`flex min-h-0 flex-1 flex-col ${ledgerEnabled && sidebarTab !== 'tasks' ? 'hidden' : ''}`}>
          {projectTree}
        </div>
      </ResizableSidebar>)}

      {/* min-h-0 / min-w-0 与左栏同一条：flex 子项默认 min-size:auto。
          B318 只补了高度——常驻栏不再被会话列表撑高。无头 Chrome 在 /cards
          上仍量到 document.scrollWidth=22486（视口 1440）：工作台常驻在 main
          里，min-width:auto 按终端画布固有宽把 main 撑到两万像素，常驻栏被
          推到 x=22126，空白处横滑 scrollLeft 带动左栏一起走。overflow-hidden
          把泄漏切断在壳内，不让它变成窗口滚动条。 */}
      <div className={`relative flex min-h-0 min-w-0 flex-1 overflow-hidden ${cardsRoute ? 'flex-row' : 'flex-col'}`}>
        {/* 薄壳里这一行不画：同样的内容已经在窗口顶部那条 28px 上，
            两处都画就是把一行重复了两遍 */}
        {focusedBase && !desktop && !compact && !fullPageRoute && <Breadcrumb base={focusedBase} tail={crumbTail} />}
        <main className="relative min-h-0 min-w-0 flex-1 overflow-hidden">
          {/* 工作台常驻。整页路由盖在上面，不走 path="*" 卸载——卸了 xterm
              会断 WS 再重放 1004h，OpenTUI/Grok 卡死（B270 的病在整页入口复发）。
              不用 display:none / invisible：那些会捏尺寸——B280 约束「不卸载、
              不捏尺寸、WS 不断」依旧全禁。B369.8（T7）：覆盖期加条件三件套
              aria-hidden + inert + pointer-events-none（沿 WorkbenchPage 后台组
              先例，覆盖层后面的后台内容对读屏与键盘不可达）——都是属性/类式闸，
              覆盖期才挂、掀开即整体摘除，keep-alive 语义不变（不卸载、不捏
              尺寸、WS 不断；TerminalTab :378-380 事件层已兼容 [aria-hidden]/
              [inert] 祖先；命中随类摘除而回来）。
              B369.9：singleFocus 只在 phone 档下传（phone 专用投影；pad/desktop
              不投影，岔口裁决见 b369.9-plan §3.1——compact 含 pad，不能沿用）。 */}
          <div
            data-testid="workbench-underlay"
            className={`h-full min-w-0 overflow-hidden${workbenchCovered ? ' pointer-events-none' : ''}`}
            aria-hidden={workbenchCovered}
            {...(workbenchCovered ? { inert: true } : {})}
          >
            <WorkbenchPage
              api={wb}
              singleFocus={viewport === 'phone'}
              onAddProject={() => setWizardOpen(true)}
              tree={treeState.data}
              tasks={tasks}
              taskName={taskNameResolver}
              onFileCreated={() => setFileTreeNonce((n) => n + 1)}
              terminalUnavailable={wb.base ? ptyNote(wb.base.machine) : ''}
              launchers={launchersSupported ? (launchersData?.launchers ?? []) : []}
              onBeforeClose={beforeCloseTab}
              renderContent={(c, base, group, tabId, active = true) => {
                switch (c.kind) {
                  case 'terminal': {
                    const note = ptyNote(base.machine)
                    if (note !== '') {
                      return <p className="p-4 text-sm text-muted-foreground">{note}</p>
                    }
                    const launcher = c.launcher
                      ? launchersData?.launchers.find((item) => item.name === c.launcher)
                      : undefined
                    return (
                      <TerminalTab
                        base={base}
                        seq={c.seq}
                        sessionId={c.sessionId}
                        spawn={c.spawn === true}
                        rel={c.rel}
                        envFile={launcher?.env_file}
                        initCommand={c.initCommand ?? launcher?.command}
                        incompatible={c.incompatible}
                        active={active && !fullPageRoute}
                        keybar={compact}
                        // 会话 id 必须写回这个 tab：不写回的话切一次 tab
                        // 就会再建一个会话，用户每切一次多留一个 shell
                        onSession={(id) => wb.setContent(group, tabId, { ...c, sessionId: id, incompatible: false })}
                        // 连接状态上报进左栏圆点（绿连红断，2026-08-29）
                        onConnection={(connected) => reportPtyConnection(tabId, connected)}
                      />
                    )
                  }
                  case 'file':
                    return (
                      <FileTab
                        base={base}
                        rel={c.rel}
                        initial={
                          c.draft !== undefined && c.baseSha !== undefined
                            ? { draft: c.draft, baseSha: c.baseSha }
                            : undefined
                        }
                        // 草稿在 pane 常驻时不能等卸载才寄存：分屏切焦点不会卸载
                        // FileTab，live 缝保证关闭入口能看到最新未保存内容；卸载回调
                        // 仍保留，覆盖切换 group/整页路由的路径
                        onDraftChange={(d) =>
                          wb.setContent(group, tabId, {
                            kind: 'file',
                            rel: c.rel,
                            draft: d?.draft,
                            baseSha: d?.baseSha,
                          })
                        }
                        onDraftChangeLive={(d) =>
                          wb.setContent(group, tabId, {
                            kind: 'file',
                            rel: c.rel,
                            draft: d?.draft,
                            baseSha: d?.baseSha,
                          })
                        }
                        // 冲突/删除上报进左栏圆点（冲突红、删灰；已编辑由
                        // 草稿有无在 openItems 投影处判，2026-08-29）
                        onStatus={(status) => reportFileStatus(tabId, status)}
                        // B369.10 T9：compact 只读档（390 无编辑交互）。home 浮窗
                        // 的 file tab（下方独立挂载）不传，桌面语义。
                        compact={compact}
                      />
                    )
                  case 'session':
                    return (
                      <SessionTab
                        sessionId={c.sessionId}
                        title={c.title}
                        // B369.8 T5：compact 用「群聊|详情」两态替换「⋯」抽屉。
                        compact={compact}
                        onOpenCard={(cardId) => {
                          // B369.7：紧凑下会话卡身份只进卡 tab/对应卡详情（带会话来源，
                          // 不直接跳任务现场）；桌面走既有 /cards 深链。
                          if (compact) nav.openCard(cardId, `session-${c.sessionId}`)
                          else navigate(`/cards?card=${encodeURIComponent(cardId)}`)
                        }}
                      />
                    )
                  case 'tui':
                    return <TuiTab taskId={c.taskId} compact={compact} />
                  default:
                    return null
                }
              }}
            />
          </div>
          <Routes>
            {!compact && ledgerEnabled && (
              <>
                <Route path="/cards" element={<FullPageCover><CardsPage onOpenCoordinatorTerminal={openCoordinatorTerminal} /></FullPageCover>} />
                <Route path="/flows" element={<FullPageCover><FlowsPage /></FullPageCover>} />
              </>
            )}
            {!compact && (
              <Route
                path="/settings"
                element={<FullPageCover><SettingsPage onClose={() => navigate('/')} /></FullPageCover>}
              />
            )}
            {/* /codegraph 的 viewer 唯一来源是同源 iframe；它不在 Shell 内复制取数或凭据。 */}
            {!compact && (
              <Route
                path="/codegraph"
                element={<FullPageCover><CodegraphFrame project={routeParams.get('project') ?? wb.base?.projectName ?? ''} /></FullPageCover>}
              />
            )}
            <Route path="/machines" element={<Navigate to="/settings" replace />} />
            <Route path="/tasks/:id" element={<FullPageCover><TaskDeepLink tree={treeState.data} tasks={tasks} onOpen={openTaskTui} compact={compact} /></FullPageCover>} />
          </Routes>
          {/* 紧凑视口的移动内容面。用绝对覆盖层而不是条件卸载：WorkbenchPage 必须
              常驻（B280——卸了 xterm 会断 WS 再重放，OpenTUI/Grok 卡死），覆盖层
              与既有 FullPageCover 同一手法，只是带底栏且由 tab 状态驱动。
              mobileDetail 为真时整层让开，露出下面的常驻工作台（下钻态）。 */}
          {compact && !nav.detail && (
            <div data-testid="mobile-home" className="absolute inset-0 z-30 flex min-h-0 flex-col bg-background">
              <div className="min-h-0 flex-1 overflow-auto">
                {nav.tab === 'sessions' && (
                  ledgerEnabled ? (
                    <SessionSidebar
                      sessions={sessions}
                      loading={sessionsState.data === null && !sessionsState.disconnected && !sessionsState.sessionExpired}
                      errorText={sessionsState.disconnected ? sessionsState.errorText : ''}
                      expired={sessionsState.sessionExpired}
                      needsOnly={needsOnly}
                      onToggleNeeds={() => setNeedsOnly((current) => !current)}
                      projectFilter={projectFilter}
                      onProjectFilter={setProjectFilter}
                      projectOptions={projectOptions}
                      projectOfCard={projectOfCard}
                      onOpen={openSession}
                      onCreate={() => { setCreateError(''); setCreateOpen(true) }}
                      compact={compact}
                    />
                  ) : (
                    <p className="p-4 text-sm text-muted-foreground">账本未启用，会话不可用。</p>
                  )
                )}
                {nav.tab === 'cards' && (
                  <CardsPage
                    onOpenCoordinatorTerminal={openCoordinatorTerminal}
                    onDrawerCardChange={onDrawerCardChange}
                    taskJumpHref={taskJumpHref}
                    compact={compact}
                    onOpenSessionForCard={onOpenSessionForCard}
                    driverSessionReady={driverSessionReady}
                    sharedData={{ cards: cardsState, decisions: decisionsState, tasks: tasksState }}
                  />
                )}
                {nav.tab === 'projects' && (
                  // B369.7 验收实走修正：ProjectTree 根是 flex-1 三段式（树独滚、
                  // 底部入口行与解释文案钉底），只在有界的 flex 父级里成立；
                  // mobile-home 的滚动容器是普通块级，不包裹的话页脚会被 16 个
                  // 项目推到 scrollHeight 底（实测 note top:6454 / 视口 844），
                  // 「隐藏并给出解释」的解释永远不在视口内。h-full 让 ProjectTree
                  // 自带的三段式在紧凑视口照常钉底；桌面 aside 不经过此处，零接触。
                  // B369.10：详情层以 absolute 覆盖层挂在同一 relative 容器内——
                  // ProjectTree 保挂载，折叠集/搜索词/滚动不因进出详情丢失
                  // （mobile-dir 同款手法）；mobile-dir 是 main 层兄弟覆盖层、DOM 序
                  // 在后，project+dir 同持时目录层天然盖在详情层上，「详情→浏览文件
                  // →返回」的层序零代码。详情层在 mobile-home 之内，不算覆盖期闸对象。
                  <div className="relative flex h-full min-h-0 flex-col">
                    {projectTree ?? <p className="p-4 text-sm text-muted-foreground">正在读取项目…</p>}
                    {detailProject !== null && (
                      <MobileProjectDetail
                        project={detailProject}
                        machines={treeState.data?.machines}
                        tasks={tasks}
                        ptySessions={ptySessionsState.data?.sessions ?? null}
                        onBack={() => nav.setProject(null)}
                        onOpenTerminalAt={openTerminalAt}
                        onOpenDirectory={openDirectory}
                        onOpenTask={(base, taskId) => openTaskTui(base, taskId)}
                        onWorktreeCreated={handleWorktreeCreated}
                        onReopenPtySession={(sessionId, base) => {
                          // restoreTerminal 消费既有恢复 seam（自动去重/找位），
                          // 不新建终端承载（B280 keep-alive 神圣）；restoreTerminal
                          // 之后 detail=1 下钻，露出常驻工作台。
                          wb.restoreTerminal(base, sessionId)
                          openMobileDetail()
                        }}
                      />
                    )}
                  </div>
                )}
                {nav.tab === 'settings' && (
                  // B369.8：compact 两级设置 IA——sub 从 nav（URL）读、写经 nav.setSub
                  // 收口 URL；组件自身不碰 router（SettingsPage 文件头「组件边界」）。
                  <SettingsPage
                    onClose={() => nav.setTab('sessions')}
                    compact
                    sub={nav.sub}
                    onSubChange={nav.setSub}
                  />
                )}
              </div>
              <MobileTabBar
                active={nav.tab}
                onSelect={nav.setTab}
                // B369.8：badges=false 时 needsCount/unread 双双归 0（设置中心「提醒」
                // 门控）；只门控底栏，SessionSidebar 行内未读点不受影响。
                needsCount={webPrefs.badges && ledgerEnabled ? cardNeedsCount : 0}
                unread={webPrefs.badges ? totalUnread(sessions) : 0}
              />
            </div>
          )}
          {/* 下钻态的返回条：没有它，手机用户进了会话/任务现场就回不到底栏首页
              （桌面靠左栏与面包屑，手机两者都不挂）。固定在顶部，含当前焦点内容名。
              紧凑视口下 fullPageRoute 恒假（S2d-1），故不与整页路由互压。 */}
          {compact && nav.detail && (
            // B369.10：单条 absolute bar 升格为「bar + 裁决横幅」的组合容器——
            // 横幅判据与落点理由见 bannerTask memo（Shell 层、不进窗格树）。
            <div className="absolute inset-x-0 top-0 z-30 flex min-h-0 flex-col">
              {bannerTask !== null && (
                <div
                  data-testid="task-verdict-banner"
                  // B369.10 review 建议修：新面根节点挂触点基线（spec §3.2「compact 新
                  // 面根节点挂 TOUCH_BASELINE」），把「去查证」抬到 24×24 底线。
                  // 不给它 min-h-11：原型 .banner .acts button（mobile-task.html:43）
                  // 是 ~30px 的窄条主动作，44px 会与原型形态相左——两档分工里这是
                  // 次级档，形态权威仍是原型。padding 对齐原型（py-1.5≈28px）。
                  className={cn(
                    'flex items-center gap-2 border-b border-amber-200 bg-amber-50 px-2 py-1.5 text-xs text-amber-900',
                    TOUCH_BASELINE,
                  )}
                >
                  <span className="min-w-0 flex-1 truncate">
                    {bannerTask.inReview
                      ? '等你裁决 · 交付与作答在对话段'
                      : `${bannerTask.ticketCount} 张工单等你答复`}
                  </span>
                  {/* 去查证 = 激活该 tui 窗格（焦点切回对话段）；waiting_review 的
                      审阅面由 TuiTab 既有 effect 自动展开。横幅不承载作答。 */}
                  <button
                    type="button"
                    data-testid="task-verdict-activate"
                    onClick={() => wb.activate(wb.wb.activeGroupId, bannerTask.tabId)}
                    className="shrink-0 rounded border border-amber-300 px-2 py-1.5 font-medium text-amber-900 hover:bg-amber-100"
                  >
                    去查证
                  </button>
                </div>
              )}
              <div
                data-testid="mobile-detail-bar"
                className="flex items-center gap-2 border-b bg-background px-2 py-1.5"
              >
              <button
                type="button"
                data-testid="mobile-detail-back"
                onClick={() => {
                  // B369.7 返回条按来源分派（plan §3.2）：会话来源重建会话 tab 且
                  // 仍在下钻态（from 清除，再返回走无 from 兜底——逐级出栈）；卡来源
                  // 落 /cards?card=<id> 不带 from（from=card-Y 意味着卡详情原本无
                  // 来源，没有「来源的来源」可恢复）；无 from 走 exitDetail 兜底。
                  const src = nav.from
                  if (src !== null && src.startsWith('session-')) {
                    const sessionId = src.slice('session-'.length)
                    const session = sessions.find((s) => s.id === sessionId)
                    wb.openOrFocus({ kind: 'session', sessionId, title: session?.title ?? sessionId }, sessionBase(sessionId))
                    console.debug('shell.mobile_nav.detail_back', { from: src })
                    navigate('/?tab=sessions&detail=1', { replace: true })
                  } else if (src !== null && src.startsWith('card-')) {
                    console.debug('shell.mobile_nav.detail_back', { from: src })
                    navigate(`/cards?card=${encodeURIComponent(src.slice('card-'.length))}`, { replace: true })
                  } else {
                    nav.exitDetail()
                  }
                }}
                className="rounded px-2 py-1 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
              >
                ‹ 返回
              </button>
              <span className="min-w-0 flex-1 truncate text-sm">{crumbTail ?? focusedBase?.label ?? ''}</span>
              </div>
            </div>
          )}
          {/* 紧凑视口的目录面（项目 tab → 位置 → 目录）：右栏无处安放，改为覆盖层，
              带返回条回项目列表。文件/终端下钻切工作台（S2b 的两个回调）——
              下钻时本层让开（!nav.detail），返回条再把它露回来，形成
              任务/文件 → 目录 → 底栏首页 的逐级返回。 */}
          {compact && fileDrawer !== null && !nav.detail && (
            <div data-testid="mobile-dir" className="absolute inset-0 z-30 flex min-h-0 flex-col bg-background">
              <div className="flex shrink-0 items-center gap-2 border-b px-2 py-1.5">
                <button
                  type="button"
                  data-testid="mobile-dir-back"
                  onClick={() => nav.setDir(null)}
                  className="rounded px-2 py-1 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
                >
                  ‹ 返回
                </button>
                <span className="min-w-0 flex-1 truncate text-sm">{fileDrawer.label}</span>
              </div>
              <div className="min-h-0 flex-1 overflow-hidden">
                <FileTree
                  base={fileDrawer}
                  refreshKey={fileTreeNonce}
                  taskId={currentTaskId}
                  onOpenFile={(rel) => openMobileFile(fileDrawer, rel)}
                  onOpenTerminal={(rel) => openMobileTerminal(fileDrawer, rel)}
                  revealSupported={caps.reveal('')}
                  onClose={() => nav.setDir(null)}
                  compact={compact}
                />
              </div>
            </div>
          )}
        </main>
      </div>

      {/* scratch 不是可选中的 wb 基准，只被浮窗 file tab 使用，所以不该渲染右栏文件树。 */}
      {!compact && fileDrawer !== null && !fullPageRoute && (
        <div className="w-[280px] shrink-0">
          <FileTree
            base={fileDrawer}
            refreshKey={fileTreeNonce}
            taskId={currentTaskId}
            onOpenFile={(rel) => wb.open({ kind: 'file', rel }, fileDrawer)}
            onOpenTerminal={(rel) => wb.openTerminal(fileDrawer, undefined, rel)}
            revealSupported={caps.reveal('')}
            onClose={() => {
              setDesktopFileDrawer(null)
              console.debug('shell.directory.close', { project: fileDrawer.projectName, machine: fileDrawer.machine, baseKey: fileDrawer.key, path: fileDrawer.path })
            }}
          />
        </div>
      )}
      </div>

      {/* home 终端走独立浮窗，不进 wb 的 tab 组——它不挂在任何目录上，
          塞进按目录组织的容器里就会跟着目录切换走 */}
      {!compact && caps.pty('') !== false && (
        <HomeDock
          dock={dock}
          onKill={killHomeSession}
          onNewFile={scratchRoot === '' ? undefined : newScratchFile}
          renderTab={(t, active = true) =>
            t.kind === 'file' ? (
              <FileTab
                base={scratchBase(scratchRoot, t.machine)}
                rel={t.rel ?? ''}
                initial={
                  t.draft !== undefined && t.baseSha !== undefined
                    ? { draft: t.draft, baseSha: t.baseSha }
                    : undefined
                }
                onDraftChange={(d) => dock.setDraft(t.id, d)}
              />
            ) : (
              <TerminalTab
                key={t.id}
                base={HOME_BASE}
                seq={t.seq}
                sessionId={t.sessionId}
                spawn={t.spawn === true}
                incompatible={t.incompatible}
                active={active}
                onSession={(id) => dock.setSession(t.id, id)}
              />
            )
          }
        />
      )}

      {scratchError !== '' && (
        <p role="alert" className="fixed right-5 bottom-24 z-40 rounded border border-destructive/30 bg-background px-3 py-1.5 text-xs text-destructive shadow">
          临时文件失败：{scratchError}
        </p>
      )}

      {overlay === 'board' && (
        <BoardOverlay
          tasksState={tasksState}
          tree={treeState.data}
          unlinkedTaskIds={unlinkedTaskIds}
          ledgerEnabled={ledgerEnabled}
          onOpenTask={openTaskTui}
          onClose={() => setOverlay('none')}
        />
      )}
      {overlay === 'tickets' && (
        <TicketsOverlay
          tickets={tickets}
          onOpenTask={openTaskTui}
          onClose={() => setOverlay('none')}
        />
      )}

      <ConfirmDialog
        open={closingPty !== null || closingHome !== null}
        title="关闭终端会话"
        description={
          closingGone === true
            ? // 会话已经不在了：没有东西可终止，这一步只是把 tab 收掉
              '这个终端会话在服务端已经不存在了（终端会话跨 agentd 重启存活，只有机器重启、退出 shell 或显式停止才会让它消失）。\n' +
              '关闭只是把这个 tab 收起来，不会再终止什么。'
            : '关闭会终止这个终端会话，里面正在运行的命令会被一并结束。\n' +
              '只是想切走的话直接切到别的 tab——会话会继续在后台跑。' +
              (closingBusyProc === true ? '\n\n⚠ 这个终端里现在还有命令在运行。' : '')
        }
        confirmLabel={closingGone === true ? '关闭' : '关闭并终止'}
        destructive={closingGone !== true}
        busy={closeBusy}
        error={closeError}
        onConfirm={() => void (closingPty ? confirmClosePty() : confirmCloseHome())}
        onCancel={() => { setClosingPty(null); setClosingHome(null) }}
      />

      <ConfirmDialog
        open={closingDirtyFile !== null || closingDirtyHome !== null}
        title="关闭未保存的文件"
        description={
          `${closingDirtyFile?.rel ?? closingDirtyHome?.rel ?? ''} 还有未保存的改动，关掉就没了。\n` +
          // 文案要点明「切 tab 不丢」：Task 8 刚让草稿在切走时回写进 tab 内容，
          // 用户不知道这件事，误以为必须二选一。切走是零成本的
          '只是想看别的东西的话直接切到别的 tab——草稿会留着。'
        }
        confirmLabel="不保存，关闭"
        destructive
        onConfirm={() => {
          if (closingDirtyFile) wb.closeById(closingDirtyFile.tabId)
          if (closingDirtyHome) dock.closeTab(closingDirtyHome.id)
          setClosingDirtyFile(null)
          setClosingDirtyHome(null)
        }}
        onCancel={() => { setClosingDirtyFile(null); setClosingDirtyHome(null) }}
      />

      <NewSessionDialog open={createOpen} busy={createBusy} error={createError} configured={consoleConfigured}
        onCancel={() => setCreateOpen(false)} onCreate={(title) => void confirmCreateSession(title)} />

      <AddProjectWizard
        open={wizardOpen}
        machines={machinesState.data?.machines ?? []}
        onClose={() => setWizardOpen(false)}
        onDone={() => treeState.refresh()}
      />

      <ProjectEditDialog
        open={editProject !== null}
        project={editProject}
        onClose={() => setEditProject(null)}
        onDone={() => treeState.refresh()}
      />
    </div>
  )
}

// FullPageCover 把设置/工作项等整页盖在常驻工作台上。
// 不透明底 + 更高 z-index，观感仍是「中央换成整页」；工作台在下面保持
// 原尺寸，避免 xterm 被卸掉或捏成 0。
function FullPageCover({ children }: { children: ReactNode }) {
  return <div className="absolute inset-0 z-20 overflow-auto bg-background">{children}</div>
}

// TaskDeepLink 承接 /tasks/:id 这条 W3b 留下的深链。
//
// 为什么保留：已有书签与 --notify 的通知文案里都可能带这个地址，直接删路由会
// 让它们 404。行为改为「选中该任务所在目录 + 开它的 TUI tab + 换回 /」——地址栏
// 不停在一个不再有对应页面的路径上。
// B181 起 /cards 抽屉里每行任务的 ↗ 也落到这条深链——它是本路由的第二个消费者；
// 目录解析/开 TUI tab 的行为以这里为唯一实现，消费方不得复制。
// B369.7：compact 下 from/tab 参数原样透传给 openTaskTui（→ nav.enterDetail 落
// 任务现场 URL），终态导航省略——URL 已由 enterDetail 以 replace 收口；桌面保留
// navigate('/', { replace: true })。
function TaskDeepLink({
  tree,
  tasks,
  onOpen,
  compact,
}: {
  tree: ProjectTreeResp | null
  tasks: Task[]
  onOpen: (base: BaseDir | null, taskId: string, opts?: { from?: string | null; tab?: string | null }) => void
  compact: boolean
}) {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [searchParams] = useSearchParams()
  const [done, setDone] = useState(false)

  useEffect(() => {
    // 等树到位再解析目录：树还没来时目录解析不出来，会把 tab 开在错的基准上
    if (!id || done || !tree) return
    onOpen(
      findBaseOfTask(tree, tasks, id),
      id,
      compact ? { from: searchParams.get('from'), tab: searchParams.get('tab') } : undefined,
    )
    setDone(true)
    if (!compact) navigate('/', { replace: true })
  }, [id, done, tree, tasks, onOpen, navigate, compact, searchParams])

  return <p className="p-4 text-sm text-muted-foreground">正在打开任务…</p>
}
