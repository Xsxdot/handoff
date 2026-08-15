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
  ChevronRight, FolderGit2, GitBranch, HardDrive, Home, LayoutGrid, Plus, Search, Settings, Ticket, WifiOff,
} from 'lucide-react'
import { filterTree } from './search'
import type { MachineStatus, ProjectLocationNode, ProjectNode, ProjectTreeResp, Task, Workspace } from '../../api/types'
import type { BaseDir } from '../workbench/useWorkbench'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'
import { ContextMenu } from '../shared/ContextMenu'
import { countsForMachine, countsForProject } from './counts'
import { stateTone } from '../board/columns'
import { StateDot } from '../board/StateDot'
import { RowCounts } from './RowCounts'
import { projectColorClass } from './projectColor'
import { cn } from '@/lib/utils'

export interface ProjectTreeProps {
  tree: ProjectTreeResp
  tasks: Task[]
  selectedKey: string | null            // 当前选中目录的 BaseDir.key
  ticketCount: number                   // 挂起工单总数，0 时不显示角标
  onSelectDir: (base: BaseDir) => void
  onOpenTask: (base: BaseDir | null, taskId: string) => void  // base null = 未归属任务
  onOpenBoard: () => void
  onOpenTickets: () => void
  onOpenSettings: () => void
  onAddProject?: () => void
  onUnregister?: (name: string, machine: string) => Promise<void> | void
  onEdit?: (project: ProjectNode) => void
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

export function ProjectTree({ tree, tasks, selectedKey, ticketCount, onSelectDir, onOpenTask, onOpenBoard, onOpenTickets, onOpenSettings, onAddProject, onUnregister, onEdit }: ProjectTreeProps) {
  // collapsed：空集 = 全展开。为什么用「收起集合」而不是「展开集合」：默认全展开
  // 意味着初值空集，渲染时 `!collapsed.has(key)` 天然为真，不用为每个节点预填。
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [query, setQuery] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)

  // 过滤结果。tasks 每 2.5s 刷新一次，useMemo 避免每次任务流心跳都重算整棵树。
  const filtered = useMemo(() => filterTree(tree, tasks, query), [tree, tasks, query])
  const searching = filtered.query !== ''

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

  const unassigned = filtered.unassignedTasks
  const hasUnowned = unassigned.length > 0 || filtered.unownedNames.length > 0

  const taskName = (t: Task) => t.name || t.plan_summary || '（无名称）'

  // wsCounts 统计一个工作树目录下的运行/待处理任务（目录行只显示这两个数）。
  // 计数与列出的任务共用 tasksOfWorkspace 一个口径，原地任务不会被算漏。
  const wsCounts = (project: ProjectNode, machine: string, ws: Workspace) => {
    const under = tasksOfWorkspace(tasks, project, machine, ws)
    return {
      running: under.filter((t) => t.state === 'running').length,
      pending: under.filter((t) => t.state === 'waiting_answer' || t.state === 'waiting_review').length,
    }
  }

  return (
    // 三段式：顶部（导航+搜索+标题）不滚 · 中间树独滚 · 底部入口钉死。
    // 为什么不让整个 aside 滚：项目一多，「添加项目」会被推到 scrollHeight
    // 的最下面（实测 top:1100 / 视口 1024），要滚到底才找得到入口
    <div className="flex min-h-0 flex-1 flex-col py-2">
      {/* 第一段：不滚——任务看板入口 + 搜索框 + 「项目 N」 */}
      <button
        type="button"
        onClick={onOpenBoard}
        className={cn(ROW_CLASS, 'mb-1 hover:bg-accent/60')}
        style={{ paddingLeft: 8 }}
      >
        <LayoutGrid className="size-4 shrink-0 text-muted-foreground" />
        <span>任务看板</span>
      </button>

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
          {filtered.projectCount}
        </span>
      </div>

      {/* 第二段：只有它滚 */}
      <div data-testid="tree-scroll" className="min-h-0 flex-1 overflow-y-auto">

      {filtered.projects.map((project) => {
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
                return (
                  // 外层只负责分组，不再是定位祖先
                  <div key={mKey}>
                    {/* 定位上下文收在机器行这一层：右键菜单按鼠标坐标 fixed 定位，
                        不依赖它；但目录行/任务行仍不该进这个分组容器 */}
                    <div
                      className="group relative"
                      data-testid="machine-row"
                      onContextMenu={
                        onUnregister || onEdit
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
                        <RowCounts dirs={mCounts.dirs} running={mCounts.running} pending={mCounts.pending} />
                      </button>
                      {/* 注销入口在右键菜单里，不在行内。
                          行内 absolute 按钮与同一行右端的 RowCounts 抢位置——
                          08-14 修过一次垂直居中（定位上下文从 578px 子树收进本行），
                          但水平方向两者都要右端，改不出不重叠的排法 */}
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

                    {problem === '' &&
                      mOpen &&
                      loc.workspaces.map((ws) => {
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
                      })}
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
      {/* 底部三入口：添加项目占主位，工单与设置收在右侧图标区（spec §3.2）。
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
          aria-label="工单"
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

      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          onClose={() => setMenu(null)}
          items={[
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
