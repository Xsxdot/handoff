// SessionTab —— 会话的工作台 tab 窗格宿主（B358.8 #3：chat↔detail 双态退役，右侧
// 抽屉与会话框并存；走查 09-17：⋯ 搬进窗格标题行，本组件只留抽屉与开启器注册）。
// 职责：详情+历史两路轮询、打开即已读（markedReads 去重守卫）、拉卡对话框态、
// 抽屉开关与归档确认。边界：不发身份字段；已读失败只告警不阻塞渲染。
// 标题不在此渲染——唯一来源是窗格标题行（tabTitle →「会话 · 标题」）。
// B369.8（T5）：compact 改「群聊 | 详情」两态语义切换（plan 岔口 5）——头部
// tablist 两枚 tab，两态内容都保持挂载、hidden 属性翻转可见性（群聊的草稿/
// 回复引用条/@联想全是 SessionChat 内部 state，卸载即丢；群聊无 canvas/WS，
// display:none 无 B280 式副作用。已知代价：隐藏期间聊天 scrollTop 归零，回
// 群聊落在顶部——如实接受，不做滚动恢复）。桌面「⋯」抽屉逐字节不动。
import { useEffect, useRef, useState } from 'react'
import { archiveSession, fetchRoomMessages, fetchSessionDetail, joinSessionCard, markRoomRead } from '../../api/rooms'
import type { SessionDetail as SessionDetailDTO } from '../../api/rooms'
import { errorMessage } from '../lib/format'
import { TOUCH_BASELINE } from '@/lib/touch'
import { cn } from '@/lib/utils'
import { SessionExpiredBanner } from '../lib/Banners'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { usePoll } from '../data/usePoll'
import { COLLAB_POLL_MS } from './constants'
import { logRoom } from './roomLog'
import { registerSessionDetailOpener } from './sessionDetailOpener'
import { JoinCardDialog } from './JoinCardDialog'
import { SessionChat } from './SessionChat'
import { SessionDetail } from './SessionDetail'

const HISTORY_LIMIT = 200
const SESSION_READ_TIMEOUT_MS = 15_000

export function SessionTab({ sessionId, title, onOpenCard, compact = false }: {
  sessionId: string
  title: string
  onOpenCard?: (cardId: string) => void
  compact?: boolean
}) {
  const [drawerOpen, setDrawerOpen] = useState(false)
  // paneDetail 是 compact 两态的唯一事实源（真 = 详情态）；桌面不用它，
  // drawerOpen 语义原样。同层切换、不是导航层级，不进 URL（plan §10 明令）。
  const [paneDetail, setPaneDetail] = useState(false)
  const [joinOpen, setJoinOpen] = useState(false)
  const [joinBusy, setJoinBusy] = useState(false)
  const [joinError, setJoinError] = useState('')
  const [archiveConfirm, setArchiveConfirm] = useState(false)
  const [archiveBusy, setArchiveBusy] = useState(false)
  const [archiveError, setArchiveError] = useState('')
  const markedReads = useRef<Record<string, number>>({})

  const detailPoll = usePoll((signal) => fetchSessionDetail(sessionId, signal), COLLAB_POLL_MS,
    { timeoutMs: SESSION_READ_TIMEOUT_MS })
  const historyPoll = usePoll((signal) => {
    logRoom('debug', 'session_history_fetch_started', { session: sessionId })
    return fetchRoomMessages(sessionId, { limit: HISTORY_LIMIT, signal })
      .then((events) => {
        logRoom('debug', 'session_history_fetch_succeeded', { session: sessionId, count: events.length })
        return events
      })
      .catch((error: unknown) => {
        logRoom('warn', 'session_history_fetch_failed', { session: sessionId, error: errorMessage(error) })
        throw error
      })
  }, COLLAB_POLL_MS, { timeoutMs: SESSION_READ_TIMEOUT_MS })
  const detail: SessionDetailDTO | null = detailPoll.data
  const history = historyPoll.data ?? []
  const maxSeq = history.reduce((max, item) => Math.max(max, item.seq), 0)

  // 请求本身可能一直不 settle，超时由 usePoll 生成；在视图边界补一条带会话号
  // 的诊断日志，下一次现场可区分历史失败与真正的空会话。
  useEffect(() => {
    if (!historyPoll.disconnected) return
    logRoom('warn', 'session_history_unavailable', { session: sessionId, error: historyPoll.errorText })
  }, [sessionId, historyPoll.disconnected, historyPoll.errorText])

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

  // 详情开启投递（走查 09-17 #3）：⋯ 住窗格标题行（WorkbenchPage 渲染），抽屉
  // 开关状态住本组件——挂载即注册开启器，标题行按钮经注册表投递到这里；卸载注销。
  useEffect(() => registerSessionDetailOpener(sessionId, () => setDrawerOpen(true)), [sessionId])

  // 抽屉 Esc 收起：与会话流并存（无遮罩），Esc 是 spec 拍板的第二收起通道。
  // B369.8：compact 下 Esc 同样关详情态（切回群聊）；桌面抽屉语义原样。
  useEffect(() => {
    if (!drawerOpen && !(compact && paneDetail)) return
    const onKey = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return
      if (compact) setPaneDetail(false)
      else setDrawerOpen(false)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [drawerOpen, compact, paneDetail])

  // joinCards 批量拉卡（B358.8 #8）：逐张顺序发既有单卡端点；失败聚合原文留在
  // 对话框供重试，部分成功也刷新详情——端点语义零改动，批量只是前端循环。
  const joinCards = async (cardIds: string[]) => {
    setJoinBusy(true)
    setJoinError('')
    logRoom('debug', 'session_join_card_started', { session: sessionId, cards: cardIds })
    const failed: string[] = []
    for (const cardId of cardIds) {
      try {
        await joinSessionCard(sessionId, cardId)
        logRoom('debug', 'session_join_card_succeeded', { session: sessionId, card: cardId })
      } catch (error: unknown) {
        failed.push(`${cardId}：${errorMessage(error)}`)
        logRoom('error', 'session_join_card_failed', { session: sessionId, card: cardId, error: errorMessage(error) })
      }
    }
    if (failed.length > 0) setJoinError(failed.join('\n'))
    else setJoinOpen(false)
    detailPoll.refresh()
    setJoinBusy(false)
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
    <div className={cn('relative flex h-full min-h-0 flex-col', compact && TOUCH_BASELINE)}>
      {compact && (
        // 桌面随 main：⋯ 唯一住窗格标题行（WorkbenchPage 渲染），本组件内不渲染
        // 第二个 —— 头部整块只在 compact 出现，装着「群聊 | 详情」两态 tablist。
        <header className="flex shrink-0 items-center justify-end border-b px-2 py-1">
          {/* 两态 tablist（岔口 5）：同层「群聊 | 详情」视图切换，aria-selected 翻转。 */}
          <div role="tablist" aria-label="会话视图" className="flex items-center gap-1">
            <button type="button" role="tab" aria-selected={!paneDetail} data-testid="session-view-chat"
              onClick={() => setPaneDetail(false)} className="rounded-md px-2 py-1 text-xs hover:bg-accent">群聊</button>
            <button type="button" role="tab" aria-selected={paneDetail} data-testid="session-view-detail"
              onClick={() => setPaneDetail(true)} className="rounded-md px-2 py-1 text-xs hover:bg-accent">详情</button>
          </div>
        </header>
      )}
      {compact ? (
        // —— compact 两态（岔口 5）——
        <>
          {/* 群聊面板：hidden 属性翻转保挂载。草稿/回复引用条/@联想是
              SessionChat 内部 state，卸载即丢；群聊无 canvas/WS，display:none
              无 B280 式副作用。已知代价：隐藏期间聊天 scrollTop 归零，回群聊
              落在顶部——如实接受，不做滚动恢复。被详情态盖住时 hidden 面板
              本就不进读屏树与 Tab 序，无需再叠 inert（被盖面不渲染盒子）。 */}
          <div role="tabpanel" aria-label="群聊" hidden={paneDetail ? true : undefined} className="flex min-h-0 flex-1 flex-col">
            <SessionChat sessionId={sessionId} summary={detail?.summary ?? null} events={history}
              historyLoading={historyPoll.data === null && !historyPoll.disconnected && !historyPoll.sessionExpired}
              historyExpired={historyPoll.sessionExpired}
              historyError={historyPoll.disconnected && !historyPoll.sessionExpired ? historyPoll.errorText : ''}
              onSent={() => historyPoll.refresh()}
              onJoinCard={() => setJoinOpen(true)} compact={compact} onOpenCard={onOpenCard} />
          </div>
          {/* 详情态占满会话内容区（五块全宽）；归档/拉卡流程接线原样。 */}
          <div role="tabpanel" aria-label="会话详情" hidden={paneDetail ? undefined : true} className="min-h-0 flex-1 overflow-y-auto">
            {detail === null
              ? detailPoll.sessionExpired
                ? <div className="p-3"><SessionExpiredBanner /></div>
                : detailPoll.disconnected
                  ? <p className="p-3 text-sm text-muted-foreground">详情读取失败：{detailPoll.errorText}</p>
                  : <p className="p-3 text-sm text-muted-foreground">正在读取…</p>
              : <SessionDetail detail={detail} onOpenCard={onOpenCard}
                  onArchive={() => setArchiveConfirm(true)} archiveBusy={archiveBusy} archiveError={archiveError} />}
          </div>
        </>
      ) : (
        // B406：401 是终止态要单独成面（专用 prop 渲染过期横幅）；historyError
        // 挂 !sessionExpired——先断线后 401 时 disconnected 有残留，过期优先，
        // 断线文案不与过期横幅混排
        <SessionChat sessionId={sessionId} summary={detail?.summary ?? null} events={history}
          historyLoading={historyPoll.data === null && !historyPoll.disconnected && !historyPoll.sessionExpired}
          historyExpired={historyPoll.sessionExpired}
          historyError={historyPoll.disconnected && !historyPoll.sessionExpired ? historyPoll.errorText : ''}
          onSent={() => historyPoll.refresh()}
          onJoinCard={() => setJoinOpen(true)} />
      )}
      {!compact && drawerOpen && (
        <aside data-testid="session-drawer" aria-label="会话详情"
          className="absolute inset-y-0 right-0 z-30 flex w-80 max-w-[85%] flex-col border-l bg-background shadow-xl">
          <div className="flex shrink-0 items-center justify-between border-b px-3 py-2">
            <h2 className="text-sm font-semibold">会话详情</h2>
            <button type="button" aria-label="关闭详情" onClick={() => setDrawerOpen(false)}
              className="rounded-md px-2 py-1 text-xs text-muted-foreground hover:bg-accent">×</button>
          </div>
          {detail === null
            ? detailPoll.sessionExpired
              ? <div className="p-3"><SessionExpiredBanner /></div>
              : detailPoll.disconnected
                ? <p className="p-3 text-sm text-muted-foreground">详情读取失败：{detailPoll.errorText}</p>
                : <p className="p-3 text-sm text-muted-foreground">正在读取…</p>
            : <SessionDetail detail={detail} onOpenCard={onOpenCard}
                onArchive={() => setArchiveConfirm(true)} archiveBusy={archiveBusy} archiveError={archiveError} />}
        </aside>
      )}
      <JoinCardDialog open={joinOpen} busy={joinBusy} error={joinError}
        existingCardIds={(detail?.summary?.cards ?? []).map((card) => card.card_id)}
        onCancel={() => setJoinOpen(false)} onJoin={(cardIds) => void joinCards(cardIds)} />
      <ConfirmDialog open={archiveConfirm} title="归档会话"
        description={`归档「${title}」后本会话转为只读（归档是幂等操作）。`}
        confirmLabel="归档" destructive busy={archiveBusy} error={archiveError}
        onConfirm={() => void archive()} onCancel={() => setArchiveConfirm(false)} />
    </div>
  )
}
