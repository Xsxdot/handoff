// DebugDrawer —— 调试抽屉：原始事件流 + 原始正文（render.log）。
//
// 职责：帧渲染出问题时，用原始数据区分「渲染错了」还是「采集错了」（W4b 保留
// 原始视图的同一条理由，收进抽屉后日常不可见）。
// 边界：
//   - 事件 payload 是截断摘要，全文只在工单面板（EventsPanel 的既有纪律）
//   - 原始正文按需连接：切到该 tab 才挂 RenderPanel，关闭即卸载（不留常驻流）
//   - 列表封顶丢最旧（maxShownEvents，沿 EventsPanel）
import { useState } from 'react'
import type { Event } from '../../api/types'
import type { WsStatus } from '../../api/ws'
import { Badge } from '@/components/ui/badge'
import { formatFull } from '../lib/format'
import { RenderPanel } from './RenderPanel'
import { cn } from '@/lib/utils'

const maxShownEvents = 200

// eventSummary 从事件 payload 提取一行可读简览（自 EventsPanel 平移）。
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

// DebugDrawer 渲染为右侧覆盖层（fixed）；onClose 关闭。
export function DebugDrawer({ events, taskId, status, error, onClose }: {
  taskId: string
  events: Event[]
  status: WsStatus
  error: string | null
  onClose: () => void
}) {
  const [tab, setTab] = useState<'events' | 'render'>('events')
  return (
    <div className="fixed inset-0 z-40 flex justify-end bg-black/30" onClick={onClose}>
      <div className="flex h-full w-[560px] max-w-[92vw] flex-col bg-background shadow-xl" onClick={(e) => e.stopPropagation()}>
        <div className="flex items-center gap-2 border-b px-3 py-2">
          <span className="text-sm font-medium">调试数据</span>
          <span className="text-xs text-muted-foreground">区分「渲染错了」还是「采集错了」</span>
          <button type="button" onClick={onClose} className="ml-auto px-1 text-muted-foreground hover:text-foreground">✕</button>
        </div>
        <div className="flex items-center gap-1.5 border-b px-3 py-1.5">
          <button type="button" onClick={() => setTab('events')} className={cn('rounded-md px-2.5 py-1 text-xs', tab === 'events' ? 'bg-muted font-medium' : 'text-muted-foreground')}>
            原始事件（{events.length} 条）
          </button>
          <button type="button" onClick={() => setTab('render')} className={cn('rounded-md px-2.5 py-1 text-xs', tab === 'render' ? 'bg-muted font-medium' : 'text-muted-foreground')}>
            原始正文
          </button>
          {tab === 'events' && <Badge variant={status === 'open' ? 'default' : status === 'connecting' ? 'secondary' : 'destructive'} className="ml-auto">{status}</Badge>}
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto p-3">
          {tab === 'events' ? (
            <>
              {error && <p role="alert" className="mb-2 break-words text-sm text-destructive">{error}（将自动重连；事件全部落库，重连后可凭游标补拉）</p>}
              {events.length === 0 ? (
                <p className="text-sm text-muted-foreground">还没有事件。</p>
              ) : (
                <ul className="flex flex-col gap-1 font-mono text-xs">
                  {events.slice(-maxShownEvents).map((ev) => (
                    <li key={ev.seq} className="border-b border-border/60 py-1 last:border-b-0">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-muted-foreground">#{ev.seq}</span>
                        <span>{ev.type}</span>
                        <span className="ml-auto text-muted-foreground">{formatFull(ev.created_at)}</span>
                      </div>
                      <p className="break-words text-foreground/80">{eventSummary(ev)}</p>
                    </li>
                  ))}
                </ul>
              )}
            </>
          ) : (
            // 切进来才挂流；RenderPanel 自带卸载即断连（AbortController）
            <RenderPanel taskId={taskId} />
          )}
        </div>
      </div>
    </div>
  )
}
