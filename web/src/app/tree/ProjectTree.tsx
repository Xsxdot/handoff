// ProjectTree —— 左栏项目树。
//
// 层级（B288 重绘，形态真源 option-1 左栏 + b288-workbench-ux renderTree）：
//   项目行（项目名 + 进行中计数 + 右侧折叠箭头）
//     ├「任务」组（小标题已移除，2026-08-29 用户裁定）：终端/文件已打开行（Shell
//     │  注入的 openItems，在前，顺序=打开顺序，不随聚焦/切基准重排）→ 任务列表
//     │  （created_at 降序的显式排序，聚合序不可信；已打开的 tui 原位呈现已打开
//     │  态，不再置顶——2026-08-29 用户裁定：打开一个任务不许让别的行挪位置）
//     │  → 空状态行（组里一个进行中的东西都没有时显示「暂无进行中的任务」，
//     │  2026-08-29 用户裁定；搜索期间不显示）
//     ├「目录」小标题组：机器行（绿点 + 机器名 + 右侧箭头 + 悬停动作）
//     │  └ 工作树子行（紧凑、缩进；点击选中，不再列任务——任务已上移任务组）
//     └「已结束」行：项目内全部终态任务，默认收起；终态后 30 分钟缓冲窗内的
//        任务留在上面任务列表，不进这组。挂项目块最底（2026-08-29 用户裁定，
//        不再夹在任务列表与目录之间）
// 任务不再挂目录下：跨机器平铺在任务组里（同一项目的活一眼看全），目录组只
// 回答「代码在哪台机器的哪个目录」。已打开项与任务的行名同源（taskDisplayName /
// tabTitle），左栏与顶部 chrome 不会各说各话。
//
// 圆点语义（2026-08-29）：行右侧圆点按行类表达状态——任务行=任务状态
// （stateTone：running 绿 / 等工单琥珀 / 终态灰红），终端行=PTY 连接
// （绿连红断，Shell 上报），文件行=文件状态（净绿/改琥珀/冲突红/删灰）。
// 机器行的圆点=可达性，语义独立，勿混。
//
// 诚实展示（spec §8）：
//   - 不可达机器（machines[].ok===false 或 location.probe_error）保持可见、
//     标「已断开」并透出原因原文，绝不静默少一台
//   - 未归属任务（project_id===""）挂在树末尾的「未归属」分组，不被吞掉
//
// 点击语义：
//   - 项目行：展开/收起项目；看板的筛选归看板自己的 FilterBar
//   - 机器行：展开该机的主目录与工作树；悬停动作开主目录终端 / 新建工作树
//   - 工作树子行：选中目录并打开可关闭的文件抽屉
//   - 任务组已打开行：聚焦对应 tab（onFocusOpenItem，由 Shell 提供）；悬停从
//     右侧滑出 × 快速关闭该 tab（onCloseOpenItem，Shell 走与窗格 × 同一条守卫）
//   - 任务组普通任务行：按其项目/机器开 TUI tab（onOpenTask）
//   - 未归属任务没有基准目录，中央以当前选中目录开它的 TUI tab
//
// 拖放（W4 §3）：任务行可拖进中央区。拖到某一栏的边缘 = 在那一侧分出新栏；
// 拖到栏中间 = 在那一栏开一个 tab。行上带 data-drag-task 标记，拖动期间
// WorkbenchPage 据此关闭窗格内容层的 pointer-events（xterm canvas 不再截事件）。
//
// 计数只用于排序与折叠判据（counts.ts / wsMetrics），行上不再渲染计数控件——
// 原型把行留给了名字与机器归属（spec §5 功能保留清单）。
import { useEffect, useMemo, useRef, useState } from 'react'
import '../workbench/desktop-workspace.css'
import {
  AppWindow, Archive, ChevronDown, ChevronRight, FolderOpen, FileText, FolderGit2, GitBranch, LayoutGrid, MessagesSquare, Monitor, Plus, Search, Server, Settings, SquareKanban, SquareTerminal, Terminal, Ticket, WifiOff, Workflow, X,
} from 'lucide-react'
import dispatchTaskUrl from '../../assets/dispatch-task.png'
import { filterTree, taskMatchesQuery } from './search'
import { sortWorkspaces, type WorkspaceMetrics } from './sortWorkspaces'
import type { MachineStatus, PreviewSession, ProjectLocationNode, ProjectNode, ProjectTreeResp, Task, Workspace } from '../../api/types'
import type { BaseDir } from '../workbench/useWorkbench'
import { taskDisplayName } from '../lib/taskName'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'
import { ContextMenu } from '../shared/ContextMenu'
import { countsForProject } from './counts'
import { ARCHIVED_LABEL, ARCHIVED_TITLE, archivedKey, archivedTasks, isTerminalState, recentlyCompleted } from './archived'
import { StateDot } from '../board/StateDot'
import { stateTone, type StateTone } from '../board/columns'
import { cn } from '@/lib/utils'
import { TOUCH_BASELINE } from '@/lib/touch'
import { DRAG_BASE_MIME, DRAG_DIR_MIME, DRAG_SESSION_MIME, DRAG_TAB_MIME, DRAG_TASK_MIME } from '../workbench/paneDrop'
import { TreePrefsMenu } from './TreePrefsMenu'
import { sortProjects, splitHiddenProjects, splitIdleWorkspaces } from './treePrefs'
import { useTreePrefs } from './useTreePrefs'
import { NewWorktreeDialog } from './NewWorktreeDialog'
import { normalizePreviewOrigin, previewKey, previewLabel } from '../data/usePreviews'

// OpenItem 是左栏「已打开行」的一行数据，由 Shell 从工作台投影注入。
// key 是去重/React 键；name 已是展示名（tui=任务原名，terminal/file=tabTitle 结果），
// 本组件不做任何命名解析——持有任务流的层负责注入。
// tone 是 terminal/file 行圆点的视觉基调（终端=连接状态、文件=文件状态，
// 由 Shell 持有的上报缝计算）；省略时按连接正常/干净显示绿色。
// tui 行不消费它——任务状态圆点由本组件从任务流取 stateTone。
export interface OpenItem {
  key: string          // `${baseKey}\x1f${tabId}`
  kind: 'tui' | 'terminal' | 'file'
  name: string         // 展示名
  taskId?: string      // kind==='tui' 时必填
  machine: string      // '' = 本机
  base: BaseDir
  group: string
  tabId: string
  // detail 是搜索用的内容定位线索（文件相对路径 / 终端 rel）；省略 = 该内容
  // 没有额外定位字段。plan 的 OpenItem 形状没有它——补上是为了保住
  // 「按文件相对路径搜到已打开行」的既有搜索能力（search.ts openedText 口径）。
  detail?: string
  tone?: StateTone
}

/** Collaboration projection derived by Shell from real card links, never from a guessed machine. */
export interface ProjectSessionRow {
  id: string
  title: string
  projectIds: string[]
  unresolved?: boolean
  archived?: boolean
  needsHuman?: boolean
}

export interface ProjectTreeProps {
  sessions?: ProjectSessionRow[]
  onOpenSession?: (id: string, title: string) => void
  onOpenArchive?: () => void
  currentBrowseBase?: BaseDir | null
  tree: ProjectTreeResp
  tasks: Task[]
  selectedKey: string | null            // 当前选中目录的 BaseDir.key
  ticketCount: number                   // 挂起工单总数，0 时不显示角标
  // ticketsByDir 是「目录绝对路径 → 挂起工单张数」，来自 useGlobalTickets。
  // 只用于目录行排序，不显示在界面上——工单数已经由 ticketCount 角标在
  // 底部说了一次，行上再说一遍是噪音。
  ticketsByDir: Map<string, number>
  // openItems 是工作台当前打开的全部内容（当前基准在前，见 Shell）。
  // 它们列在所属项目的任务组最前，点击经 onFocusOpenItem 聚焦对应 tab。
  openItems: OpenItem[]
  // focusedTaskId 是焦点窗格里 tui 内容的 taskId，否则 null；命中行加 is-selected。
  focusedTaskId: string | null
  onFocusOpenItem: (item: OpenItem) => void
  // onCloseOpenItem 是已打开行悬停 × 的关闭入口（终端/文件/tui 已打开行都有）。
  // 没传就不渲染 ×——关闭语义由持有 workbench 的层实现，树只发信号。
  onCloseOpenItem?: (item: OpenItem) => void
  // onOpenTerminalAt 在指定目录开终端（机器行悬停钮 / 工作树子行悬停钮共用）。
  onOpenTerminalAt: (base: BaseDir) => void
  onOpenDirectory: (base: BaseDir) => void
  onOpenTask: (base: BaseDir | null, taskId: string) => void  // base null = 未归属任务
  onOpenBoard: () => void
  onOpenCards?: () => void
  onOpenProjectCards?: (project: ProjectNode) => void
  // ledgerEnabled 未启用时不渲染账本的「工作项」与「流程」两个入口。
  ledgerEnabled?: boolean
  // onOpenFlows 流程页（工作流形状 / 派发模板）。以前没有 dock 入口，
  // 只能手敲 /flows——spec §5 要求入口挂 dock
  onOpenFlows?: () => void
  // unlinkedCount 未挂账 task 数，挂在任务看板按钮上——它现在是兜底入口，
  // 有未挂账时才值得点开（主入口是工作项看板）。
  unlinkedCount?: number
  cardNeedsCount?: number
  onOpenTickets: () => void
  onOpenSettings: () => void
  onOpenCodegraph?: () => void
  onOpenProjectCodegraph?: (project: ProjectNode) => void
  // compact（B369.7）：紧凑视口形态。缺省 false = 桌面行为零改动。
  // 三个处置：①项目行右侧簇常驻渲染「工作项」钮（触屏无 hover 也可见可点），
  // 「代码图」子钮不渲染；②底部「流程」「代码图」钮配合回调缺席一并隐藏——
  // /flows、/codegraph 整页路由只在桌面注册，紧凑下点了就是「URL 变了没人消费」
  // 的死按钮；③底部入口行下方渲染一行解释文案（mobile-nav-note），同一视口内
  // 说清「为什么不在、去哪用」。
  compact?: boolean
  // onAddProject 打开项目登记向导。入口是「项目 N」标题行右侧的 + 图标——
  // 它改变树本身，与底部那排「去别处看」的跳转入口不是一类东西。
  onAddProject?: () => void
  onUnregister?: (name: string, machine: string) => Promise<void> | void
  onEdit?: (project: ProjectNode) => void
  // onOpenProjectDetail（B369.10 T3）：compact 下项目行**主点击**的落点——进移动
  // 项目详情面（Shell 接 nav.setProject）。它缺席时主点击维持折叠 toggle（桌面
  // 与「直渲染树」的既有语义逐字节不变）。
  onOpenProjectDetail?: (project: ProjectNode) => void
  // onWorktreeCreated 建完树后回调，由 Shell 刷新树并把新目录选为当前基准目录。
  // 与 onUnregister / onEdit 同一条规矩：没传就不给这个入口。
  onWorktreeCreated?: (project: ProjectNode, machine: string, ws: Workspace) => void
  previews?: PreviewSession[]
  previewMachines?: MachineStatus[]
  previewOpenKeys?: ReadonlySet<string>
  previewOpeningKeys?: ReadonlySet<string>
  onOpenPreview?: (id: string, machine: string) => void
}

// Creation and browsing keep separate device-local preferences. Neither rewrites existing BaseDir values.
function readLocationPrefs(kind: 'terminal' | 'browse'): Record<string, string> {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(`handoff.desktop.${kind}-locations`) ?? '{}')
    return value && typeof value === 'object' && !Array.isArray(value)
      ? Object.fromEntries(Object.entries(value).filter((entry) => typeof entry[1] === 'string')) : {}
  } catch (error) { console.warn('project_tree.location_preferences.read_failed', { kind, error }); return {} }
}
function saveLocationPref(kind: 'terminal' | 'browse', previous: Record<string, string>, project: string, key: string): Record<string, string> {
  const next = { ...previous, [project]: key }
  try { localStorage.setItem(`handoff.desktop.${kind}-locations`, JSON.stringify(next)) }
  catch (error) { console.warn('project_tree.location_preferences.save_failed', { kind, project, error }) }
  return next
}

// One resolver serves the action and its radio menu. A missing/offline remembered
// location never falls back to a different machine; the user must choose again.
function terminalDefaultBase(project: ProjectNode, remembered: string | undefined, machines: MachineStatus[] | undefined): BaseDir | undefined {
  if (remembered) return project.locations.flatMap((loc) => loc.workspaces.map((ws) => workspaceBase(project, loc.machine, ws))).find((base) => base.key === remembered)
  const available = project.locations.filter((loc) => locationProblem(loc, machines) === '').flatMap((loc) => loc.workspaces.map((ws) => ({ base: workspaceBase(project, loc.machine, ws), isMain: ws.is_main })))
  return (available.find((entry) => entry.isMain) ?? available[0])?.base
}

// Resolve by the stable location tuple. projectName is only a tie-breaker when
// multiple registered projects point at the exact same machine/path; it is never
// used as an identity fallback because labels can collide or change.
function projectIDForBase(tree: ProjectTreeResp, base: BaseDir): string {
  const candidates = tree.projects.filter((project) => project.locations.some((location) =>
    location.machine === base.machine && location.workspaces.some((workspace) => workspace.path === base.path),
  ))
  if (candidates.length === 1) return candidates[0].project_id
  const named = candidates.filter((project) => project.name === base.projectName ||
    project.locations.some((location) => location.name === base.projectName),
  )
  return named.length === 1 ? named[0].project_id : ''
}

// MACHINE_LABEL 给机器名做人话标签：""=本机。B369.10 导出：详情面/位置芯片复用。
export function machineLabel(machine: string): string {
  return machine === '' ? '本机' : machine
}

// createdDesc 是全部任务行列表（任务组 / 已结束 / 未归属）的统一排序键：
// created_at 降序，解析不了的当最旧（稳定排序保证同键行保持原相对顺序）。
// 不能信任务流原序：scope=all 的跨机聚合响应（真机读数 2026-08-29）既不是
// 单机库里的 created_at DESC（store.go:416 只管单机查询），各机镜像拼接后
// 同项目内的行序会随状态迁移漂移——「打开一个任务整个列表顺序就变」的
// 另一半病根。created_at 恒定不跳，显式排序后行序只增不减。
function createdDesc(a: Task, b: Task): number {
  const ka = Date.parse(a.created_at)
  const kb = Date.parse(b.created_at)
  return (Number.isFinite(kb) ? kb : -Infinity) - (Number.isFinite(ka) ? ka : -Infinity)
}

// locationProblem 判定一个机器节点是否不可达：location 探测失败优先，否则看
// 跨机汇总信封里对应机器是否 ok=false。返回原因原文；空串=正常。
// B369.10 导出：MobileProjectDetail 的 locbar 离线判定复用同一判据。
export function locationProblem(loc: ProjectLocationNode, machines: MachineStatus[] | undefined): string {
  if (loc.probe_error !== '') return loc.probe_error
  const ms = machines?.find((m) => m.name === loc.machine)
  if (ms && !ms.ok) return ms.error
  return ''
}

// Arrow 是展开箭头所在的可点 span——必须是 span 而不是 button，避免与行 button 嵌套。
function Arrow({ open, onToggle }: { open: boolean; onToggle: () => void }) {
  return (
    <span
      aria-label={open ? '收起' : '展开'}
      className="flex size-4 shrink-0 items-center justify-center text-muted-foreground"
      onClick={(e) => {
        e.stopPropagation()
        onToggle()
      }}
    >
      <ChevronRight className={cn('size-3.5 transition-transform', open && 'rotate-90')} />
    </span>
  )
}

// DisconnectedBadge 是机器行名称右侧的「已断开」徽标。
function DisconnectedBadge() {
  return (
    <span className="flex shrink-0 items-center gap-0.5 rounded bg-amber-500/10 px-1 py-px text-[10px] text-amber-600">
      <WifiOff className="size-3" />
      <span>已断开</span>
    </span>
  )
}

// dirLabel 是目录行显示的短名。
//
// 优先分支名（原型显示的是 `integration/b2-b3` 这样的分支），detached 时分支为
// 空串，退回路径最后一段——总得有个能认的名字，显示整条绝对路径会把行撑爆。
function dirLabel(ws: Workspace): string {
  if (ws.branch !== '') return ws.branch
  const seg = ws.path.split('/').filter(Boolean)
  return seg.length > 0 ? seg[seg.length - 1] : ws.path
}

// workspaceBase 把树上的一个目录节点转成 BaseDir。
//
// 参数：project 所属项目；machine 机器名（""=本机）；ws 目录节点
// 返回：可直接交给 useWorkbench.select 的基准目录
//
// key 必须带机器维度：两台机器上完全可能出现同路径的工作树（同一个项目
// 在两台开发机上 clone 到同一个位置），不带机器名它们的 tab 组会撞进同一个
// key 里混在一起。形状与 home 基准的 `~` / `~@machine` 同构。
//
// machine 为空串（本机）时 key **逐字节等于 path**，与改动前完全一致——
// 单机用户的既有行为不受影响。
export function workspaceBase(project: ProjectNode, machine: string, ws: Workspace): BaseDir {
  return {
    key: machine ? `${ws.path}@${machine}` : ws.path,
    kind: 'workspace',
    path: ws.path,
    label: dirLabel(ws),
    projectName: project.name,
    machine,
  }
}

// tasksOfWorkspace 挑出挂在这个目录下的任务。
//
// work_dir 为空 = 原地模式，挂到主目录（is_main）。这条回退与后端
// proto.Task.Workdir() 一致，不要改成「挂到第一个目录」。
export function tasksOfWorkspace(
  tasks: Task[],
  project: ProjectNode,
  machine: string,
  ws: Workspace,
): Task[] {
  return tasks.filter((t) => {
    if (t.project_id !== project.project_id || t.machine !== machine) return false
    if (t.work_dir === '') return ws.is_main
    return t.work_dir === ws.path
  })
}

// locationActiveCount 数一个位置下沉睡的活跃任务（running + waiting_answer +
// waiting_review）——B369.10 T3 的项目行位置芯片上那个「N 活跃」。
// B426 S2 导出：MobileProjectList 的项目卡位置 chips 复用同一口径（不另立第二套）。
//
// 口径与 wsMetrics.tasks（目录行排序键）逐字同源：逐个工作树走 tasksOfWorkspace
// 再按同样三态过滤。为什么不用 countsForProject 的 running+waiting：那个是项目级
// 计数（含 pending/previews 的展示口径），位置芯片问的是「这台机器上有没有在跑
// 的活」，同一棵树两处报不同的数才叫分叉。
export function locationActiveCount(tasks: Task[], project: ProjectNode, loc: ProjectLocationNode): number {
  return loc.workspaces.reduce(
    (n, ws) =>
      n +
      tasksOfWorkspace(tasks, project, loc.machine, ws).filter(
        (t) => t.state === 'running' || t.state === 'waiting_answer' || t.state === 'waiting_review',
      ).length,
    0,
  )
}

// findBaseOfTask 在树上反查任务所在的目录。
//
// 返回 null 的两种情形都是真实的，不要当异常处理：任务未归属（项目不在树上），
// 或者它的目录已经不在了（工作树被删但任务还在）。调用方（Shell 的 openTaskTui）
// 拿到 null 时退回「用当前选中目录开」，一个都没选中才提示。
export function findBaseOfTask(
  tree: ProjectTreeResp,
  tasks: Task[],
  taskId: string,
): BaseDir | null {
  if (!tasks.some((t) => t.id === taskId)) return null
  for (const project of tree.projects) {
    for (const loc of project.locations) {
      for (const ws of loc.workspaces) {
        if (tasksOfWorkspace(tasks, project, loc.machine, ws).some((t) => t.id === taskId)) {
          return workspaceBase(project, loc.machine, ws)
        }
      }
    }
  }
  return null
}

// findBaseByKey 在树上按 key 反查一个目录基准。
//
// 用途：恢复「上次选中的目录」（spec §6 规则三）。必须走 workspaceBase 生成 key
// 再比对，而不是直接比 path——key 带机器维度，两台机器上同路径的工作树只有
// 连机器一起比才分得开。
//
// 参数：tree 是已经加载的项目树；key 是待反查的 BaseDir.key。
// 返回：找到时返回树上重新构造的 BaseDir；找不到时返回 null。
// 注意：null 是正常情形，不要当异常处理：那个目录已经不在树上了（worktree 被
// done 回收、项目被注销）。调用方据此退回「未选中」态。
export function findBaseByKey(tree: ProjectTreeResp, key: string): BaseDir | null {
  for (const project of tree.projects) {
    for (const loc of project.locations) {
      for (const ws of loc.workspaces) {
        const base = workspaceBase(project, loc.machine, ws)
        if (base.key === key) return base
      }
    }
  }
  return null
}

// ── 行样式（数值 1:1 对照 option-1 左栏 CSS / b288-workbench-ux renderTree）──

// taskRowClass 对应 option-1 的 .task-row：34px 高、8px 圆角、8px 间距；
// is-open 与 hover 同为 #fafafa，is-selected 深一档 #ededed。
const taskRowClass = 'flex min-h-[34px] w-full min-w-0 items-center gap-2 rounded-lg px-2 py-0.5 text-left transition-[background,color] duration-[140ms]'
const taskRowOpen = 'bg-[#fafafa]'
const taskRowSelected = 'bg-[#ededed]'

// taskIconSlot 对应 option-1 的 .task-icon：22px 槽位、图标 17px、#666666。
// tui 用 dispatch-task 资产图标（与顶部 chrome、项目行计数同一资产），
// terminal/file 用 lucide 线性图标。
function TaskIconSlot({ kind }: { kind: 'tui' | 'terminal' | 'file' | 'preview' }) {
  return (
    <span className="flex size-[22px] shrink-0 items-center justify-center text-[#666666]">
      {kind === 'tui' && <img src={dispatchTaskUrl} className="size-[17px]" alt="" />}
      {kind === 'terminal' && <Terminal className="size-[17px]" />}
      {kind === 'file' && <FileText className="size-[17px]" />}
      {kind === 'preview' && <AppWindow className="size-[17px]" />}
    </span>
  )
}

export function ProjectTree({ tree, tasks, selectedKey, ticketCount, ticketsByDir, openItems, focusedTaskId, onFocusOpenItem, onCloseOpenItem, onOpenTerminalAt, onOpenDirectory, onOpenTask, onOpenBoard, onOpenCards, onOpenProjectCards, ledgerEnabled = false, onOpenFlows, cardNeedsCount = 0, unlinkedCount = 0, onOpenTickets, onOpenSettings, onOpenCodegraph, onOpenProjectCodegraph, compact = false, onAddProject, onUnregister, onEdit, onOpenProjectDetail, onWorktreeCreated, previews = [], previewMachines = [], previewOpenKeys = new Set<string>(), previewOpeningKeys = new Set<string>(), onOpenPreview = () => {}, sessions = [], onOpenSession, onOpenArchive, currentBrowseBase }: ProjectTreeProps) {
  // collapsed：空集 = 全展开。为什么用「收起集合」而不是「展开集合」：默认全展开
  // 意味着初值空集，渲染时 `!collapsed.has(key)` 天然为真，不用为每个节点预填。
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [desktopDisclosure, setDesktopDisclosure] = useState<Record<string, boolean>>({})
  const [query, setQuery] = useState('')
  const [locationMenus, setLocationMenus] = useState<Set<string>>(new Set())
  const [terminalMenu, setTerminalMenu] = useState<{ project: ProjectNode; x: number; y: number; trigger: HTMLButtonElement } | null>(null)
  const [terminalDefaults, setTerminalDefaults] = useState<Record<string, string>>(() => readLocationPrefs('terminal'))
  const [browseDefaults, setBrowseDefaults] = useState<Record<string, string>>(() => readLocationPrefs('browse'))
  const browseProjectID = currentBrowseBase ? projectIDForBase(tree, currentBrowseBase) : ''
  useEffect(() => {
    if (compact || !currentBrowseBase || !browseProjectID) return
    setBrowseDefaults((previous) => previous[browseProjectID] === currentBrowseBase.key ? previous
      : saveLocationPref('browse', previous, browseProjectID, currentBrowseBase.key))
  }, [compact, currentBrowseBase, browseProjectID])
  const [directoryOpen, setDirectoryOpen] = useState<Set<string>>(new Set())
  // 显示偏好走共享层：设置页的「常规」分区改的是同一份，两处即时同步（B160 §4.3）
  const [prefs, updatePrefs] = useTreePrefs()
  // 「已隐藏 N 个目录」的展开状态：**刻意不落盘**——它是一次性的「我现在想看看」，
  // 不是长期设定。键用机器节点 key
  const [openHiddenDirs, setOpenHiddenDirs] = useState<Set<string>>(new Set())
  const toggleHiddenDirs = (key: string) =>
    setOpenHiddenDirs((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  const searchRef = useRef<HTMLInputElement>(null)

  // 过滤结果。tasks 每 2.5s 刷新一次，useMemo 避免每次任务流心跳都重算整棵树。
  const filtered = useMemo(() => filterTree(tree, tasks, query, openItems, previews), [tree, tasks, query, openItems, previews])
  const searching = filtered.query !== ''
  const visibleSessions = sessions.filter((session) => !session.archived && (!searching
    || session.title.toLowerCase().includes(filtered.query)
    || tree.projects.some((project) => session.projectIds.includes(project.project_id) && project.name.toLowerCase().includes(filtered.query))))
  const singleProjectSessions = visibleSessions.filter((session) => session.projectIds.length === 1 && !session.unresolved && tree.projects.some((project) => project.project_id === session.projectIds[0]))
  const crossProjectSessions = visibleSessions.filter((session) => session.projectIds.length > 1 && !session.unresolved)
  const otherSessions = visibleSessions.filter((session) => !singleProjectSessions.includes(session) && !crossProjectSessions.includes(session))
  const sessionRow = (session: ProjectSessionRow) => <button key={session.id} type="button" className="desktop-session-row" data-drag-session="1" draggable
    onDragStart={(event) => { event.dataTransfer.setData(DRAG_SESSION_MIME, JSON.stringify({ sessionId: session.id, title: session.title })); event.dataTransfer.effectAllowed = 'copy' }}
    onClick={() => onOpenSession?.(session.id, session.title)} title={session.title}>
    <MessagesSquare size={14} /><span className="truncate">{session.title}</span>{session.needsHuman && <StateDot tone="intervention" />}
  </button>
  const sessionProjectMatches = searching ? tree.projects.filter((project) => singleProjectSessions.some((session) => session.projectIds[0] === project.project_id)) : []
  const projectsWithSessions = [...filtered.projects, ...sessionProjectMatches.filter((project) => !filtered.projects.some((candidate) => candidate.project_id === project.project_id))]

  // 「已结束」分组的数据源：项目内全部终态任务（B288 口径，见 archived.ts 文件头）。
  const archived = useMemo(() => archivedTasks(tasks), [tasks])
  // openArchived：**空集 = 全部收起**，取向与 collapsed 刚好相反。
  // 这个分组是历史堆积（实测单台机器 60 条），默认展开会把正在做的活挤出视口。
  const [openArchived, setOpenArchived] = useState<Set<string>>(new Set())
  const toggleArchived = (key: string) =>
    setOpenArchived((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })

  // ⌘K / Ctrl+K 聚焦搜索框。
  //
  // 刻意挂在**冒泡阶段**（addEventListener 第三参不传 true），不是捕获阶段。
  // xterm **不处理** ⌘K，不会 stopPropagation；终端侧 handler 会 preventDefault
  // + clear，这里再按焦点漏一次。Ctrl+K 在终端里由 xterm 处理并
  // stopPropagation，到不了这里。改成 capture 会当场破坏这一点。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'k') return
      if (e.defaultPrevented) return
      const el = document.activeElement
      if (el instanceof HTMLElement && (
        el.classList.contains('xterm-helper-textarea') || el.closest('.xterm') !== null
      )) return
      e.preventDefault()
      searchRef.current?.focus()
      searchRef.current?.select()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  const [unregisterTarget, setUnregisterTarget] = useState<{ name: string; machine: string } | null>(null)
  const [unregisterError, setUnregisterError] = useState('')
  // 建树弹层的目标位置。project 与 loc 一起记：弹层要用 loc.name（登记名）寻址，
  // 而回调要把 project 交回去组装 BaseDir
  const [worktreeTarget, setWorktreeTarget] = useState<{ project: ProjectNode; loc: ProjectLocationNode } | null>(null)
  // 同时只允许一个右键菜单，所以状态挂在树这一层而不是每行一份。
  // null = 没有菜单打开。project 一并记下：编辑弹层要以**整个项目**为输入，
  // 而菜单锚在机器行——闭包里只有 loc，得把所在 project 一起带进菜单状态。
  const [projectMenu, setProjectMenu] = useState<{ x: number; y: number; project: ProjectNode } | null>(null)
  const [menu, setMenu] = useState<{ x: number; y: number; name: string; machine: string; project: ProjectNode } | null>(null)
  const hasLiveResource = (project: ProjectNode) =>
    tasks.some((task) => task.project_id === project.project_id && (!isTerminalState(task.state) || recentlyCompleted(task, Date.now()))) ||
    openItems.some((item) => projectIDForBase(tree, item.base) === project.project_id) ||
    previews.some((session) => previewBelongsToProject(session, project)) ||
    sessions.some((session) => !session.archived && !session.unresolved && session.projectIds.length === 1 && session.projectIds[0] === project.project_id)
  const defaultExpanded = (key: string) => {
    const project = tree.projects.find((item) => 'p:' + item.project_id === key)
    return project ? hasLiveResource(project) : false
  }
  const toggle = (key: string) => {
    if (!compact) {
      setDesktopDisclosure((previous) => ({ ...previous, [key]: !(previous[key] ?? defaultExpanded(key)) }))
      return
    }
    setCollapsed((previous) => { const next = new Set(previous); if (next.has(key)) next.delete(key); else next.add(key); return next })
  }
  // 搜索期间旁路 collapsed：搜到了却折叠着等于没搜到。
  // 注意是「旁路」不是「清空」——collapsed 原样保留，查询清空后用户手动
  // 折起来的布局立刻回来，搜索不破坏布局。
  // B369.8（T6）：compact 缺省折叠——集合语义随缺省档翻转：compact 下集合存
  // 「已展开」键（空集 = 全折叠，位置摘要代替铺开的内容面），桌面存「已收起」
  // 键（空集 = 全展开）。toggle 的翻转动作两档同形零改动（翻转成员资格即可，
  // 语义由本判定解释）；搜索旁路两档一致。
  const expanded = (key: string) => searching || (compact ? collapsed.has(key) : (desktopDisclosure[key] ?? defaultExpanded(key)))
  const openDirectory = (base: BaseDir) => {
    console.debug('project_tree.directory.open', {
      project: base.projectName, machine: base.machine, baseKey: base.key, path: base.path,
    })
    const projectID = projectIDForBase(tree, base)
    if (!compact && projectID) setBrowseDefaults((prev) => saveLocationPref('browse', prev, projectID, base.key))
    onOpenDirectory(base)
  }
  const openTerminalAt = (base: BaseDir) => {
    console.debug('project_tree.terminal.open', {
      project: base.projectName, machine: base.machine, baseKey: base.key, path: base.path,
    })
    const projectID = projectIDForBase(tree, base)
    if (!compact && projectID) setTerminalDefaults((prev) => saveLocationPref('terminal', prev, projectID, base.key))
    onOpenTerminalAt(base)
  }
  const toggleDirectory = (key: string) =>
    setDirectoryOpen((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })

  // 搜索期间旁路「隐藏」类偏好：搜到了却被偏好过滤掉，等于搜索坏了。
  // 排序不旁路——排序不会让东西消失，跟着当前档反而更连贯
  const projectSplit = searching
    ? { shown: projectsWithSessions, hiddenCount: 0 }
    : splitHiddenProjects(filtered.projects, (p) => p.project_id, prefs.hiddenProjects)
  // 项目的「最近活动」= 该项目下任务 updated_at 的最大值；一条任务都没有时为空串
  const lastActivity = (projectID: string) =>
    tasks.reduce((max, t) => (t.project_id === projectID && t.updated_at > max ? t.updated_at : max), '')
  const orderedProjects = sortProjects(
    projectSplit.shown,
    (p) => {
      const c = countsForProject(tasks, p, previews)
      // Desktop active sorting includes device-local open content and linked
      // conversations. These are resources, not execution-count badges.
      const resources = compact ? 0 : openItems.filter((item) => projectIDForBase(tree, item.base) === p.project_id).length
        + singleProjectSessions.filter((session) => session.projectIds[0] === p.project_id).length
      return { active: c.running + c.pending + c.previews + resources, updatedAt: lastActivity(p.project_id), name: p.name }
    },
    prefs.projectSort,
  )

  const unassigned = filtered.unassignedTasks
  const previewMachineErrors = previewMachines.filter((machine) => !machine.ok || machine.error !== '')
  const visiblePreviewMachineErrors = searching
    ? previewMachineErrors.filter((machine) => `${machine.name} ${machine.error}`.toLowerCase().includes(filtered.query))
    : previewMachineErrors
  const hasUnowned = unassigned.length > 0 || filtered.unownedNames.length > 0 || filtered.unassignedPreviews.length > 0 || visiblePreviewMachineErrors.length > 0

  // 行名与 tab 标题同源：统一走 taskDisplayName（name → branch → plan_summary），
  // 不再留本地副本——三处口径分叉正是「派发行认不出谁是谁」的帮凶之一。

  // archivedBase 给「已结束」任务挑一个打开时的基准目录：该位置的**主目录**。
  // 它们自己的工作目录已被回收，用主目录至少能落在同一个仓库上；连主目录都没有
  // （整台机器探测失败）时返回 null，由 Shell 回退到当前选中目录。
  //
  // 刻意从原树 tree 里找而不是用渲染闭包里的 loc：搜索期间 loc.workspaces 是
  // 裁剪过的，主目录可能不在里面。
  const archivedBase = (project: ProjectNode, machine: string): BaseDir | null => {
    const loc = tree.projects
      .find((p) => p.project_id === project.project_id)
      ?.locations.find((l) => l.machine === machine)
    const main = loc?.workspaces.find((ws) => ws.is_main)
    return main ? workspaceBase(project, machine, main) : null
  }

  // wsCounts 统计一个工作树目录下的运行/待处理任务（目录行排序与折叠判据的内部输入，
  // 不再渲染在行上）。
  const wsCounts = (project: ProjectNode, machine: string, ws: Workspace) => {
    const under = tasksOfWorkspace(tasks, project, machine, ws)
    return {
      running: under.filter((t) => t.state === 'running').length,
      pending: under.filter((t) => t.state === 'waiting_answer' || t.state === 'waiting_review').length,
    }
  }

  // wsMetrics 给一个目录行算出三个排序键。
  //
  // 工单归集：ticketsByDir 的键是任务的 work_dir，而原地模式任务的 work_dir
  // 是空串——它们的工单在那张表里没有键。这里按 is_main 把它们补回来，判据与
  // tasksOfWorkspace 完全一致（work_dir 为空归主目录），两处不会分叉。
  const wsMetrics = (project: ProjectNode, machine: string, ws: Workspace): WorkspaceMetrics => {
    const under = tasksOfWorkspace(tasks, project, machine, ws)
    let tickets = ticketsByDir.get(ws.path) ?? 0
    if (ws.is_main) {
      // 原地模式任务的工单：它们在 byWorkDir 里没有键，逐个从任务侧找回来。
      // 只有主目录走这一支，与 tasksOfWorkspace 的回退口径对齐
      for (const t of tasks) {
        if (t.work_dir === '' && t.project_id === project.project_id && t.machine === machine) {
          tickets += ticketsByDir.get('') ?? 0
          break
        }
      }
    }
    return {
      tickets,
      tasks: under.filter(
        (t) => t.state === 'running' || t.state === 'waiting_answer' || t.state === 'waiting_review',
      ).length,
      createdAt: ws.created_at ?? '',
    }
  }

  // 右键菜单状态只保存机器名；这里按机器名找回同一 location，复用机器行的可达性判据，
  // 避免断开机器仍出现一个必然失败的建树入口，同时不影响编辑与注销。
  const menuLocation = menu ? menu.project.locations.find((l) => l.machine === menu.machine) : undefined
  const menuProblem = menuLocation ? locationProblem(menuLocation, tree.machines) : ''

  return (
    // 三段式：顶部（导航+搜索+标题）不滚 · 中间树独滚 · 底部入口钉死。
    // 为什么不让整个 aside 滚：项目一多，「添加项目」会被推到 scrollHeight
    // 的最下面（实测 top:1100 / 视口 1024），要滚到底才找得到入口
    // B369.8：compact 根挂触点基线类（树三钮/× 等 24px 次级底线）；桌面零接触。
    <div className={cn('flex min-h-0 flex-1 flex-col py-[13px] pr-[18px] pl-[26px]', compact ? TOUCH_BASELINE : 'desktop-project-tree')}>
      {/* 第一段：不滚——搜索框 + 「项目 N」。 */}

      {/* 搜索框与「项目 N」。N 跟随过滤，搜索时它就是「找到几个」的即时反馈；
          「未归属」不计入——它不是项目，是收纳箱 */}
      <div className="mb-0 px-0">
        <label className="flex min-h-[39px] items-center gap-[9px] rounded-[9px] border bg-background px-[10px] py-0">
          <Search className="size-[17px] shrink-0 text-[#525252]" />
          <input
            ref={searchRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              // Esc 清空并失焦：一次按键回到无过滤状态，不用手动全选删除
              if (e.key === 'Escape') {
                setQuery('')
                e.currentTarget.blur()
              }
            }}
            placeholder="搜索项目、机器或任务"
            className="min-w-0 flex-1 bg-transparent text-[14px] outline-none placeholder:text-muted-foreground"
          />
          <kbd className="flex min-h-[22px] shrink-0 items-center rounded-[5px] border px-[6px] text-[12px] leading-none text-muted-foreground">⌘K</kbd>
        </label>
      </div>

      <div
        data-testid="project-overview"
        className="mt-[7px] flex min-h-[31px] items-center gap-1 px-0 text-[15px] font-medium text-muted-foreground"
      >
        <span>项目</span>
        <span data-testid="project-count" className="font-normal text-muted-foreground/70">
          {orderedProjects.length}
        </span>
        {/* 藏了东西就必须说一声：偏好可以让它不占地方，但不能让人以为它不存在 */}
        {projectSplit.hiddenCount > 0 && (
          <span className="font-normal normal-case text-muted-foreground/70">· 已隐藏 {projectSplit.hiddenCount}</span>
        )}
        <span className="ml-auto flex items-center gap-0.5">
          {/* 「添加项目」跟着它作用的对象走：这一行说的就是「项目 N」，加一个
              项目属于同一件事。原先钉在左栏底部，离标题一屏远，而底部那排全是
              「离开这棵树去别处看」的跳转入口，一个会改变树本身的动作混在里面
              不是一类东西 */}
          <button
            type="button"
            aria-label="添加项目"
            title="添加项目"
            onClick={onAddProject}
            className="rounded p-0.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
          >
            <Plus className="size-3.5" />
          </button>
          {/* 菜单吃的是**原树**的项目，不是过滤后的：藏起来的项目要能勾回来 */}
          <TreePrefsMenu
            prefs={prefs}
            projects={tree.projects.map((p) => ({ project_id: p.project_id, name: p.name }))}
            onChange={updatePrefs}
          />
        </span>
      </div>

      {/* 第二段：只有它滚。scrollbar-none 隐藏滚动条（滚轮/触控板不受影响）：
          2026-08-29 用户裁定细条在纯白左栏里仍嫌突兀，索性不显示 */}
      <div data-testid="tree-scroll" className="scrollbar-none min-h-0 flex-1 overflow-y-auto">
        <div className="relative mt-[7px] pl-4">
          <span aria-hidden className="absolute bottom-[30px] left-0 top-4 w-px bg-[#dedede]" />

      {orderedProjects.map((project, projectIndex) => {
        const pKey = 'p:' + project.project_id
        const pOpen = expanded(pKey)
        // B369.10 T3：这个项目行的「主点击」落点。compact 下注入方给了详情入口
        // 就进详情（折叠改走行内 Arrow），否则维持折叠 toggle——桌面恒走后者。
        const openDetail = compact ? onOpenProjectDetail : undefined
        // B369.10 验收实走修正：有位置芯片时行分两段（上段名+右簇、下段芯片整行），
        // 见行 button 上的长注释——390 实走实测的挤压/压字由此而起。
        const chipsLine = openDetail !== undefined && project.locations.length > 0
        // 「工作项」绝对定位钮在 compact 常驻（right-20，宽约 26px）：芯片行要给它
        // 预留行尾 54~80px 那段，否则芯片文字被图标压住。钮的条件与下方渲染同源。
        const rowActionZone = compact && (onOpenProjectCards !== undefined || onOpenProjectCodegraph !== undefined)
        const pCounts = countsForProject(tasks, project, previews)
        const projectHit = searching && project.name.toLowerCase().includes(filtered.query)
        const projectPreviews = previews.filter((session) => previewBelongsToProject(session, project))
        const visibleProjectPreviews = projectPreviews.filter((session) =>
          !searching || projectHit || previewMatches(session, filtered.query),
        )
        const allProject = tree.projects.find((candidate) => candidate.project_id === project.project_id) ?? project
        const allLocations = allProject.locations
        const availableBases = allLocations.flatMap((loc) => locationProblem(loc, tree.machines) === ''
          ? loc.workspaces.map((ws) => workspaceBase(allProject, loc.machine, ws)) : [])
        const mainKeys = new Set(allLocations.flatMap((loc) => loc.workspaces.filter((ws) => ws.is_main).map((ws) => workspaceBase(allProject, loc.machine, ws).key)))
        // A remembered unavailable machine must not silently become a local terminal.
        const remembered = terminalDefaults[project.project_id]
        const preferredBase = terminalDefaultBase(allProject, remembered, tree.machines)
        const terminalProblem = preferredBase
          ? locationProblem(allLocations.find((loc) => loc.machine === preferredBase.machine)!, tree.machines)
          : remembered ? '上次选择的位置已不存在，请重新选择机器' : '没有可用目录'
        const terminalAvailable = !!preferredBase && terminalProblem === ''
        // Directory browsing has its own default; selecting a terminal machine cannot move it.
        const browsingBase = availableBases.find((base) => base.key === browseDefaults[project.project_id]) ?? availableBases.find((base) => mainKeys.has(base.key)) ?? availableBases[0]
        const locationsVisible = compact || locationMenus.has(pKey) || searching
        // 位置状态原料（B369.8 T6）：断开数 = 探测失败或机器探活失败的位置数
        //（locationProblem 同机器行 StateDot 的判据）
        const locationProblemCount = allLocations.filter((loc) => locationProblem(loc, tree.machines) !== '').length

        // 本项目的已打开行（Shell 给的顺序即展示顺序 = 组序×格序的打开顺序，
        // 不随聚焦/切基准重排）。搜索时按行名/机器/基准字段放行——与 filterTree
        // 留住项目祖先的口径互补。
        const projectOpenRows = openItems.filter((item) =>
          projectIDForBase(tree, item.base) === project.project_id &&
          (!searching || projectHit || openItemMatches(item, filtered.query)),
        )
        // 终端/文件已打开行留在任务组最前：它们不是任务流成员，没有「原位」可言，
        // 顺序即打开顺序。tui 已打开行不在此列——它改在下面任务流原位渲染
        // （2026-08-29 裁定：打开一个任务不许让其他行挪位置）。
        const openChromeRows = projectOpenRows.filter((item) => item.kind !== 'tui')
        const openTuiRows = projectOpenRows.flatMap((item) =>
          item.kind === 'tui' && item.taskId ? [item] : [],
        )
        const openTuiById = new Map(openTuiRows.map((item) => [item.taskId as string, item]))
        const openTuiIds = new Set(openTuiById.keys())
        // 任务列表 = 显式 created_at 降序（排序键见模块级 createdDesc 的完整论证）
        const now = Date.now()
        const projectTasks = tasks.filter((task) => {
          if (task.project_id !== project.project_id) return false
          if (searching && !projectHit) {
            if (!taskMatchesQuery(task, filtered.query) &&
                !machineLabel(task.machine).toLowerCase().includes(filtered.query) &&
                !allLocations.some((loc) =>
                  loc.machine === task.machine &&
                  loc.workspaces.some((ws) =>
                    dirLabel(ws).toLowerCase().includes(filtered.query) && tasksOfWorkspace([task], project, loc.machine, ws).length > 0,
                  ),
                )) return false
          }
          if (isTerminalState(task.state) && !recentlyCompleted(task, now) && !openTuiIds.has(task.id)) return false
          return true
        }).sort(createdDesc)
        // 任务流里找不到的已打开 tui（任务刚派发尚未进流、已删、或挂在别的项目）：
        // 追加在任务列表末尾——行不能因为流没到就闪没。
        const orphanOpenTui = openTuiRows.filter((item) => !projectTasks.some((t) => t.id === item.taskId))
        const archivedForProject = archived.get(archivedKey(project.project_id)) ?? []
        // 已结束子行与任务组同一个排序键（filter 已产出新数组，原地 sort 不碰
        // archived useMemo 里的原数组）
        const visibleArchived = archivedForProject.filter((task) =>
          !openTuiIds.has(task.id) &&
          !recentlyCompleted(task, now) &&
          (!searching || projectHit || taskMatchesQuery(task, filtered.query) ||
          machineLabel(task.machine).toLowerCase().includes(filtered.query)),
        ).sort(createdDesc)
        const archiveKey = 'project-archive:' + project.project_id
        const archivedOpen = openArchived.has(archiveKey) || (searching && visibleArchived.length > 0)

        const baseForTask = (task: Task): BaseDir | null =>
          findBaseOfTask(tree, tasks, task.id) ?? archivedBase(project, task.machine)

        return (
          <div
            key={pKey}
            data-testid={'project-node-' + project.project_id}
            className={cn(
              'relative mb-[7px] border-b border-border pb-[9px]',
              projectIndex === orderedProjects.length - 1 && 'mb-0 border-b-0 pb-0',
            )}
          >
            {/* 项目圆点把主树干锚定到项目行；没有它，竖线会被误读成普通分隔线。 */}
            {compact && <span
              aria-hidden
              data-testid={'project-marker-' + project.project_id}
              className={cn(
                'absolute -left-[15px] top-[11px] size-[9px] rounded-full border-2 bg-background',
                projectIndex === 0 ? 'border-[#737373]' : 'border-[#a3a3a3]',
              )}
            />}
            {/* 项目行（option-1 .project-head）：常规字重项目名（2026-08-29 用户裁定
                去掉加粗，18px 本身已有层级），右侧簇 = 派发任务图标 +
                进行中计数 + 折叠箭头（箭头在计数之后，原型 chev 位置） */}
            <div className={cn('group relative', !compact && 'desktop-project-header')} onContextMenu={(event) => {
              if (compact || (!onOpenProjectCards && !onOpenProjectCodegraph && !onEdit)) return
              event.preventDefault(); setMenu(null); setProjectMenu({ x: event.clientX, y: event.clientY, project })
            }} onKeyDown={(event) => {
              if (compact || (!onOpenProjectCards && !onOpenProjectCodegraph && !onEdit) || !(event.key === 'ContextMenu' || (event.shiftKey && event.key === 'F10'))) return
              event.preventDefault(); const rect = event.currentTarget.getBoundingClientRect(); setMenu(null); setProjectMenu({ x: rect.left, y: rect.bottom, project })
            }}>
              <button
                type="button"
                // B369.10 T3：compact 且能进详情时，行 button 不再是折叠开关——
                // aria-expanded 不再挂它（折叠语义收窄到行内 Arrow 的 aria-label），
                // 否则读屏会把它报成「可展开的按钮」而点下去是跳页面。
                aria-expanded={(!compact || project.locations.length > 0) && openDetail === undefined ? pOpen : undefined}
                title={!compact ? project.name + ' · 右键打开项目操作' : undefined}
                onClick={() => (openDetail ? openDetail(project) : toggle(pKey))}
                className={cn(
                  'flex min-h-[31px] w-full items-center justify-between gap-2.5 rounded-lg px-0 text-left text-[18px] font-normal hover:bg-[#fafafa]',
                  // B369.10 验收实走修正（390 真机实测）：有位置芯片时行升为两段——
                  // 上段名+右簇、下段芯片整行（原型 mobile-projects 的 .pcard .top /
                  // .locs 两段形态，chips 本就自带一行）。原先把芯片塞在名旁，长名下
                  // 容器被挤到 95px 而单枚芯片 151px，溢出后伸进「工作项」绝对定位钮的
                  // right-20 区被图标压字（实测 chip right=361 vs 钮 284..310）。
                  chipsLine && 'flex-wrap gap-y-1',
                )}
              >
                <span className="flex min-w-0 items-center gap-2.5">
                  {!compact && <Arrow open={pOpen} onToggle={() => toggle(pKey)} />}
                  <FolderGit2
                    data-project-color={compact ? 'green' : 'neutral'}
                    className={cn('size-[17px] shrink-0', compact ? 'text-[#16a34a]' : 'text-foreground')}
                  />
                  <span className="min-w-0 truncate">{project.name}</span>
                  {/* B369.8：compact 位置状态摘要——「项目优先、位置为状态信息」。
                      位置数 + 断开数（locationProblem 判据），StateDot tone 随之；
                      明细点 Arrow 展开逐台看。放在名旁而不是右侧簇：右簇还要给
                      B369.7 的「工作项」绝对定位钮（right-20）让位，摘要在簇内
                      会伸进钮区（390 目检项 1 的挤压）；名旁随 min-w-0 收缩，
                      超长时项目名先截断。桌面不渲染（铺开面自答这个问题）。
                      B369.10 T3：能进详情时升格为**逐位置芯片**（原型
                      mobile-projects ②「活跃数 = 该位置在跑任务数」），落成见右簇
                      之后的整行芯片；onOpenProjectDetail 缺席（桌面/直渲染树）时
                      维持摘要形态。 */}
                  {compact && project.locations.length > 0 && openDetail === undefined && (
                    <span data-testid="project-loc-summary" className="flex shrink-0 items-center gap-1 text-[13px] text-muted-foreground">
                      <StateDot tone={locationProblemCount > 0 ? 'failed' : 'active'} />
                      <span>{project.locations.length} 处位置{locationProblemCount > 0 ? ` · ${locationProblemCount} 处断开` : ''}</span>
                    </span>
                  )}
                </span>
                <span className="flex shrink-0 items-center gap-[7px] text-[15px] font-medium text-muted-foreground">
                  {compact && <span data-testid="project-running-count" className="flex items-center gap-[7px]">
                    <img src={dispatchTaskUrl} className="size-4" alt="" />
                    <span className="text-[16px]">{pCounts.running + pCounts.pending + pCounts.previews}</span>
                  </span>}
                  {compact && project.locations.length > 0 && <Arrow open={pOpen} onToggle={() => toggle(pKey)} />}
                </span>
                {chipsLine && (
                  // 整行芯片：w-full 让它在 flex-wrap 的父里独占一行；pr-20 预备
                  // 「工作项」绝对定位钮的 zone（钮在 right-20、宽约 26px，挡的是
                  // 行尾 54~80px 那段），否则芯片文字仍会被钮压住。
                  <span
                    data-testid="project-loc-chips"
                    className={cn(
                      'flex w-full min-w-0 flex-wrap items-center gap-1 text-[13px] text-muted-foreground',
                      rowActionZone && 'pr-20',
                    )}
                  >
                    {project.locations.map((loc) => {
                      const offline = locationProblem(loc, tree.machines) !== ''
                      return (
                        <span
                          key={loc.machine}
                          data-testid="project-loc-chip"
                          className="flex min-w-0 items-center gap-1 rounded-full border px-1.5 py-px"
                        >
                          <StateDot tone={offline ? 'failed' : 'active'} />
                          {/* min-w-0 + truncate：390 下芯片比可用宽度还长的兜底是
                              截断文本，不是溢出容器（溢出会横滚或压到邻元素）。 */}
                          <span className="truncate">
                            {offline
                              ? `${machineLabel(loc.machine)} · 离线`
                              : `${machineLabel(loc.machine)} · ${locationActiveCount(tasks, project, loc)} 活跃`}
                          </span>
                        </span>
                      )
                    })}
                  </span>
                )}
              </button>
              {!compact && <div className="desktop-project-actions">
                <div className="desktop-terminal-control" role="group" aria-label={`${project.name} 新建终端与机器选择`}>
                  <button type="button" aria-label={`在 ${project.name} 新建终端`}
                    title={terminalAvailable ? `新建终端 · ${machineLabel(preferredBase!.machine)} / ${preferredBase!.label}；拖入工作区分屏` : `新建终端不可用：${terminalProblem}；请从右侧选择机器`}
                    disabled={!terminalAvailable} draggable={terminalAvailable} data-drag-task="1"
                    onClick={() => terminalAvailable && preferredBase && openTerminalAt(preferredBase)}
                    onDragStart={(event) => {
                      if (!terminalAvailable || !preferredBase) { event.preventDefault(); return }
                      console.debug('project_tree.terminal.drag', { projectID: project.project_id, machine: preferredBase.machine, baseKey: preferredBase.key })
                      event.dataTransfer.setData(DRAG_DIR_MIME, JSON.stringify(preferredBase))
                      event.dataTransfer.setData(DRAG_BASE_MIME, JSON.stringify(preferredBase))
                      event.dataTransfer.effectAllowed = 'copy'
                    }}>
                    <span className="desktop-new-terminal-icon" aria-hidden="true"><SquareTerminal size={15} /><Plus size={8} /></span>
                  </button>
                  <button type="button" aria-label={`选择 ${project.name} 终端位置`}
                    aria-haspopup="menu" aria-expanded={terminalMenu?.project.project_id === project.project_id}
                    title={preferredBase ? `选择终端机器 · 当前 ${machineLabel(preferredBase.machine)}` : '选择终端机器'}
                    onClick={(event) => {
                      if (terminalMenu?.project.project_id === project.project_id) { setTerminalMenu(null); return }
                      const trigger = event.currentTarget
                      const rect = trigger.getBoundingClientRect()
                      setProjectMenu(null); setMenu(null)
                      setTerminalMenu({ project: allProject, x: rect.left, y: rect.bottom + 4, trigger })
                      console.debug('project_tree.terminal_machine_menu.open', { projectID: project.project_id })
                    }}><ChevronDown size={11} /></button>
                </div>
                <button type="button" aria-label={`浏览 ${project.name} 文件`} title={browsingBase ? `浏览文件 · ${machineLabel(browsingBase.machine)} / ${browsingBase.label}` : '没有可用目录'} disabled={!browsingBase} onClick={() => browsingBase && openDirectory(browsingBase)}><FolderOpen size={14} /></button>
              </div>}
              {/* B369.7：桌面 hidden group-hover:flex 逐字节保留；compact 下容器
                  常驻 flex（触屏无 hover），「代码图」子钮不渲染（/codegraph 整页
                  只在桌面注册，compact 点了就是死入口），「工作项」钮触点与底栏
                  同档（p-1.5）。同一 aria-label，两档只差可见性策略。offset 用
                  right-20（桌面 right-14）：compact 行宽 390 下两位数进行中计数
                  （图标 16 + 间距 7 + 两位数字 ≈19 + 间距 7 + 箭头 16 ≈ 65px）
                  比 right-14 的 56px 让位更宽，常驻钮不与计数/箭头挤压。 */}
              {compact && (onOpenProjectCards || onOpenProjectCodegraph) && (
                <span className={cn(
                  'absolute top-1/2 -translate-y-1/2 items-center gap-0.5 bg-background',
                  compact ? 'right-20 flex' : 'right-14 hidden group-hover:flex',
                )}>
                  {onOpenProjectCards && (
                    <button
                      type="button"
                      aria-label={'打开 ' + project.name + ' 工作项'}
                      title="工作项"
                      onClick={() => onOpenProjectCards(project)}
                      className={cn(
                        'rounded text-muted-foreground hover:bg-accent hover:text-foreground',
                        compact ? 'p-1.5' : 'p-0.5',
                      )}
                    >
                      <SquareKanban className="size-3.5" />
                    </button>
                  )}
                  {!compact && onOpenProjectCodegraph && (
                    <button
                      type="button"
                      aria-label={'打开 ' + project.name + ' 代码图'}
                      title="代码图"
                      onClick={() => onOpenProjectCodegraph(project)}
                      className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
                    >
                      <GitBranch className="size-3.5" />
                    </button>
                  )}
                </span>
              )}
            </div>

            {!compact && pOpen && singleProjectSessions.filter((session) => session.projectIds[0] === project.project_id).map(sessionRow)}
            {pOpen && (
              // option-1 的 .project-content：margin-left 7px、padding 6px 0 0 16px，
              // 左轨线（::before，1px var(--rail)=#dedede，bottom 让位 25px）用真实元素实现
              <div className="relative ml-[7px] pl-4 pt-[6px]">
                <span aria-hidden className="absolute bottom-[25px] left-0 top-0 w-px bg-[#dedede]" />
                <div data-testid="task-group">
                  {/* 「任务」小标题已移除（2026-08-29 用户裁定）：项目名下直接列任务，
                      行本身的名字与圆点已足够定位，小标题是多余一层 */}
                  <div className="mt-[7px] mb-2">
                    {/* 终端/文件已打开行在前（Shell 注入顺序=打开顺序） */}
                    {openChromeRows.map((item) => (
                      <TaskRow
                        key={item.key}
                        kind={item.kind}
                        label={item.name}
                        machine={item.machine}
                        dotTone={item.tone ?? 'active'}
                        open
                        draggable
                        onClose={onCloseOpenItem ? () => onCloseOpenItem(item) : undefined}
                        dragPayload={(e) => {
                          e.dataTransfer.setData(DRAG_TAB_MIME, JSON.stringify({ groupId: item.group, tabId: item.tabId }))
                          e.dataTransfer.effectAllowed = 'move'
                          console.debug('project_tree.drag.tab', { tabId: item.tabId, groupId: item.group, project: item.base.projectName, machine: item.base.machine, path: item.base.path })
                        }}
                        onClick={() => {
                          console.debug('project_tree.opened_item.focus', {
                            project: item.base.projectName,
                            machine: item.machine,
                            baseKey: item.base.key,
                            tabId: item.tabId,
                            groupId: item.group,
                          })
                          onFocusOpenItem(item)
                        }}
                        testId="open-item-row"
                        nameTestId="open-item-name"
                      compact={compact}
                      />
                    ))}
                    {/* 任务按任务流原序渲染；已打开的 tui 原位呈现已打开态
                        （open-item testid 与聚焦/拖拽语义沿用），不再置顶 */}
                    {visibleProjectPreviews.map((session) => {
                      const machine = session.machine ?? ''
                      const key = previewKey(session, machine)
                      return (
                        <TaskRow
                          key={'preview:' + key}
                          kind="preview"
                          label={previewLabel(session)}
                          machine={machine}
                          open={previewOpenKeys.has(key)}
                          opening={previewOpeningKeys.has(key)}
                          onClick={() => onOpenPreview(session.id, machine)}
                          testId={'preview-row-' + session.id}
                        compact={compact}
                        />
                      )
                    })}
                    {projectTasks.map((task) => {
                      const opened = openTuiById.get(task.id)
                      const taskBase = baseForTask(task)
                      if (opened) {
                        return (
                          <TaskRow
                            key={opened.key}
                            kind="tui"
                            label={opened.name}
                            machine={opened.machine}
                            dotTone={stateTone(task.state)}
                            open
                            selected={opened.taskId === focusedTaskId}
                            draggable
                            onClose={onCloseOpenItem ? () => onCloseOpenItem(opened) : undefined}
                            dragPayload={(e) => {
                              e.dataTransfer.setData(DRAG_TAB_MIME, JSON.stringify({ groupId: opened.group, tabId: opened.tabId }))
                              e.dataTransfer.effectAllowed = 'move'
                              console.debug('project_tree.drag.tab', { tabId: opened.tabId, groupId: opened.group, project: opened.base.projectName, machine: opened.base.machine, path: opened.base.path })
                            }}
                            onClick={() => {
                              console.debug('project_tree.opened_item.focus', {
                                project: opened.base.projectName,
                                machine: opened.machine,
                                baseKey: opened.base.key,
                                tabId: opened.tabId,
                                groupId: opened.group,
                              })
                              onFocusOpenItem(opened)
                            }}
                            testId="open-item-row"
                            nameTestId="open-item-name"
                          compact={compact}
                          />
                        )
                      }
                      return (
                        <TaskRow
                          key={'task:' + task.id}
                          kind="tui"
                          label={taskDisplayName(task)}
                          machine={task.machine}
                          dotTone={stateTone(task.state)}
                          draggable
                          dragPayload={(e) => {
                            e.dataTransfer.setData(DRAG_TASK_MIME, task.id)
                            e.dataTransfer.setData(DRAG_BASE_MIME, JSON.stringify(taskBase))
                            e.dataTransfer.effectAllowed = 'copy'
                            console.debug('project_tree.drag.task', { taskId: task.id, project: taskBase?.projectName ?? '', machine: task.machine, path: taskBase?.path ?? '' })
                          }}
                          onClick={() => onOpenTask(taskBase, task.id)}
                        compact={compact}
                        />
                      )
                    })}
                    {/* 流里还没有的已打开 tui：追加在末尾，保持可见不闪烁 */}
                    {orphanOpenTui.map((item) => (
                      <TaskRow
                        key={item.key}
                        kind="tui"
                        label={item.name}
                        machine={item.machine}
                        dotTone="idle"
                        open
                        selected={item.taskId === focusedTaskId}
                        draggable
                        onClose={onCloseOpenItem ? () => onCloseOpenItem(item) : undefined}
                        dragPayload={(e) => {
                          e.dataTransfer.setData(DRAG_TAB_MIME, JSON.stringify({ groupId: item.group, tabId: item.tabId }))
                          e.dataTransfer.effectAllowed = 'move'
                          console.debug('project_tree.drag.tab', { tabId: item.tabId, groupId: item.group, project: item.base.projectName, machine: item.base.machine, path: item.base.path })
                        }}
                        onClick={() => {
                          console.debug('project_tree.opened_item.focus', {
                            project: item.base.projectName,
                            machine: item.machine,
                            baseKey: item.base.key,
                            tabId: item.tabId,
                            groupId: item.group,
                          })
                          onFocusOpenItem(item)
                        }}
                        testId="open-item-row"
                        nameTestId="open-item-name"
                      compact={compact}
                      />
                    ))}
                    {/* 空状态行：没有任何进行中的东西（无已打开行、无任务流任务）时，
                        一段空白会被读成「渲染缺了什么」。搜索期间不显示——搜索有
                        全局「没有匹配」的反馈，逐项目再报一遍是噪音 */}
                    {!searching && openChromeRows.length === 0 && projectTasks.length === 0 && orphanOpenTui.length === 0 && (
                      <p
                        data-testid="task-empty-row"
                        className="flex min-h-[30px] items-center px-2 text-[13px] text-muted-foreground/70"
                      >
                        暂无进行中的任务
                      </p>
                    )}
                  </div>
                </div>

                {locationsVisible && <div data-testid="directory-group" className="mt-[7px]">
                  {/* 「目录」小标题（option-1 .section-label）：任务组不再有小标题，
                      这行是组与组之间仅存的节奏锚 */}
                  <div data-testid="dir-group-head" className="flex min-h-6 items-center gap-2 text-[15px] font-medium text-muted-foreground">
                    <span>目录</span>
                  </div>
                  <div className="mt-2 rounded-[11px] bg-[#f7f7f7] px-[9px] pb-[5px] pt-1">
                  {project.locations.map((loc) => {
                    const mKey = 'm:' + project.project_id + ':' + loc.machine
                    const mOpen = directoryOpen.has(mKey) || searching
                    const problem = locationProblem(loc, tree.machines)
                    const hasChildren = loc.workspaces.length > 0
                    const sorted = sortWorkspaces(loc.workspaces, (ws) => wsMetrics(project, loc.machine, ws))
                    const split = splitIdleWorkspaces(
                      sorted,
                      (ws) => {
                        const c = wsCounts(project, loc.machine, ws)
                        return {
                          isMain: ws.is_main,
                          selected: selectedKey === workspaceBase(project, loc.machine, ws).key,
                          active: c.running + c.pending,
                        }
                      },
                      prefs.hideIdleWorktrees && !searching,
                    )
                    const main = sorted.find((ws) => ws.is_main) ?? sorted[0]
                    const mainBase = main ? workspaceBase(project, loc.machine, main) : null
                    const hiddenOpen = openHiddenDirs.has(mKey)
                    const renderWorkspace = (ws: Workspace) => {
                      const base = workspaceBase(project, loc.machine, ws)
                      const dSelected = selectedKey === base.key
                      return (
                        <div key={base.key} className="group relative">
                          <button
                            type="button"
                            data-testid="workspace-row"
                            aria-current={dSelected ? 'true' : undefined}
                            data-drag-task="1"
                            draggable
                            onClick={() => openDirectory(base)}
                            onDragStart={(e) => {
                              e.dataTransfer.setData(DRAG_DIR_MIME, JSON.stringify(base))
                              e.dataTransfer.setData(DRAG_BASE_MIME, JSON.stringify(base))
                              e.dataTransfer.effectAllowed = 'copy'
                            }}
                            className={cn(
                              'flex min-h-[30px] w-full min-w-0 items-center rounded-[7px] py-0.5 pl-[9px] pr-2 text-left text-[14px] text-muted-foreground hover:bg-[#fafafa]',
                              dSelected && 'bg-[#fafafa]',
                            )}
                          >
                            <span className="min-w-0 flex-1 truncate font-mono">{dirLabel(ws)}</span>
                          </button>
                          <button
                            type="button"
                            aria-label="在此打开终端"
                            title="在此打开终端"
                            onClick={() => openTerminalAt(base)}
                            // B369.8 岔口 3 #2：桌面 hover 显形串逐字节保留；compact
                            // 行内常驻（block）+ 触点加档（p-1.5）
                            className={
                              compact
                                ? 'absolute right-2 top-1/2 block -translate-y-1/2 rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground'
                                : 'absolute right-2 top-1/2 hidden -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground group-hover:block'
                            }
                          >
                            <Terminal className="size-[15px]" />
                          </button>
                        </div>
                      )
                    }
                    return (
                      <div key={mKey}>
                        <div
                          className="group relative"
                          data-testid="directory-machine-row"
                          onContextMenu={
                            onUnregister || onEdit || onWorktreeCreated
                              ? (e) => {
                                  e.preventDefault()
                                  setMenu({ x: e.clientX, y: e.clientY, name: loc.name, machine: loc.machine, project })
                                }
                              : undefined
                          }
                        >
                          {/* 机器行（b288 .mach-row + spec §5）：绿点 + 机器名，
                              箭头与悬停动作都在行右 */}
                          <button
                            type="button"
                            data-testid="machine-row"
                            aria-disabled={problem !== '' || undefined}
                            aria-expanded={hasChildren && problem === '' ? mOpen : undefined}
                            draggable={mainBase !== null && problem === ''}
                            onClick={problem !== '' ? undefined : () => toggleDirectory(mKey)}
                            onDragStart={mainBase === null || problem !== '' ? undefined : (e) => {
                              e.dataTransfer.setData(DRAG_DIR_MIME, JSON.stringify(mainBase))
                              e.dataTransfer.setData(DRAG_BASE_MIME, JSON.stringify(mainBase))
                              e.dataTransfer.effectAllowed = 'copy'
                            }}
                            data-drag-task={mainBase !== null && problem === '' ? '1' : undefined}
                            className="flex min-h-[32px] w-full min-w-0 items-center gap-2 rounded-[7px] px-1 py-0.5 text-left text-[15px] font-medium hover:bg-[rgba(255,255,255,0.82)]"
                          >
                            {hasChildren && problem === '' ? (
                              <Arrow open={mOpen} onToggle={() => toggleDirectory(mKey)} />
                            ) : (
                              <span className="size-4 shrink-0" aria-hidden />
                            )}
                            {loc.machine === '' ? <Monitor className="size-[17px] shrink-0 text-[#666666]" /> : <Server className="size-[17px] shrink-0 text-[#666666]" />}
                            <StateDot tone={problem !== '' ? 'failed' : 'active'} />
                            <span className="min-w-0 flex-1 truncate">{machineLabel(loc.machine)}</span>
                            {problem !== '' && <DisconnectedBadge />}
                          </button>
                          {/* 悬停动作簇（option-1 .directory-actions）：开主目录终端 /
                              新建工作树；主目录不存在或机器断连时对应钮不渲染 */}
                          {mainBase && problem === '' && (
                            <button
                              type="button"
                              aria-label="打开主目录终端"
                              title="打开主目录终端"
                              onClick={() => openTerminalAt(mainBase)}
                              // B369.8 岔口 3 #3：compact 常驻（right-14 位置不变，
                              // 27px 宽的钮与 right-6 的新建钮不重叠）
                              className={
                                compact
                                  ? 'absolute right-14 top-1/2 block -translate-y-1/2 rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground'
                                  : 'absolute right-14 top-1/2 hidden -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground group-hover:block'
                              }
                            >
                              <Terminal className="size-[15px]" />
                            </button>
                          )}
                          {onWorktreeCreated && problem === '' && (
                            <button
                              type="button"
                              aria-label="新建工作树"
                              title="新建工作树"
                              onClick={() => setWorktreeTarget({ project, loc })}
                              // B369.8 岔口 3 #4：compact 常驻（right-6 位置不变）
                              className={
                                compact
                                  ? 'absolute right-6 top-1/2 block -translate-y-1/2 rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground'
                                  : 'absolute right-6 top-1/2 hidden -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground group-hover:block'
                              }
                            >
                              <Plus className="size-[15px]" />
                            </button>
                          )}
                        </div>
                        {problem !== '' && (
                          <p
                            className="break-words pb-1 pr-2 text-[11px] text-destructive"
                            style={{ paddingLeft: 6 + 14 }}
                          >
                            {problem}
                          </p>
                        )}
                        {problem === '' && mOpen && (
                          // option-1 .directory-children：左轨线 + 缩进
                          <div className="mb-1 ml-[33px] border-l border-[#dedede] pl-1">
                            {split.shown.map(renderWorkspace)}
                            {split.hidden.length > 0 && (
                              <>
                                <button
                                  type="button"
                                  data-testid="hidden-dirs-row"
                                  aria-expanded={hiddenOpen}
                                  onClick={() => toggleHiddenDirs(mKey)}
                                  className="flex min-h-[30px] w-full min-w-0 items-center rounded-[7px] px-2 py-0.5 text-left text-[14px] text-muted-foreground hover:bg-[#fafafa]"
                                >
                                  <Arrow open={hiddenOpen} onToggle={() => toggleHiddenDirs(mKey)} />
                                  <span className="min-w-0 flex-1 truncate">已隐藏 {split.hidden.length} 个目录</span>
                                </button>
                                {hiddenOpen && split.hidden.map(renderWorkspace)}
                              </>
                            )}
                          </div>
                        )}
                      </div>
                    )
                  })}
                  </div>
                </div>

                }
                {/* 「已结束」行（b288 .archive-row）：label + 计数 + 右侧箭头。
                    2026-08-29 用户裁定挂项目块最底——目录组之后，不再夹在任务列表
                    与目录之间 */}
                {visibleArchived.length > 0 && !(prefs.hideArchived && !searching) && (
                  <div className="mt-[7px]">
                    <button
                      type="button"
                      data-testid="archived-row"
                      aria-expanded={archivedOpen}
                      title={ARCHIVED_TITLE}
                      onClick={() => toggleArchived(archiveKey)}
                      className="flex min-h-[30px] w-full min-w-0 items-center gap-1.5 rounded-lg px-2 py-1 text-left text-[12px] text-muted-foreground hover:bg-accent/60"
                    >
                      <Archive className="size-3.5 shrink-0 opacity-70" />
                      <span className="min-w-0 flex-1 truncate">{ARCHIVED_LABEL}</span>
                      <span className="shrink-0 font-mono text-[11px] tabular-nums">{visibleArchived.length}</span>
                      <Arrow open={archivedOpen} onToggle={() => toggleArchived(archiveKey)} />
                    </button>
                    {archivedOpen && visibleArchived.map((task) => {
                      const taskBase = baseForTask(task)
                      return (
                        <TaskRow
                          key={'archived:' + task.id}
                          kind="tui"
                          label={taskDisplayName(task)}
                          machine={task.machine}
                          dotTone={stateTone(task.state)}
                          indent
                          draggable
                          dragPayload={(e) => {
                            e.dataTransfer.setData(DRAG_TASK_MIME, task.id)
                            e.dataTransfer.setData(DRAG_BASE_MIME, JSON.stringify(taskBase))
                            e.dataTransfer.effectAllowed = 'copy'
                            console.debug('project_tree.drag.task', { taskId: task.id, project: taskBase?.projectName ?? '', machine: task.machine, path: taskBase?.path ?? '' })
                          }}
                          onClick={() => onOpenTask(taskBase, task.id)}
                        compact={compact}
                        />
                      )
                    })}
                  </div>
                )}
              </div>
            )}
          </div>
        )
      })}

      {hasUnowned && (
        <div>
          <p className="px-3 pb-1 pt-2 text-[15px] font-medium text-muted-foreground">未归属</p>
          {visiblePreviewMachineErrors.map((machine) => (
            <p
              key={`preview-machine-error:${machine.name}`}
              data-testid={`preview-machine-error-${machine.name || 'local'}`}
              className="px-3 py-1 text-[13px] text-amber-600"
            >
              预览机器 {machineLabel(machine.name)}：{machine.error || '机器不可达'}
            </p>
          ))}
          {/* 与任务组同一排序键：未归属也是任务行，聚合序一样不可信 */}
          {[...unassigned].sort(createdDesc).map((t) => (
            <TaskRow
              key={t.id}
              kind="tui"
              label={taskDisplayName(t)}
              machine={t.machine}
              dotTone={stateTone(t.state)}
              draggable
              dragPayload={(e) => {
                e.dataTransfer.setData(DRAG_TASK_MIME, t.id)
                e.dataTransfer.setData(DRAG_BASE_MIME, 'null')
                e.dataTransfer.effectAllowed = 'copy'
                console.debug('project_tree.drag.task', { taskId: t.id, project: '', machine: t.machine, path: '' })
              }}
              onClick={() => onOpenTask(null, t.id)}
            compact={compact}
            />
          ))}
          {filtered.unownedNames.map((name) => (
            <p key={name} className="py-1 pr-2 text-[13px] text-muted-foreground" style={{ paddingLeft: 8 + 16 }}>
              {name}（未登记为项目）
            </p>
          ))}
          {filtered.unassignedPreviews.map((session) => {
            const machine = session.machine ?? ''
            return (
              <TaskRow
                key={'preview-unassigned:' + previewKey(session, machine)}
                kind="preview"
                label={previewLabel(session)}
                machine={machine}
                open={previewOpenKeys.has(previewKey(session, machine))}
                opening={previewOpeningKeys.has(previewKey(session, machine))}
                onClick={() => onOpenPreview(session.id, machine)}
                testId={'preview-row-' + session.id}
              compact={compact}
              />
            )
          })}
        </div>
      )}

      {/* 左栏搜到全白会像加载失败，必须有话说 */}
      {filtered.isEmpty && searching && (
        <p className="px-3 py-4 text-[13px] text-muted-foreground">没有匹配的项目或任务</p>
      )}
        </div>
        {!compact && crossProjectSessions.length > 0 && <section data-testid="cross-project-sessions" className="desktop-session-group"><h3>跨项目会话</h3>{crossProjectSessions.map(sessionRow)}</section>}
        {!compact && otherSessions.length > 0 && <section data-testid="unassociated-sessions" className="desktop-session-group"><h3>其他会话</h3>{otherSessions.map(sessionRow)}</section>}
      </div>

      {/* 第三段：钉在底部 */}
      {/* 底部只放跳转入口：工作项 / 看板 / 流程 / 工单 / 代码图 / 设置。
          它们是同一类东西——都是「离开这棵树去别处看」的全局入口，所以摆在
          一起（看板原先单独钉在顶部，那个位置让它看起来像是树的一部分）。
          「添加项目」不在这里：它改变树本身，已上移到「项目 N」那一行。
          工单数为 0 时按钮仍在、角标不显示——按钮消失会让人以为功能没了。
          justify-around 而非左对齐：少了占主位的文字按钮后，一排图标挤在
          左半边会让右半边看起来像渲染缺了东西 */}
      {!compact && onOpenArchive && <button className="desktop-archive-entry" type="button" onClick={onOpenArchive}><Archive size={14} />归档会话</button>}

      <div className="mt-1 flex items-center justify-around gap-0.5 border-t px-2 pt-2">
        {ledgerEnabled && (
          <button
            type="button"
            aria-label="工作项"
            title="工作项"
            onClick={onOpenCards}
            className="relative rounded-md p-1.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
          >
            <SquareKanban className="size-4" />
            {cardNeedsCount > 0 && (
              <span className="absolute -right-0.5 -top-0.5 min-w-4 rounded-full bg-state-intervention px-1 text-center text-[10px] leading-4 text-white">
                {cardNeedsCount}
              </span>
            )}
          </button>
        )}
        <button
          type="button"
          aria-label="任务看板"
          // 账本未启用时看板就是任务主入口，标题不应引入「未挂账」概念。
          title={ledgerEnabled ? '任务看板（未挂账兜底）' : '任务看板'}
          onClick={onOpenBoard}
          className="relative rounded-md p-1.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
        >
          <LayoutGrid className="size-4" />
          {unlinkedCount > 0 && (
            <span className="absolute -right-0.5 -top-0.5 min-w-4 rounded-full bg-muted-foreground px-1 text-center text-[10px] leading-4 text-background">
              {unlinkedCount}
            </span>
          )}
        </button>
        {/* B369.7：紧凑下 onOpenFlows 缺省（/flows 整页只在桌面注册）→ 按钮不渲染。
            与代码图钮的 onOpenCodegraph 注入门控同一先例。 */}
        {ledgerEnabled && onOpenFlows && (
          <button
            type="button"
            aria-label="流程"
            title="流程（工作流 / 派发模板）"
            onClick={onOpenFlows}
            className="rounded-md p-1.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
          >
            <Workflow className="size-4" />
          </button>
        )}
        <button
          type="button"
          aria-label="工单"
          title="工单"
          onClick={onOpenTickets}
          className="relative rounded-md p-1.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
        >
          <Ticket className="size-4" />
          {/* 角标用状态 token 而非裸 bg-amber-500：同一个左栏里两种橙
              （工单角标一种、干预态圆点另一种）看起来像 bug */}
          {ticketCount > 0 && (
            <span className="absolute -right-0.5 -top-0.5 min-w-4 rounded-full bg-state-intervention px-1 text-center text-[10px] leading-4 text-white">
              {ticketCount}
            </span>
          )}
        </button>
        {onOpenCodegraph && (
          <button
            type="button"
            aria-label="代码图"
            title="代码图"
            onClick={onOpenCodegraph}
            className="relative rounded-md p-1.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
          >
            <GitBranch className="size-4" />
          </button>
        )}
        <button
          type="button"
          aria-label="设置"
          title="设置"
          onClick={onOpenSettings}
          className="rounded-md p-1.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
        >
          <Settings className="size-4" />
        </button>
      </div>

      {/* B369.7：被藏入口的解释文案——一行覆盖全部三类（底栏「流程」、底栏
          「代码图」、项目行「代码图」），只在 compact 渲染，与入口隐藏同条件
          挂在同一 prop 缝上，永不脱钩（plan §2 岔口 3，措辞逐字定死）。 */}
      {compact && (
        <p data-testid="mobile-nav-note" className="px-3 pb-2 pt-1.5 text-[11px] leading-4 text-muted-foreground">
          流程与代码图暂未适配移动端，请在桌面宽屏使用。
        </p>
      )}

      {terminalMenu && (() => {
        const project = tree.projects.find((item) => item.project_id === terminalMenu.project.project_id) ?? terminalMenu.project
        const savedKey = terminalDefaults[project.project_id]
        const preferred = terminalDefaultBase(project, savedKey, tree.machines)
        const savedBase = savedKey ? preferred : undefined
        const selectedMachine = preferred?.machine
        return <ContextMenu x={terminalMenu.x} y={terminalMenu.y} ariaLabel={`${project.name} 的终端机器`}
          onClose={() => { terminalMenu.trigger.focus(); setTerminalMenu(null) }}
          items={[
            ...project.locations.map((loc) => {
              const main = loc.workspaces.find((ws) => ws.is_main) ?? loc.workspaces[0]
              const base = main ? workspaceBase(project, loc.machine, main) : null
              const problem = locationProblem(loc, tree.machines) || (base ? '' : '没有可用目录')
              return { label: machineLabel(loc.machine), checked: loc.machine === selectedMachine,
                icon: <><StateDot tone={problem ? 'failed' : 'active'} />{loc.machine === '' ? <Monitor size={14} className="ml-1" /> : <Server size={14} className="ml-1" />}</>,
                description: problem || `${savedBase?.machine === loc.machine ? savedBase.label : base?.label} · ${savedBase?.machine === loc.machine ? savedBase.path : base?.path}`,
                disabled: problem !== '', disabledReason: problem || undefined,
                onSelect: () => {
                  if (!base || problem) { console.warn('project_tree.terminal_machine.select_unavailable', { projectID: project.project_id, machine: loc.machine, problem }); return }
                  // Selecting changes the default only. Existing tabs retain their own BaseDir.
                  const chosen = savedBase?.machine === loc.machine ? savedBase : base
                  setTerminalDefaults((previous) => saveLocationPref('terminal', previous, project.project_id, chosen.key))
                  console.debug('project_tree.terminal_machine.selected', { projectID: project.project_id, machine: chosen.machine, baseKey: chosen.key })
                },
              }
            }),
            { separator: true },
            { label: '管理工作树…', onSelect: () => {
              const key = 'p:' + project.project_id
              setDesktopDisclosure((previous) => ({ ...previous, [key]: true }))
              setLocationMenus((previous) => new Set(previous).add(key))
              console.debug('project_tree.worktrees.manage', { projectID: project.project_id })
            } },
          ]} />
      })()}

      {projectMenu && <ContextMenu x={projectMenu.x} y={projectMenu.y} onClose={() => setProjectMenu(null)} items={[
        ...(onOpenProjectCards ? [{ label: '项目工作项', onSelect: () => onOpenProjectCards(projectMenu.project) }] : []),
        ...(onOpenProjectCodegraph ? [{ label: '项目代码图', onSelect: () => onOpenProjectCodegraph(projectMenu.project) }] : []),
        ...(onEdit ? [{ label: '编辑项目位置', onSelect: () => onEdit(projectMenu.project) }] : []),
      ]} />}

      {onUnregister && (
        <ConfirmDialog
          open={unregisterTarget !== null}
          title="注销项目位置"
          description={
            unregisterTarget
              ? `将解除「${unregisterTarget.name}」在${machineLabel(unregisterTarget.machine)}上的登记。只解除登记，不删除磁盘上的代码。`
              : ''
          }
          confirmLabel="注销"
          destructive
          error={unregisterError || undefined}
          onConfirm={async () => {
            if (!unregisterTarget || !onUnregister) return
            try {
              await onUnregister(unregisterTarget.name, unregisterTarget.machine)
              setUnregisterTarget(null)
              setUnregisterError('')
            } catch (err) {
              // agentd 报错原文透出，不缩略成「操作失败」（spec §10）
              setUnregisterError(errorMessage(err))
            }
          }}
          onCancel={() => {
            setUnregisterTarget(null)
            setUnregisterError('')
          }}
        />
      )}

      {onWorktreeCreated && worktreeTarget && (
        <NewWorktreeDialog
          open
          projectName={worktreeTarget.loc.name}
          machine={worktreeTarget.loc.machine}
          onClose={() => setWorktreeTarget(null)}
          onCreated={(ws) => onWorktreeCreated(worktreeTarget.project, worktreeTarget.loc.machine, ws)}
        />
      )}

      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          onClose={() => setMenu(null)}
          items={[
            ...(onWorktreeCreated && menuLocation && menuProblem === ''
              ? [{
                  label: '新建工作树',
                  // hover 出现的 + 按钮对键盘/触屏不友好，右键是它的等价通道，
                  // 走的是同一个弹层
                  onSelect: () => {
                    const loc = menu.project.locations.find((l) => l.machine === menu.machine)
                    if (loc) setWorktreeTarget({ project: menu.project, loc })
                  },
                }]
              : []),
            // 「编辑」排在「注销」前；onEdit 没传就不给这个入口
            // （与 onUnregister 的「没传就不给操作」一致）
            ...(onEdit
              ? [{ label: '编辑', onSelect: () => onEdit(menu.project) }]
              : []),
            ...(onUnregister
              ? [
                  {
                    label: '注销',
                    danger: true,
                    // 走的仍是既有的确认弹层，一字不改——右键只是换了个入口，
                    // 不是换一条注销路径
                    onSelect: () => setUnregisterTarget({ name: menu.name, machine: menu.machine }),
                  },
                ]
              : []),
          ]}
        />
      )}
    </div>
  )
}

// openItemMatches 是已打开行的行级搜索谓词：展示名之外，基准的 key/path/label
// 与内容定位字段（文件相对路径、终端 rel、taskId）都参与——用户记得的往往是
// 路径不是 tab 名，这是 filterTree 的 openedText 口径在行级的对应物。
function openItemMatches(item: OpenItem, q: string): boolean {
  return item.name.toLowerCase().includes(q) ||
    machineLabel(item.machine).toLowerCase().includes(q) ||
    item.base.key.toLowerCase().includes(q) ||
    item.base.path.toLowerCase().includes(q) ||
    item.base.label.toLowerCase().includes(q)
}

// TaskRow 是任务组/未归属分组的统一行（option-1 .task-row）：
// 类型图标槽 + 名称 + 右侧机器簇（状态圆点 + 机器名）。
// 圆点语义随行类（2026-08-29）：任务行=任务状态基调（调用方传 stateTone 结果），
// 终端行=PTY 连接（绿连红断），文件行=文件状态（净绿/改琥珀/冲突红/删灰）。
// open = 已打开态（is-open，与 hover 同色），selected = 焦点态（is-selected，深一档）。
// onClose 提供时行外套 group 壳，悬停从右侧滑出 × 快速关闭对应 tab——× 是行
// button 的**兄弟**而非子元素（button 不能嵌套），点击也就不会触发行本身的聚焦；
// 悬停期间机器名让位淡出（状态圆点保留），给 × 腾出位置。
// compact（B369.8 T6）：× 常驻（去 opacity-0/translate-x-1/group-hover 依赖，
// focus-visible 路径保留），机器名不再被 group-hover 抹掉——否则 × 常驻而标签
// 恒被 hover 抹掉（plan §12 修正 2）。
function TaskRow({
  kind, label, machine, dotTone = 'active', open = false, selected = false, indent = false,
  opening = false, draggable = false, dragPayload, onClose, onClick, testId = 'task-row', nameTestId, compact = false,
}: {
  kind: 'tui' | 'terminal' | 'file' | 'preview'
  label: string
  machine: string
  dotTone?: StateTone
  open?: boolean
  selected?: boolean
  indent?: boolean
  opening?: boolean
  draggable?: boolean
  dragPayload?: (e: React.DragEvent<HTMLButtonElement>) => void
  onClose?: () => void
  onClick: () => void
  testId?: string
  nameTestId?: string
  compact?: boolean
}) {
  const row = (
    <button
      type="button"
      data-testid={testId}
      data-open={open ? 'true' : undefined}
      aria-busy={opening ? 'true' : undefined}
      data-drag-task={draggable ? '1' : undefined}
      draggable={draggable || undefined}
      onDragStart={dragPayload}
      onClick={onClick}
      title={label}
      aria-current={selected ? 'true' : undefined}
      className={cn(
        taskRowClass,
        'text-muted-foreground hover:bg-[#fafafa] hover:text-foreground',
        open && taskRowOpen,
        selected && taskRowSelected,
        indent && 'pl-7',
      )}
    >
      <TaskIconSlot kind={kind} />
      <span data-testid={nameTestId} className={cn('min-w-0 flex-1 truncate', compact ? 'text-[15px] font-medium' : 'text-[12px] font-normal')}>
        {label}
      </span>
      <span data-testid="task-machine" className="ml-auto flex max-w-[88px] shrink-0 items-center gap-[7px] truncate text-[14px] text-muted-foreground">
        <StateDot tone={dotTone} />
        <span className={cn('truncate', onClose && !compact && 'transition-opacity duration-150 group-hover:opacity-0')}>
          {machineLabel(machine)}
        </span>
      </span>
    </button>
  )
  if (!onClose) return row
  return (
    <div className="group relative">
      {row}
      {/* × 在不透明度 0 时仍占位可聚焦，键盘用户 tab 到它时必须现身。
          compact 常驻：opacity-100 translate-x-0，无 hover 依赖。 */}
      <button
        type="button"
        aria-label={`关闭 ${label}`}
        title="关闭"
        onClick={onClose}
        className={
          compact
            ? 'absolute right-1 top-1/2 flex size-5 -translate-y-1/2 translate-x-0 items-center justify-center rounded text-muted-foreground opacity-100 transition-[opacity,transform] duration-150 hover:bg-accent/60 hover:text-foreground focus-visible:opacity-100 focus-visible:translate-x-0'
            : 'absolute right-1 top-1/2 flex size-5 -translate-y-1/2 translate-x-1 items-center justify-center rounded text-muted-foreground opacity-0 transition-[opacity,transform] duration-150 hover:bg-accent/60 hover:text-foreground focus-visible:opacity-100 focus-visible:translate-x-0 group-hover:translate-x-0 group-hover:opacity-100'
        }
      >
        <X className="size-3.5" />
      </button>
    </div>
  )
}

function previewBelongsToProject(session: PreviewSession, project: ProjectNode): boolean {
  const origin = normalizePreviewOrigin(session.origin_url ?? '')
  const projectOrigin = normalizePreviewOrigin(project.origin_url)
  return origin !== '' && projectOrigin !== '' && origin === projectOrigin
}

function previewMatches(session: PreviewSession, query: string): boolean {
  return [previewLabel(session), session.entry_url, session.branch ?? '', session.machine ?? ''].some((value) =>
    value.toLowerCase().includes(query),
  )
}
