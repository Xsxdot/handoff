// TuiTab —— 桌面端 TUI（spec 2026-08-17 对话式重构）。
//
// 职责：总装页头（TuiHeader）/ 主区（ConversationStream + ReviewSidePanel）/
// Composer / DebugDrawer；持有 frames 流（页头回合下拉与会话流共享）与
// 审阅栏开合状态。
//
// 状态联动（spec §5）：
//   - waiting_review 进入时审阅栏自动滑出；人手动收起后本 tab 内记住不再自动开
//   - running / waiting_answer / 终态：审阅栏隐藏
// 边界（沿旧 TuiTab）：不含 TicketsPanel（全局工单弹层）；不含面包屑；
// 会话数据全部经 useTaskSession / useFramesStream。
import { useEffect, useMemo, useRef, useState } from 'react'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { useTaskSession } from '../task/useTaskSession'
import { useFramesStream } from '../task/useFramesStream'
import { buildBlocks, turnsOf } from '../task/frames'
import { TuiHeader } from '../task/TuiHeader'
import { ConversationStream, type ConversationStreamHandle } from '../task/ConversationStream'
import { ReviewSidePanel } from '../task/ReviewSidePanel'
import { Composer } from '../task/Composer'
import { DebugDrawer } from '../task/DebugDrawer'

// TuiTab 渲染一个任务的对话式 TUI；对外签名保持不变，Shell 无需知道内部重排。
export function TuiTab({ taskId }: { taskId: string }) {
  const s = useTaskSession(taskId)
  const { frames, badLines, startOffset, error, active, atCap, loadingEarlier, loadEarlier, retry } =
    useFramesStream(taskId)
  const blocks = useMemo(() => buildBlocks(frames), [frames])
  const turns = useMemo(() => turnsOf(frames), [frames])
  const streamRef = useRef<ConversationStreamHandle>(null)

  const [reviewOpen, setReviewOpen] = useState(false)
  const [debugOpen, setDebugOpen] = useState(false)
  // reviewDismissed 记「人手动收起过」：waiting_review 里自动开一次，人收起后
  // 不再抢开；离开 review 态重置，下次进入再自动开
  const reviewDismissed = useRef(false)

  const state = s.detail?.task.state
  useEffect(() => {
    if (state === 'waiting_review') {
      if (!reviewDismissed.current) setReviewOpen(true)
    } else {
      setReviewOpen(false)
      reviewDismissed.current = false
    }
  }, [state])

  if (s.detail === null) {
    if (s.loadError) return <LoadFailed message={s.loadError} onRetry={s.refresh} />
    if (s.sessionExpired) return <SessionExpiredBanner />
    return <p className="p-4 text-sm text-muted-foreground">正在加载任务…</p>
  }

  const inReview = s.detail.task.state === 'waiting_review'
  const closeReview = () => { setReviewOpen(false); reviewDismissed.current = true }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <TuiHeader
        task={s.detail.task}
        turns={turns}
        turnsPartial={startOffset > 0}
        onJumpTurn={(t) => streamRef.current?.jumpToTurn(t)}
        reviewAvailable={inReview}
        reviewOpen={reviewOpen}
        onToggleReview={() => (reviewOpen ? closeReview() : setReviewOpen(true))}
        onOpenDebug={() => setDebugOpen(true)}
        wsStatus={s.wsStatus}
        disconnected={s.disconnected}
      />

      {s.sessionExpired && <SessionExpiredBanner />}
      {s.disconnected && !s.sessionExpired && <DisconnectedBanner message={s.disconnectReason} />}

      <div className="flex min-h-0 flex-1">
        <div className="min-w-0 flex-1">
          <ConversationStream
            ref={streamRef}
            taskId={taskId}
            taskState={s.detail.task.state}
            blocks={blocks}
            badLines={badLines}
            startOffset={startOffset}
            atCap={atCap}
            error={error}
            loadingEarlier={loadingEarlier}
            onLoadEarlier={loadEarlier}
            onRetry={retry}
            active={active}
          />
        </div>
        {inReview && reviewOpen && <ReviewSidePanel taskId={taskId} onClose={closeReview} />}
      </div>

      <Composer task={s.detail.task} disabled={s.disconnected} onChanged={s.refresh} />

      {debugOpen && (
        <DebugDrawer taskId={taskId} events={s.events} status={s.wsStatus} error={s.wsError} onClose={() => setDebugOpen(false)} />
      )}
    </div>
  )
}
