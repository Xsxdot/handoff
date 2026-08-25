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
import { useEffect, useMemo, useRef, useState } from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate, useParams } from 'react-router-dom'
import { ApiError, deleteProject, deletePtySession, fetchLaunchers, fetchPtySessions } from '../../api/client'
import { fetchCards, fetchDecisions } from '../../api/ledger'
import type { ProjectNode, ProjectTreeResp, Task } from '../../api/types'
import { useMachines } from '../data/useMachines'
import { useProjectTree } from '../data/useProjectTree'
import { useTasks } from '../data/useTasks'
import { useMachineCaps } from '../data/useMachineCaps'
import { useLedgerEnabled } from '../data/useLedgerEnabled'
import { usePoll } from '../data/usePoll'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { isDesktopShell } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { AddProjectWizard } from '../projects/AddProjectWizard'
import { ProjectEditDialog } from '../projects/ProjectEditDialog'
import { findBaseByKey, findBaseOfTask, ProjectTree, workspaceBase } from '../tree/ProjectTree'
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
import { type TabContent } from '../workbench/tabs'
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

// OverlayKind 是当前打开的弹层。同时只允许一个（spec §0）：两个叠在一起时
// Esc 该关哪个会变得含糊。
type OverlayKind = 'none' | 'board' | 'tickets'

export function Shell() {
  const tasksState = useTasks()
  const treeState = useProjectTree()
  const tasks = useMemo(() => tasksState.data ?? [], [tasksState.data])
  const wb = useWorkbench()
  const navigate = useNavigate()
  const location = useLocation()

  const [overlay, setOverlay] = useState<OverlayKind>('none')
  const [wizardOpen, setWizardOpen] = useState(false)
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
  const split = wb.split
  // ⌘D 分屏。
  //
  // 挂 window 而不是像 BlankTab 的 ⌘T 那样挂面板：那里必须区分「按的是哪一栏的
  // 空白面板」，window 级会让一次 ⌘T 开出两个终端（BlankTab.tsx:75）。⌘D 没有这个
  // 问题——它只作用于当前焦点组，全局唯一。
  //
  // **只认 metaKey，绝不接 ctrlKey**：Ctrl+D 在终端里是 EOF，绑上去等于让用户
  // 没法退出 shell。这与 BlankTab.tsx:44 已确立的口径一致（本控制台只在 macOS 用，
  // 将来上 Windows 时这两处要一起改，而且要另选一个不撞 EOF 的键）。
  //
  // 必须 preventDefault：macOS 浏览器的 ⌘D 是「加入书签」，不拦会在分屏的同时弹
  // 书签面板。不排除输入框——⌘D 在 input/textarea 里没有默认语义，排除它只会让
  // 「光标在 Composer 里时 ⌘D 不好使」变成一个要解释的例外。
  //
  // 冒泡阶段监听（第三参不传 true），与 ProjectTree 的 ⌘K 同一条让位次序。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!e.metaKey || e.ctrlKey || e.key.toLowerCase() !== 'd') return
      e.preventDefault()
      split()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [split])
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
    byBase: wb.byBase,
    baseDirs: wb.baseDirs,
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
  // closingGone：服务端已经查不到这个会话了（最常见是 agentd 重启把内存里的会话
  // 全清了）。只影响措辞——弹层不能一边说「会终止里面正在运行的命令」，一边
  // 关的其实是一个早就没了的会话。null = 还没问出来，按「可能还活着」说话
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
  const beforeCloseTab = (c: TabContent, tabId: string): boolean => {
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
    setClosingPty({ tabId, sessionId: c.sessionId, machine: wb.base?.machine || '' })
    setCloseError('')
    probeClosingSession(c.sessionId)
    return false
  }

  // probeClosingSession 向服务端问一句「这个会话还在吗、忙不忙」，答案只用于
  // 弹层措辞，**不阻塞弹层出现**，也不影响能不能确认。
  //
  // 查不到 = 会话已经不在（agentd 重启是最常见的一种）：那句「会终止正在运行的
  // 命令」对它是假话，而假话会让用户以为自己正在杀掉什么东西。问不出来
  // （请求本身失败）时一律退回 null，按「可能还活着」说话——宁可吓一跳，不可
  // 骗人说没事
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
      // 404 是**成功**的一种：服务端根本没有这个会话，最常见的是 agentd 重启后
      // 内存里的会话全没了。此时「不许吞错误」那条纪律护的东西（别把还活着的
      // shell 从视野里抹掉）根本不存在——已经没有 shell 可留。照旧当失败处理的
      // 代价是这个 tab 被焊死：确认弹层每次都红字报「会话不存在」，关不掉，也
      // 没有第二个出口。删除对这一路是幂等的
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
  // why 每个「改工作台状态」的入口都得先调它：工作台挂在 path="*" 上，停在
  // /cards、/flows、/settings 时它根本没渲染。只改状态不换路由的后果是——
  // 面包屑跟着变了，中央还是原来那一页，看着像点击没反应（2026-08-19 真机踩到）。
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

  // selectDir 是「点一个目录」的唯一实现：换回工作台 + 选中。
  const selectDir = (base: BaseDir) => {
    backToWorkbench()
    wb.select(base)
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
    wb.open({ kind: 'tui', taskId }, target ?? undefined)
  }

  // currentTaskId 是当前目录上「最该看的那个任务」，只用于右栏 M 角标的数据源。
  // 一个目录下可能有多个任务，取第一个正在跑的，没有就取第一个——角标是装饰，
  // 选谁都不影响正确性，但要稳定（不随渲染抖动）。
  const currentTaskId = useMemo(() => {
    if (!wb.base || wb.base.kind !== 'workspace') return null
    const under = tasks.filter((t) => t.work_dir === wb.base?.path)
    return under.find((t) => t.state === 'running')?.id ?? under[0]?.id ?? null
  }, [tasks, wb.base])

  // 薄壳里窗口顶部那 28px 是 AppKit 的隐形拖动区（左键被拿去拖窗口，传不到
  // 页面）。与其空着，不如让它承担面包屑那一行的展示职责——面包屑本来就零
  // 交互，落在吞点击的区域里零代价，页面反而省下原来那一整行。
  // 浏览器里 desktop 为 false，这条不渲染，布局与从前一模一样。
  const desktop = isDesktopShell()

  return (
    <div className="flex h-dvh flex-col bg-background">
      {desktop && <DesktopTitleBar base={wb.base} />}
      <div className="flex min-h-0 flex-1">
      {/* 左栏自身不滚：滚动交给 ProjectTree 内部的树区，好让底部入口钉在底部。
          min-h-0 是必须的——flex 子项默认 min-height:auto，缺它内部的
          overflow-y-auto 不会生效，树会把父容器撑高、footer 照样被顶出去 */}
      <aside role="complementary" className="flex min-h-0 w-[260px] shrink-0 flex-col border-r bg-sidebar">
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
            selectedKey={wb.base?.key ?? null}
            ticketCount={tickets.count}
            ticketsByDir={tickets.byWorkDir}
            onSelectDir={selectDir}
            onOpenTask={openTaskTui}
            onOpenBoard={() => setOverlay('board')}
            onOpenCards={() => navigate('/cards')}
            onOpenFlows={() => navigate('/flows')}
            ledgerEnabled={ledgerEnabled}
            cardNeedsCount={cardNeedsCount}
            unlinkedCount={unlinkedTaskIds?.size ?? 0}
            onOpenTickets={() => setOverlay('tickets')}
            onOpenSettings={() => navigate('/settings')}
            onOpenCodegraph={() => navigate('/codegraph')}
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
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {/* 薄壳里这一行不画：同样的内容已经在窗口顶部那条 28px 上，
            两处都画就是把一行重复了两遍 */}
        {wb.base && !desktop && !fullPageRoute && <Breadcrumb base={wb.base} />}
        <main className="min-h-0 flex-1">
          <Routes>
            {ledgerEnabled && (
              <>
                <Route path="/cards" element={<CardsPage />} />
                <Route path="/flows" element={<FlowsPage />} />
              </>
            )}
            <Route
              path="/settings"
              element={<SettingsPage onClose={() => navigate('/')} />}
            />
            {/* /codegraph 的 viewer 唯一来源是同源 iframe；它不在 Shell 内复制取数或凭据。 */}
            <Route
              path="/codegraph"
              element={<CodegraphFrame project={wb.base?.projectName ?? ''} />}
            />
            <Route path="/machines" element={<Navigate to="/settings" replace />} />
            <Route path="/tasks/:id" element={<TaskDeepLink tree={treeState.data} tasks={tasks} onOpen={openTaskTui} />} />
            <Route
              path="*"
              element={
                <WorkbenchPage
                  api={wb}
                  onAddProject={() => setWizardOpen(true)}
                  tree={treeState.data}
                  tasks={tasks}
                  onFileCreated={() => setFileTreeNonce((n) => n + 1)}
                  terminalUnavailable={wb.base ? ptyNote(wb.base.machine) : ''}
                  launchers={launchersSupported ? (launchersData?.launchers ?? []) : []}
                  onBeforeClose={beforeCloseTab}
                  renderContent={(c, base, group, tabId) => {
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
                            // 会话 id 必须写回这个 tab：不写回的话切一次 tab
                            // 就会再建一个会话，用户每切一次多留一个 shell
                            onSession={(id) => wb.setContent(group, tabId, { ...c, sessionId: id, incompatible: false })}
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
                            // 草稿必须写回这个 tab：不写回的话切一次 tab 就把改动
                            // 丢了（WorkbenchPage 只渲染 activeTab，切走即卸载）
                            onDraftChange={(d) =>
                              wb.setContent(group, tabId, {
                                kind: 'file',
                                rel: c.rel,
                                draft: d?.draft,
                                baseSha: d?.baseSha,
                              })
                            }
                          />
                        )
                      case 'tui':
                        return <TuiTab taskId={c.taskId} />
                      default:
                        return null
                    }
                  }}
                />
              }
            />
          </Routes>
        </main>
      </div>

      {/* scratch 不是可选中的 wb 基准，只被浮窗 file tab 使用，所以不该渲染右栏文件树。 */}
      {wb.base && wb.base.kind === 'workspace' && !fullPageRoute && (
        <div className="w-[280px] shrink-0">
          <FileTree
            base={wb.base}
            refreshKey={fileTreeNonce}
            taskId={currentTaskId}
            onOpenFile={(rel) => wb.open({ kind: 'file', rel })}
            onOpenTerminal={(rel) => wb.openTerminal(undefined, undefined, rel)}
            revealSupported={caps.reveal('')}
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
          renderTab={(t) =>
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
                base={HOME_BASE}
                seq={t.seq}
                sessionId={t.sessionId}
                incompatible={t.incompatible}
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
              '这个终端会话在服务端已经不存在了（agentd 重启会清掉所有会话）。\n' +
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
