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
import { fetchCards, fetchDecisions } from '../../api/ledger'
import type { ProjectNode, ProjectTreeResp, Task } from '../../api/types'
import { useMachines } from '../data/useMachines'
import { useProjectTree } from '../data/useProjectTree'
import { useTasks } from '../data/useTasks'
import { usePreviews } from '../data/usePreviews'
import { useMachineCaps } from '../data/useMachineCaps'
import { useLedgerEnabled } from '../data/useLedgerEnabled'
import { usePoll } from '../data/usePoll'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { isDesktopShell } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { AddProjectWizard } from '../projects/AddProjectWizard'
import { ProjectEditDialog } from '../projects/ProjectEditDialog'
import { findBaseByKey, findBaseOfTask, ProjectTree, workspaceBase, type OpenItem } from '../tree/ProjectTree'
import { FileTree } from '../files/FileTree'
import { WorkbenchPage } from '../workbench/WorkbenchPage'
import { TerminalTab } from '../workbench/TerminalTab'
import { FileTab } from '../workbench/FileTab'
import { TuiTab } from '../workbench/TuiTab'
import { HomeDock } from '../homedock/HomeDock'
import { useHomeDock } from '../homedock/useHomeDock'
import type { DockSnapshot } from '../homedock/dockPersist'
import { HOME_BASE, scratchBase, useWorkbench, type BaseDir } from '../workbench/useWorkbench'
import { createUntitledFile } from '../workbench/newFile'
import { nextTerminalSeq, tabTitle, type Tab, type TabContent, type Workbench } from '../workbench/tabs'
import { taskDisplayName } from '../lib/taskName'
import type { StateTone } from '../board/columns'
import { useWorkbenchSync } from '../workbench/useWorkbenchSync'
import { BoardOverlay } from '../overlay/BoardOverlay'
import { TicketsOverlay } from '../overlay/TicketsOverlay'
import { useGlobalTickets } from '../overlay/useGlobalTickets'
import { SettingsPage } from '../settings/SettingsPage'
import { CodegraphFrame } from '../codegraph/CodegraphFrame'
import { CardsPage } from '../cards/CardsPage'
import { FlowsPage } from '../flows/FlowsPage'
import { needsAttention } from '../cards/columns'
import { UpdateToasts } from '../update/UpdateToasts'
import { Breadcrumb } from './Breadcrumb'
import { DesktopTitleBar } from './DesktopTitleBar'
import { ResizableSidebar } from './ResizableSidebar'

// OverlayKind 是当前打开的弹层。同时只允许一个（spec §0）：两个叠在一起时
// Esc 该关哪个会变得含糊。
type OverlayKind = 'none' | 'board' | 'tickets'

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
  const location = useLocation()
  const [routeParams] = useSearchParams()

  const [overlay, setOverlay] = useState<OverlayKind>('none')
  const [wizardOpen, setWizardOpen] = useState(false)
  const [fileDrawer, setFileDrawer] = useState<BaseDir | null>(null)
  // fileTreeNonce 是右栏刷新的触发器。中央区新建文件后递增它。
  // 用计数器而不是把 FileTree 的 refresh 传上来：那会把中央区与右栏焊死，
  // 而它们现在互不认识
  const [fileTreeNonce, setFileTreeNonce] = useState(0)
  // editProject 是正在被编辑的项目（右键菜单「编辑」传入）；null = 弹层关闭。
  const [editProject, setEditProject] = useState<ProjectNode | null>(null)
  const machinesState = useMachines(wizardOpen)
  const tickets = useGlobalTickets(tasks)
  const { enabled: ledgerEnabled } = useLedgerEnabled()
  const cardsState = usePoll(fetchCards, 2500, { enabled: ledgerEnabled })
  const decisionsState = usePoll(() => fetchDecisions(true), 2500, { enabled: ledgerEnabled })
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
    const summary = cardsState.data?.unlinked
    if (!summary) return null
    return new Set((summary.tasks ?? []).map((task) => task.task_id))
  }, [cardsState.data])
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
  const backToWorkbench = () => {
    if (location.pathname !== '/') navigate('/')
  }

  // fullPageRoute = 中央区被整页替换掉的那些路由。
  //
  // why 要判它：右栏文件树与面包屑都挂在 <Routes> 外面、只跟 wb.base 走，
  // 于是点了目录再点「工作项」，中央换成了看板、右边那棵文件树却一直挂着，
  // 面包屑也还写着上一个目录（2026-08-19 真机看到）。它们是工作台的一部分，
  // 不属于这些整页。左栏导航树不在此列——它是导航，任何页面都该在。
  const fullPageRoute = ['/cards', '/flows', '/settings', '/machines', '/codegraph']
    .some((path) => location.pathname.startsWith(path))

  // onOpenDirectory 是左栏目录的完整入口：选中基准并打开可关闭的文件抽屉。
  // 抽屉自己的文件点击只开 tab，不清掉 drawer，直到用户明确点 X。
  const openDirectory = (base: BaseDir) => {
    backToWorkbench()
    wb.select(base)
    setFileDrawer(base)
    console.debug('shell.directory.open', { project: base.projectName, machine: base.machine, baseKey: base.key, path: base.path })
  }

  // openWorkbenchItem 是左栏「已打开行」的聚焦入口（onFocusOpenItem）。
  // focusTab 而不是 open：无会话终端等内容没有去重键，open 会开出第二个 tab。
  const openWorkbenchItem = (item: OpenItem) => {
    backToWorkbench()
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
    wb.select(base)
    wb.openOrFocus({ kind: 'terminal', seq: nextTerminalSeq(wb.wb) }, base)
    console.debug('shell.directory.terminal.new_group', {
      project: base.projectName, machine: base.machine, baseKey: base.key, path: base.path,
    })
  }

  // openTaskTui 是「点一个任务 → 在它所在目录开 TUI tab」的唯一实现。
  // 左栏任务行、看板卡片、/tasks/:id 深链、工单弹层的「跳到该任务」都走它。
  // 首参为 null（工单弹层、未归属任务）时先用树解析任务自己的目录；解析不出
  // （任务真的不在树上）才退回「当前选中目录」，一个都没选中则 wb.open 空操作。
  const openTaskTui = (base: BaseDir | null, taskId: string) => {
    setOverlay('none')
    backToWorkbench()
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

  return (
    <div className="flex h-dvh flex-col bg-background">
      {desktop && <DesktopTitleBar base={focusedBase} />}
      <div className="flex min-h-0 flex-1">
      {/* 左栏自身不滚：滚动交给 ProjectTree 内部的树区，好让底部入口钉在底部。
          min-h-0 是必须的——flex 子项默认 min-height:auto，缺它内部的
          overflow-y-auto 不会生效，树会把父容器撑高、footer 照样被顶出去 */}
      <ResizableSidebar>
        {treeState.sessionExpired && <SessionExpiredBanner />}
        {treeState.disconnected && !treeState.sessionExpired && (
          <DisconnectedBanner message={treeState.errorText} compact />
        )}
        {sync.error !== '' && (
          <DisconnectedBanner message={`工作台状态恢复失败，本次不会保存布局：${sync.error}`} compact />
        )}
        {treeState.data && (
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
            onOpenCards={() => navigate('/cards')}
            onOpenProjectCards={ledgerEnabled ? openProjectCards : undefined}
            onOpenFlows={() => navigate('/flows')}
            ledgerEnabled={ledgerEnabled}
            cardNeedsCount={cardNeedsCount}
            unlinkedCount={unlinkedTaskIds?.size ?? 0}
            onOpenTickets={() => setOverlay('tickets')}
            onOpenSettings={() => navigate('/settings')}
            onOpenCodegraph={() => navigate('/codegraph')}
            onOpenProjectCodegraph={openProjectCodegraph}
            onAddProject={() => setWizardOpen(true)}
            onEdit={(p) => setEditProject(p)}
            onUnregister={onUnregister}
            onWorktreeCreated={(project, machine, ws) => {
              // 先刷新树再选中：选中只改 useWorkbench 的 base，树上那一行要等
              // 这次 refresh 回来才会出现，两件事都必须做
              treeState.refresh()
              wb.select(workspaceBase(project, machine, ws))
            }}
          />
        )}
      </ResizableSidebar>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* 薄壳里这一行不画：同样的内容已经在窗口顶部那条 28px 上，
            两处都画就是把一行重复了两遍 */}
        {focusedBase && !desktop && !fullPageRoute && <Breadcrumb base={focusedBase} tail={crumbTail} />}
        <main className="relative min-h-0 flex-1">
          {/* 工作台常驻。整页路由盖在上面，不走 path="*" 卸载——卸了 xterm
              会断 WS 再重放 1004h，OpenTUI/Grok 卡死（B270 的病在整页入口复发）。
              不用 display:none / invisible / pointer-events-none：那些会捏尺寸
              或让 WKWebView 命中回不来。 */}
          <div className="h-full">
            <WorkbenchPage
              api={wb}
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
                        rel={c.rel}
                        envFile={launcher?.env_file}
                        initCommand={launcher?.command}
                        incompatible={c.incompatible}
                        active={active && !fullPageRoute}
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
                      />
                    )
                  case 'tui':
                    return <TuiTab taskId={c.taskId} />
                  default:
                    return null
                }
              }}
            />
          </div>
          <Routes>
            {ledgerEnabled && (
              <>
                <Route path="/cards" element={<FullPageCover><CardsPage /></FullPageCover>} />
                <Route path="/flows" element={<FullPageCover><FlowsPage /></FullPageCover>} />
              </>
            )}
            <Route
              path="/settings"
              element={<FullPageCover><SettingsPage onClose={() => navigate('/')} /></FullPageCover>}
            />
            {/* /codegraph 的 viewer 唯一来源是同源 iframe；它不在 Shell 内复制取数或凭据。 */}
            <Route
              path="/codegraph"
              element={<FullPageCover><CodegraphFrame project={routeParams.get('project') ?? wb.base?.projectName ?? ''} /></FullPageCover>}
            />
            <Route path="/machines" element={<Navigate to="/settings" replace />} />
            <Route path="/tasks/:id" element={<FullPageCover><TaskDeepLink tree={treeState.data} tasks={tasks} onOpen={openTaskTui} /></FullPageCover>} />
          </Routes>
        </main>
      </div>

      {/* scratch 不是可选中的 wb 基准，只被浮窗 file tab 使用，所以不该渲染右栏文件树。 */}
      {fileDrawer !== null && !fullPageRoute && (
        <div className="w-[280px] shrink-0">
          <FileTree
            base={fileDrawer}
            refreshKey={fileTreeNonce}
            taskId={currentTaskId}
            onOpenFile={(rel) => wb.open({ kind: 'file', rel }, fileDrawer)}
            onOpenTerminal={(rel) => wb.openTerminal(fileDrawer, undefined, rel)}
            revealSupported={caps.reveal('')}
            onClose={() => {
              setFileDrawer(null)
              console.debug('shell.directory.close', { project: fileDrawer.projectName, machine: fileDrawer.machine, baseKey: fileDrawer.key, path: fileDrawer.path })
            }}
          />
        </div>
      )}
      </div>

      {/* home 终端走独立浮窗，不进 wb 的 tab 组——它不挂在任何目录上，
          塞进按目录组织的容器里就会跟着目录切换走 */}
      {caps.pty('') !== false && (
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
                incompatible={t.incompatible}
                active={active}
                onSession={(id) => dock.setSession(t.id, id)}
              />
            )
          }
        />
      )}

      {/* 更新提示与 home 浮窗共享右下角；直接把 dock.windowOpen 下传，避免再造一份全局状态。 */}
      <UpdateToasts homeOpen={dock.windowOpen} />

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
function TaskDeepLink({
  tree,
  tasks,
  onOpen,
}: {
  tree: ProjectTreeResp | null
  tasks: Task[]
  onOpen: (base: BaseDir | null, taskId: string) => void
}) {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [done, setDone] = useState(false)

  useEffect(() => {
    // 等树到位再解析目录：树还没来时目录解析不出来，会把 tab 开在错的基准上
    if (!id || done || !tree) return
    onOpen(findBaseOfTask(tree, tasks, id), id)
    setDone(true)
    navigate('/', { replace: true })
  }, [id, done, tree, tasks, onOpen, navigate])

  return <p className="p-4 text-sm text-muted-foreground">正在打开任务…</p>
}
