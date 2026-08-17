// TuiHeader —— TUI tab 的两行页头。
//
// 职责：第一行身份 + 动作（审阅栏开关/调试）；第二行遥测（executor·模型·回合
// 下拉·运行时长·ctx）。回合下拉的数据由 TuiTab 从 frames 派生传入。
// 边界：不取数、不持流；「回合下拉只覆盖已加载范围」必须写在下拉里（turnsPartial）。
import { useState } from 'react'
import type { Task } from '../../api/types'
import type { WsStatus } from '../../api/ws'
import { Badge } from '@/components/ui/badge'
import { stateBadgeVariant, stateLabel } from '../board/columns'
import { formatRelative, shortID } from '../lib/format'
import { UsageChip } from './UsageChip'
import { cn } from '@/lib/utils'

export interface TuiHeaderProps {
  task: Task
  turns: number[]
  turnsPartial: boolean
  onJumpTurn: (turn: number) => void
  reviewAvailable: boolean
  reviewOpen: boolean
  onToggleReview: () => void
  onOpenDebug: () => void
  wsStatus: WsStatus
  disconnected: boolean
}

// TuiHeader 渲染页头。动作按钮的可见性由父级传入的状态决定，这里不判状态机。
export function TuiHeader({
  task, turns, turnsPartial, onJumpTurn,
  reviewAvailable, reviewOpen, onToggleReview, onOpenDebug,
  wsStatus, disconnected,
}: TuiHeaderProps) {
  const [turnsOpen, setTurnsOpen] = useState(false)
  const latestTurn = turns.length > 0 ? turns[turns.length - 1] : null

  return (
    <div className="flex flex-col gap-0.5 border-b px-3.5 py-2">
      <div className="flex min-w-0 items-center gap-2.5">
        <span className="truncate text-sm font-semibold">
          {task.name || task.plan_summary || '（无名称）'}
        </span>
        <Badge variant={stateBadgeVariant(task.state)}>{stateLabel(task.state)}</Badge>
        {disconnected && <Badge variant="destructive">已断开</Badge>}
        {!disconnected && wsStatus === 'open' && <Badge variant="outline">实时</Badge>}
        <span className="ml-auto" />
        {reviewAvailable && (
          <button
            type="button"
            onClick={onToggleReview}
            className={cn(
              'rounded-md border px-2.5 py-0.5 text-xs',
              reviewOpen ? 'border-primary bg-primary text-primary-foreground' : 'hover:bg-muted',
            )}
          >
            审阅栏
          </button>
        )}
        <button type="button" onClick={onOpenDebug} className="rounded-md border px-2.5 py-0.5 text-xs hover:bg-muted">
          调试
        </button>
      </div>

      <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground">
        <span>
          {task.executor}
          {task.actual_model ? ` · ${task.actual_model}` : ''}
        </span>
        <span className="opacity-50">·</span>
        {latestTurn !== null && (
          <span className="relative">
            <button type="button" onClick={() => setTurnsOpen((v) => !v)} className="hover:text-foreground">
              回合 {latestTurn} ▾
            </button>
            {turnsOpen && (
              <div className="absolute left-0 top-6 z-10 min-w-40 rounded-lg border bg-background p-1 shadow-lg">
                {turns.map((t) => (
                  <button
                    key={t}
                    type="button"
                    onClick={() => { setTurnsOpen(false); onJumpTurn(t) }}
                    className="block w-full rounded px-2.5 py-1 text-left hover:bg-muted"
                  >
                    回合 {t}
                  </button>
                ))}
                {/* 锚点只覆盖已加载范围，必须写出来——不假装是全量目录 */}
                {turnsPartial && (
                  <p className="px-2.5 py-1 text-[11px]">仅覆盖已加载范围，更早的需先加载</p>
                )}
              </div>
            )}
          </span>
        )}
        <span className="opacity-50">·</span>
        <span>派发于 {formatRelative(task.created_at)}</span>
        <span className="opacity-50">·</span>
        <UsageChip usage={task.usage} cumulative={task.cumulative} />
        <span className="ml-auto font-mono text-[11px]">handoff-{shortID(task.id)}</span>
      </div>
    </div>
  )
}
