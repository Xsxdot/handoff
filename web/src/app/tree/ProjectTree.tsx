// ProjectTree —— 左栏项目树。
//
// 层级（严格三层 + 任务层）：项目 → 机器 → 目录（workspace）→ 任务。
// 为什么机器下不再分组一层：W3a §1.1 保证一个项目在一台机器上至多一个位置，
// 这条不变式可以直接依赖——每个项目下的机器节点恰好对应它的一个 location。
//
// 诚实展示（spec §8）：
//   - 不可达机器（machines[].ok===false 或 location.probe_error）保持可见、
//     标「已断开」并透出原因原文，绝不静默少一台
//   - 未归属任务（project_id===""）挂在树末尾的「未归属」分组，不被吞掉
//
// 点击语义（W4 §3.1 改造）：
//   - 项目 / 开发机行：只展开折叠，不再写筛选。看板的筛选归看板弹层自己的
//     FilterBar，树不再是筛选的编辑入口
//   - 目录行：**选中为当前目录**——切中央 tab 组 + 右栏文件树 + 面包屑
//   - 任务行：选中其所在目录，并在中央开它的 TUI tab
//   - 未归属任务没有基准目录，中央以当前选中目录开它的 TUI tab；一个都没选中时
//     由 Shell 提示先选目录
//
// 拖放（W4 §3）：任务行可拖进中央区。拖到某一栏的边缘 = 在那一侧分出新栏
// 并在其中打开；拖到栏中间 = 在那一栏开一个 tab。数据用自定义 MIME，从别处
// 拖进来的东西不会被误判。拖动不影响点击——HTML5 拖放只在真的拖起来之后
// 才吞掉 click。
//
// 任务挂到目录的依据是 Task.work_dir 与 Workspace.path 路径等值（纯前端 join，
// 不需要新接口）。work_dir 为空表示原地模式，挂到主目录——与 proto.Task.Workdir()
// 的回退语义一致。
//
// 计数来源：任务流（2.5s），见 counts.ts 的文件头注释。
//
// 任务 9：底部「添加项目」接 onAddProject 打开登记向导；机器（位置）行右侧悬浮
// 注销按钮（仅当 onUnregister 提供时渲染），点按弹 ConfirmDialog 二次确认，
// agentd 报错原文透出（spec §10）。
import { useEffect, useMemo, useRef, useState } from 'react'
import {
  Archive, ChevronRight, FolderGit2, GitBranch, HardDrive, Home, LayoutGrid, Plus, Search, Settings, SquareKanban, Ticket, WifiOff,
} from 'lucide-react'
import { filterTree } from './search'
import { sortWorkspaces, type WorkspaceMetrics } from './sortWorkspaces'
import type { MachineStatus, ProjectLocationNode, ProjectNode, ProjectTreeResp, Task, Workspace } from '../../api/types'
import type { BaseDir } from '../workbench/useWorkbench'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'
import { ContextMenu } from '../shared/ContextMenu'
import { countsForMachine, countsForProject } from './counts'
import { ARCHIVED_LABEL, ARCHIVED_TITLE, archivedKey, archivedTasks } from './archived'
import { stateTone } from '../board/columns'
import { StateDot } from '../board/StateDot'
import { RowCounts } from './RowCounts'
import { projectColorClass } from './projectColor'
import { cn } from '@/lib/utils'
import { DRAG_BASE_MIME, DRAG_TASK_MIME } from '../workbench/paneDrop'
import { TreePrefsMenu } from './TreePrefsMenu'
import { loadPrefs, savePrefs, sortProjects, splitHiddenProjects, splitIdleWorkspaces, type TreePrefs } from './treePrefs'
import { NewWorktreeDialog } from './NewWorktreeDialog'

export interface ProjectTreeProps {
  tree: ProjectTreeResp
  tasks: Task[]
  selectedKey: string | null            // 当前选中目录的 BaseDir.key
  ticketCount: number                   // 挂起工单总数，0 时不显示角标
  // ticketsByDir 是「目录绝对路径 → 挂起工单张数」，来自 useGlobalTickets。
  // 只用于目录行排序，不显示在界面上——工单数已经由 ticketCount 角标在
  // 底部说了一次，行上再说一遍是噪音。
  ticketsByDir: Map<string, number>
  onSelectDir: (base: BaseDir) => void
  onOpenTask: (base: BaseDir | null, taskId: string) => void  // base null = 未归属任务
  onOpenBoard: () => void
  onOpenCards?: () => void
  cardNeedsCount?: number
  onOpenTickets: () => void
  onOpenSettings: () => void
  onAddProject?: () => void
  onUnregister?: (name: string, machine: string) => Promise<void> | void
  onEdit?: (project: ProjectNode) => void
  // onWorktreeCreated 建完树后回调，由 Shell 刷新树并把新目录选为当前基准目录。
  // 与 onUnregister / onEdit 同一条规矩：没传就不给这个入口。
  onWorktreeCreated?: (project: ProjectNode, machine: string, ws: Workspace) => void
}

// MACHINE_LABEL 给机器名做人话标签：""=本机。
function machineLabel(machine: string): string {
  return machine === '' ? '本机' : machine
}

// locationProblem 判定一个机器节点是否不可达：location 探测失败优先，否则看
// 跨机汇总信封里对应机器是否 ok=false。返回原因原文；空串=正常。
function locationProblem(loc: ProjectLocationNode, machines: MachineStatus[] | undefined): string {
  if (loc.probe_error !== '') return loc.probe_error
  const ms = machines?.find((m) => m.name === loc.machine)
  if (ms && !ms.ok) return ms.error
  return ''
}

// ROW_CLASS 是所有行的基础样式；选中态 / hover 态在其上叠加。
const ROW_CLASS = 'flex w-full items-center gap-1.5 py-1 pr-2 text-left text-[13px]'

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
// key 用绝对路径：同一台机器上路径唯一，且它正是后端白名单比对的那个值，
// 前后端用同一个字符串做身份，不需要额外的映射表。
export function workspaceBase(project: ProjectNode, machine: string, ws: Workspace): BaseDir {
  return {
    key: ws.path,
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

export function ProjectTree({ tree, tasks, selectedKey, ticketCount, ticketsByDir, onSelectDir, onOpenTask, onOpenBoard, onOpenCards, cardNeedsCount = 0, onOpenTickets, onOpenSettings, onAddProject, onUnregister, onEdit, onWorktreeCreated }: ProjectTreeProps) {
  // collapsed：空集 = 全展开。为什么用「收起集合」而不是「展开集合」：默认全展开
  // 意味着初值空集，渲染时 `!collapsed.has(key)` 天然为真，不用为每个节点预填。
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [query, setQuery] = useState('')
  // 显示偏好：初值从 localStorage 读一次（惰性初始化，不要每次渲染都读）。
  // 改动统一走 updatePrefs——落盘与 setState 必须成对，分开写迟早漏一处
  const [prefs, setPrefs] = useState<TreePrefs>(() => loadPrefs())
  const updatePrefs = (next: TreePrefs) => {
    setPrefs(next)
    savePrefs(next)
  }
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
  const filtered = useMemo(() => filterTree(tree, tasks, query), [tree, tasks, query])
  const searching = filtered.query !== ''

  // 「已结束」分组的数据源：目录已被回收的终态任务（见 archived.ts 文件头）。
  // 入参是**原树 tree** 而不是 filtered.projects——裁剪过的树会把被搜索过滤掉的
  // 目录当成「已不在」，正常任务会突然涌进这个分组。
  const archived = useMemo(() => archivedTasks(tree, tasks), [tree, tasks])
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
  // 这是一条让位次序：将来中央的终端 tab 拿到焦点时，xterm 会吞掉自己的
  // 按键；冒泡阶段监听意味着「任何调用 stopPropagation 的组件优先」——
  // 在终端里按 ⌘K 不该把焦点抢到左栏来。改成 capture 会当场破坏这一点。
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.metaKey || e.ctrlKey) || e.key.toLowerCase() !== 'k') return
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
  const [menu, setMenu] = useState<{ x: number; y: number; name: string; machine: string; project: ProjectNode } | null>(null)
  const toggle = (key: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  // 搜索期间旁路 collapsed：搜到了却折叠着等于没搜到。
  // 注意是「旁路」不是「清空」——collapsed 原样保留，查询清空后用户手动
  // 折起来的布局立刻回来，搜索不破坏布局。
  const expanded = (key: string) => searching || !collapsed.has(key)

  // 搜索期间旁路「隐藏」类偏好：搜到了却被偏好过滤掉，等于搜索坏了。
  // 排序不旁路——排序不会让东西消失，跟着当前档反而更连贯
  const projectSplit = searching
    ? { shown: filtered.projects, hiddenCount: 0 }
    : splitHiddenProjects(filtered.projects, (p) => p.project_id, prefs.hiddenProjects)
  // 项目的「最近活动」= 该项目下任务 updated_at 的最大值；一条任务都没有时为空串
  const lastActivity = (projectID: string) =>
    tasks.reduce((max, t) => (t.project_id === projectID && t.updated_at > max ? t.updated_at : max), '')
  const orderedProjects = sortProjects(
    projectSplit.shown,
    (p) => {
      const c = countsForProject(tasks, p)
      return { active: c.running + c.pending, updatedAt: lastActivity(p.project_id), name: p.name }
    },
    prefs.projectSort,
  )

  const unassigned = filtered.unassignedTasks
  const hasUnowned = unassigned.length > 0 || filtered.unownedNames.length > 0

  const taskName = (t: Task) => t.name || t.plan_summary || '（无名称）'

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

  // wsCounts 统计一个工作树目录下的运行/待处理任务（目录行只显示这两个数）。
  // 计数与列出的任务共用 tasksOfWorkspace 一个口径，原地任务不会被算漏。
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
    <div className="flex min-h-0 flex-1 flex-col py-2">
      {/* 第一段：不滚——搜索框 + 「项目 N」。
          任务看板入口不在这里，它和工单、设置一起收在底部图标区：三者都是
          「离开这棵树去别处看」的全局入口，散在一头一尾会让人以为它们是两类
          东西，而顶部那一整行也把搜索框往下压了一格 */}

      {/* 搜索框与「项目 N」——形态基准是原型左栏的 sidebar-search +
          sidebar-section-title。N 跟随过滤，搜索时它就是「找到几个」的
          即时反馈；「未归属」不计入——它不是项目，是收纳箱 */}
      <div className="mb-1 px-2">
        <label className="flex items-center gap-1.5 rounded-md border bg-background px-2 py-1">
          <Search className="size-3.5 shrink-0 text-muted-foreground" />
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
            className="min-w-0 flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted-foreground"
          />
          <kbd className="shrink-0 rounded border px-1 text-[10px] text-muted-foreground">⌘K</kbd>
        </label>
      </div>

      {/* 数字紧跟标签、比标签浅一档——形态基准是原型的
          .sidebar-section-title span { margin-left:3px; color:#969696; font-weight:500 } */}
      <div className="flex items-center gap-1 px-3 pb-1 pt-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        <span>项目</span>
        <span data-testid="project-count" className="font-normal text-muted-foreground/70">
          {orderedProjects.length}
        </span>
        {/* 藏了东西就必须说一声：偏好可以让它不占地方，但不能让人以为它不存在 */}
        {projectSplit.hiddenCount > 0 && (
          <span className="font-normal normal-case text-muted-foreground/70">· 已隐藏 {projectSplit.hiddenCount}</span>
        )}
        <span className="ml-auto">
          {/* 菜单吃的是**原树**的项目，不是过滤后的：藏起来的项目要能勾回来 */}
          <TreePrefsMenu
            prefs={prefs}
            projects={tree.projects.map((p) => ({ project_id: p.project_id, name: p.name }))}
            onChange={updatePrefs}
          />
        </span>
      </div>

      {/* 第二段：只有它滚 */}
      <div data-testid="tree-scroll" className="min-h-0 flex-1 overflow-y-auto">

      {orderedProjects.map((project) => {
        const pKey = `p:${project.project_id}`
        const pOpen = expanded(pKey)
        const pCounts = countsForProject(tasks, project)
        return (
          <div key={pKey}>
            <button
              type="button"
              aria-expanded={project.locations.length > 0 ? pOpen : undefined}
              onClick={() => toggle(pKey)}
              className={cn(ROW_CLASS, 'hover:bg-accent/60')}
              style={{ paddingLeft: 8 }}
            >
              {project.locations.length > 0 ? (
                <Arrow open={pOpen} onToggle={() => toggle(pKey)} />
              ) : (
                <span className="size-4 shrink-0" />
              )}
              {/* 项目身份色：让整棵树不至于只有一个灰。取色只依赖 project_id，
                  与列表顺序无关（见 projectColor.ts 的边界说明） */}
              <FolderGit2
                data-project-color={projectColorClass(project.project_id)}
                className={cn('size-4 shrink-0', projectColorClass(project.project_id))}
              />
              <span className="min-w-0 flex-1 truncate">{project.name}</span>
              <RowCounts dirs={pCounts.dirs} running={pCounts.running} pending={pCounts.pending} />
            </button>

            {pOpen &&
              project.locations.map((loc) => {
                const mKey = `m:${project.project_id}:${loc.machine}`
                const mOpen = expanded(mKey)
                const problem = locationProblem(loc, tree.machines)
                const hasChildren = loc.workspaces.length > 0
                const mCounts = countsForMachine(tasks, project, loc.machine)
                const aKey = archivedKey(project.project_id, loc.machine)
                // 搜索期间分组内也跟着按任务名过滤：搜到了却埋在几十条历史里等于没搜到
                const aTasks = (archived.get(aKey) ?? []).filter(
                  (t) => !searching || taskName(t).toLowerCase().includes(filtered.query),
                )
                // 搜索命中时自动展开（与 expanded() 旁路 collapsed 同一条理由），
                // 但没命中时不跟着展开——历史堆积不该反客为主
                const aOpen = openArchived.has(aKey) || (searching && aTasks.length > 0)
                return (
                  // 外层只负责分组，不再是定位祖先
                  <div key={mKey}>
                    {/* 定位上下文收在机器行这一层：右键菜单按鼠标坐标 fixed 定位，
                        不依赖它；但目录行/任务行仍不该进这个分组容器 */}
                    <div
                      className="group relative"
                      data-testid="machine-row"
                      onContextMenu={
                        onUnregister || onEdit || onWorktreeCreated
                          ? (e) => {
                              // 阻止浏览器原生菜单，换成我们这份。
                              // Shift+F10 与 ContextMenu 键也派发这个事件，
                              // 所以键盘用户走的是同一条路，不需要额外快捷键
                              e.preventDefault()
                              setMenu({ x: e.clientX, y: e.clientY, name: loc.name, machine: loc.machine, project })
                            }
                          : undefined
                      }
                    >
                      <button
                        type="button"
                        aria-disabled={problem !== '' || undefined}
                        aria-expanded={hasChildren && problem === '' ? mOpen : undefined}
                        onClick={problem !== '' ? undefined : () => toggle(mKey)}
                        className={cn(ROW_CLASS, 'hover:bg-accent/60')}
                        style={{ paddingLeft: 8 + 16 }}
                      >
                        {hasChildren && problem === '' ? (
                          <Arrow open={mOpen} onToggle={() => toggle(mKey)} />
                        ) : (
                          <span className="size-4 shrink-0" />
                        )}
                        <HardDrive className="size-4 shrink-0 text-muted-foreground" />
                        {/* 连接态用与任务状态同一套圆点：一个界面里两套"绿点"含义不同会更糊涂。
                            probe_error 非空 = 这个位置探测失败 = 机器当前不可达 */}
                        <StateDot tone={loc.probe_error !== '' ? 'failed' : 'active'} />
                        <span className="min-w-0 flex-1 truncate">{machineLabel(loc.machine)}</span>
                        {problem !== '' && <DisconnectedBadge />}
                        {/* 机器行保留三段（原型只有两段）：待处理是「你还欠什么」的信号，
                            机器是任务的实际落点，在这层藏掉等于逼人展开到目录才看得见 */}
                        {/* hover 时让位给 + 按钮：两者都要行右端，让位是唯一
                            不重叠的排法（此前的结论是「排不出来」，那是因为只
                            试过叠加）。用 invisible 而不是 hidden——保留占位，
                            行内其它元素不会因为 hover 左右位移 */}
                        <span className={cn(onWorktreeCreated && problem === '' && 'group-hover:invisible')}>
                          <RowCounts dirs={mCounts.dirs} running={mCounts.running} pending={mCounts.pending} />
                        </span>
                      </button>
                      {onWorktreeCreated && problem === '' && (
                        <button
                          type="button"
                          aria-label="新建工作树"
                          title="新建工作树"
                          onClick={() => setWorktreeTarget({ project, loc })}
                          className="absolute right-2 top-1/2 hidden -translate-y-1/2 rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground group-hover:block"
                        >
                          <Plus className="size-3.5" />
                        </button>
                      )}
                    </div>
                    {/* 目录行、任务行留在外层，不进定位上下文 */}
                    {problem !== '' && (
                      <p
                        className="break-words pb-1 pr-2 text-[11px] text-destructive"
                        style={{ paddingLeft: 8 + 16 + 20 }}
                      >
                        {problem}
                      </p>
                    )}

                    {problem === '' && mOpen && (() => {
                      const sorted = sortWorkspaces(loc.workspaces, (ws) => wsMetrics(project, loc.machine, ws))
                      const split = splitIdleWorkspaces(
                        sorted,
                        (ws) => {
                          const c = wsCounts(project, loc.machine, ws)
                          return {
                            isMain: ws.is_main,
                            selected: selectedKey === ws.path,
                            active: c.running + c.pending,
                          }
                        },
                        // 搜索期间不折叠，理由同项目隐藏
                        prefs.hideIdleWorktrees && !searching,
                      )
                      const renderWorkspace = (ws: Workspace) => {
                        const base = workspaceBase(project, loc.machine, ws)
                        const dSelected = selectedKey === base.key
                        const under = wsCounts(project, loc.machine, ws)
                        const wsTasks = tasksOfWorkspace(tasks, project, loc.machine, ws)
                        return (
                          <div key={base.key}>
                            <button
                              type="button"
                              data-testid="workspace-row"
                              aria-current={dSelected ? 'true' : undefined}
                              onClick={() => onSelectDir(base)}
                              className={cn(ROW_CLASS, 'hover:bg-accent/60', dSelected && 'bg-sidebar-accent font-medium')}
                              style={{ paddingLeft: 8 + 32 }}
                            >
                              <span className="size-4 shrink-0" />
                              {ws.is_main ? (
                                <Home className="size-3.5 shrink-0 text-muted-foreground" />
                              ) : (
                                <GitBranch className="size-3.5 shrink-0 text-muted-foreground" />
                              )}
                              <span className="min-w-0 flex-1 truncate font-mono">{dirLabel(ws)}</span>
                              <RowCounts running={under.running} pending={under.pending} />
                            </button>

                            {wsTasks.map((t) => (
                              <button
                                key={t.id}
                                type="button"
                                draggable
                                onDragStart={(e) => {
                                  e.dataTransfer.setData(DRAG_TASK_MIME, t.id)
                                  e.dataTransfer.setData(DRAG_BASE_MIME, JSON.stringify(base))
                                  e.dataTransfer.effectAllowed = 'copy'
                                }}
                                onClick={() => onOpenTask(base, t.id)}
                                className={cn(ROW_CLASS, 'text-muted-foreground hover:bg-accent/60 hover:text-foreground')}
                                style={{ paddingLeft: 8 + 48 }}
                              >
                                <span className="size-4 shrink-0" />
                                {/* 圆点跟随任务状态：同一个任务在看板上标着琥珀、
                                    在左栏是灰点的话，两个面自相矛盾 */}
                                <StateDot tone={stateTone(t.state)} />
                                <span className="min-w-0 flex-1 truncate">{taskName(t)}</span>
                              </button>
                            ))}
                          </div>
                        )
                      }
                      const hiddenOpen = openHiddenDirs.has(mKey)
                      return (
                        <>
                          {split.shown.map(renderWorkspace)}
                          {split.hidden.length > 0 && (
                            <>
                              <button
                                type="button"
                                data-testid="hidden-dirs-row"
                                aria-expanded={hiddenOpen}
                                onClick={() => toggleHiddenDirs(mKey)}
                                className={cn(ROW_CLASS, 'text-muted-foreground hover:bg-accent/60')}
                                style={{ paddingLeft: 8 + 32 }}
                              >
                                <Arrow open={hiddenOpen} onToggle={() => toggleHiddenDirs(mKey)} />
                                <span className="min-w-0 flex-1 truncate">已隐藏 {split.hidden.length} 个目录</span>
                              </button>
                              {hiddenOpen && split.hidden.map(renderWorkspace)}
                            </>
                          )}
                        </>
                      )
                    })()}

                    {/* 「已结束」分组：done 回收了 worktree，这些任务在树上没有可挂的
                        目录行。默认收起，展开后仍可点开它们的 TUI 回看（spec §8 的
                        「不静默少一条」在任务这一层的兑现） */}
                    {problem === '' && mOpen && aTasks.length > 0 && (
                      <div>
                        <button
                          type="button"
                          data-testid="archived-row"
                          aria-expanded={aOpen}
                          title={ARCHIVED_TITLE}
                          onClick={() => toggleArchived(aKey)}
                          className={cn(ROW_CLASS, 'text-muted-foreground hover:bg-accent/60')}
                          style={{ paddingLeft: 8 + 32 }}
                        >
                          <Arrow open={aOpen} onToggle={() => toggleArchived(aKey)} />
                          <Archive className="size-3.5 shrink-0" />
                          <span className="min-w-0 flex-1 truncate">{ARCHIVED_LABEL}</span>
                          <span className="ml-auto shrink-0 font-mono text-[9.5px] tabular-nums">{aTasks.length}</span>
                        </button>
                        {aOpen &&
                          aTasks.map((t) => (
                            <button
                              key={t.id}
                              type="button"
                              draggable
                              onDragStart={(e) => {
                                e.dataTransfer.setData(DRAG_TASK_MIME, t.id)
                                e.dataTransfer.setData(
                                  DRAG_BASE_MIME,
                                  JSON.stringify(archivedBase(project, loc.machine)),
                                )
                                e.dataTransfer.effectAllowed = 'copy'
                              }}
                              onClick={() => onOpenTask(archivedBase(project, loc.machine), t.id)}
                              className={cn(ROW_CLASS, 'text-muted-foreground hover:bg-accent/60 hover:text-foreground')}
                              style={{ paddingLeft: 8 + 48 }}
                            >
                              <span className="size-4 shrink-0" />
                              <StateDot tone={stateTone(t.state)} />
                              <span className="min-w-0 flex-1 truncate">{taskName(t)}</span>
                            </button>
                          ))}
                      </div>
                    )}
                    </div>
                )
              })}
          </div>
        )
      })}

      {hasUnowned && (
        <div>
          <p className="px-3 pb-1 pt-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">未归属</p>
          {unassigned.map((t) => (
            <button
              key={t.id}
              type="button"
              draggable
              onDragStart={(e) => {
                e.dataTransfer.setData(DRAG_TASK_MIME, t.id)
                e.dataTransfer.setData(DRAG_BASE_MIME, 'null')
                e.dataTransfer.effectAllowed = 'copy'
              }}
              onClick={() => onOpenTask(null, t.id)}
              className={cn(ROW_CLASS, 'text-muted-foreground hover:bg-accent/60 hover:text-foreground')}
              style={{ paddingLeft: 8 + 48 }}
            >
              <span className="size-4 shrink-0" />
              <StateDot tone={stateTone(t.state)} />
              <span className="min-w-0 flex-1 truncate">{taskName(t)}</span>
            </button>
          ))}
          {filtered.unownedNames.map((name) => (
            <p key={name} className="py-1 pr-2 text-[13px] text-muted-foreground" style={{ paddingLeft: 8 + 16 }}>
              {name}（未登记为项目）
            </p>
          ))}
        </div>
      )}

      {/* 左栏搜到全白会像加载失败，必须有话说 */}
      {filtered.isEmpty && searching && (
        <p className="px-3 py-4 text-[13px] text-muted-foreground">没有匹配的项目或任务</p>
      )}
      </div>

      {/* 第三段：钉在底部 */}
      {/* 底部四入口：添加项目占主位，看板 / 工单 / 设置收在右侧图标区。
          三个图标是同一类东西——都是「离开这棵树去别处看」的全局入口，
          所以摆在一起（看板原先单独钉在顶部，那个位置让它看起来像是树的一部分）。
          工单数为 0 时按钮仍在、角标不显示——按钮消失会让人以为功能没了 */}
      <div className="mt-1 flex items-center gap-1 border-t px-2 pt-2">
        <button
          type="button"
          onClick={onAddProject}
          className="flex flex-1 items-center gap-1.5 rounded-md py-1 pl-1 text-left text-[13px] text-muted-foreground hover:bg-accent/60 hover:text-foreground"
        >
          <Plus className="size-4 shrink-0" />
          <span>添加项目</span>
        </button>
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
        <button
          type="button"
          aria-label="任务看板"
          title="任务看板"
          onClick={onOpenBoard}
          className="rounded-md p-1.5 text-muted-foreground hover:bg-accent/60 hover:text-foreground"
        >
          <LayoutGrid className="size-4" />
        </button>
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
