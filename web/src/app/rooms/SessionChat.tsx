// SessionChat —— 会话 tab 的群聊态：人话气泡 + @高亮 + 回复引用条 + 回复快捷钮 +
// @ 输入联想 + 发送。
// 边界：member 身份由服务端注入，前端不自报（请求不带身份字段）；发送被拒的
// 原文透传（403 非成员 / 409 归档）——可行动报错是岔口 1 的组件半边。
// B358.8 #2：回复态是**前端引用条**——不发送 reply_to（发送半边归 B365；
// sendRoomMessage 契约不动）。缝位：replyTarget 现只喂引用条显示，B365 接线时
// 仅消费 replyTarget.seq 作为 reply_to 字段，不新增 wire 字段。
// B358.8 #3：群主与卡列表迁详情抽屉（SessionDetail），拉卡入口在输入框左下工具钮。
import { useEffect, useMemo, useRef, useState } from 'react'
import { CornerUpLeft, ListPlus } from 'lucide-react'
import type { KeyboardEvent } from 'react'
import type { RoomHistoryItem, SessionSummary } from '../../api/rooms'
import { addSessionMember, sendRoomMessage } from '../../api/rooms'
import { ApiError } from '../../api/client'
import { errorMessage } from '../lib/format'
import { logRoom } from './roomLog'
import { applyMention, isSelfActor, memberKindLabel, mentionCandidates, segmentBody } from './sessionModel'

export interface SessionChatProps {
  sessionId: string
  summary: SessionSummary | null
  events: RoomHistoryItem[]
  historyError: string
  onSent: () => void
  onJoinCard?: () => void
}

function MessageRow({ event, referenced, highlight, archived, onJump, onReply }: {
  event: RoomHistoryItem
  referenced: RoomHistoryItem | null
  highlight: boolean
  archived: boolean
  onJump: (seq: number) => void
  onReply: (event: RoomHistoryItem) => void
}) {
  const payload = event.payload as { body?: string; mentions?: string[]; reply_to?: number }
  const self = isSelfActor(event.actor)
  const body = typeof payload.body === 'string' ? payload.body : ''
  const replyTo = typeof payload.reply_to === 'number' && payload.reply_to > 0 ? payload.reply_to : null
  return (
    <div data-msg-seq={event.seq} data-testid={`msg-${event.seq}`}
      className={`group flex flex-col rounded-lg p-1 ${self ? 'items-end' : 'items-start'} ${highlight ? 'highlight bg-amber-50' : ''}`}>
      {replyTo !== null && (
        <button type="button" data-testid={`quote-${event.seq}`} onClick={() => onJump(replyTo)}
          className="mb-1 max-w-[74%] rounded border-l-2 border-border bg-black/[.02] px-2 py-0.5 text-left text-[11px] text-muted-foreground">
          ↩ {referenced ? `${referenced.actor}：${(referenced.payload as { body?: string }).body ?? ''}` : `#${replyTo}`}
        </button>
      )}
      <div className={`flex max-w-[74%] items-center gap-1 ${self ? 'flex-row-reverse' : ''}`}>
        <div className={`rounded-2xl px-3 py-2 text-sm shadow-sm ${self ? 'bg-slate-900 text-white' : 'border bg-white/65 backdrop-blur-[12px]'}`}>
          <div className={`mb-0.5 text-[10px] ${self ? 'text-white/60' : 'text-muted-foreground'}`}>{event.actor} · #{event.seq}</div>
          <p className="whitespace-pre-wrap">
            {segmentBody(body, payload.mentions).map((segment, index) => (
              segment.mention
                ? <span key={index} data-testid={`mention-${event.seq}-${index}`} className="font-semibold text-amber-600">{segment.text}</span>
                : <span key={index}>{segment.text}</span>
            ))}
          </p>
        </div>
        {!archived && (
          <button type="button" data-testid={`reply-${event.seq}`} aria-label={`回复 #${event.seq}`}
            onClick={() => onReply(event)}
            className="shrink-0 rounded p-1 text-muted-foreground opacity-0 transition-opacity hover:bg-accent hover:text-foreground focus:opacity-100 group-hover:opacity-100">
            <CornerUpLeft className="size-3.5" />
          </button>
        )}
      </div>
    </div>
  )
}

export function SessionChat({ sessionId, summary, events, historyError, onSent, onJoinCard }: SessionChatProps) {
  const [draft, setDraft] = useState('')
  const [sending, setSending] = useState(false)
  const [sendError, setSendError] = useState('')
  // sendErrorStatus 记发送被拒的 HTTP 状态（0=非 ApiError/网络层）：B366 一键
  // 「以当前身份加入会话」只在 403（非成员）出现，409 归档等不给加入入口。
  const [sendErrorStatus, setSendErrorStatus] = useState(0)
  const [joining, setJoining] = useState(false)
  const [highlightSeq, setHighlightSeq] = useState<number | null>(null)
  // replyTarget 回复态（B358.8 #2）：只作引用条显示，**不进请求体**——B365 接线时
  // 仅消费 replyTarget.seq（缝位见文件头注释）。
  const [replyTarget, setReplyTarget] = useState<RoomHistoryItem | null>(null)
  // @ 联想（B358.8 #2）：草稿末尾的进行中 token 驱动候选面板；dismissed 记 Esc
  // 关闭，token 变化（继续输入）时重开。
  const [mentionDismissed, setMentionDismissed] = useState(false)
  const [mentionIndex, setMentionIndex] = useState(0)
  const textareaRef = useRef<HTMLTextAreaElement | null>(null)
  const bySeq = new Map(events.map((item) => [item.seq, item]))
  const archived = summary?.archived === true

  const mentionToken = draft.match(/@[^\s]*$/)?.[0] ?? null
  const candidates = useMemo(
    () => (mentionToken === null || archived ? [] : mentionCandidates(summary?.members ?? [], mentionToken.slice(1))),
    [mentionToken, archived, summary],
  )
  const mentionOpen = mentionToken !== null && !mentionDismissed && candidates.length > 0
  useEffect(() => { setMentionDismissed(false) }, [mentionToken])
  useEffect(() => { setMentionIndex(0) }, [mentionToken, candidates.length])

  // jumpTo 引用条跳转：滚动定位 + 高亮保留下一次跳转（无定时器——无卸载清理面）。
  const jumpTo = (seq: number) => {
    document.querySelector(`[data-msg-seq="${seq}"]`)?.scrollIntoView({ block: 'center' })
    setHighlightSeq(seq)
    logRoom('debug', 'session_quote_jumped', { session: sessionId, to_seq: seq })
  }

  const startReply = (event: RoomHistoryItem) => {
    setReplyTarget(event)
    textareaRef.current?.focus()
    logRoom('debug', 'session_reply_started', { session: sessionId, target_seq: event.seq })
  }

  const pickMention = (identity: string) => {
    setDraft((current) => applyMention(current, identity))
    textareaRef.current?.focus()
  }

  // mentionKeyDown 面板打开时的键盘语义：↑↓ 移动、Enter/Tab 选中、Esc 关闭。
  const mentionKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
    if (!mentionOpen) return
    if (event.key === 'ArrowDown') {
      event.preventDefault()
      setMentionIndex((index) => (index + 1) % candidates.length)
    } else if (event.key === 'ArrowUp') {
      event.preventDefault()
      setMentionIndex((index) => (index - 1 + candidates.length) % candidates.length)
    } else if (event.key === 'Enter' || event.key === 'Tab') {
      event.preventDefault()
      // 候选列表收窄的同一帧里 index 可能越界：夹紧到现存范围再取。
      const picked = candidates[Math.min(mentionIndex, candidates.length - 1)]
      if (picked) pickMention(picked.identity)
    } else if (event.key === 'Escape') {
      setMentionDismissed(true)
      // Esc 分层（review P2）：stopPropagation 挡住原生事件继续冒泡到 window，
      // SessionTab 的抽屉收起监听器不再同帧收到这次 Esc——面板开着时 Esc 只关面板。
      event.stopPropagation()
    }
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
      // 缝位（B365）：回复发送半边在此接入 reply_to（消费 replyTarget.seq），
      // 本卡契约不动——请求体只有 body/refs/mentions。
      await sendRoomMessage(sessionId, body, mentions.length > 0 ? { mentions } : {})
      setDraft('')
      setReplyTarget(null)
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

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {archived && (
        <div className="shrink-0 border-b bg-amber-50 px-3 py-1.5 text-xs font-semibold text-amber-700">会话已归档，只读。</div>
      )}
      <div className="min-h-0 flex-1 space-y-2 overflow-y-auto bg-slate-50/60 p-3">
        {events.length === 0 ? <p className="text-sm text-muted-foreground">（还没有消息）</p>
          : events.map((item) => {
            const payload = item.payload as { reply_to?: number }
            const replyTo = typeof payload.reply_to === 'number' && payload.reply_to > 0 ? payload.reply_to : null
            return <MessageRow key={item.seq} event={item} referenced={replyTo !== null ? bySeq.get(replyTo) ?? null : null}
              highlight={highlightSeq === item.seq} archived={archived} onJump={jumpTo} onReply={startReply} />
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
        {replyTarget !== null && (
          <div data-testid="reply-target" className="mb-1 flex items-center gap-2 rounded-lg border-l-2 border-border bg-black/[.02] px-2 py-1 text-[11px] text-muted-foreground">
            <CornerUpLeft className="size-3 shrink-0" />
            <span className="min-w-0 flex-1 truncate">{replyTarget.actor}：{(replyTarget.payload as { body?: string }).body ?? ''}</span>
            <button type="button" aria-label="取消回复" onClick={() => setReplyTarget(null)}
              className="shrink-0 rounded p-0.5 hover:bg-accent">×</button>
          </div>
        )}
        {mentionOpen && (
          <div data-testid="mention-menu" role="listbox" aria-label="@ 候选"
            className="mb-1 rounded-xl border bg-background p-1 shadow-md">
            {candidates.map((member, index) => (
              <button key={member.identity} type="button" role="option" aria-selected={index === mentionIndex}
                data-testid={`mention-option-${index}`} data-mention-identity={member.identity}
                onClick={() => pickMention(member.identity)}
                className={`flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm ${index === mentionIndex ? 'bg-accent' : 'hover:bg-accent/60'}`}>
                <span className="font-mono text-xs">{member.identity}</span>
                <span className="shrink-0 rounded-full border px-1.5 text-[10px] text-muted-foreground">{memberKindLabel(member.kind)}</span>
                {member.card_title && <span className="min-w-0 flex-1 truncate text-xs text-muted-foreground">{member.card_title}</span>}
              </button>
            ))}
          </div>
        )}
        <div className="flex items-end gap-2 rounded-2xl border bg-background p-1.5">
          {onJoinCard && (
            <button type="button" aria-label="拉卡进群" title="拉卡进群" onClick={onJoinCard}
              className="rounded-lg p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground">
              <ListPlus className="size-4" />
            </button>
          )}
          <textarea ref={textareaRef} aria-label="发送消息" value={draft} onChange={(event) => setDraft(event.target.value)}
            onKeyDown={mentionKeyDown} disabled={archived} rows={2}
            className="min-w-0 flex-1 resize-none border-0 bg-transparent px-1.5 py-1 text-sm outline-none"
            placeholder={archived ? '' : '发消息…（要谁办就 @ 谁；没 @ 的发言不唤醒任何人）'} />
          <button type="button" aria-label="发送" onClick={() => void send()} disabled={archived || sending || draft.trim() === ''}
            className="rounded-xl bg-slate-900 px-3 py-1.5 text-xs text-white disabled:opacity-50">发送</button>
        </div>
      </footer>
    </div>
  )
}
