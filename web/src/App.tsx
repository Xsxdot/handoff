// 控制台入口：用真实 cookie 会话完成三件事——
//  1. GET /api/status 渲染 agentd 版本与状态
//  2. GET /api/tasks 渲染任务列表（id + state 起）
//  3. 对第一个任务开 /ws/events 并收到至少一条事件
//
// 职责：
//   - 编排数据加载（status/tasks 并行）、错误与加载态的统一渲染
//   - 决定 WS 验证怎么跑：无任务时明确显示「无任务，跳过 WS 验证」，
//     绝不假装成功
//
// 边界：
//   - 不做看板、不做任务详情——那是后续任务
//   - 不做任何业务写操作；本页全部只读
//
// 错误处理约定：鉴权失败（401）与其他失败分别给出可行动的提示，不静默。
import { useCallback, useEffect, useState } from 'react'
import { WifiOff } from 'lucide-react'
import { fetchStatus, fetchTasks } from './api/client'
import type { StatusResp, Task } from './api/types'
import { StatusSection } from './app/StatusSection'
import { TaskSection } from './app/TaskSection'
import { EventSection } from './app/EventSection'
import { ErrorBanner } from './app/ErrorBanner'

export type AppState =
  | { phase: 'loading' }
  | { phase: 'error'; error: Error }
  | { phase: 'ready'; status: StatusResp; tasks: Task[] }

function App() {
  const [state, setState] = useState<AppState>({ phase: 'loading' })

  const reload = useCallback(() => {
    setState({ phase: 'loading' })
    Promise.all([fetchStatus(), fetchTasks()])
      .then(([status, tasks]) => setState({ phase: 'ready', status, tasks }))
      .catch((err: unknown) => {
        setState({ phase: 'error', error: err instanceof Error ? err : new Error(String(err)) })
      })
  }, [])

  useEffect(reload, [reload])

  return (
    <div className="flex min-h-dvh flex-col bg-muted/40">
      <header className="flex flex-wrap items-center gap-x-3 gap-y-1 border-b bg-background px-4 py-3 sm:px-6">
        <h1 className="text-base font-semibold">handoff 控制台</h1>
        <p className="text-xs text-muted-foreground">agentd 托管的 Web UI · 基础联通验证</p>
      </header>

      <main className="mx-auto flex w-full max-w-4xl flex-1 flex-col gap-4 p-4 sm:p-6">
        {state.phase === 'loading' && <p className="text-sm text-muted-foreground">正在连接 agentd…</p>}

        {state.phase === 'error' && <ErrorBanner error={state.error} onRetry={reload} />}

        {state.phase === 'ready' && (
          <>
            <StatusSection status={state.status} />
            <TaskSection tasks={state.tasks} />
            {/* WS 验证的目标任务由 EventSection 从任务列表里选，空列表走「跳过」分支 */}
            <EventSection tasks={state.tasks} />
          </>
        )}
      </main>

      <footer className="flex flex-wrap items-center gap-2 border-t bg-background px-4 py-2 text-xs text-muted-foreground sm:px-6">
        <WifiOff className="size-3.5" />
        <span>鉴权：会话 cookie（handoff_session）经 vite 反代由浏览器自动携带。</span>
      </footer>
    </div>
  )
}

export default App
