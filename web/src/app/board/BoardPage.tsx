// BoardPage —— 任务看板（顶层页面，嵌在 Shell 内容区）。
//
// 数据源：任务流由 Shell 的 useTasks()（2.5s）持有并经 Outlet context 下发，
// 本页不再自己轮询。为什么轮询而不是 WS：/ws/events 是**按单任务**订阅的，
// 看板照搬就得开 N 条连接；看板是低频视图，轮询足够（整机级订阅记给 W3）。
//
// 筛选：全部在客户端做（applyFilter），看板已 2.5s 全量拉 /api/tasks，改走后端
// 只会让筛选变成一次网络往返、并与轮询节奏打架（spec §3.1）。filter 是唯一真相，
// 本页与左栏项目树是它的两个编辑入口，不维护第二份状态。
//
// 列与状态机的映射是硬契约（columns.ts，vitest 钉死）：pending → 等待执行；
// running / waiting_answer → 进行中（waiting_answer 加「等你答复」）；
// waiting_review → Review；completed / failed → 完成（failed 视觉区分）。
import { useMemo } from 'react'
import { Badge } from '@/components/ui/badge'
import type { Task } from '../../api/types'
import {
  BOARD_COLUMNS,
  COLUMN_LABELS,
  isFailed,
  isWaitingAnswer,
  stateBadgeVariant,
  stateLabel,
  stateToColumn,
  type BoardColumn,
} from './columns'
import { applyFilter } from './filter'
import { FilterBar } from './FilterBar'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { formatRelative } from '../lib/format'
import { useShellContext } from '../shell/Shell'

export function BoardPage() {
  const { tasksState, tree, filter, setFilter, onOpenTask } = useShellContext()
  const tasks = tasksState.data ?? []
  const { disconnected, sessionExpired, errorText } = tasksState

  const projects = tree?.projects ?? []
  const filtered = applyFilter(tasks, filter, projects)

  // machines：机器下拉的候选。树流带跨机汇总信封（tree.machines），本机（""）恒在。
  const machines = useMemo(() => {
    const names = new Set<string>([''])
    for (const m of tree?.machines ?? []) names.add(m.name)
    return [...names]
  }, [tree])

  // taskCounts：每个项目的任务数（从全量任务流算，供项目下拉项右侧显示）。
  const taskCounts = useMemo(() => {
    const counts: Record<string, number> = {}
    for (const t of tasks) counts[t.project_id] = (counts[t.project_id] ?? 0) + 1
    return counts
  }, [tasks])

  // projectNameOf 在项目树里反查显示名；查不到 = 未归属任务。
  const projectNameOf = (id: string) => projects.find((p) => p.project_id === id)?.name ?? ''

  return (
    <main className="flex w-full flex-col gap-3 p-3">
      {sessionExpired && <SessionExpiredBanner />}
      {disconnected && !sessionExpired && <DisconnectedBanner message={errorText} />}

      {tasksState.data === null ? (
        sessionExpired ? null : (
          <LoadFailed message={errorText || '正在连接 agentd…'} onRetry={() => window.location.reload()} />
        )
      ) : (
        <>
          <FilterBar
            filter={filter}
            onChange={setFilter}
            projects={projects}
            machines={machines}
            taskCounts={taskCounts}
            taskCount={filtered.length}
          />
          <div className="flex flex-1 items-stretch gap-3 overflow-x-auto pb-2">
            {BOARD_COLUMNS.map((col) => (
              <BoardColumn
                key={col}
                column={col}
                tasks={filtered.filter((t) => stateToColumn(t.state) === col)}
                onOpen={onOpenTask}
                projectNameOf={projectNameOf}
              />
            ))}
          </div>
        </>
      )}
    </main>
  )
}

// BoardColumn 渲染一列看板：列名 + 计数 + 该列的任务卡片。
function BoardColumn({
  column,
  tasks,
  onOpen,
  projectNameOf,
}: {
  column: BoardColumn
  tasks: Task[]
  onOpen: (id: string) => void
  projectNameOf: (projectId: string) => string
}) {
  return (
    <section className="flex w-64 shrink-0 flex-col rounded-lg border bg-background/60">
      <header className="flex items-center justify-between border-b px-3 py-2">
        <h2 className="text-sm font-medium">{COLUMN_LABELS[column]}</h2>
        <Badge variant="secondary">{tasks.length}</Badge>
      </header>
      <div className="flex flex-1 flex-col gap-2 p-2">
        {tasks.length === 0 ? (
          <p className="px-1 py-2 text-xs text-muted-foreground">（空）</p>
        ) : (
          tasks.map((t) => <TaskCard key={t.id} task={t} projectName={projectNameOf(t.project_id)} onOpen={() => onOpen(t.id)} />)
        )}
      </div>
    </section>
  )
}

// TaskCard 是一张任务卡片：名称、状态、执行器、以及三行元信息——
// 项目（未归属时标「未归属」）、工作树（branch）、机器（""=本机）。
// waiting_answer 加「等你答复」标记；failed 整卡视觉区分。
function TaskCard({ task, projectName, onOpen }: { task: Task; projectName: string; onOpen: () => void }) {
  const waitingAnswer = isWaitingAnswer(task.state)
  const failed = isFailed(task.state)
  return (
    <button
      type="button"
      onClick={onOpen}
      className={`flex flex-col gap-1.5 rounded-md border bg-background p-2.5 text-left shadow-sm transition-colors hover:bg-accent/60 ${
        failed ? 'border-destructive/40 bg-destructive/5' : ''
      }`}
    >
      <div className="flex items-start justify-between gap-2">
        <span className="min-w-0 flex-1 truncate text-sm font-medium">
          {task.name || task.plan_summary || '（无名称）'}
        </span>
        {waitingAnswer && <Badge variant="destructive">等你答复</Badge>}
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge variant={stateBadgeVariant(task.state)}>{stateLabel(task.state)}</Badge>
        <span className="font-mono text-xs text-muted-foreground">{task.executor}</span>
      </div>
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
        <span className="min-w-0 truncate font-mono">{task.branch}</span>
        <span className="shrink-0">{formatRelative(task.updated_at)}</span>
      </div>
      <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
        <span className="min-w-0 truncate">{projectName || '未归属'}</span>
        <span aria-hidden>·</span>
        <span className="shrink-0">{task.machine === '' ? '本机' : task.machine}</span>
      </div>
    </button>
  )
}
