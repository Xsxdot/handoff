// SessionTab —— 会话的工作台 tab 窗格宿主（B358.8 #3：chat↔detail 双态退役，
// 「⋯」改右侧抽屉与会话框并存）。
// 职责：详情+历史两路轮询、打开即已读（markedReads 去重守卫）、拉卡对话框态、
// 抽屉开关与归档确认。边界：不发身份字段；已读失败只告警不阻塞渲染。
// 标题不在此渲染——唯一来源是窗格标题行（tabTitle →「会话 · 标题」）。
import { useEffect, useRef, useState } from 'react'
import { archiveSession, fetchRoomMessages, fetchSessionDetail, joinSessionCard, markRoomRead } from '../../api/rooms'
import type { SessionDetail as SessionDetailDTO } from '../../api/rooms'
import { errorMessage } from '../lib/format'
import { ConfirmDialog } from '../lib/ConfirmDialog'
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
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [joinOpen, setJoinOpen] = useState(false)
  const [joinBusy, setJoinBusy] = useState(false)
  const [joinError, setJoinError] = useState('')
  const [archiveConfirm, setArchiveConfirm] = useState(false)
  const [archiveBusy, setArchiveBusy] = useState(false)
  const [archiveError, setArchiveError] = useState('')
  const markedReads = useRef<Record<string, number>>({})

  const detailPoll = usePoll(() => fetchSessionDetail(sessionId), COLLAB_POLL_MS)
  const historyPoll = usePoll(() => fetchRoomMessages(sessionId, { limit: HISTORY_LIMIT }), COLLAB_POLL_MS)
  const detail: SessionDetailDTO | null = detailPoll.data
  const history = historyPoll.data ?? []
  const maxSeq = history.reduce((max, item) => Math.max(max, item.seq), 0)

  // 打开即已读（spec §7）：seq 水位去重，失败从水位表剔除让下一轮重试。
  useEffect(() => {
    if (detailPoll.data === null || maxSeq <= 0 || maxSeq <= (markedReads.current[sessionId] ?? 0)) return
    markedReads.current[sessionId] = maxSeq
    logRoom('debug', 'session_mark_read_started', { session: sessionId, upto_seq: maxSeq })
    void markRoomRead(sessionId, maxSeq).catch((error: unknown) => {
      delete markedReads.current[sessionId]
      logRoom('error', 'session_mark_read_failed', { session: sessionId, error: errorMessage(error) })
    })
  }, [sessionId, maxSeq, detailPoll.data])

  // 抽屉 Esc 收起：与会话流并存（无遮罩），Esc 是 spec 拍板的第二收起通道。
  useEffect(() => {
    if (!drawerOpen) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setDrawerOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [drawerOpen])

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

  const archive = async () => {
    setArchiveBusy(true)
    setArchiveError('')
    logRoom('debug', 'session_archive_started', { session: sessionId })
    try {
      await archiveSession(sessionId)
      setArchiveConfirm(false)
      detailPoll.refresh()
      logRoom('debug', 'session_archive_succeeded', { session: sessionId })
    } catch (error: unknown) {
      setArchiveError(errorMessage(error))
      logRoom('error', 'session_archive_failed', { session: sessionId, error: errorMessage(error) })
    } finally {
      setArchiveBusy(false)
    }
  }

  return (
    <div className="relative flex h-full min-h-0 flex-col">
      <header className="flex shrink-0 items-center justify-end border-b px-2 py-1">
        <button type="button" aria-label="会话详情" aria-expanded={drawerOpen} onClick={() => setDrawerOpen(true)}
          className="rounded-md px-2 py-1 text-xs hover:bg-accent">⋯</button>
      </header>
      <SessionChat sessionId={sessionId} summary={detail?.summary ?? null} events={history}
        historyError={historyPoll.disconnected ? historyPoll.errorText : ''} onSent={() => historyPoll.refresh()}
        onJoinCard={() => setJoinOpen(true)} />
      {drawerOpen && (
        <aside data-testid="session-drawer" aria-label="会话详情"
          className="absolute inset-y-0 right-0 z-30 flex w-80 max-w-[85%] flex-col border-l bg-background shadow-xl">
          <div className="flex shrink-0 items-center justify-between border-b px-3 py-2">
            <h2 className="text-sm font-semibold">会话详情</h2>
            <button type="button" aria-label="关闭详情" onClick={() => setDrawerOpen(false)}
              className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent">×</button>
          </div>
          {detail === null
            ? <p className="p-3 text-sm text-muted-foreground">{detailPoll.disconnected ? `详情读取失败：${detailPoll.errorText}` : '正在读取…'}</p>
            : <SessionDetail detail={detail} onOpenCard={onOpenCard}
                onArchive={() => setArchiveConfirm(true)} archiveBusy={archiveBusy} archiveError={archiveError} />}
        </aside>
      )}
      <JoinCardDialog open={joinOpen} busy={joinBusy} error={joinError} onCancel={() => setJoinOpen(false)} onJoin={(cardIds) => void joinCard(cardIds)} />
      <ConfirmDialog open={archiveConfirm} title="归档会话"
        description={`归档「${title}」后本会话转为只读（归档是幂等操作）。`}
        confirmLabel="归档" destructive busy={archiveBusy} error={archiveError}
        onConfirm={() => void archive()} onCancel={() => setArchiveConfirm(false)} />
    </div>
  )
}
