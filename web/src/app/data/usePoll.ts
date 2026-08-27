// usePoll —— 三条数据流共用的轮询原语。
//
// 职责：
//   - 立即首拉 + 定时续拉，返回 { data, disconnected, sessionExpired, errorText, refresh }
//   - 复刻 W2 在 BoardPage 里验证过的三条实时性纪律：document.hidden 停表、
//     断线保留最后数据、401 落终止态不再重试
//
// 边界：
//   - 不关心具体接口，fetcher 由调用方给
//   - 不做跨组件请求缓存：三条流各自独立；同一条流只合并尚未完成的请求，
//     共享缓存只会让"机器探活失败"
//     污染看板（spec §10 要求三条流互不影响）
//
// 为什么把 W2 BoardPage 里的循环提取出来：W3b 有三条节奏不同的流，
// 复制三份轮询逻辑意味着三份各自会跑偏的 document.hidden 处理。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../../api/client'
import { errorMessage } from '../lib/format'

export interface PollState<T> {
  data: T | null
  disconnected: boolean
  sessionExpired: boolean
  errorText: string
  refresh: () => void
}

export function usePoll<T>(
  fetcher: () => Promise<T>,
  intervalMs: number,
  opts?: { enabled?: boolean },
): PollState<T> {
  const enabled = opts?.enabled ?? true
  const [data, setData] = useState<T | null>(null)
  const [disconnected, setDisconnected] = useState(false)
  const [sessionExpired, setSessionExpired] = useState(false)
  const [errorText, setErrorText] = useState('')
  // fetcher 常是内联箭头函数，放进 ref 避免每次渲染重启轮询
  const fetcherRef = useRef(fetcher)
  fetcherRef.current = fetcher
  // interval 与 visibilitychange/refresh 可能同时触发；跨 effect 保留当前 Promise，
  // 避免上一轮慢请求尚未结束时又启动同一数据流的下一轮。新的 effect 可订阅同一
  // Promise，兼容 StrictMode 的 effect 重放而不丢首拉结果。
  const inFlightRef = useRef<Promise<T> | null>(null)
  const [nonce, setNonce] = useState(0)

  const refresh = useCallback(() => setNonce((n) => n + 1), [])

  useEffect(() => {
    if (!enabled) return
    let stopped = false
    let timer: number | undefined
    const stopTimer = () => { if (timer !== undefined) { window.clearInterval(timer); timer = undefined } }

    const poll = async (subscribe = false) => {
      let request = inFlightRef.current
      if (request && !subscribe) return
      try {
        if (!request) {
          request = fetcherRef.current()
          inFlightRef.current = request
        }
        const v = await request
        if (stopped) return
        setData(v)
        setDisconnected(false)
      } catch (err) {
        if (stopped) return
        if (err instanceof ApiError && err.status === 401) {
          // 会话失效是终止态：继续轮询只会刷 401
          stopTimer()
          setSessionExpired(true)
          return
        }
        // 断线保留 data 不清空——空看板比旧看板更误导
        setDisconnected(true)
        setErrorText(errorMessage(err))
      } finally {
        if (request && inFlightRef.current === request) inFlightRef.current = null
      }
    }

    const startTimer = () => { if (timer === undefined) timer = window.setInterval(() => { void poll() }, intervalMs) }
    const onVisibility = () => {
      if (document.hidden) stopTimer()
      else { startTimer(); void poll(true) }
    }

    void poll(true)
    startTimer()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      stopped = true
      stopTimer()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [enabled, intervalMs, nonce])

  return { data, disconnected, sessionExpired, errorText, refresh }
}
