// RoomPanel —— 控制台统一的房间面板。
//
// 职责：在一个组件内承载会话列表、当前房间和详情三态，并连接房间 API、轮询
// 以及 attach 后的 Workbench 终端入口。
// 边界：不改变路由、不直接创建 PTY；列表与收件箱轮询独立，消息正文只进入界面，
// 不进入结构化日志。
import { useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent } from 'react'
import { fetchCardDetail } from '../../api/ledger'
import type { CardDetail } from '../../api/ledger'
import { fetchInbox, fetchRoomMessages, fetchRooms, markRoomRead, sendRoomMessage } from '../../api/rooms'
import type { RoomHistoryItem, RoomSummary } from '../../api/rooms'
import { nextTerminalSeq } from '../workbench/tabs'
import type { WorkbenchApi } from '../workbench/useWorkbench'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage, formatRelative } from '../lib/format'
import { usePoll } from '../data/usePoll'
import { COLLAB_POLL_MS } from './constants'
import { logRoom } from './roomLog'
import { useRoomPanelGeom } from './useRoomPanelGeom'
import {
  attachBase,
  messageBody,
  orderRooms,
  roomInitials,
  roomNeedsReply,
  type RoomPanelView,
  visibleRooms,
} from './roomPanelModel'

export interface RoomPanelProps {
  workbench: WorkbenchApi
  persistent: boolean
  onOpenCard?: (id: string) => void
}

const HISTORY_LIMIT = 200

const KIND_LABEL: Record<string, string> = {
  card: '卡房间',
  project: '项目群',
  global: '全员群',
}

function roomMessageIsSelf(event: RoomHistoryItem): boolean {
  const kind = (event.payload as { kind?: unknown } | undefined)?.kind
  return kind === 'user' || event.actor === 'user:me'
}

function messageLabel(event: RoomHistoryItem): string {
  const kind = (event.payload as { kind?: unknown } | undefined)?.kind
  return typeof kind === 'string' ? kind : event.type
}

function PreviewText({ room, needsReply }: { room: RoomSummary; needsReply: boolean }) {
  const text = room.preview?.body || '暂无预览'
  return (
    <p className="truncate text-xs text-muted-foreground">
      {needsReply && <span className="mr-1 text-amber-700">[待回复]</span>}
      <span>{text}</span>
      <span className="sr-only"> {KIND_LABEL[room.kind] ?? room.kind}</span>
    </p>
  )
}

function ListRow({ room, needsReply, onOpen }: { room: RoomSummary; needsReply: boolean; onOpen: (id: string) => void }) {
  return (
    <button
      type="button"
      aria-label={`会话 ${room.title}`}
      onClick={() => onOpen(room.id)}
      className={`relative flex w-full items-center gap-2.5 rounded-xl px-2.5 py-2 text-left transition-colors ${needsReply ? 'bg-amber-50 hover:bg-amber-100' : 'hover:bg-accent/60'}`}
    >
      <span className="relative flex size-11 shrink-0 items-center justify-center rounded-full bg-slate-200 text-xs font-semibold text-slate-700">
        {roomInitials(room)}
        {room.unread > 0 && <span className="absolute -right-1 -top-1 min-w-4 rounded-full bg-red-500 px-1 text-center text-[10px] leading-4 text-white">{room.unread}</span>}
      </span>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-1.5">
          <span className="truncate text-sm font-medium">{room.title}</span>
          <span className="shrink-0 rounded-full border px-1.5 py-0.5 text-[10px] text-muted-foreground">{KIND_LABEL[room.kind] ?? room.kind}</span>
        </span>
        <PreviewText room={room} needsReply={needsReply} />
      </span>
      <span className="shrink-0 self-start pt-0.5 text-[10px] text-muted-foreground">{formatRelative(room.last_activity)}</span>
    </button>
  )
}

function MessageBubble({ event }: { event: RoomHistoryItem }) {
  const self = roomMessageIsSelf(event)
  return (
    <div className={`flex ${self ? 'justify-end' : 'justify-start'}`}>
      <div className={`max-w-[82%] rounded-2xl px-3 py-2 text-sm shadow-sm ${self ? 'bg-slate-900 text-white' : 'border bg-white/65 backdrop-blur-[12px]'}`}>
        <div className={`mb-0.5 text-[10px] ${self ? 'text-white/60' : 'text-muted-foreground'}`}>{messageLabel(event)} · #{event.seq}</div>
        <p className="whitespace-pre-wrap">{messageBody(event)}</p>
      </div>
    </div>
  )
}

function PanelHeader({ title, onCollapse, onDragDown }: { title: string; onCollapse: () => void; onDragDown: (event: ReactPointerEvent) => void }) {
  return (
    <header data-testid="room-panel-title" onPointerDown={onDragDown} className="flex shrink-0 cursor-move select-none items-center justify-between border-b px-3 py-2.5">
      <span className="text-sm font-semibold">{title}</span>
      <button type="button" aria-label="收起房间面板" onPointerDown={(event) => event.stopPropagation()} onClick={onCollapse} className="rounded-md px-2 py-1 text-xs hover:bg-accent">×</button>
    </header>
  )
}

/**
 * 显示房间列表、当前房间或详情。
 * 参数 workbench 是 attach 的唯一工作台接缝；persistent=true 渲染右侧栏，false 渲染可收起浮窗。
 * 房间态不改地址栏，避免三态产生第二套路由状态。
 */
export function RoomPanel({ workbench, persistent, onOpenCard }: RoomPanelProps) {
  const [view, setView] = useState<RoomPanelView>('list')
  const [roomID, setRoomID] = useState('')
  // 常驻栏与浮窗的开合状态分账（B287 返修）：Shell 挂的是同一个实例、
  // persistent 随路由切换——收起状态若共用，在工作项页之外收起浮窗会连带
  // 藏掉工作项页的常驻栏。两种形态各记各的，切形态互不可见。
  const [collapsedFloating, setCollapsedFloating] = useState(false)
  const [collapsedRail, setCollapsedRail] = useState(false)
  const collapsed = persistent ? collapsedRail : collapsedFloating
  const setCollapsed = persistent ? setCollapsedRail : setCollapsedFloating
  const [project, setProject] = useState('')
  const [needsOnly, setNeedsOnly] = useState(false)
  const [attachConfirm, setAttachConfirm] = useState(false)
  const [draft, setDraft] = useState('')
  const [readError, setReadError] = useState('')
  const [sendError, setSendError] = useState('')
  const [stepBusy, setStepBusy] = useState(false)
  const [cardDetail, setCardDetail] = useState<CardDetail | null>(null)
  const [cardDetailError, setCardDetailError] = useState('')
  const markedReads = useRef<Record<string, number>>({})
  // 浮窗几何：仅非持久形态消费；geom===null 时浮窗不渲染（避免闪现在 (0,0)）。
  const { geom: panelGeom, ensurePlaced, onGeom: applyGeom } = useRoomPanelGeom()
  const geomRef = useRef(panelGeom)
  geomRef.current = panelGeom
  const floating = !persistent && !collapsed
  useEffect(() => {
    if (floating) ensurePlaced()
  }, [floating, ensurePlaced])

  // grab 照抄 HomeWindow：按下记起点、document 收 move、抬起一次性解绑；
  // 指针拖出窗口也收得到 move，窗口不会卡在半路。
  const grab = (event: ReactPointerEvent, apply: (dx: number, dy: number) => void) => {
    event.preventDefault()
    const sx = event.clientX
    const sy = event.clientY
    const move = (e: PointerEvent) => apply(e.clientX - sx, e.clientY - sy)
    const up = () => {
      document.removeEventListener('pointermove', move)
      const g = geomRef.current
      if (g) logRoom('debug', 'room_geom_committed', { x: g.x, y: g.y, w: g.w, h: g.h })
    }
    document.addEventListener('pointermove', move)
    document.addEventListener('pointerup', up, { once: true })
    document.addEventListener('pointercancel', up, { once: true })
  }
  const onTitleDown = (event: ReactPointerEvent) => {
    const from = panelGeom
    if (persistent || !from) return
    grab(event, (dx, dy) => applyGeom({ x: from.x + dx, y: from.y + dy }))
  }
  const onCornerDown = (event: ReactPointerEvent) => {
    const from = panelGeom
    if (persistent || !from) return
    grab(event, (dx, dy) => applyGeom({ w: from.w + dx, h: from.h + dy }))
  }

  const loadRooms = async () => {
    logRoom('debug', 'rooms_request_started', { request: 'rooms' })
    try {
      const result = await fetchRooms()
      logRoom('debug', 'rooms_request_succeeded', { request: 'rooms', count: result.length })
      return result
    } catch (error: unknown) {
      logRoom('error', 'rooms_request_failed', { request: 'rooms', error: errorMessage(error) })
      throw error
    }
  }
  const loadInbox = async () => {
    logRoom('debug', 'inbox_request_started', { request: 'inbox' })
    try {
      const result = await fetchInbox()
      logRoom('debug', 'inbox_request_succeeded', { request: 'inbox', count: result.length })
      return result
    } catch (error: unknown) {
      logRoom('error', 'inbox_request_failed', { request: 'inbox', error: errorMessage(error) })
      throw error
    }
  }
  const loadHistory = async () => {
    logRoom('debug', 'history_request_started', { room: roomID, request: 'messages' })
    try {
      const result = await fetchRoomMessages(roomID, { limit: HISTORY_LIMIT })
      logRoom('debug', 'history_request_succeeded', { room: roomID, request: 'messages', count: result.length })
      return result
    } catch (error: unknown) {
      logRoom('error', 'history_request_failed', { room: roomID, request: 'messages', error: errorMessage(error) })
      throw error
    }
  }
  const roomsPoll = usePoll(loadRooms, COLLAB_POLL_MS)
  const inboxPoll = usePoll(loadInbox, COLLAB_POLL_MS)
  const rooms = useMemo(() => roomsPoll.data ?? [], [roomsPoll.data])
  const inbox = useMemo(() => inboxPoll.data ?? [], [inboxPoll.data])
  const needRoomIDs = useMemo(() => new Set(inbox.map((item) => item.card_id).filter((id): id is string => !!id)), [inbox])
  const visible = useMemo(() => orderRooms(visibleRooms(rooms, project, needsOnly, needRoomIDs), needRoomIDs), [rooms, project, needsOnly, needRoomIDs])
  const projectOptions = useMemo(() => [...new Set(rooms.map((room) => room.project).filter((name): name is string => !!name))].sort(), [rooms])
  const selectedRoom = useMemo(() => rooms.find((room) => room.id === roomID), [rooms, roomID])

  useEffect(() => {
    if (view !== 'detail' || selectedRoom?.kind !== 'card' || roomID === '') {
      setCardDetail(null)
      setCardDetailError('')
      return
    }
    let cancelled = false
    setCardDetail(null)
    setCardDetailError('')
    logRoom('debug', 'card_detail_request_started', { room: roomID, request: 'card_detail' })
    void fetchCardDetail(roomID)
      .then((result) => {
        if (cancelled) return
        setCardDetail(result)
        logRoom('debug', 'card_detail_request_succeeded', { room: roomID, request: 'card_detail' })
      })
      .catch((error: unknown) => {
        if (cancelled) return
        setCardDetailError(errorMessage(error))
        logRoom('error', 'card_detail_request_failed', { room: roomID, request: 'card_detail', error: errorMessage(error) })
      })
    return () => { cancelled = true }
  }, [roomID, selectedRoom?.kind, view])

  const historyPoll = usePoll(loadHistory, COLLAB_POLL_MS, { enabled: view === 'room' && roomID !== '' })
  const history = useMemo(() => historyPoll.data ?? [], [historyPoll.data])
  const maxSeq = useMemo(() => history.reduce((max, event) => Math.max(max, event.seq), 0), [history])

  useEffect(() => {
    if (view !== 'room' || !selectedRoom || maxSeq <= 0 || maxSeq <= (markedReads.current[selectedRoom.id] ?? 0)) return
    markedReads.current[selectedRoom.id] = maxSeq
    logRoom('debug', 'mark_read_started', { room: selectedRoom.id, request: 'read', upto_seq: maxSeq })
    void markRoomRead(selectedRoom.id, maxSeq)
      .then(() => logRoom('debug', 'mark_read_succeeded', { room: selectedRoom.id, request: 'read', upto_seq: maxSeq }))
      .catch((error: unknown) => {
        delete markedReads.current[selectedRoom.id]
        setReadError(errorMessage(error))
        logRoom('error', 'mark_read_failed', { room: selectedRoom.id, request: 'read', error: errorMessage(error) })
      })
  }, [view, selectedRoom, maxSeq])

  useEffect(() => {
    if (view === 'room' && roomID !== '' && roomsPoll.data !== null && !selectedRoom) {
      logRoom('warn', 'room_removed', { room: roomID, view })
      setRoomID('')
      setView('list')
    }
  }, [roomID, roomsPoll.data, selectedRoom, view])

  const openRoom = (id: string) => {
    logRoom('debug', 'room_opened', { room: id, view: 'list' })
    setRoomID(id)
    setView('room')
    setDraft('')
    setSendError('')
    setReadError('')
  }

  const backToList = () => {
    logRoom('debug', 'room_list_opened', { view })
    setView('list')
    setRoomID('')
  }

  const send = async () => {
    if (selectedRoom?.read_only) {
      logRoom('warn', 'send_blocked_read_only', { room: roomID, request: 'messages' })
      return
    }
    const body = draft.trim()
    if (body === '' || stepBusy) return
    setStepBusy(true)
    setSendError('')
    logRoom('debug', 'send_started', { room: roomID, request: 'messages' })
    try {
      await sendRoomMessage(roomID, body)
      setDraft('')
      historyPoll.refresh()
      // B287：回复即清提及类的前端半边——后端已在发送路径消费该房间提及，
      // 这里立即重取收件箱把新事实拉进角标，不等下一个 5s 周期。
      inboxPoll.refresh()
      logRoom('debug', 'send_succeeded', { room: roomID, request: 'messages' })
    } catch (error: unknown) {
      setSendError(errorMessage(error))
      logRoom('error', 'send_failed', { room: roomID, request: 'messages', error: errorMessage(error) })
    } finally {
      setStepBusy(false)
    }
  }

  const openAttachConfirm = () => {
    if (!selectedRoom?.attach) {
      logRoom('warn', 'attach_blocked', { room: roomID, view, error: '暂无可 attach 的任务' })
      return
    }
    logRoom('debug', 'attach_confirmation_opened', { room: roomID, view, task: selectedRoom.attach.task_id })
    setAttachConfirm(true)
  }

  const confirmAttach = () => {
    if (!selectedRoom?.attach) {
      logRoom('warn', 'attach_confirm_blocked', { room: roomID, view, error: '暂无可 attach 的任务' })
      return
    }
    const base = attachBase(selectedRoom)
    if (!base) {
      logRoom('error', 'attach_base_failed', { room: roomID, error: '暂无可 attach 的任务' })
      return
    }
    setStepBusy(true)
    logRoom('debug', 'attach_confirmed', { room: roomID, request: 'workbench.open', task: selectedRoom.attach.task_id })
    workbench.open({ kind: 'terminal', seq: nextTerminalSeq(workbench.wb), initCommand: selectedRoom.attach.command }, base)
    setStepBusy(false)
    setAttachConfirm(false)
  }

  const content = view === 'list' ? (
    <>
      <PanelHeader title="会话" onCollapse={() => setCollapsed(true)} onDragDown={onTitleDown} />
      <div className="shrink-0 space-y-2 border-b px-3 py-2">
        <div className="flex items-center justify-between gap-2 text-xs">
          <span role="presentation" onClick={() => setNeedsOnly((current) => !current)} className={`cursor-pointer ${needsOnly ? 'font-semibold text-amber-700' : 'text-muted-foreground'}`}>⚑ 需要你 {needRoomIDs.size}</span>
          <span className="text-muted-foreground">{roomsPoll.data === null ? '读取中' : `${visible.length} 个会话`}</span>
        </div>
        <div className="flex items-center gap-2 text-xs">
          <span className="whitespace-nowrap text-muted-foreground">▦ 全部项目 ∨</span>
          <select aria-label="项目" value={project} onChange={(event) => setProject(event.target.value)} className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1">
            <option value="">全部项目</option>
            {projectOptions.map((name) => <option key={name} value={name}>{name}</option>)}
          </select>
        </div>
      </div>
      {(roomsPoll.disconnected || roomsPoll.sessionExpired || inboxPoll.disconnected || inboxPoll.sessionExpired) && (
        <div className="shrink-0 space-y-1 border-b bg-amber-50 px-3 py-2 text-xs text-amber-800">
          {roomsPoll.disconnected && <p role="alert">会话列表已断开：{roomsPoll.errorText}</p>}
          {roomsPoll.sessionExpired && <p role="alert">会话已过期，请重新登录。</p>}
          {inboxPoll.disconnected && <p role="alert">收件箱已断开：{inboxPoll.errorText}</p>}
          {inboxPoll.sessionExpired && <p role="alert">收件箱会话已过期，请重新登录。</p>}
          <button type="button" className="underline" onClick={() => { roomsPoll.refresh(); inboxPoll.refresh() }}>重试</button>
        </div>
      )}
      <div className="relative min-h-0 flex-1 overflow-y-auto p-2">
        {roomsPoll.data === null && !roomsPoll.disconnected && !roomsPoll.sessionExpired ? <p className="p-2 text-sm text-muted-foreground">正在读取…</p> : visible.length === 0 && roomsPoll.data !== null ? <p className="p-2 text-sm text-muted-foreground">（暂无会话）</p> : visible.map((room) => <ListRow key={room.id} room={room} needsReply={roomNeedsReply(room, needRoomIDs)} onOpen={openRoom} />)}
      </div>
    </>
  ) : view === 'room' ? (
    <>
      <header onPointerDown={onTitleDown} className="flex shrink-0 cursor-move select-none items-center gap-2 border-b px-3 py-2.5">
        <button type="button" aria-label="返回会话列表" onPointerDown={(event) => event.stopPropagation()} onClick={backToList} className="rounded-md px-1.5 py-1 text-sm hover:bg-accent">‹</button>
        <span className="min-w-0 flex-1 truncate text-sm font-semibold">{selectedRoom?.title ?? roomID}</span>
        <button type="button" aria-label="更多" onPointerDown={(event) => event.stopPropagation()} onClick={() => setView('detail')} className="rounded-md px-2 py-1 text-xs hover:bg-accent">•••</button>
      </header>
      {(historyPoll.disconnected || historyPoll.sessionExpired || readError !== '') && <p role="alert" className="shrink-0 border-b bg-amber-50 px-3 py-1.5 text-xs text-amber-800">{readError !== '' ? `已读失败：${readError}` : historyPoll.sessionExpired ? '消息会话已过期，请重新登录。' : `消息流已断开：${historyPoll.errorText}`}</p>}
      <div className="relative min-h-0 flex-1 space-y-2 overflow-y-auto bg-slate-50/60 p-3">
        {historyPoll.data === null && !historyPoll.disconnected && !historyPoll.sessionExpired ? <p className="text-sm text-muted-foreground">正在读取…</p> : history.length === 0 ? <p className="text-sm text-muted-foreground">（还没有消息）</p> : history.map((event) => <MessageBubble key={event.seq} event={event} />)}
      </div>
      <footer className="shrink-0 border-t bg-background p-2.5">
        {selectedRoom?.read_only && <p className="mb-1.5 text-xs text-amber-700">房间只读，不能发送消息。</p>}
        <div className="flex items-end gap-2 rounded-2xl border bg-background p-1.5">
          <textarea aria-label="发送消息" value={draft} onChange={(event) => setDraft(event.target.value)} disabled={selectedRoom?.read_only || historyPoll.disconnected || historyPoll.sessionExpired} rows={2} className="min-w-0 flex-1 resize-none border-0 bg-transparent px-1.5 py-1 text-sm outline-none" placeholder={selectedRoom?.read_only ? '' : '发消息…'} />
          <button type="button" onClick={() => void send()} disabled={selectedRoom?.read_only || historyPoll.disconnected || historyPoll.sessionExpired || stepBusy} className="rounded-xl bg-slate-900 px-3 py-1.5 text-xs text-white disabled:opacity-50">发送</button>
        </div>
        {sendError !== '' && <p role="alert" className="mt-1 text-xs text-destructive">{sendError}</p>}
      </footer>
    </>
  ) : (
    <>
      <header onPointerDown={onTitleDown} className="flex shrink-0 cursor-move select-none items-center gap-2 border-b px-3 py-2.5">
        <button type="button" aria-label="返回房间" onPointerDown={(event) => event.stopPropagation()} onClick={() => setView('room')} className="rounded-md px-1.5 py-1 text-sm hover:bg-accent">‹</button>
        <span className="min-w-0 flex-1 truncate text-sm font-semibold">房间详情</span>
        <button type="button" aria-label="更多" onPointerDown={(event) => event.stopPropagation()} onClick={() => setView('list')} className="rounded-md px-2 py-1 text-xs hover:bg-accent">•••</button>
      </header>
      <div className="relative min-h-0 flex-1 overflow-y-auto p-3">
        <section className="rounded-2xl border bg-white/65 p-3 shadow-sm" aria-label="协调者">
          <div className="flex items-center gap-2">
            <button type="button" aria-label="房间头像" onClick={openAttachConfirm} disabled={!selectedRoom?.attach} title={!selectedRoom?.attach ? '暂无可 attach 的任务' : undefined} className="flex size-10 items-center justify-center rounded-full bg-slate-200 text-xs font-semibold disabled:opacity-50">{selectedRoom ? roomInitials(selectedRoom) : '?'}</button>
            <div className="min-w-0"><h3 className="text-sm font-semibold">协调者</h3><p className="truncate font-mono text-[11px] text-muted-foreground">{selectedRoom?.bound_session || '未绑定会话'}</p></div>
            <span className={`ml-auto text-xs ${selectedRoom?.live ? 'text-green-600' : 'text-muted-foreground'}`}>{selectedRoom?.live ? '在线' : '离线'}</span>
          </div>
          <div className="mt-3 space-y-2 border-t pt-3 text-xs">
            <p className="text-muted-foreground">任务工作目录：{selectedRoom?.attach?.work_dir ?? '暂无可 attach 的任务'}</p>
            <button type="button" aria-label="attach" disabled={!selectedRoom?.attach || stepBusy} title={!selectedRoom?.attach ? '暂无可 attach 的任务' : undefined} onClick={openAttachConfirm} className="rounded-md border px-2.5 py-1.5 hover:bg-accent disabled:cursor-not-allowed disabled:opacity-50">attach</button>
            {!selectedRoom?.attach && <p className="text-muted-foreground">暂无可 attach 的任务</p>}
          </div>
        </section>
        {selectedRoom?.kind === 'card' && (
          <section className="mt-3 rounded-2xl border bg-white/65 p-3 shadow-sm" aria-label="卡片">
            <div className="flex items-center gap-2">
              <h3 className="text-sm font-semibold">卡片</h3>
              <button type="button" aria-label={`打开卡片 ${selectedRoom.id}`} onClick={() => { logRoom('debug', 'card_open_requested', { room: selectedRoom.id, view: 'detail' }); onOpenCard?.(selectedRoom.id) }} className="ml-auto rounded-md border px-2 py-1 text-xs hover:bg-accent">打开</button>
            </div>
            <p className="mt-2 truncate text-sm font-medium">{cardDetail?.card.title ?? selectedRoom.title}</p>
            <div className="mt-2 flex flex-wrap gap-1.5 text-[11px]">
              <span className="rounded-full border px-2 py-0.5">{cardDetail?.card.status ?? '状态未知'}</span>
              <span className="rounded-full border px-2 py-0.5">{cardDetail?.card.priority ?? '优先级未知'}</span>
              {(cardDetail?.card.attachments ?? []).map((attachment) => <span key={`${attachment.kind}:${attachment.path}`} className="rounded-full border px-2 py-0.5">{attachment.kind}</span>)}
            </div>
            {cardDetailError !== '' && <p role="alert" className="mt-2 text-xs text-destructive">卡片信息读取失败：{cardDetailError}</p>}
          </section>
        )}
      </div>
    </>
  )

  return (
    <>
      {(!persistent || collapsed) && <button type="button" aria-label="打开房间面板" title="打开房间面板" onClick={() => setCollapsed((current) => !current)} className="fixed bottom-[104px] right-5 z-40 flex size-11 items-center justify-center rounded-full bg-slate-900 text-white shadow-lg">◌</button>}
      {!collapsed && (persistent
        ? <aside data-testid="room-panel" className="relative flex h-full min-h-0 w-[360px] shrink-0 flex-col overflow-hidden border-l bg-background">{content}</aside>
        : panelGeom !== null && <aside data-testid="room-panel" className="fixed z-40 flex flex-col overflow-hidden rounded-2xl border bg-background shadow-xl" style={{ left: panelGeom.x, top: panelGeom.y, width: panelGeom.w, height: panelGeom.h }}>{content}<span data-testid="room-panel-corner" onPointerDown={onCornerDown} aria-hidden="true" className="absolute bottom-0 right-0 size-[15px] cursor-nwse-resize" style={{ background: 'linear-gradient(135deg, transparent 50%, #71717a 50%)' }} /></aside>)}
      <ConfirmDialog open={attachConfirm} title="确认 attach" description={selectedRoom?.attach ? `${selectedRoom.attach.task_id} · ${selectedRoom.attach.work_dir}\n将在对应工作目录打开终端。` : '暂无可 attach 的任务'} confirmLabel="确认 attach" busy={stepBusy} onConfirm={confirmAttach} onCancel={() => setAttachConfirm(false)} />
    </>
  )
}
