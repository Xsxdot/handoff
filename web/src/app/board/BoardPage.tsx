// BoardPage —— 任务看板（顶层页面）。
//
// 数据源：`GET /api/tasks`，2.5 秒轮询一次。为什么轮询而不是 WS：`/ws/events`
// 是**按单任务**订阅的，看板照搬就得开 N 条连接；看板是低频视图，轮询足够。
// 整机级订阅是更好的终局，但那是后端改动（backlog 记给 W3），本轮不做。
//
// 实时性纪律：
//   - 页面不可见时（document.hidden）停掉轮询，回来再续——不烧 agentd 与带宽
//   - 断线时保留最后拿到的列表继续显示，标注「已断开」；会话失效（401）时停轮询
//     落到终止态，不做无脑重试
//
// 列与状态机的映射是硬契约（src/app/board/columns.ts，vitest 钉死）：pending →
// 等待执行；running / waiting_answer → 进行中（waiting_answer 加「等你答复」）；
// waiting_review → Review；completed / failed → 完成（failed 视觉区分）。
import { useCallback, useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { LayoutDashboard, WifiOff } from 'lucide-react'
import { ApiError, fetchTasks } from '../../api/client'
import type { Task } from '../../api/types'
import { Badge } from '@/components/ui/badge'
import {
  BOARD_COLUMNS,
  COLUMN_LABELS,
  isFailed,
  isWaitingAnswer,
  stateBadgeVariant,
  stateLabel,
  stateToColumn,
  type BoardColumn,
} from './columns'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { errorMessage, formatRelative } from '../lib/format'

// POLL_INTERVAL 是看板轮询间隔（毫秒）。2–3 秒契约内，取 2.5s 平衡刷新与开销。
const POLL_INTERVAL = 2500

export function BoardPage() {
  const navigate = useNavigate()
  const [tasks, setTasks] = useState<Task[] | null>(null)
  const [disconnected, setDisconnected] = useState(false)
  const [sessionExpired, setSessionExpired] = useState(false)
  const [errorText, setErrorText] = useState('')

  // 轮询循环：立即首拉 → 定时续拉；页面隐藏停表、可见恢复并立即补拉。
  useEffect(() => {
    let stopped = false
    let timer: number | undefined

    const stopTimer = () => {
      if (timer !== undefined) {
        window.clearInterval(timer)
        timer = undefined
      }
    }

    const poll = async () => {
      try {
        const ts = await fetchTasks()
        if (stopped) return
        setTasks(ts)
        setDisconnected(false)
      } catch (err) {
        if (stopped) return
        if (err instanceof ApiError && err.status === 401) {
          // 会话失效是终止态：继续轮询只会刷 401，停表并落终止态
          stopTimer()
          setSessionExpired(true)
          return
        }
        setDisconnected(true)
        setErrorText(errorMessage(err))
      }
    }

    const startTimer = () => {
      if (timer !== undefined) return
      timer = window.setInterval(poll, POLL_INTERVAL)
    }

    const onVisibility = () => {
      if (document.hidden) {
        stopTimer()
      } else {
        startTimer()
        void poll()
      }
    }

    void poll()
    startTimer()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      stopped = true
      stopTimer()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [])

  const openTask = useCallback((id: string) => navigate(`/tasks/${id}`), [navigate])

  return (
    <div className="flex min-h-dvh flex-col bg-muted/40">
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b bg-background px-4 py-3 sm:px-6">
        <h1 className="flex items-center gap-2 text-base font-semibold">
          <LayoutDashboard className="size-4" />
          handoff 控制台 · 任务看板
        </h1>
        {disconnected && (
          <Badge variant="destructive">
            <WifiOff className="size-3" />
            已断开
          </Badge>
        )}
        <p className="ml-auto text-xs text-muted-foreground">
          {tasks === null ? '连接中…' : `共 ${tasks.length} 个任务，每 ${POLL_INTERVAL / 1000} 秒刷新`}
        </p>
      </header>

      <main className="flex w-full flex-1 flex-col gap-4 p-4 sm:p-6">
        {sessionExpired && <SessionExpiredBanner />}
        {disconnected && !sessionExpired && <DisconnectedBanner message={errorText} />}

        {tasks === null ? (
          sessionExpired ? null : (
            <LoadFailed message={errorText || '正在连接 agentd…'} onRetry={() => window.location.reload()} />
          )
        ) : (
          <div className="flex flex-1 items-stretch gap-4 overflow-x-auto pb-2">
            {BOARD_COLUMNS.map((col) => (
              <BoardColumn
                key={col}
                column={col}
                tasks={tasks.filter((t) => stateToColumn(t.state) === col)}
                onOpen={openTask}
              />
            ))}
          </div>
        )}
      </main>
    </div>
  )
}

// BoardColumn 渲染一列看板：列名 + 计数 + 该列的任务卡片。
function BoardColumn({
  column,
  tasks,
  onOpen,
}: {
  column: BoardColumn
  tasks: Task[]
  onOpen: (id: string) => void
}) {
  return (
    <section className="flex w-64 shrink-0 flex-col rounded-lg border bg-background/60">
      <header className="flex items-center justify-between border-b px-3 py-2">
        <h2 className="text-sm font-medium">{COLUMN_LABELS[column]}</h2>
        <Badge variant="secondary">{tasks.length}</Badge>
      </header>
      <div className="flex flex-1 flex-col gap-2 p-2">
        {tasks.length === 0 ? (
          <p className="px-1 py-2 text-xs text-muted-foreground">（空）</p>
        ) : (
          tasks.map((t) => <TaskCard key={t.id} task={t} onOpen={() => onOpen(t.id)} />)
        )}
      </div>
    </section>
  )
}

// TaskCard 是一张任务卡片：名称（空则退 plan_summary）、状态、执行器、分支、
// 更新时间。waiting_answer 加「等你答复」标记；failed 整卡视觉区分。
function TaskCard({ task, onOpen }: { task: Task; onOpen: () => void }) {
  const waitingAnswer = isWaitingAnswer(task.state)
  const failed = isFailed(task.state)
  return (
    <button
      type="button"
      onClick={onOpen}
      className={`flex flex-col gap-1.5 rounded-md border bg-background p-3 text-left shadow-sm transition-colors hover:bg-accent/60 ${
        failed ? 'border-destructive/40 bg-destructive/5' : ''
      }`}
    >
      <div className="flex items-start justify-between gap-2">
        <span className="min-w-0 flex-1 truncate text-sm font-medium">
          {task.name || task.plan_summary || '（无名称）'}
        </span>
        {waitingAnswer && <Badge variant="destructive">等你答复</Badge>}
      </div>
      <div className="flex flex-wrap items-center gap-1.5">
        <Badge variant={stateBadgeVariant(task.state)}>{stateLabel(task.state)}</Badge>
        <span className="font-mono text-xs text-muted-foreground">{task.executor}</span>
      </div>
      <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
        <span className="min-w-0 truncate font-mono">{task.branch}</span>
        <span className="shrink-0">{formatRelative(task.updated_at)}</span>
      </div>
    </button>
  )
}
