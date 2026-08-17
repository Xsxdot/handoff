// ConversationStream —— 会话流（TimelinePanel 的继任者，任务 TUI 的主角）。
//
// 职责：
//   - 渲染块序列：回合分隔（send 回合接审核者气泡）、正文（末尾 trailer 拆
//     交付卡）、思维链、工具行、事件行、未知块
//   - 唯一滚动区：跟随滚动（stickBottom）、加载更早 + prepend 补偿
//   - 坏行/帧上限/错误提示以流内元数据行呈现
//
// 边界：
//   - 不取数：frames 流由 TuiTab 持有（页头回合下拉与本组件共享 turns），
//     本组件只吃 blocks 与流状态 props
//   - 不含原始视图切换：原始 render.log 在调试抽屉（spec §2.5）
//   - 回合锚点 id 约定 `turn-${taskId}-${turn}`，TuiHeader 跳转靠它
import { useLayoutEffect, useRef } from 'react'
import { Button } from '@/components/ui/button'
import type { Block } from './frames'
import { extractDelivery } from './delivery'
import { TextBlock } from './TextBlock'
import { ThinkingBlock } from './ThinkingBlock'
import { ToolCard } from './ToolCard'
import { EventChip } from './EventChip'
import { UserInstructionBlock } from './UserInstructionBlock'
import { DeliverySummaryCard } from './DeliverySummaryCard'
import { UnknownBlock } from './UnknownBlock'
import { MetaRow } from './meta'
import { formatFull } from '../lib/format'

// TURN_REASON 沿自 TimelinePanel：dispatch/send 的中文映射，未知原样显示。
const TURN_REASON: Record<string, string> = { dispatch: '派发', send: '续发指令' }

// stickThreshold 是「算作在底部」的像素阈值（与原 TimelinePanel 一致）。
const stickThreshold = 40

export interface ConversationStreamProps {
  taskId: string
  taskState: string
  blocks: Block[]
  badLines: number
  startOffset: number
  atCap: boolean
  error: string | null
  loadingEarlier: boolean
  onLoadEarlier: () => void
  onRetry: () => void
}

// ConversationStream 渲染一个任务的会话流。滚动补偿与跟随逻辑整体平移自
// TimelinePanel（useLayoutEffect + prependRef 的实现原样保留，注释见彼处 git 史）。
export function ConversationStream({
  taskId, taskState, blocks, badLines, startOffset, atCap, error,
  loadingEarlier, onLoadEarlier, onRetry,
}: ConversationStreamProps) {
  const scrollRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)
  const prependRef = useRef<number | null>(null)

  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el) return
    if (prependRef.current !== null) {
      el.scrollTop += el.scrollHeight - prependRef.current
      prependRef.current = null
      return
    }
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < stickThreshold
    if (stickBottom.current || nearBottom) {
      el.scrollTop = el.scrollHeight
      stickBottom.current = true
    }
  }, [blocks])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < stickThreshold
  }

  const handleLoadEarlier = () => {
    prependRef.current = scrollRef.current?.scrollHeight ?? 0
    onLoadEarlier()
  }

  return (
    <div ref={scrollRef} onScroll={onScroll} className="h-full min-h-0 overflow-y-auto">
      <div className="mx-auto max-w-[760px] px-6 py-5">
        {startOffset > 0 && !atCap && (
          <div className="mb-3 flex justify-center">
            <Button variant="ghost" size="sm" disabled={loadingEarlier} onClick={handleLoadEarlier}>
              {loadingEarlier ? '加载中…' : '↑ 加载更早'}
            </Button>
          </div>
        )}

        {badLines > 0 && (
          <MetaRow glyph="⚠" tone="warn">
            {badLines} 行无法解析，已跳过（其余帧不受影响；帧文件可能被截断或采集侧有 bug）
          </MetaRow>
        )}
        {atCap && (
          <MetaRow glyph="◇">
            已加载帧数到上限，不再往前加载——更早的内容请用 <span className="font-mono">handoff frames</span> 回看
          </MetaRow>
        )}
        {error && (
          <p role="alert" className="my-2 flex flex-wrap items-center gap-2 break-words text-sm text-destructive">
            {error}
            <Button variant="outline" size="sm" onClick={onRetry}>重试</Button>
          </p>
        )}

        {blocks.length === 0 && error === null ? (
          <p className="text-sm text-muted-foreground">等待模型输出…（frames.jsonl 尚为空属正常）</p>
        ) : (
          blocks.map((b) => {
            switch (b.kind) {
              case 'turn':
                return (
                  <div key={b.key}>
                    <div
                      id={`turn-${taskId}-${b.turn}`}
                      className="mb-2 mt-5 flex items-center gap-2 text-xs text-muted-foreground first:mt-0"
                    >
                      <span className="h-px flex-1 bg-border" />
                      <span>
                        <b className="font-semibold text-foreground">回合 {b.turn}</b>
                        {' · '}{TURN_REASON[b.reason] ?? b.reason}{' · '}{formatFull(b.ts)}
                      </span>
                      <span className="h-px flex-1 bg-border" />
                    </div>
                    {/* send 回合带指令原文时渲染审核者气泡；旧帧缺席则只有分隔线 */}
                    {b.instructions !== '' && <UserInstructionBlock text={b.instructions} ts={b.ts} />}
                  </div>
                )
              case 'text': {
                // 末尾报工 trailer 拆成交付卡（best-effort，见 delivery.ts）
                const d = extractDelivery(b.text)
                if (d) {
                  return (
                    <div key={b.key} className="my-2">
                      {d.body !== '' && <TextBlock text={d.body} />}
                      <DeliverySummaryCard delivery={d.delivery} />
                    </div>
                  )
                }
                return <div key={b.key} className="my-2"><TextBlock text={b.text} /></div>
              }
              case 'thinking':
                return <ThinkingBlock key={b.key} text={b.text} />
              case 'tool':
                return <ToolCard key={b.key} block={b} taskState={taskState} />
              case 'event':
                return <EventChip key={b.key} event={b.event} ts={b.ts} />
              case 'unknown':
                return <UnknownBlock key={b.key} type={b.type} raw={b.raw} />
            }
          })
        )}
      </div>
    </div>
  )
}
