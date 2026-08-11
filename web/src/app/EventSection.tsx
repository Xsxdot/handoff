// EventSection —— 对第一个任务开 /ws/events 订阅并展示收到的事件。
//
// 职责：
//   - 任务列表为空时明确显示「无任务，跳过 WS 验证」——绝不假装成功
//   - 非空时取列表第一个任务订阅事件流，把连接状态与收到的事件实时展示
//   - 断开/出错可观察：显示原因并自动指数退避重连（事件全部落库，重连无损）
//
// 边界：
//   - 只做验证展示；事件归并/去重/游标持久化是任务现场的责任，不在这里做
//   - 只订阅第一个任务：这是「联通性验证」而非任务总览，够证明即可
import { useEffect, useState } from 'react'
import { Radio } from 'lucide-react'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Badge } from '@/components/ui/badge'
import { connectEvents, type WsStatus } from '../api/ws'
import type { Event, Task } from '../api/types'

// maxShownEvents 是页面保留展示的事件条数上限；超出丢最旧的。
// 为什么封顶：验证页不承担回放职责，连住 1 小时的事件全塞进来只添乱。
const maxShownEvents = 20

const STATUS_BADGE: Record<WsStatus, 'default' | 'secondary' | 'destructive'> = {
  connecting: 'secondary',
  open: 'default',
  closed: 'destructive',
}

export function EventSection({ tasks }: { tasks: Task[] }) {
  if (tasks.length === 0) {
    return (
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="flex items-center gap-2 text-base">
            <Radio className="size-4" />
            事件流验证
          </CardTitle>
        </CardHeader>
        <CardContent>
          <p className="text-sm text-muted-foreground">无任务，跳过 WS 验证（没有可订阅的事件源，不假装成功）。</p>
        </CardContent>
      </Card>
    )
  }
  return <EventStream task={tasks[0]} />
}

// EventStream 对指定任务维持一条事件订阅。
//
// 注意 StrictMode 下 mount 会跑两次：connectEvents 返回的 close 在清理阶段
// 调用，第二次 mount 重新订阅，不会重复累积连接。
function EventStream({ task }: { task: Task }) {
  const [status, setStatus] = useState<WsStatus>('connecting')
  const [error, setError] = useState<string | null>(null)
  const [events, setEvents] = useState<Event[]>([])

  useEffect(() => {
    setStatus('connecting')
    setError(null)
    setEvents([])
    const conn = connectEvents({
      taskId: task.id,
      fromSeq: 0,
      onEvent: (ev) =>
        setEvents((prev) => {
          const next = [...prev, ev]
          return next.length > maxShownEvents ? next.slice(next.length - maxShownEvents) : next
        }),
      onStatus: setStatus,
      onError: (message) => setError(message),
    })
    return () => conn.close()
  }, [task.id])

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between gap-2 pb-2">
        <CardTitle className="flex items-center gap-2 text-base">
          <Radio className="size-4" />
          事件流验证
        </CardTitle>
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs text-muted-foreground">{task.id.slice(0, 8)}</span>
          <Badge variant={STATUS_BADGE[status]}>{status}</Badge>
        </div>
      </CardHeader>
      <CardContent className="flex flex-col gap-3">
        <p className="text-xs text-muted-foreground">
          订阅 /ws/events?task={task.id.slice(0, 8)}…&amp;from_seq=0（已收到 {events.length} 条）
        </p>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            连接异常：{error}（将自动重连；事件全部落库，重连后可凭游标补拉）
          </p>
        )}
        {events.length === 0 ? (
          <p className="text-sm text-muted-foreground">连接已建立，等待事件…（任务有新的 progress 等事件时会出现在这里）</p>
        ) : (
          <ul className="flex flex-col gap-1 rounded-md border bg-background p-3 font-mono text-xs">
            {events.map((ev) => (
              <li key={`${ev.seq}`} className="flex flex-wrap items-center gap-2 break-all">
                <span className="text-muted-foreground">#{ev.seq}</span>
                <span className="text-foreground">{ev.type}</span>
                <span className="text-muted-foreground">{ev.created_at}</span>
              </li>
            ))}
          </ul>
        )}
      </CardContent>
    </Card>
  )
}
