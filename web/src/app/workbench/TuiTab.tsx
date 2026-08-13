// TuiTab —— 桌面端 TUI（spec §2.3）。
//
// 职责：把一个任务会话按原型的单栏纵向流渲染出来——会话正文在上，指令输入框
// 固定在底部；任务进 waiting_review 后，审阅取证接在正文末尾。
//
// 边界：
//   - **不含 TicketsPanel**。工单裁决收敛到全局工单弹层（spec §5.2）：一张工单
//     可能属于任何一个任务，藏在某个 tab 里就等于要求人先猜对是哪个任务
//   - 不含返回看板链接与页头：那是面包屑与左栏的事
//   - 不自己取数：全部经 useTaskSession
//
// 关于「TUI 的终局是一个 agent」（spec §2.3 末段）：本组件对外只依赖一个
// taskId，但内部布局不假设「必须有 task」——将来以 home 为基准开一个不绑任务的
// agent 会话时，替换的是数据源，不是这套布局。
import { Badge } from '@/components/ui/badge'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { useTaskSession } from '../task/useTaskSession'
import { TaskHeader } from '../task/TaskHeader'
import { TimelinePanel } from '../task/TimelinePanel'
import { EventsPanel } from '../task/EventsPanel'
import { ReviewPanel } from '../task/ReviewPanel'
import { AdvanceActions } from '../task/AdvanceActions'

export function TuiTab({ taskId }: { taskId: string }) {
  const s = useTaskSession(taskId)

  if (s.detail === null) {
    if (s.loadError) return <LoadFailed message={s.loadError} onRetry={s.refresh} />
    if (s.sessionExpired) return <SessionExpiredBanner />
    return <p className="p-4 text-sm text-muted-foreground">正在加载任务…</p>
  }

  // waiting_review 才挂 ReviewPanel：它是「这一轮干完了，你来验」的自然延续，
  // 不是常驻侧栏。任务还在跑时挂着它，等于邀请人去 diff 一个半成品
  const inReview = s.detail.task.state === 'waiting_review'

  return (
    <div className="flex h-full min-h-0 flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs">
        <TaskHeader task={s.detail.task} compact />
        <div className="ml-auto flex items-center gap-2">
          {s.disconnected && <Badge variant="destructive">已断开</Badge>}
          {s.wsStatus === 'open' && <Badge variant="outline">实时</Badge>}
        </div>
      </div>

      <div className="min-h-0 flex-1 space-y-4 overflow-auto p-3">
        {s.sessionExpired && <SessionExpiredBanner />}
        {s.disconnected && !s.sessionExpired && <DisconnectedBanner message={s.disconnectReason} />}
        <TimelinePanel taskId={taskId} taskState={s.detail.task.state} />
        <EventsPanel events={s.events} status={s.wsStatus} error={s.wsError} />
        {inReview && <ReviewPanel taskId={taskId} />}
      </div>

      {/* 指令输入框固定在底部——原型的形态判据之一 */}
      <div className="border-t p-3">
        <AdvanceActions task={s.detail.task} disabled={s.disconnected} onChanged={s.refresh} />
      </div>
    </div>
  )
}
