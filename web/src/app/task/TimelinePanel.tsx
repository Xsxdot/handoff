// TimelinePanel —— 结构化回合时间线（任务详情页左列的主视图）。
//
// 职责：
//   - 把 useFramesStream 的帧交给 frames.ts 聚合成块，逐块渲染
//   - 回合分隔与顶部锚点条、加载更早、跟随徽章、坏行与上限提示
//   - 一个开关切回原始 render.log 流（RenderPanel）
//
// 边界：
//   - 不解析原始文本（那是 frames.ts），不发请求（那是 useFramesStream）
//   - 不提供任何审批入口。同一张工单在三处出现是有意的：时间线管因果、
//     EventsPanel 管实时、TicketsPanel 管裁决——审批不可逆，只留一个入口
//
// 为什么保留原始视图：这批帧是四个 adapter 各自分流出来的，质量并不齐平
// （grok 的工具信息只是人类摘要、codex 没有真实抓包因而没有黄金基线）。
// 撞上「某家 adapter 的帧不完整」时，能一键退回原始文本是区分「渲染错了」
// 还是「采集错了」的关键证据。等四家帧质量都被真机验证过，再谈取代。
//
// 切换会卸载对侧的流（原始视图重新从 tail=65536 开始）。这是刻意的：两个视图
// 各自维护加载位置，而让两条常驻连接同时开着换取「切回去还在原位」是更坏的交易。
import { useLayoutEffect, useMemo, useRef, useState } from 'react'
import { Eye, List } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { buildBlocks, turnsOf } from './frames'
import { useFramesStream } from './useFramesStream'
import { RenderPanel } from './RenderPanel'
import { TextBlock } from './TextBlock'
import { ThinkingBlock } from './ThinkingBlock'
import { ToolCard } from './ToolCard'
import { EventMark } from './EventMark'
import { UnknownBlock } from './UnknownBlock'

// TURN_REASON 把 turn_start 的 reason 映射成中文。
// 只有两个取值（W4a §3.2：dispatch = Adapter.Start，send = Adapter.Send），
// 未知取值原样显示，不吞掉。
const TURN_REASON: Record<string, string> = { dispatch: '派发', send: '续发指令' }

// stickThreshold 是「算作在底部」的像素阈值，与 RenderPanel 保持一致：
// 距底这么近才自动跟随，用户往上翻则停止跟随，不抢滚轮。
const stickThreshold = 40

// TimelinePanel 渲染一个任务的结构化回合时间线。
//
// 参数：
//   - taskId: 任务完整 UUID
//   - taskState: 任务当前状态，用于判定未配对工具卡是「进行中」还是「未返回」
export function TimelinePanel({ taskId, taskState }: { taskId: string; taskState: string }) {
  const [raw, setRaw] = useState(false)
  const { frames, badLines, startOffset, error, active, atCap, loadingEarlier, loadEarlier, retry } =
    useFramesStream(raw ? undefined : taskId)

  const blocks = useMemo(() => buildBlocks(frames), [frames])
  const turns = useMemo(() => turnsOf(frames), [frames])

  const scrollRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)
  // prependRef 记录 prepend 之前的 scrollHeight；prepend 后按差值补偿滚动位置，
  // 否则每次「加载更早」都会把用户弹到别处。
  const prependRef = useRef<number | null>(null)

  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el) return
    if (prependRef.current !== null) {
      // 本次变化来自 prepend：补偿高度差，视线停在原处，且不触发跟随
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

  const onLoadEarlier = () => {
    prependRef.current = scrollRef.current?.scrollHeight ?? 0
    loadEarlier()
  }

  const gotoTurn = (t: number) => {
    document.getElementById(`turn-${taskId}-${t}`)?.scrollIntoView({ block: 'start' })
  }

  return (
    <section className="flex flex-col gap-2 rounded-lg border bg-background p-4">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-sm font-medium">
          {raw ? <Eye className="size-4" /> : <List className="size-4" />}
          {raw ? '实况正文（原始）' : '回合时间线'}
        </h2>
        <div className="flex items-center gap-2">
          {!raw && <Badge variant={active ? 'default' : 'secondary'}>{active ? '跟随中' : '已结束'}</Badge>}
          <Button variant="outline" size="sm" onClick={() => setRaw((v) => !v)}>
            {raw ? '回合时间线' : '原始正文'}
          </Button>
        </div>
      </header>

      {raw ? (
        // 原始视图：RenderPanel 零改动地整块放进来，自带它自己的头与流
        <RenderPanel taskId={taskId} />
      ) : (
        <>
          {turns.length > 0 && (
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <span className="text-muted-foreground">回合</span>
              {turns.map((t) => (
                <button
                  key={t}
                  type="button"
                  onClick={() => gotoTurn(t)}
                  className="rounded border px-2 py-0.5 hover:border-foreground"
                >
                  {t}
                </button>
              ))}
              {/* 锚点只覆盖已加载范围，必须写出来——不假装是全量目录 */}
              <span className="ml-auto text-[11px] text-muted-foreground">
                {startOffset > 0 ? '仅覆盖已加载范围，更早的需先加载' : '已覆盖全部回合'}
              </span>
            </div>
          )}

          {badLines > 0 && (
            <p className="rounded border border-amber-500/40 bg-amber-500/5 px-2.5 py-1.5 text-xs text-amber-600 dark:text-amber-500">
              ⚠ {badLines} 行无法解析，已跳过（其余帧不受影响；帧文件可能被截断或采集侧有 bug）
            </p>
          )}

          {atCap && (
            <p className="rounded border px-2.5 py-1.5 text-xs text-muted-foreground">
              已加载帧数到上限，不再往前加载——更早的内容请用 <span className="font-mono">handoff frames</span> 回看
            </p>
          )}

          {error && (
            <p role="alert" className="flex flex-wrap items-center gap-2 break-words text-sm text-destructive">
              {error}
              <Button variant="outline" size="sm" onClick={retry}>重试</Button>
            </p>
          )}

          <div ref={scrollRef} onScroll={onScroll} className="h-96 overflow-y-auto rounded-md bg-muted/30 p-3">
            {startOffset > 0 && !atCap && (
              <div className="mb-2 flex justify-center">
                <Button variant="ghost" size="sm" disabled={loadingEarlier} onClick={onLoadEarlier}>
                  {loadingEarlier ? '加载中…' : '↑ 加载更早'}
                </Button>
              </div>
            )}
            {blocks.length === 0 && error === null ? (
              <p className="text-sm text-muted-foreground">等待模型输出…（frames.jsonl 尚为空属正常）</p>
            ) : (
              <div className="flex flex-col gap-1.5">
                {blocks.map((b) => {
                  switch (b.kind) {
                    case 'turn':
                      return (
                        <div
                          key={b.key}
                          id={`turn-${taskId}-${b.turn}`}
                          className="mt-2 flex items-center gap-2 text-[11px] text-muted-foreground first:mt-0"
                        >
                          <span className="h-px flex-1 bg-border" />
                          <span>
                            <b className="font-semibold text-foreground">回合 {b.turn}</b>
                            {' · '}
                            {TURN_REASON[b.reason] ?? b.reason}
                          </span>
                          <span className="h-px flex-1 bg-border" />
                        </div>
                      )
                    case 'text':
                      return <TextBlock key={b.key} text={b.text} />
                    case 'thinking':
                      return <ThinkingBlock key={b.key} text={b.text} />
                    case 'tool':
                      return <ToolCard key={b.key} block={b} taskState={taskState} />
                    case 'event':
                      return <EventMark key={b.key} event={b.event} ts={b.ts} />
                    case 'unknown':
                      return <UnknownBlock key={b.key} type={b.type} raw={b.raw} />
                  }
                })}
              </div>
            )}
          </div>
        </>
      )}
    </section>
  )
}
