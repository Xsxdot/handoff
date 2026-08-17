// ConversationStream —— 会话流（TimelinePanel 的继任者，任务 TUI 的主角）。
//
// 职责：
//   - 渲染块序列：回合分隔（send 回合接审核者气泡）、正文（末尾 trailer 拆
//     交付卡）、思维链、工具行、事件行、未知块
//   - 唯一滚动区：跟随滚动（stickBottom）、近顶自动加载更早 + prepend 补偿、回合跳转
//   - 坏行/帧上限/错误提示以流内元数据行呈现
//   - 连续工具块折叠与运行中状态提示
//
// 边界：
//   - 不取数：frames 流由 TuiTab 持有（页头回合下拉与本组件共享 turns），
//     本组件只吃 blocks 与流状态 props
//   - 不含原始视图切换：原始 render.log 在调试抽屉（spec §2.5）
//   - 回合锚点 id 约定 `turn-${taskId}-${turn}`，TuiHeader 跳转靠它
import { forwardRef, useCallback, useEffect, useImperativeHandle, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Button } from '@/components/ui/button'
import { cn } from '@/lib/utils'
import type { Block } from './frames'
import { extractDelivery } from './delivery'
import { groupBlocks, type ToolGroupBlock } from './streamGroups'
import { TextBlock } from './TextBlock'
import { ThinkingBlock } from './ThinkingBlock'
import { ToolCard } from './ToolCard'
import { EventChip } from './EventChip'
import { UserInstructionBlock } from './UserInstructionBlock'
import { useTaskPlan } from './useTaskPlan'
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
  // taskCreatedAt 是派发时刻，用作会话流顶部那条派发指令气泡的时间戳
  taskCreatedAt: string
  blocks: Block[]
  badLines: number
  startOffset: number
  atCap: boolean
  error: string | null
  loadingEarlier: boolean
  onLoadEarlier: () => void
  onRetry: () => void
  active: boolean
}

export interface ConversationStreamHandle {
  jumpToTurn(turn: number): void
}

// ToolGroupRow 渲染一组折叠的连续工具行：一行摘要，点开平铺原行。
function ToolGroupRow({ group, taskState }: { group: ToolGroupBlock; taskState: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="my-1 text-xs">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 py-0.5 text-muted-foreground hover:text-foreground"
      >
        <span className={cn('w-3.5 shrink-0 text-center transition-transform', open && 'rotate-90')}>▸</span>
        执行了 {group.tools.length} 步操作
        {group.failed > 0 && <span className="text-destructive">（{group.failed} 失败）</span>}
        {group.pending > 0 && <span className="text-amber-600 dark:text-amber-500">（{group.pending} 未回音）</span>}
      </button>
      {open && (
        <div className="ml-[7px] border-l-2 border-border pl-3">
          {group.tools.map((t) => <ToolCard key={t.key} block={t} taskState={taskState} />)}
        </div>
      )}
    </div>
  )
}

// ConversationStream 渲染一个任务的会话流。滚动补偿与跟随逻辑整体平移自
// TimelinePanel（useLayoutEffect + prependRef 的实现原样保留，注释见彼处 git 史）。
export const ConversationStream = forwardRef<ConversationStreamHandle, ConversationStreamProps>(function ConversationStream({
  taskId, taskState, taskCreatedAt, blocks, badLines, startOffset, atCap, error,
  loadingEarlier, onLoadEarlier, onRetry, active,
}, ref) {
  // 派发指令：任务的第一条「审核者消息」。turn_start 帧的 instructions 在
  // dispatch 那一回合恒为空串（见 internal/executor/turn/frames.go），所以它
  // 不在 blocks 里，只能单独取
  const plan = useTaskPlan(taskId)
  const scrollRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)
  const prependRef = useRef<number | null>(null)
  const pendingTurnRef = useRef<number | null>(null)
  const lastFrameAtRef = useRef(Date.now())
  const [now, setNow] = useState(Date.now())
  const items = useMemo(() => groupBlocks(blocks), [blocks])

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

  const handleLoadEarlier = useCallback(() => {
    if (startOffset <= 0 || atCap || loadingEarlier) return
    prependRef.current = scrollRef.current?.scrollHeight ?? 0
    onLoadEarlier()
  }, [atCap, loadingEarlier, onLoadEarlier, startOffset])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < stickThreshold
    if (el.scrollTop < 200 && startOffset > 0 && !atCap && !loadingEarlier) {
      handleLoadEarlier()
    }
  }

  useImperativeHandle(ref, () => ({
    jumpToTurn(turn: number) {
      const anchor = document.getElementById(`turn-${taskId}-${turn}`)
      if (anchor) {
        // 先停用跟底再滚动，避免新帧在 scrollIntoView 后把用户重新拽到底部。
        stickBottom.current = false
        anchor.scrollIntoView?.({ block: 'start' })
        pendingTurnRef.current = null
        return
      }
      pendingTurnRef.current = turn
      // 锚点尚未加载时先回翻一页；后续由 blocks/加载状态 effect 接力检查。
      handleLoadEarlier()
    },
  }), [handleLoadEarlier, taskId])

  useEffect(() => {
    const pendingTurn = pendingTurnRef.current
    if (pendingTurn === null) return
    const anchor = document.getElementById(`turn-${taskId}-${pendingTurn}`)
    if (anchor) {
      pendingTurnRef.current = null
      // 与 jumpToTurn 一致，先解除跟底再执行跳转，避免竞态把滚动位置拉回末尾。
      stickBottom.current = false
      anchor.scrollIntoView?.({ block: 'start' })
      return
    }
    if (startOffset <= 0 || atCap) {
      // 到文件头或帧上限仍没有锚点时放弃：下拉只承诺已加载范围，不能假装跳到了目标。
      pendingTurnRef.current = null
      return
    }
    if (!loadingEarlier) handleLoadEarlier()
  }, [atCap, blocks, handleLoadEarlier, loadingEarlier, startOffset, taskId])

  useEffect(() => {
    lastFrameAtRef.current = Date.now()
  }, [blocks])

  useEffect(() => {
    if (taskState !== 'running') return
    setNow(Date.now())
    const timer = setInterval(() => setNow(Date.now()), 1000)
    return () => clearInterval(timer)
  }, [taskState])

  const staleSeconds = Math.max(0, Math.floor((now - lastFrameAtRef.current) / 1000))

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

        {/* 派发指令气泡只在**流的开头**出现：startOffset>0 意味着当前只加载了
            帧文件的尾部，此刻把派发指令画在顶上等于谎报时间顺序——它属于最早
            那一刻，不属于「你现在看到的这一段」。回翻到头后它自然出现 */}
        {plan !== null && startOffset === 0 && plan.content !== '' && (
          <UserInstructionBlock
            text={plan.content}
            ts={taskCreatedAt}
            label="派发指令"
            extra={plan.truncated ? `${plan.name}（已截断）` : plan.name}
          />
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

        {items.length === 0 && error === null ? (
          <p className="text-sm text-muted-foreground">等待模型输出…（frames.jsonl 尚为空属正常）</p>
        ) : (
          items.map((b) => {
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
              case 'toolGroup':
                // 组内有未回音工具且任务在跑：正在发生的事不能折起来，整组平铺。
                if (b.pending > 0 && taskState === 'running') {
                  return b.tools.map((t) => <ToolCard key={t.key} block={t} taskState={taskState} />)
                }
                return <ToolGroupRow key={b.key} group={b} taskState={taskState} />
              case 'event':
                return <EventChip key={b.key} event={b.event} ts={b.ts} />
              case 'unknown':
                return <UnknownBlock key={b.key} type={b.type} raw={b.raw} />
            }
          })
        )}

        {taskState === 'running' && active && (
          <div className="my-2 flex items-center gap-2 text-xs text-muted-foreground">
            <span className="size-[7px] shrink-0 animate-pulse rounded-full bg-green-600" />
            模型工作中…
            {staleSeconds >= 15 && <span>（已 {staleSeconds}s 没有新输出）</span>}
          </div>
        )}
        {taskState === 'waiting_answer' && (
          <MetaRow glyph="⚠" tone="warn">等待工单裁决——入口在左栏底部的工单面板</MetaRow>
        )}
      </div>
    </div>
  )
})
