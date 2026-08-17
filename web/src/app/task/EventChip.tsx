// EventChip —— 会话流的生命周期事件行（EventMark 的继任者）。
//
// 职责：一行人话说明因果（派发/工单/回合结束/失败…），文案与色调来自 eventPhrase。
// 边界：
//   - **不可操作**（沿 EventMark 纪律）：裁决入口只在全局工单弹层，这里只指路
//   - 不显示 payload：权限/提问全文只在工单面板
import { formatFull } from '../lib/format'
import { eventPhrase } from './eventPhrase'
import { MetaRow } from './meta'

// EventChip 渲染一行事件。event 是帧的事件类型名，ts 是帧时间戳（RFC3339）。
// eventPhrase 返回 null 的纯噪声事件不渲染；原始事件由调试抽屉保留。
export function EventChip({ event, ts }: { event: string; ts: string }) {
  const p = eventPhrase(event)
  if (p === null) return null
  return (
    <MetaRow glyph={p.tone === 'warn' ? '⚠' : '◇'} tone={p.tone}>
      <span className="min-w-0 flex-1 break-words">
        {p.text}
        <span className="ml-2 text-[11px] opacity-70">{formatFull(ts)}</span>
      </span>
    </MetaRow>
  )
}
