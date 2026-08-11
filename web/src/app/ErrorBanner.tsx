// ErrorBanner —— 顶层失败的统一渲染。
//
// 职责：
//   - 把 ApiError（含 401 鉴权失败）翻译成可行动的提示文案
//   - 提供重试按钮，让「agentd 没起 / 反代没配 / 会话过期」都能自愈重试
//
// 边界：
//   - 只负责展示；重试逻辑由父级传入
import { AlertTriangle, RotateCw } from 'lucide-react'
import { ApiError } from '../api/client'

// ErrorBanner 渲染一条醒目的错误提示。
//
// 参数：
//   - error: 加载失败的错误（ApiError 或任意 Error）
//   - onRetry: 用户点「重试」时的回调
//
// 注意：
//   - 401 必须特判：这是浏览器会话失效的唯一直观信号，提示重跑 handoff console
export function ErrorBanner({ error, onRetry }: { error: Error; onRetry: () => void }) {
  const status = error instanceof ApiError ? error.status : null
  const authNote =
    status === 401
      ? '请执行 handoff console --print-url，把打印出的地址端口从 7777 换成 5173 后打开，完成 cookie 兑换后再回来。'
      : null

  return (
    <div role="alert" className="flex flex-col gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-4">
      <div className="flex items-start gap-2">
        <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" />
        <div className="flex flex-col gap-1 text-sm">
          <p className="font-medium text-destructive">
            连接失败{status !== null ? `（HTTP ${status}）` : ''}
          </p>
          <p className="text-foreground/80">{error.message}</p>
          {authNote && <p className="text-foreground/70">{authNote}</p>}
        </div>
      </div>
      <div>
        <button
          type="button"
          onClick={onRetry}
          className="inline-flex items-center gap-1.5 rounded-md border border-input bg-background px-3 py-1.5 text-sm font-medium shadow-sm hover:bg-accent"
        >
          <RotateCw className="size-4" />
          重试
        </button>
      </div>
    </div>
  )
}
