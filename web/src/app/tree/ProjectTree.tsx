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
// 筛选写入：点项目/机器/目录行分别走 selectProject/selectMachine/selectWorkspace
// （规则见 filter.ts）；树本身不被 filter 过滤——它就是筛选的编辑入口。
//
// 计数来源：任务流（2.5s），见 counts.ts 的文件头注释。
//
// 任务 9：底部「添加项目」接 onAddProject 打开登记向导；机器（位置）行右侧悬浮
// 注销按钮（仅当 onUnregister 提供时渲染），点按弹 ConfirmDialog 二次确认，
// agentd 报错原文透出（spec §10）。
import { useState } from 'react'
import { ChevronRight, FolderGit2, HardDrive, Plus, Trash2, WifiOff } from 'lucide-react'
import type { MachineStatus, ProjectLocationNode, ProjectNode, ProjectTreeResp, Task } from '../../api/types'
import { selectMachine, selectProject, selectWorkspace, type BoardFilter } from '../board/filter'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'
import { countsForMachine, countsForProject } from './counts'
import { cn } from '@/lib/utils'

export interface ProjectTreeProps {
  tree: ProjectTreeResp
  tasks: Task[]
  filter: BoardFilter
  onFilterChange: (f: BoardFilter) => void
  onOpenTask: (id: string) => void
  onAddProject?: () => void
  onUnregister?: (name: string, machine: string) => Promise<void> | void
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

// RowCounts 渲染一行右侧的计数（项目/机器行 目录·运行·待处理；目录行 运行·待处理）。
function RowCounts({ text, title }: { text: string; title: string }) {
  return (
    <span
      className="ml-auto shrink-0 text-[10px] tabular-nums text-muted-foreground"
      title={title}
    >
      {text}
    </span>
  )
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

export function ProjectTree({ tree, tasks, filter, onFilterChange, onOpenTask, onAddProject, onUnregister }: ProjectTreeProps) {
  // collapsed：空集 = 全展开。为什么用「收起集合」而不是「展开集合」：默认全展开
  // 意味着初值空集，渲染时 `!collapsed.has(key)` 天然为真，不用为每个节点预填。
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [unregisterTarget, setUnregisterTarget] = useState<{ name: string; machine: string } | null>(null)
  const [unregisterError, setUnregisterError] = useState('')
  const toggle = (key: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev)
      if (next.has(key)) next.delete(key)
      else next.add(key)
      return next
    })
  const expanded = (key: string) => !collapsed.has(key)

  // 选中判定：单选（恰一个项目）时才逐行高亮；多选时任何行都不带 aria-current，
  // 只在树顶显示「已选 N 个项目」——单项高亮会让"到底全选了多少个"失去可读性。
  const multi = filter.projects.size > 1
  const singleProject = filter.projects.size === 1 ? [...filter.projects][0] : null

  const unassigned = tasks.filter((t) => t.project_id === '')
  const hasUnowned = unassigned.length > 0 || tree.unowned.length > 0

  const taskName = (t: Task) => t.name || t.plan_summary || '（无名称）'

  // wsCounts 统计一个工作树目录下的运行/待处理任务（目录行只显示这两个数）。
  const wsCounts = (project: ProjectNode, machine: string, path: string) => {
    const under = tasks.filter(
      (t) => t.project_id === project.project_id && t.machine === machine && t.work_dir === path,
    )
    return {
      running: under.filter((t) => t.state === 'running').length,
      pending: under.filter((t) => t.state === 'waiting_answer' || t.state === 'waiting_review').length,
    }
  }

  return (
    <div className="py-2">
      {multi && (
        <div className="mx-2 mb-1 rounded-md border border-accent bg-accent/40 px-2 py-1 text-[11px] text-muted-foreground">
          已选 {filter.projects.size} 个项目
        </div>
      )}

      {tree.projects.map((project) => {
        const pKey = `p:${project.project_id}`
        const pOpen = expanded(pKey)
        const pSelected = !multi && singleProject === project.project_id
        const pCounts = countsForProject(tasks, project)
        return (
          <div key={pKey}>
            <button
              type="button"
              aria-current={pSelected ? 'true' : undefined}
              aria-expanded={project.locations.length > 0 ? pOpen : undefined}
              onClick={() => onFilterChange(selectProject(filter, project.project_id))}
              className={cn(ROW_CLASS, 'hover:bg-accent/60', pSelected && 'bg-sidebar-accent font-medium')}
              style={{ paddingLeft: 8 }}
            >
              {project.locations.length > 0 ? (
                <Arrow open={pOpen} onToggle={() => toggle(pKey)} />
              ) : (
                <span className="size-4 shrink-0" />
              )}
              <FolderGit2 className="size-4 shrink-0 text-muted-foreground" />
              <span className="min-w-0 flex-1 truncate">{project.name}</span>
              <RowCounts text={`${pCounts.dirs}·${pCounts.running}·${pCounts.pending}`} title="目录·运行·待处理" />
            </button>

            {pOpen &&
              project.locations.map((loc) => {
                const mKey = `m:${project.project_id}:${loc.machine}`
                const mOpen = expanded(mKey)
                const problem = locationProblem(loc, tree.machines)
                const mSelected = !multi && singleProject === project.project_id && filter.machine === loc.machine
                const hasChildren = loc.workspaces.length > 0
                const mCounts = countsForMachine(tasks, project, loc.machine)
                return (
                  <div key={mKey} className="group relative">
                    <button
                      type="button"
                      aria-disabled={problem !== '' || undefined}
                      aria-expanded={hasChildren && problem === '' ? mOpen : undefined}
                      aria-current={mSelected ? 'true' : undefined}
                      onClick={problem !== '' ? undefined : () => onFilterChange(selectMachine(filter, loc.machine))}
                      className={cn(ROW_CLASS, 'hover:bg-accent/60', mSelected && 'bg-sidebar-accent font-medium')}
                      style={{ paddingLeft: 8 + 16 }}
                    >
                      {hasChildren && problem === '' ? (
                        <Arrow open={mOpen} onToggle={() => toggle(mKey)} />
                      ) : (
                        <span className="size-4 shrink-0" />
                      )}
                      <HardDrive className="size-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1 truncate">{machineLabel(loc.machine)}</span>
                      {problem !== '' && <DisconnectedBadge />}
                      <RowCounts text={`${mCounts.dirs}·${mCounts.running}·${mCounts.pending}`} title="目录·运行·待处理" />
                    </button>
                    {onUnregister && (
                      <button
                        type="button"
                        aria-label="注销"
                        onClick={() => setUnregisterTarget({ name: loc.name, machine: loc.machine })}
                        className="absolute right-2 top-1/2 hidden -translate-y-1/2 rounded p-1 text-muted-foreground group-hover:inline-flex hover:text-destructive"
                      >
                        <Trash2 className="size-3.5" />
                      </button>
                    )}

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
                        const dKey = `d:${project.project_id}:${loc.machine}:${ws.path}`
                        const dSelected =
                          !multi && singleProject === project.project_id && filter.machine === loc.machine && filter.workspace === ws.path
                        const under = wsCounts(project, loc.machine, ws.path)
                        const wsTasks = tasks.filter(
                          (t) => t.project_id === project.project_id && t.machine === loc.machine && t.work_dir === ws.path,
                        )
                        return (
                          <div key={dKey}>
                            <button
                              type="button"
                              aria-current={dSelected ? 'true' : undefined}
                              onClick={() => onFilterChange(selectWorkspace(filter, ws.path))}
                              className={cn(ROW_CLASS, 'hover:bg-accent/60', dSelected && 'bg-sidebar-accent font-medium')}
                              style={{ paddingLeft: 8 + 32 }}
                            >
                              <span className="size-4 shrink-0" />
                              <span className="inline-block size-2.5 shrink-0 rounded-sm bg-muted-foreground/50" />
                              <span className="min-w-0 flex-1 truncate font-mono">{ws.path}</span>
                              <RowCounts text={`${under.running}·${under.pending}`} title="运行·待处理" />
                            </button>

                            {wsTasks.map((t) => (
                              <button
                                key={t.id}
                                type="button"
                                onClick={() => onOpenTask(t.id)}
                                className={cn(ROW_CLASS, 'text-muted-foreground hover:bg-accent/60 hover:text-foreground')}
                                style={{ paddingLeft: 8 + 48 }}
                              >
                                <span className="size-4 shrink-0" />
                                <span className="inline-block size-1.5 shrink-0 rounded-full bg-muted-foreground/40" />
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
              onClick={() => onOpenTask(t.id)}
              className={cn(ROW_CLASS, 'text-muted-foreground hover:bg-accent/60 hover:text-foreground')}
              style={{ paddingLeft: 8 + 48 }}
            >
              <span className="size-4 shrink-0" />
              <span className="inline-block size-1.5 shrink-0 rounded-full bg-muted-foreground/40" />
              <span className="min-w-0 flex-1 truncate">{taskName(t)}</span>
            </button>
          ))}
          {tree.unowned.map((name) => (
            <p key={name} className="py-1 pr-2 text-[13px] text-muted-foreground" style={{ paddingLeft: 8 + 16 }}>
              {name}（未登记为项目）
            </p>
          ))}
        </div>
      )}

      <button
        type="button"
        onClick={onAddProject}
        className={cn(ROW_CLASS, 'mt-1 text-muted-foreground hover:bg-accent/60 hover:text-foreground')}
        style={{ paddingLeft: 8 }}
      >
        <Plus className="size-4 shrink-0" />
        <span>添加项目</span>
      </button>

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
    </div>
  )
}
