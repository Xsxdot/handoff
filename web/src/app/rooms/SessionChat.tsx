// SessionChat —— 会话 tab 的群聊态：人话气泡 + @高亮 + 回复引用条（只读渲染，
// 回复锚生产面缺口归 B365）+ 卡 chips（空座虚线）+ 发送。
// 边界：member 身份由服务端注入，前端不自报（请求不带身份字段）；发送被拒的
// 原文透传（403 非成员 / 409 归档）——可行动报错是岔口 1 的组件半边。
import { useState } from 'react'
import type { RoomHistoryItem, SessionSummary } from '../../api/rooms'
import { addSessionMember, sendRoomMessage } from '../../api/rooms'
import { ApiError } from '../../api/client'
import { errorMessage } from '../lib/format'
import { logRoom } from './roomLog'
import { isSelfActor, segmentBody } from './sessionModel'

export interface SessionChatProps {
  sessionId: string
  summary: SessionSummary | null
  events: RoomHistoryItem[]
  historyError: string
  onSent: () => void
  onOpenCard?: (cardId: string) => void
}

function MessageRow({ event, referenced, highlight, onJump }: {
  event: RoomHistoryItem
  referenced: RoomHistoryItem | null
  highlight: boolean
  onJump: (seq: number) => void
}) {
  const payload = event.payload as { body?: string; mentions?: string[]; reply_to?: number }
  const self = isSelfActor(event.actor)
  const body = typeof payload.body === 'string' ? payload.body : ''
  const replyTo = typeof payload.reply_to === 'number' && payload.reply_to > 0 ? payload.reply_to : null
  return (
    <div data-msg-seq={event.seq} data-testid={`msg-${event.seq}`}
      className={`flex flex-col rounded-lg p-1 ${self ? 'items-end' : 'items-start'} ${highlight ? 'highlight bg-amber-50' : ''}`}>
      {replyTo !== null && (
        <button type="button" data-testid={`quote-${event.seq}`} onClick={() => onJump(replyTo)}
          className="mb-1 max-w-[74%] rounded border-l-2 border-border bg-black/[.02] px-2 py-0.5 text-left text-[11px] text-muted-foreground">
          ↩ {referenced ? `${referenced.actor}：${(referenced.payload as { body?: string }).body ?? ''}` : `#${replyTo}`}
        </button>
      )}
      <div className={`max-w-[74%] rounded-2xl px-3 py-2 text-sm shadow-sm ${self ? 'bg-slate-900 text-white' : 'border bg-white/65 backdrop-blur-[12px]'}`}>
        <div className={`mb-0.5 text-[10px] ${self ? 'text-white/60' : 'text-muted-foreground'}`}>{event.actor} · #{event.seq}</div>
        <p className="whitespace-pre-wrap">
          {segmentBody(body, payload.mentions).map((segment, index) => (
            segment.mention
              ? <span key={index} data-testid={`mention-${event.seq}-${index}`} className="font-semibold text-amber-600">{segment.text}</span>
              : <span key={index}>{segment.text}</span>
          ))}
        </p>
      </div>
    </div>
  )
}

export function SessionChat({ sessionId, summary, events, historyError, onSent, onOpenCard }: SessionChatProps) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState('')
  // sendErrorStatus 记发送被拒的 HTTP 状态（0=非 ApiError/网络层）：B366 一键
  // 「以当前身份加入会话」只在 403（非成员）出现，409 归档等不给加入入口。
  const [sendErrorStatus, setSendErrorStatus] = useState(0)
  const [joining, setJoining] = useState(false)
  const [highlightSeq, setHighlightSeq] = useState<number | null>(null)
  const bySeq = new Map(events.map((item) => [item.seq, item]))

  // jumpTo 引用条跳转：滚动定位 + 高亮保留下一次跳转（无定时器——无卸载清理面）。
  const jumpTo = (seq: number) => {
    document.querySelector(`[data-msg-seq="${seq}"]`)?.scrollIntoView({ block: 'center' })
    setHighlightSeq(seq)
    logRoom('debug', 'session_quote_jumped', { session: sessionId, to_seq: seq })
  }

  const send = async () => {
    const body = draft.trim()
    if (body === '' || sending) return
    setSending(true)
    setSendError('')
    setSendErrorStatus(0)
    const mentions = body.match(/@[^\s]+/g)?.map((token) => token.slice(1)) ?? []
    logRoom('debug', 'session_send_started', { session: sessionId, mentions: mentions.length })
    try {
      await sendRoomMessage(sessionId, body, mentions.length > 0 ? { mentions } : {})
      setDraft('')
      onSent()
      logRoom('debug', 'session_send_succeeded', { session: sessionId })
    } catch (error: unknown) {
      // 403（非成员）/409（归档）等哨兵文案原文透传——ApiError 保留 agentd error 字段。
      setSendError(errorMessage(error))
      setSendErrorStatus(error instanceof ApiError ? error.status : 0)
      logRoom('error', 'session_send_failed', { session: sessionId, error: errorMessage(error) })
    } finally {
      setSending(false)
    }
  }

  // joinSelf 「以当前身份加入会话」一键（B366，发言权岔口1 的组件半边）：身份
  // 服务端权威——addSessionMember 不带身份字段（前端不自报，B358.4 门禁纪律），
  // 服务端以注入 actor 入列；成功即清 403 横幅，草稿仍在可直接再发。
  const joinSelf = async () => {
    if (joining) return
    setJoining(true)
    try {
      await addSessionMember(sessionId)
      setSendError('')
      setSendErrorStatus(0)
      logRoom('debug', 'session_self_joined', { session: sessionId })
    } catch (error: unknown) {
      setSendError(errorMessage(error))
      setSendErrorStatus(error instanceof ApiError ? error.status : 0)
      logRoom('error', 'session_self_join_failed', { session: sessionId, error: errorMessage(error) })
    } finally {
      setJoining(false)
    }
  }

  const archived = summary?.archived === true
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="shrink-0 border-b px-3 py-2 text-xs text-muted-foreground">
        {summary?.owner != null && summary.owner !== '' && <span>群主：{summary.owner} · </span>}
        {archived && <span className="font-semibold text-amber-700">会话已归档，只读。</span>}
      </div>
      {(summary?.cards ?? []).length > 0 && (
        <div className="flex shrink-0 items-center gap-1.5 overflow-x-auto border-b bg-slate-50 px-3 py-1.5">
          {summary!.cards!.map((card) => (
            <button key={card.card_id} type="button" data-testid="session-card-chip" onClick={() => onOpenCard?.(card.card_id)}
              className={`inline-flex shrink-0 items-center gap-1.5 rounded-md border bg-background px-2 py-0.5 text-xs ${card.seat ? '' : 'border-dashed'}`}>
              <b className="font-mono">{card.card_id}</b>
              {card.status && <span className="text-muted-foreground">{card.status}</span>}
              <span className={card.seat ? 'text-muted-foreground' : 'text-amber-700'}>{card.seat ?? '空座 · 还没配人'}</span>
            </button>
          ))}
        </div>
      )}
      <div className="min-h-0 flex-1 space-y-2 overflow-y-auto bg-slate-50/60 p-3">
        {events.length === 0 ? <p className="text-sm text-muted-foreground">（还没有消息）</p>
          : events.map((item) => {
            const payload = item.payload as { reply_to?: number }
            const replyTo = typeof payload.reply_to === 'number' && payload.reply_to > 0 ? payload.reply_to : null
            return <MessageRow key={item.seq} event={item} referenced={replyTo !== null ? bySeq.get(replyTo) ?? null : null} highlight={highlightSeq === item.seq} onJump={jumpTo} />
          })}
      </div>
      <footer className="shrink-0 border-t bg-background p-2.5">
        {historyError !== '' && <p role="alert" className="mb-1 text-xs text-amber-800">消息流已断开：{historyError}</p>}
        {sendError !== '' && (
          <p role="alert" className="mb-1 text-xs text-destructive">
            发送被拒：{sendError}
            {sendErrorStatus === 403 && !archived && (
              <button type="button" data-testid="session-join-self" onClick={() => void joinSelf()} disabled={joining}
                className="ml-2 rounded-md border px-2 py-0.5 text-xs text-foreground hover:bg-muted disabled:opacity-50">
                以当前身份加入会话
              </button>
            )}
          </p>
        )}
        <div className="flex items-end gap-2 rounded-2xl border bg-background p-1.5">
          <textarea aria-label="发送消息" value={draft} onChange={(event) => setDraft(event.target.value)} disabled={archived} rows={2}
            className="min-w-0 flex-1 resize-none border-0 bg-transparent px-1.5 py-1 text-sm outline-none"
            placeholder={archived ? '' : '发消息…（要谁办就 @ 谁；没 @ 的发言不唤醒任何人）'} />
          <button type="button" aria-label="发送" onClick={() => void send()} disabled={archived || sending || draft.trim() === ''}
            className="rounded-xl bg-slate-900 px-3 py-1.5 text-xs text-white disabled:opacity-50">发送</button>
        </div>
      </footer>
    </div>
  )
}
