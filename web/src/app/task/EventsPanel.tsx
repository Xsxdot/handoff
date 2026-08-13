// EventsPanel —— 任务的事件流展示。
//
// 数据来源：进页面时 GET /api/tasks/{id} 拿 recent_events 打底，之后 /ws/events
// 追加实时增量（WS 层自行按 seq 游标续拉，重连不重放已见事件）。
//
// 展示纪律：
//   - 事件 payload 里的 permission 是**截断摘要**，这里只做简览；权限/提问全文
//     只在工单面板（读工单 request）——两个区域不混用
//   - 连接状态可观察：open / connecting / closed 都有明确展示，不静默
//   - 列表封顶（丢最旧）：本区是实时视图，不承担回放职责
import { Radio } from 'lucide-react'
import type { Event } from '../../api/types'
import { Badge } from '@/components/ui/badge'
import type { WsStatus } from '../../api/ws'
import { formatFull } from '../lib/format'

// maxShownEvents 是保留展示的事件条数上限；超出丢最旧的。
const maxShownEvents = 200

const STATUS_BADGE: Record<WsStatus, 'default' | 'secondary' | 'destructive'> = {
  connecting: 'secondary',
  open: 'default',
  closed: 'destructive',
}

// eventSummary 从事件 payload 提取一行可读的简览。
//
// 只读已知形状的字段，其余回退 JSON 原文；绝不因为解析失败而吞掉整条事件。
function eventSummary(ev: Event): string {
  const p = ev.payload
  if (p !== null && typeof p === 'object') {
    const obj = p as Record<string, unknown>
    for (const key of ['question', 'permission', 'text', 'reason', 'hint', 'fail_reason', 'note']) {
      const v = obj[key]
      if (typeof v === 'string' && v !== '') return v
    }
    const s = JSON.stringify(obj)
    return s.length > 120 ? `${s.slice(0, 120)}…` : s
  }
  return String(p ?? '')
}

export function EventsPanel({ events, status, error }: { events: Event[]; status: WsStatus; error: string | null }) {
  return (
    <section className="flex flex-col gap-2 rounded-lg border bg-background p-4">
      <header className="flex items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-sm font-medium">
          <Radio className="size-4" />
          事件流
        </h2>
        <div className="flex items-center gap-2">
          <span className="text-xs text-muted-foreground">{events.length} 条</span>
          <Badge variant={STATUS_BADGE[status]}>{status}</Badge>
        </div>
      </header>
      {error && (
        <p role="alert" className="break-words text-sm text-destructive">
          {error}（将自动重连；事件全部落库，重连后可凭游标补拉）
        </p>
      )}
      {events.length === 0 ? (
        <p className="text-sm text-muted-foreground">还没有事件。</p>
      ) : (
        <ul className="flex max-h-80 flex-col gap-1 overflow-y-auto rounded-md border bg-muted/30 p-2 font-mono text-xs">
          {events.slice(-maxShownEvents).map((ev) => (
            <li key={`${ev.seq}`} className="flex flex-col gap-0.5 border-b border-border/60 py-1 last:border-b-0">
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-muted-foreground">#{ev.seq}</span>
                <span className="text-foreground">{ev.type}</span>
                <span className="ml-auto text-muted-foreground">{formatFull(ev.created_at)}</span>
              </div>
              <p className="break-words text-foreground/80">{eventSummary(ev)}</p>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}
