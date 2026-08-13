// TaskHeader —— 任务详情页的任务头：id、名称、状态、执行器、model、分支、
// 工作目录、基准提交短号、派发时仓库脏改动提示。
//
// 边界：纯展示；「已断开」/「会话失效」等全局状态由父级 TaskPage 处理。
import { AlertTriangle, GitBranch, TerminalSquare } from 'lucide-react'
import type { Task } from '../../api/types'
import { Badge } from '@/components/ui/badge'
import { stateBadgeVariant, stateLabel } from '../board/columns'
import { shortCommit, shortID } from '../lib/format'

// DirtyRepoHint 是 repo_dirty_count > 0 时的提示。
//
// 为什么显式提示：这些改动在派发时**不在任务工作树里**（worktree 从干净基线
// 检出），executor 看不到它们——审核者如果以为任务会包含这些改动，会误判
// 产出范围。
function DirtyRepoHint({ task }: { task: Task }) {
  if (task.repo_dirty_count <= 0) return null
  return (
    <div className="flex items-start gap-2 rounded-md border border-amber-500/40 bg-amber-500/10 p-2 text-xs text-amber-700">
      <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
      <p className="break-words">
        派发时主仓库有 {task.repo_dirty_count} 处未提交改动（{task.repo_dirty_files || '未知'}），
        这些改动不在任务工作树里，executor 看不到。
      </p>
    </div>
  )
}

// compact 为真时只出单行摘要：TUI tab 的顶栏只有一行高度，塞不下完整的
// 定义列表，而任务 ID、分支、工作目录这些在面包屑与左栏已经能看到。
export function TaskHeader({ task, compact = false }: { task: Task; compact?: boolean }) {
  if (compact) {
    return (
      <div className="flex min-w-0 items-center gap-2">
        <span className="truncate text-sm font-medium">
          {task.name || task.plan_summary || '（无名称）'}
        </span>
        <span className="shrink-0 font-mono text-[11px] text-muted-foreground">
          handoff-{shortID(task.id)}
        </span>
        <Badge variant={stateBadgeVariant(task.state)}>{stateLabel(task.state)}</Badge>
      </div>
    )
  }
  return (
    <section className="flex flex-col gap-3 rounded-lg border bg-background p-4">
      <div className="flex flex-wrap items-center gap-2">
        <h1 className="min-w-0 flex-1 text-base font-semibold">
          {task.name || task.plan_summary || '（无名称）'}
        </h1>
        <Badge variant={stateBadgeVariant(task.state)}>{stateLabel(task.state)}</Badge>
      </div>

      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1.5 text-sm">
        <dt className="text-muted-foreground">任务 ID</dt>
        <dd className="break-all font-mono text-xs">
          {task.id}
          <span className="ml-2 text-muted-foreground">handoff-{shortID(task.id)}</span>
        </dd>
        <dt className="text-muted-foreground">执行器</dt>
        <dd>{task.executor || '（缺省）'}{task.model ? ` · ${task.model}` : ''}</dd>
        <dt className="text-muted-foreground">分支</dt>
        <dd className="flex items-center gap-1 break-all font-mono text-xs">
          <GitBranch className="size-3.5 shrink-0" />
          {task.branch || '（无）'}
        </dd>
        <dt className="text-muted-foreground">工作目录</dt>
        <dd className="break-all font-mono text-xs">{task.work_dir || task.repo_path}</dd>
        <dt className="text-muted-foreground">基准提交</dt>
        <dd className="flex items-center gap-1 font-mono text-xs">
          <TerminalSquare className="size-3.5 shrink-0" />
          {task.base_commit ? shortCommit(task.base_commit) : '（无/切已存在分支）'}
          {task.base_ahead > 0 ? (
            <span className="text-muted-foreground">，仓库 HEAD 领先 {task.base_ahead} 个提交</span>
          ) : null}
        </dd>
      </dl>

      <DirtyRepoHint task={task} />
    </section>
  )
}
