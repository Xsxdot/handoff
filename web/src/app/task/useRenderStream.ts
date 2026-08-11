// useRenderStream —— 任务实况（render.log）流的读取 hook。
//
// 数据源：GET /api/tasks/{id}/render?tail=65536&follow=1，text/plain 流
// （attach 的第二个窗口就是它）。用 fetch + ReadableStream 读增量，组件卸载时
// 必须 AbortController 中止——否则每次进出详情页都泄漏一条常驻连接。
//
// 语义注意：
//   - 文件不存在时返回 200 空内容（任务刚派发、模型还没吐字是正常态），不是错误
//   - 响应头 X-Handoff-Render-Size 是开始时的文件大小
//   - follow 空闲时 agentd 每 20s 发一个换行保活，会出现空行，属正常
import { useLayoutEffect, useState } from 'react'
import { errorMessage } from '../lib/format'

// maxRenderChars 是保留展示的实况文本上限；超出丢最旧的，防止长跑任务把 DOM
// 撑爆。初始 tail=64KB，留足余量。
const maxRenderChars = 512 * 1024

// useRenderStream 维护一条 render 实况流。
//
// 返回：
//   - content: 已收到的文本（封顶 maxRenderChars，丢最旧）
//   - size: 开始时文件大小（X-Handoff-Render-Size），未知为 null
//   - error: 流错误的人类可读原因（401 / 非 2xx 原文 / 网络错）
//   - active: 是否仍在跟随（流未结束）
export function useRenderStream(taskId: string | undefined): {
  content: string
  size: number | null
  error: string | null
  active: boolean
} {
  const [content, setContent] = useState('')
  const [size, setSize] = useState<number | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [active, setActive] = useState(true)

  useLayoutEffect(() => {
    if (!taskId) return
    const ac = new AbortController()
    let cancelled = false
    let text = ''
    setContent('')
    setSize(null)
    setError(null)
    setActive(true)

    const run = async () => {
      try {
        const resp = await fetch(
          `/api/tasks/${encodeURIComponent(taskId)}/render?tail=65536&follow=1`,
          { credentials: 'same-origin', signal: ac.signal },
        )
        if (cancelled) return
        if (resp.status === 401) {
          setError('未授权：会话已失效，请重新打开控制台')
          return
        }
        if (!resp.ok) {
          // 非 2xx 时尽量透出 agentd 的错误原文，读不到再回退状态码文案
          let msg = `agentd 返回 ${resp.status} ${resp.statusText}`
          try {
            const body = (await resp.json()) as { error?: string }
            if (body.error) msg = body.error
          } catch {
            // 响应体不是 JSON，保留兜底文案
          }
          setError(msg)
          return
        }
        const hdr = resp.headers.get('X-Handoff-Render-Size')
        setSize(hdr !== null && hdr !== '' ? Number(hdr) : null)
        if (!resp.body) {
          setError('响应没有可读流（浏览器不支持 ReadableStream？）')
          return
        }
        const reader = resp.body.getReader()
        const decoder = new TextDecoder()
        for (;;) {
          const { done, value } = await reader.read()
          if (cancelled || done) break
          text += decoder.decode(value, { stream: true })
          if (text.length > maxRenderChars) {
            text = text.slice(text.length - maxRenderChars)
          }
          setContent(text)
        }
        setActive(false)
      } catch (err) {
        if (cancelled) return // 组件卸载中止是预期收尾，不算错误
        if (err instanceof DOMException && err.name === 'AbortError') return
        setError(errorMessage(err))
        setActive(false)
      }
    }

    void run()
    return () => {
      cancelled = true
      ac.abort() // 离开页面必须中止常驻连接，否则每次进出都泄漏一条
    }
  }, [taskId])

  return { content, size, error, active }
}
