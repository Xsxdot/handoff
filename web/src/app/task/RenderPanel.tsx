// RenderPanel —— 任务实况正文（render.log 流）。
//
// 数据源：GET /api/tasks/{id}/render?tail=65536&follow=1，text/plain 流
// （attach 的第二个窗口就是它）。用 fetch + ReadableStream 读增量，组件卸载时
// 必须 AbortController 中止——否则每次进出详情页都泄漏一条常驻连接。
//
// 语义注意：
//   - 文件不存在时返回 200 空内容（任务刚派发、模型还没吐字是正常态），不是错误
//   - 响应头 X-Handoff-Render-Size 是开始时的文件大小，展示出来供人对齐
//   - follow 空闲时 agentd 每 20s 发一个换行保活，会出现空行，属正常
import { useLayoutEffect, useRef } from 'react'
import { Eye } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { useRenderStream } from './useRenderStream'

export function RenderPanel({ taskId }: { taskId: string }) {
  const { content, size, error, active } = useRenderStream(taskId)
  const scrollRef = useRef<HTMLDivElement>(null)
  const stickBottom = useRef(true)

  // 内容增长时若用户在底部附近就跟随滚动；用户往上翻则停止跟随，避免抢滚轮。
  useLayoutEffect(() => {
    const el = scrollRef.current
    if (!el) return
    const nearBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 40
    if (stickBottom.current || nearBottom) {
      el.scrollTop = el.scrollHeight
      stickBottom.current = true
    }
  }, [content])

  const onScroll = () => {
    const el = scrollRef.current
    if (!el) return
    stickBottom.current = el.scrollHeight - el.scrollTop - el.clientHeight < 40
  }

  return (
    <section className="flex flex-col gap-2 rounded-lg border bg-background p-4">
      <header className="flex flex-wrap items-center justify-between gap-2">
        <h2 className="flex items-center gap-2 text-sm font-medium">
          <Eye className="size-4" />
          实况正文
        </h2>
        <div className="flex items-center gap-2">
          {size !== null && <span className="text-xs text-muted-foreground">起始 {size} 字节</span>}
          <Badge variant={active ? 'default' : 'secondary'}>{active ? '跟随中' : '已结束'}</Badge>
        </div>
      </header>
      {error && (
        <p role="alert" className="break-words text-sm text-destructive">
          {error}
        </p>
      )}
      <div
        ref={scrollRef}
        onScroll={onScroll}
        className="h-96 overflow-y-auto rounded-md bg-muted/30 p-3 font-mono text-xs leading-relaxed"
      >
        {content === '' && error === null ? (
          <p className="text-muted-foreground">等待模型输出…（render.log 尚为空属正常）</p>
        ) : (
          <pre className="whitespace-pre-wrap break-words">{content}</pre>
        )}
      </div>
    </section>
  )
}
