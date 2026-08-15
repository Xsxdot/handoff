// FilterBar —— 看板顶部筛选栏：搜索 + 项目多选 + 开发机 + 只看待处理 + 任务总数。
//
// 数据流：filter 是筛选的唯一真相（filter.ts），本组件是它的一个编辑入口（另一个
// 是左栏项目树）。所有写入都走 filter.ts 的纯函数，本组件不维护第二份筛选状态——
// 两套状态一定会出现"左栏选了 A、顶部显示 B、看板按 C 筛"的第三种状态。
//
// 机器选项语义（与 filter.ts 一致）：""=本机（显示「本机」），null=不筛。选「本机」
// 必须写空串，写成 null 等于没筛，看板会把远程任务也列出来。
import { useMemo, useRef } from 'react'
import { Search } from 'lucide-react'
import type { ProjectNode } from '../../api/types'
import { cn } from '@/lib/utils'
import { setMachine, setPendingOnly, setProjects, setSearch, type BoardFilter } from './filter'
import { Dropdown } from '../lib/Dropdown'

export interface FilterBarProps {
  filter: BoardFilter
  onChange: (f: BoardFilter) => void
  projects: ProjectNode[]
  machines: string[] // 机器名列表（""=本机）
  taskCounts: Record<string, number> // project_id → 任务数（全量，供下拉项右侧显示）
  taskCount: number // 筛选后的任务总数（由 BoardPage 用 applyFilter 算好传入）
}

export function FilterBar({ filter, onChange, projects, machines, taskCounts, taskCount }: FilterBarProps) {
  const projectOptions = useMemo(
    () => projects.map((p) => ({ value: p.project_id, label: p.name, extra: String(taskCounts[p.project_id] ?? 0) })),
    [projects, taskCounts],
  )
  const machineOptions = useMemo(
    () => machines.map((m) => ({ value: m, label: m === '' ? '本机' : m })),
    [machines],
  )

  // 项目多选的写入走 setProjects：它会顺带清理「不再属于任一选中项目」的
  // machine/workspace，顶部下拉与左栏单选因此共用同一套写入规则。
  //
  // next 从 latestRef 取而不是 filter prop：一次打开的下拉里连续勾选时，父组件
  // 未必来得及用 onChange 的结果重渲染（测试里父组件是 mock），从 prop 取会丢掉
  // 上一次勾选。latestRef 每次渲染同步 filter，真实父组件下发时同样生效。
  const latestRef = useRef(filter)
  latestRef.current = filter
  const onToggleProject = (projectId: string) => {
    const next = new Set(latestRef.current.projects)
    if (next.has(projectId)) next.delete(projectId)
    else next.add(projectId)
    const result = setProjects(latestRef.current, next, projects)
    latestRef.current = result
    onChange(result)
  }

  return (
    <div className="flex flex-wrap items-center gap-2">
      <div className="relative">
        <Search className="pointer-events-none absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
        <input
          type="search"
          value={filter.search}
          onChange={(e) => onChange(setSearch(filter, e.target.value))}
          placeholder="搜索任务、项目或执行者"
          className="h-8 w-56 rounded-md border border-input bg-background pl-8 pr-2.5 text-xs shadow-sm outline-none placeholder:text-muted-foreground focus-visible:ring-1 focus-visible:ring-ring"
        />
      </div>
      <Dropdown
        label="项目"
        multiple
        options={projectOptions}
        selected={[...filter.projects]}
        onSelect={onToggleProject}
      />
      <Dropdown
        label="开发机"
        options={machineOptions}
        selected={filter.machine !== null ? [filter.machine] : []}
        onSelect={(value) => onChange(setMachine(filter, value))}
      />
      <button
        type="button"
        role="switch"
        aria-checked={filter.pendingOnly}
        onClick={() => onChange(setPendingOnly(filter, !filter.pendingOnly))}
        className={cn(
          'inline-flex h-8 items-center gap-1.5 rounded-md border px-2.5 text-xs transition-colors',
          filter.pendingOnly
            ? 'border-primary bg-primary/10 text-primary'
            : 'border-input bg-background text-muted-foreground hover:bg-accent',
        )}
      >
        只看待处理
      </button>
      <p className="ml-auto text-xs text-muted-foreground">共 {taskCount} 个任务</p>
    </div>
  )
}
