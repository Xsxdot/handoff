// SessionTab —— 会话的工作台 tab 窗格宿主（B361：群聊↔详情切换留在 tab 内部）。
// 职责：详情+历史两路轮询、打开即已读（markedReads 去重守卫，沿用旧房间面板既有
// 模式）、拉卡进群对话框、⋯/← 切换。边界：不发身份字段；已读失败只告警不阻塞渲染。
import { useEffect, useRef, useState } from 'react'
import { fetchRoomMessages, fetchSessionDetail, joinSessionCard, markRoomRead } from '../../api/rooms'
import type { SessionDetail as SessionDetailDTO } from '../../api/rooms'
import { errorMessage } from '../lib/format'
import { usePoll } from '../data/usePoll'
import { COLLAB_POLL_MS } from './constants'
import { logRoom } from './roomLog'
import { JoinCardDialog } from './JoinCardDialog'
import { SessionChat } from './SessionChat'
import { SessionDetail } from './SessionDetail'

const HISTORY_LIMIT = 200

export function SessionTab({ sessionId, title, onOpenCard }: {
  sessionId: string
  title: string
  onOpenCard?: (cardId: string) => void
}) {
  const [view, setView] = useState<'chat' | 'detail'>('chat')
  const [joinOpen, setJoinOpen] = useState(false)
  const [joinBusy, setJoinBusy] = useState(false)
  const [joinError, setJoinError] = useState('')
  const markedReads = useRef<Record<string, number>>({})

  const detailPoll = usePoll(() => fetchSessionDetail(sessionId), COLLAB_POLL_MS)
  const historyPoll = usePoll(() => fetchRoomMessages(sessionId, { limit: HISTORY_LIMIT }), COLLAB_POLL_MS)
  const detail: SessionDetailDTO | null = detailPoll.data
  const history = historyPoll.data ?? []
  const maxSeq = history.reduce((max, item) => Math.max(max, item.seq), 0)

  // 打开即已读（spec §7）：seq 水位去重，失败从水位表剔除让下一轮重试。
  useEffect(() => {
    if (view !== 'chat' || detailPoll.data === null || maxSeq <= 0 || maxSeq <= (markedReads.current[sessionId] ?? 0)) return
    markedReads.current[sessionId] = maxSeq
    logRoom('debug', 'session_mark_read_started', { session: sessionId, upto_seq: maxSeq })
    void markRoomRead(sessionId, maxSeq).catch((error: unknown) => {
      delete markedReads.current[sessionId]
      logRoom('error', 'session_mark_read_failed', { session: sessionId, error: errorMessage(error) })
    })
  }, [view, sessionId, maxSeq, detailPoll.data])

  const joinCard = async (cardId: string) => {
    setJoinBusy(true)
    setJoinError('')
    logRoom('debug', 'session_join_card_started', { session: sessionId, card: cardId })
    try {
      await joinSessionCard(sessionId, cardId)
      setJoinOpen(false)
      detailPoll.refresh()
      logRoom('debug', 'session_join_card_succeeded', { session: sessionId, card: cardId })
    } catch (error: unknown) {
      setJoinError(errorMessage(error))
      logRoom('error', 'session_join_card_failed', { session: sessionId, card: cardId, error: errorMessage(error) })
    } finally {
      setJoinBusy(false)
    }
  }

  return (
    <div className="flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
        <span className="min-w-0 flex-1 truncate text-sm font-semibold">{title}</span>
        <button type="button" aria-label="拉卡进群" onClick={() => setJoinOpen(true)} className="rounded-md border px-2 py-1 text-xs hover:bg-accent">＋ 拉卡进群</button>
        {view === 'chat' ? (
          <button type="button" aria-label="会话详情" onClick={() => setView('detail')} className="rounded-md px-2 py-1 text-xs hover:bg-accent">⋯</button>
        ) : (
          <button type="button" aria-label="返回群聊" onClick={() => setView('chat')} className="rounded-md px-2 py-1 text-sm hover:bg-accent">←</button>
        )}
      </header>
      {view === 'chat' ? (
        <SessionChat sessionId={sessionId} summary={detail?.summary ?? null} events={history}
          historyError={historyPoll.disconnected ? historyPoll.errorText : ''} onSent={() => historyPoll.refresh()}
          onOpenCard={onOpenCard} />
      ) : detail === null ? (
        <p className="p-4 text-sm text-muted-foreground">{detailPoll.disconnected ? `详情读取失败：${detailPoll.errorText}` : '正在读取…'}</p>
      ) : (
        <SessionDetail detail={detail} />
      )}
      <JoinCardDialog open={joinOpen} busy={joinBusy} error={joinError} onCancel={() => setJoinOpen(false)} onJoin={(cardId) => void joinCard(cardId)} />
    </div>
  )
}
