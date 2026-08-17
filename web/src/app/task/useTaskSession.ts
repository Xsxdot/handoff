// useTaskSession —— 一个任务会话的数据编排（原 TaskPage 的上半截）。
//
// 职责：
//   - GET /api/tasks/{id} 轮询详情（任务 + 挂起工单，4s 一次），页面隐藏时暂停
//   - 首拉时以 recent_events 打底事件流，然后开**一条** /ws/events?task=<id>
//     &from_seq=<最大 seq> 收实时增量（WS 层自己推进游标，重连不重放）
//   - 工单应答（POST reply）并在成功后立即补拉
//
// 边界：
//   - 不渲染任何东西。W4 把它从 TaskPage 里提出来，是因为同一份会话要同时
//     喂给 TUI tab 与（未来的）其他消费者；留在页面组件里必然要复制一份
//   - 不管 frames / render 流：那两条由 TuiTab 及其子组件分别自管，且互斥
//     的呈现策略由总装层决定（原始正文仅在调试抽屉按需连接）
//
// 断线语义（硬契约，照搬不得放宽）：
//   - 断线保留最后拿到的数据继续显示，所有会改状态的按钮禁用，标注「已断开」；
//     不称为「只读」——只读暗示数据是新的，而它不是
//   - WS close code 1008（会话被吊销）落到「会话已失效」终止态，不无脑重连；
//     HTTP 401 同样收敛到终止态并停轮询
//
// cursor 归属：浏览器不碰 ~/.handoff/cursor-*（那是 CLI 审核者的本机游标账本），
// 这里从 from_seq=0 或已知最大 seq 续，是观察者；与 CLI 同时盯同一任务互不干扰。
import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError, fetchTaskDetail, replyTicket } from '../../api/client'
import type { Event, TaskDetail, Ticket } from '../../api/types'
import { connectEvents, type WsStatus } from '../../api/ws'
import { errorMessage } from '../lib/format'

// DETAIL_POLL_INTERVAL 是详情轮询间隔：任务状态与挂起工单靠它保鲜。
const DETAIL_POLL_INTERVAL = 4000

export interface TaskSession {
  detail: TaskDetail | null
  events: Event[]
  wsStatus: WsStatus
  wsError: string | null
  loadError: string | null
  disconnected: boolean
  disconnectReason: string
  sessionExpired: boolean
  refresh: () => void
  replyToTicket: (ticket: Ticket, answer: string) => Promise<void>
}

export function useTaskSession(id: string | undefined): TaskSession {
  const [detail, setDetail] = useState<TaskDetail | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [disconnected, setDisconnected] = useState(false)
  const [disconnectReason, setDisconnectReason] = useState('')
  const [sessionExpired, setSessionExpired] = useState(false)
  const [events, setEvents] = useState<Event[]>([])
  const [wsStatus, setWsStatus] = useState<WsStatus>('connecting')
  const [wsError, setWsError] = useState<string | null>(null)

  const initializedRef = useRef(false)
  const sessionExpiredRef = useRef(false)
  // 事件打底与 WS 起点：首拉后把 recent_events 及其最大 seq 记进 ref，
  // WS 订阅（单独 effect）以 [id, seeded] 为依赖，只在新任务/首拉完成时重建。
  const initialEventsRef = useRef<Event[]>([])
  const initialSeqRef = useRef(0)
  const [seeded, setSeeded] = useState(false)

  // refreshDetail 拉一次详情并合并进状态。首拉时以 recent_events 打底事件流，
  // 之后轮询只刷新 task / pending_tickets（事件增量归 WS）。
  const refreshDetail = useCallback(async () => {
    if (!id || sessionExpiredRef.current) return
    try {
      const d = await fetchTaskDetail(id)
      if (!initializedRef.current) {
        initializedRef.current = true
        initialEventsRef.current = d.recent_events
        initialSeqRef.current = d.recent_events.reduce((m, e) => Math.max(m, e.seq), 0)
        setSeeded(true)
      }
      setDetail(d)
      setLoadError(null)
      setDisconnected(false)
    } catch (err) {
      if (err instanceof ApiError && err.status === 401) {
        sessionExpiredRef.current = true
        setSessionExpired(true)
        return
      }
      const msg = errorMessage(err)
      if (!initializedRef.current) {
        setLoadError(msg) // 还没有任何可显示的数据：终止态 + 重试
      } else {
        setDisconnected(true) // 已有数据：保留显示，标注已断开
        setDisconnectReason(msg)
      }
    }
  }, [id])

  // 详情轮询循环：立即首拉 → 定时续拉；页面隐藏停表、可见恢复并立即补拉。
  useEffect(() => {
    if (!id) return
    // 换任务（同一 hook 实例换 id）时重置首拉标记与会话失效态
    initializedRef.current = false
    sessionExpiredRef.current = false
    setSeeded(false)
    let timer: number | undefined

    const stopTimer = () => {
      if (timer !== undefined) {
        window.clearInterval(timer)
        timer = undefined
      }
    }
    const startTimer = () => {
      if (timer !== undefined || sessionExpiredRef.current) return
      timer = window.setInterval(() => void refreshDetail(), DETAIL_POLL_INTERVAL)
    }
    const onVisibility = () => {
      if (document.hidden) {
        stopTimer()
      } else {
        startTimer()
        void refreshDetail()
      }
    }

    void refreshDetail()
    startTimer()
    document.addEventListener('visibilitychange', onVisibility)
    return () => {
      stopTimer()
      document.removeEventListener('visibilitychange', onVisibility)
    }
  }, [id, refreshDetail])

  // 事件流 WS：详情首拉完成后以 recent_events 的最大 seq 为起点订阅实时增量。
  // 依赖 [id, seeded]：只在新任务或首拉完成时（重建）订阅，不随 4s 详情轮询
  // 反复断开重连；WS 层自己推进游标，重连不重放已见事件。
  useEffect(() => {
    if (!id || !seeded) return
    setEvents(initialEventsRef.current)
    setWsStatus('connecting')
    setWsError(null)
    const conn = connectEvents({
      taskId: id,
      fromSeq: initialSeqRef.current,
      onEvent: (ev) => setEvents((prev) => [...prev, ev]),
      onStatus: (s) => {
        setWsStatus(s)
        if (s === 'open') {
          setDisconnected(false)
          setWsError(null)
        }
      },
      onError: (msg, code) => {
        // closeCode 0 只是「解析不出事件帧」的瞬时错，连接未断，不置为已断开
        if (code !== 0) setDisconnected(true)
        setWsError(msg)
        setDisconnectReason(msg)
      },
      onTerminal: () => {
        // 会话被吊销：WS 侧已停止重连，HTTP 侧由 401 收敛，这里落终止态
        sessionExpiredRef.current = true
        setSessionExpired(true)
        setWsStatus('closed')
      },
    })
    return () => conn.close()
  }, [id, seeded])

  // replyToTicket 是工单应答的提交入口：POST reply，成功后立即补拉让工单消失。
  const replyToTicket = useCallback(
    async (ticket: Ticket, answer: string) => {
      if (!id) return
      await replyTicket(id, { ticket_id: ticket.id, answer })
      void refreshDetail()
    },
    [id, refreshDetail],
  )

  return {
    detail,
    events,
    wsStatus,
    wsError,
    loadError,
    disconnected,
    disconnectReason,
    sessionExpired,
    refresh: () => void refreshDetail(),
    replyToTicket,
  }
}
