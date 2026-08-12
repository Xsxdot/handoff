// EventMark —— 时间线上的事件打断标记。
//
// 职责：说明因果——模型正说着话，在这里被一张工单打断了，批复后才接上
// 边界：
//   - **不可操作**。同一张工单在三个地方出现是有意的：时间线管因果、
//     EventsPanel 管实时、TicketsPanel 管裁决。审批不可逆且要当真，
//     给它开第二个入口、还开在一条摘要旁边，风险和收益不成比例
//   - 不显示 payload 全文。全文只在工单区（EventsPanel 头注释的既有纪律）
import { CircleDot } from 'lucide-react'
import { formatFull } from '../lib/format'

// EVENT_LABEL 是已知事件类型的中文标签。未知类型原样显示类型名，不吞掉。
const EVENT_LABEL: Record<string, string> = {
  permission_request: '权限工单：等待审核者裁决',
  question: '提问工单：等待审核者回答',
  completed: '一轮结束，进入待审',
  failed: '任务失败',
  delivery_failed: '裁决已落库但没送到 executor',
  stalled: '看门狗：长时间无产出',
}

// EventMark 渲染一行事件标记。
//
// 参数：
//   - event: 事件类型名（W4a 刻意冗余在帧里，前端不查 events 表就能画）
//   - ts: 帧时间戳（RFC3339）
export function EventMark({ event, ts }: { event: string; ts: string }) {
  return (
    <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 rounded-md border border-amber-500/40 bg-amber-500/5 px-2.5 py-1.5 text-xs">
      <CircleDot className="size-3.5 shrink-0 text-amber-600 dark:text-amber-500" />
      <span>{EVENT_LABEL[event] ?? event}</span>
      <span className="text-[11px] text-muted-foreground">
        {formatFull(ts)} · 裁决入口在右侧工单区
      </span>
    </div>
  )
}
