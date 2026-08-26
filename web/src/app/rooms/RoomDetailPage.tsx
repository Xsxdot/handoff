// 房间页（spec §7 注意力面「打开房间即已读」的消费面）。
//
// 职责：
//   - 轮询最新一页历史（limit=200，升序），「加载更早」按 before=最老seq-1 手动叠加
//   - 打开房间即已读：到新 maxSeq 置一次 markRoomRead(id, maxSeq)
//   - 发送框：POST /api/rooms/{id}/messages；只读房由 handler 内守卫拦截（D4——
//     disabled 断言不算数，必须真触发提交再断言没有 POST fetch）
//
// 边界：
//   - 不存在的房间 = 200 空列表（C4 合规 History），照常渲染空历史，不报错
//   - 自身摘要（read_only/bound_session/live）来自列表端点（无 /rooms/{id} 单查端点）；
//     不在列表中的房间按只读未知处理，发送合法性由服务端执法
//   - 布局/滚动/长列表性能进真机清单，jsdom 断言只锁数据流与交互语义
import { useEffect, useMemo, useState } from 'react'
import { useParams } from 'react-router-dom'
import { fetchRoomMessages, fetchRooms, markRoomRead, sendRoomMessage } from '../../api/rooms'
import type { RoomHistoryItem, RoomMessage, RoomSummary } from '../../api/rooms'
import { usePoll } from '../data/usePoll'
import { errorMessage, formatRelative } from '../lib/format'
import { COLLAB_POLL_MS } from './constants'

// HISTORY_LIMIT 每页条数：与契约 History limit<=0 取 200 的默认一致（方案 A）。
const HISTORY_LIMIT = 200

// KIND_LABEL 消息 kind → 中文标签；未知 kind 原样显示。
const KIND_LABEL: Record<string, string> = {
  escalation: '推翻级简报',
  deviation: '偏差叙事',
  closing: '收口摘要',
  relay: '父卡衔接',
  reply: '协调者应答',
  user: '用户',
  pointer: '里程碑',
}

function MessageRow({ event }: { event: RoomHistoryItem }) {
  const msg = (event.payload ?? {}) as Partial<RoomMessage>
  return (
    <div className="rounded-md border px-3 py-2 text-sm">
      <div className="flex flex-wrap items-center gap-2 text-[11px] text-muted-foreground">
        <span className="rounded-full border px-1.5 py-0.5">
          {KIND_LABEL[msg.kind ?? event.type] ?? (msg.kind ?? event.type)}
        </span>
        <span>#{event.seq}</span>
        <span>{formatRelative(event.created_at)}</span>
      </div>
      <p className="mt-1 whitespace-pre-wrap">{msg.body}</p>
    </div>
  )
}

export function RoomDetailPage() {
  const { id = '' } = useParams<{ id: string }>()
  const [older, setOlder] = useState<RoomHistoryItem[]>([])
  const [loadingOlder, setLoadingOlder] = useState(false)
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState('')
  const [markedSeq, setMarkedSeq] = useState<number | null>(null)

  const summaryPoll = usePoll(fetchRooms, COLLAB_POLL_MS)
  const summary: RoomSummary | undefined = useMemo(
    () => summaryPoll.data?.find((room) => room.id === id),
    [summaryPoll.data, id],
  )

  // 历史：只轮询最新一页，更早页手动叠加（older 前缀 + latest 按 seq 去重）。
  const messagesPoll = usePoll(
    () => fetchRoomMessages(id, { limit: HISTORY_LIMIT }),
    COLLAB_POLL_MS,
    { enabled: id !== '' },
  )
  // latest 用 useMemo 包住 ?? []：让引用稳定，下游 all 的 useMemo 依赖它在
  // 每帧失效重算（react-hooks 警告同源）。
  const latest = useMemo(() => messagesPoll.data ?? [], [messagesPoll.data])
  const all = useMemo(() => {
    const seen = new Set<number>()
    const out: RoomHistoryItem[] = []
    for (const event of [...older, ...latest]) {
      if (seen.has(event.seq)) continue
      seen.add(event.seq)
      out.push(event)
    }
    return out
  }, [older, latest])
  const oldestSeq = all.length > 0 ? all[0].seq : 0
  const maxSeq = all.length > 0 ? all[all.length - 1].seq : 0

  // 打开房间即已读（spec §7）：到新消息水位置一次已读，不逐条打。
  useEffect(() => {
    if (maxSeq > 0 && maxSeq !== markedSeq) {
      setMarkedSeq(maxSeq)
      void markRoomRead(id, maxSeq).catch(() => {})
    }
  }, [maxSeq, markedSeq, id])

  const loadOlder = async () => {
    if (loadingOlder || oldestSeq === 0) return
    setLoadingOlder(true)
    try {
      const page = await fetchRoomMessages(id, { before: oldestSeq - 1, limit: HISTORY_LIMIT })
      setOlder((prev) => [...page, ...prev])
    } catch (err) {
      console.debug('[rooms] 加载更早消息失败', id, errorMessage(err))
    } finally {
      setLoadingOlder(false)
    }
  }

  // 发送守卫顺序关键：只读判定在空正文判定**之前**——只读房点击发送必须命中
  // 这条守卫并直接返回，否则「空正文先 return」会让只读断言空转（已知陷阱二，D4）。
  const send = async () => {
    if (summary?.read_only) return
    const body = draft.trim()
    if (body === '' || sending) return
    setSending(true)
    setSendError('')
    try {
      await sendRoomMessage(id, body)
      setDraft('')
      messagesPoll.refresh()
    } catch (err) {
      console.debug('[rooms] 发送消息失败', id, errorMessage(err))
      setSendError(errorMessage(err))
    } finally {
      setSending(false)
    }
  }

  return (
    <main className="flex h-full min-h-0 w-full flex-col bg-background">
      <header className="flex flex-wrap items-center gap-2 border-b px-4 py-2.5">
        <span className="text-sm font-semibold">{summary?.title ?? id}</span>
        {summary?.read_only && <span className="text-[10px] text-amber-700">只读（并入/已归档）</span>}
        {summary?.bound_session !== undefined && summary.bound_session !== '' && (
          <span className="font-mono text-[11px] text-muted-foreground">{summary.bound_session}</span>
        )}
        {messagesPoll.disconnected && (
          <span className="text-[11px] text-amber-700">消息流断线：{messagesPoll.errorText}</span>
        )}
      </header>
      <div className="min-h-0 flex-1 space-y-1.5 overflow-y-auto p-3">
        {messagesPoll.data === null && !messagesPoll.disconnected ? (
          <p className="text-sm text-muted-foreground">正在读取…</p>
        ) : (
          <>
            {all.length > 0 && (
              <button
                type="button"
                onClick={() => void loadOlder()}
                disabled={loadingOlder}
                className="w-full rounded-md border px-3 py-1.5 text-xs"
              >
                {loadingOlder ? '加载中…' : '加载更早'}
              </button>
            )}
            {all.length === 0 ? (
              <p className="text-sm text-muted-foreground">（还没有消息）</p>
            ) : (
              all.map((event) => <MessageRow key={event.seq} event={event} />)
            )}
          </>
        )}
      </div>
      <footer className="flex flex-col gap-1.5 border-t px-4 py-2.5">
        {summary?.read_only && (
          <p className="text-[11px] text-amber-700">房间只读，不能发送消息。</p>
        )}
        <div className="flex items-center gap-2">
          <textarea
            aria-label="消息正文"
            value={draft}
            onChange={(event) => setDraft(event.target.value)}
            placeholder={summary?.read_only ? '' : '发消息…'}
            disabled={summary?.read_only}
            rows={2}
            className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1.5 text-sm"
          />
          <button
            type="button"
            onClick={() => void send()}
            className="rounded-md border px-3 py-1.5 text-sm"
          >
            发送
          </button>
        </div>
        {sendError !== '' && <p role="alert" className="text-xs text-destructive">{sendError}</p>}
      </footer>
    </main>
  )
}