// SessionRoleStatusBar consumes bounded role facts and existing task/room APIs.
// It never infers liveness, duplicates ticket state, or claims a wake succeeded.
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import type { MouseEvent, ReactNode } from 'react'
import { fetchTaskDetail, replyTicket } from '../../api/client'
import type { Ticket } from '../../api/types'
import { sendRoomMessage } from '../../api/rooms'
import type { SessionRoleStatus, SessionRoleTicketRef, SessionRoleStatusProjection, SessionSummary } from '../../api/rooms'
import { TicketsPanel } from '../task/TicketsPanel'
import { errorMessage } from '../lib/format'
import { logRoom } from './roomLog'
import './session-role-status.css'

type ResolvedTicket = { ticket: Ticket; ref: SessionRoleTicketRef; role: SessionRoleStatus }
type TicketFailure = { taskId: string; error: string }
type TicketState = { resolved: ResolvedTicket[]; failures: TicketFailure[]; verifiedTaskIds: string[]; loading: boolean }
type Density = { miniIds: string[]; noDurationIds: string[]; foldedIds: string[] }

const stateLabel: Record<SessionRoleStatus['state'], string> = {
  needs_you: '需要你', exception: '异常', waiting: '等待中', running: '运行中', idle: '空闲', unknown: '未知',
}
const stateRank: Record<SessionRoleStatus['state'], number> = {
  needs_you: 0, exception: 1, waiting: 2, running: 3, idle: 4, unknown: 5,
}

function roleName(role: SessionRoleStatus): string {
  if (role.kind === 'resident') return '驻场者'
  if (role.kind === 'coordinator') return `协调者${role.card_id ? ` · ${role.card_id}` : ''}`
  const task = role.task_id ? ` · ${role.task_id.slice(0, 8)}` : ''
  return `执行者${task}`
}
function stateClass(state: SessionRoleStatus['state']): string {
  return ({ needs_you: 's-need', exception: 's-err', waiting: 's-wait', running: 's-run', idle: 's-idle', unknown: 's-unk' })[state]
}
function stateIcon(state: SessionRoleStatus['state']): ReactNode {
  if (state === 'needs_you') return <svg viewBox="0 0 16 16" aria-hidden="true" focusable="false"><path d="M2.1 3.1h11.8v8H7.1l-3.3 2v-2H2.1z" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinejoin="round" /><circle cx="5.2" cy="7.1" r=".65" fill="currentColor" /><circle cx="8" cy="7.1" r=".65" fill="currentColor" /><circle cx="10.8" cy="7.1" r=".65" fill="currentColor" /></svg>
  return ({ exception: '▲', waiting: '◷', running: '●', idle: '○', unknown: '◌' })[state]
}
function formatElapsed(since?: string): string {
  if (!since) return '时间未记录'
  const start = Date.parse(since)
  const now = Date.now()
  if (!Number.isFinite(start) || start <= 0 || start > now) return '时间未记录'
  const minutes = Math.floor((now - start) / 60_000)
  if (minutes < 1) return '不到 1 分钟'
  if (minutes < 60) return `${minutes} 分钟`
  const hours = Math.floor(minutes / 60)
  return minutes % 60 ? `${hours} 小时 ${minutes % 60} 分钟` : `${hours} 小时`
}
function compareSince(a?: string, b?: string): number {
  const parsedA = a ? Date.parse(a) : Number.NaN
  const parsedB = b ? Date.parse(b) : Number.NaN
  const validA = Number.isFinite(parsedA) && parsedA > 0 && parsedA <= Date.now()
  const validB = Number.isFinite(parsedB) && parsedB > 0 && parsedB <= Date.now()
  if (validA && validB && parsedA !== parsedB) return parsedA - parsedB
  if (validA !== validB) return validA ? -1 : 1
  return 0
}
function refKey(ref: SessionRoleTicketRef): string { return `${ref.task_id}\u0000${ref.ticket_id}` }

export function SessionRoleStatusBar({ sessionId, projection, archived, summary, unavailable = false, unavailableReason = '', onOpenCard, onRefresh }: {
  sessionId: string
  projection?: SessionRoleStatusProjection
  archived: boolean
  summary: SessionSummary | null
  unavailable?: boolean
  unavailableReason?: string
  onOpenCard?: (cardId: string) => void
  onRefresh?: () => void
}) {
  const rawRoles = projection?.roles ?? []
  const observedAt = projection?.observed_at ?? ''
  const refs = useMemo(() => rawRoles.flatMap((role) => (role.ticket_refs ?? []).map((ref) => ({ ref, role }))), [rawRoles])
  const refSignature = refs.map(({ ref }) => refKey(ref)).join('|')
  const [ticketState, setTicketState] = useState<TicketState>({ resolved: [], failures: [], verifiedTaskIds: [], loading: false })
  const [density, setDensity] = useState<Density>({ miniIds: [], noDurationIds: [], foldedIds: [] })
  const [moreOpen, setMoreOpen] = useState(false)
  const [openRole, setOpenRole] = useState<string | null>(null)
  const [ticketOpen, setTicketOpen] = useState(false)
  const [actionError, setActionError] = useState('')
  const [actionBusy, setActionBusy] = useState(false)
  const [wakeFeedback, setWakeFeedback] = useState<Record<string, string>>({})
  const stripRef = useRef<HTMLDivElement>(null)
  const leadRef = useRef<HTMLDivElement>(null)
  const rosterRef = useRef<HTMLDivElement>(null)
  const chipsRef = useRef<HTMLDivElement>(null)
  const moreRef = useRef<HTMLButtonElement>(null)
  const roleRestoreFocusRef = useRef<HTMLElement | null>(null)
  const ticketRestoreFocusRef = useRef<HTMLElement | null>(null)
  const roleDialogRef = useRef<HTMLElement | null>(null)
  const ticketDialogRef = useRef<HTMLElement | null>(null)
  const focusedDialogRef = useRef<'role' | 'ticket' | null>(null)

  function closeRole() {
    setOpenRole(null)
    requestAnimationFrame(() => roleRestoreFocusRef.current?.focus())
  }
  function closeTicketDrawer() {
    focusedDialogRef.current = openRole ? 'role' : null
    setTicketOpen(false)
    requestAnimationFrame(() => ticketRestoreFocusRef.current?.focus())
  }

  const ticketVersion = `${projection?.observed_at ?? ''}|${refSignature}`
  useEffect(() => {
    let cancelled = false
    if (!refs.length) { setTicketState({ resolved: [], failures: [], verifiedTaskIds: [], loading: false }); return }
    setTicketState((current) => ({ ...current, loading: true }))
    const taskRefs = new Map<string, typeof refs>()
    for (const item of refs) taskRefs.set(item.ref.task_id, [...(taskRefs.get(item.ref.task_id) ?? []), item])
    void Promise.all([...taskRefs.entries()].map(async ([taskId, items]) => {
      try {
        const detail = await fetchTaskDetail(taskId)
        const byTicket = new Map(detail.pending_tickets.map((ticket) => [ticket.id, ticket]))
        return { taskId, ok: true, resolved: items.flatMap(({ ref, role }) => {
          const ticket = byTicket.get(ref.ticket_id)
          return ticket && ticket.task_id === taskId ? [{ ticket, ref, role }] : []
        }), failures: [] as TicketFailure[] }
      } catch (error) {
        logRoom('warn', 'session_role_ticket_read_failed', { session: sessionId, task: taskId, error: errorMessage(error) })
        return { taskId, ok: false, resolved: [] as ResolvedTicket[], failures: [{ taskId, error: errorMessage(error) }] }
      }
    })).then((groups) => {
      if (!cancelled) setTicketState((previous) => {
        const previousByKey = new Map(previous.resolved.map((item) => [refKey(item.ref), item]))
        const merged = groups.flatMap((group) => {
          if (group.ok) return group.resolved
          const currentRefs = taskRefs.get(group.taskId) ?? []
          return currentRefs.flatMap(({ ref, role }) => {
            const stale = previousByKey.get(refKey(ref))
            return stale ? [{ ...stale, ref, role }] : []
          })
        })
        return { resolved: merged, failures: groups.flatMap((group) => group.failures), verifiedTaskIds: groups.filter((group) => group.ok).map((group) => group.taskId), loading: false }
      })
    })
    return () => { cancelled = true }
  }, [sessionId, ticketVersion])

  const candidateKeys = new Set(refs.map(({ ref }) => refKey(ref)))
  const failedTasks = new Set(ticketState.failures.map((failure) => failure.taskId))
  const verifiedTasks = new Set(ticketState.verifiedTaskIds)
  const liveTicketState = ticketState.resolved.filter((item) => candidateKeys.has(refKey(item.ref)) && verifiedTasks.has(item.ticket.task_id) && !failedTasks.has(item.ticket.task_id))
  const roles = useMemo(() => rawRoles.map((role) => {
    if (unavailable) return { ...role, state: 'unknown' as const, conclusion: '状态信息暂不可确认', can_request_wake: false }
    const matching = liveTicketState.filter((item) => item.role.id === role.id)
    const roleRefs = refs.filter(({ role: refRole }) => refRole.id === role.id)
    const roleTasks = [...new Set(roleRefs.map(({ ref }) => ref.task_id))]
    const roleVerified = roleTasks.length > 0 && roleTasks.every((taskId) => verifiedTasks.has(taskId) && !failedTasks.has(taskId))
    if (matching.length > 0 && !archived) return { ...role, state: 'needs_you' as const, conclusion: `有 ${matching.length} 项需要你处理` }
    if (roleVerified && role.state === 'needs_you' && matching.length === 0 && (role.ticket_refs?.length ?? 0) > 0) {
      return { ...role, state: 'unknown' as const, conclusion: '待处理事项已变化，正在等待状态刷新' }
    }
    if (!roleVerified && (role.ticket_refs?.length ?? 0) > 0 && role.state === 'needs_you') {
      return { ...role, state: 'unknown' as const, conclusion: '待处理事项暂时无法确认' }
    }
    if (unavailable || archived) return { ...role, can_request_wake: false }
    return role
  }), [rawRoles, unavailable, liveTicketState, refs, verifiedTasks, failedTasks, archived])
  const ordered = [...roles].sort((a, b) => stateRank[a.state] - stateRank[b.state] || compareSince(a.since, b.since) || a.id.localeCompare(b.id))
  const current = ordered[0]
  const actionableTicketState = unavailable || archived ? [] : liveTicketState
  const ticketCount = actionableTicketState.length
  const hiddenRoles = ordered.filter((role) => density.foldedIds.includes(role.id))
  const roleById = new Map(roles.map((role) => [role.id, role]))
  const ticketRoleNames = [...new Set(actionableTicketState.map((item) => roleName(roleById.get(item.role.id) ?? item.role)))]
  const selected = openRole ? roleById.get(openRole) ?? null : null
  const visibleTickets = ticketState.resolved.filter((item) => candidateKeys.has(refKey(item.ref))).map((item) => item.ticket)
  const ticketIsLive = (ticket: Ticket) => actionableTicketState.some((item) => item.ticket.id === ticket.id && item.ticket.task_id === ticket.task_id)

  // Measure available width and progressively compact low-urgency chips; exception chips
  // are exempt from folding, while needs-you chips are represented by the lead CTA.
  useLayoutEffect(() => {
    const strip = stripRef.current
    const lead = leadRef.current
    const roster = rosterRef.current
    const box = chipsRef.current
    if (!strip || !lead || !roster || !box) return
    const fit = () => {
      const nodes = [...box.querySelectorAll<HTMLElement>('[data-role-chip]')]
      const more = moreRef.current
      nodes.forEach((node) => { node.hidden = false; node.classList.remove('mini', 'nodur', 'folded') })
      if (more) more.hidden = true
      const commitDensity = (next: Density) => setDensity((previous) =>
        JSON.stringify(previous) === JSON.stringify(next) ? previous : next)
      if (box.clientWidth < 1) {
        commitDensity({ miniIds: [], noDurationIds: [], foldedIds: [] })
        return
      }
      const rowWidth = () => nodes.reduce((sum, node) => sum + (node.hidden ? 0 : node.offsetWidth + 4), 0)
      const space = box.clientWidth
      const over = () => rowWidth() > space
      const needs = nodes.filter((node) => node.dataset.state === 'needs_you')
      const miniIds: string[] = []; const noDurationIds: string[] = []; const foldedIds: string[] = []
      if (over() && ticketCount > 0 && needs.length) {
        needs.forEach((node) => { node.hidden = true; node.classList.add('folded'); foldedIds.push(node.dataset.roleChip ?? '') })
      }
      if (!over()) { commitDensity({ miniIds, noDurationIds, foldedIds }); return }
      const quiet = nodes.filter((node) => ['running', 'idle', 'unknown'].includes(node.dataset.state ?? '') && !node.hidden)
      for (const node of quiet) { node.classList.add('mini'); miniIds.push(node.dataset.roleChip ?? ''); if (!over()) break }
      if (!over()) { commitDensity({ miniIds, noDurationIds, foldedIds }); return }
      nodes.filter((node) => !node.hidden).forEach((node) => { node.classList.add('nodur'); noDurationIds.push(node.dataset.roleChip ?? '') })
      if (!over()) { commitDensity({ miniIds, noDurationIds, foldedIds }); return }
      nodes.filter((node) => node.dataset.state === 'waiting' && !node.hidden).forEach((node) => { node.classList.add('mini'); miniIds.push(node.dataset.roleChip ?? '') })
      if (!over()) { commitDensity({ miniIds, noDurationIds, foldedIds }); return }
      for (let index = nodes.length - 1; index >= 0 && over(); index--) {
        const node = nodes[index]
        if (node.hidden || node.dataset.state === 'exception' || (node.dataset.state === 'needs_you' && ticketCount === 0)) continue
        node.hidden = true; foldedIds.push(node.dataset.roleChip ?? '')
        if (more) more.hidden = false
      }
      commitDensity({ miniIds: [...new Set(miniIds)], noDurationIds: [...new Set(noDurationIds)], foldedIds: [...new Set(foldedIds.filter(Boolean))] })
    }
    fit()
    if (typeof ResizeObserver === 'undefined') {
      window.addEventListener('resize', fit)
      return () => window.removeEventListener('resize', fit)
    }
    const observer = new ResizeObserver(fit)
    observer.observe(strip)
    observer.observe(box)
    return () => observer.disconnect()
  }, [roles, ticketCount])

  useEffect(() => {
    if (!openRole && !ticketOpen) return
    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        event.stopPropagation()
        event.stopImmediatePropagation()
        if (ticketOpen) closeTicketDrawer()
        else closeRole()
        return
      }
      if (event.key !== 'Tab') return
      const dialog = ticketOpen ? ticketDialogRef.current : roleDialogRef.current
      if (!dialog) return
      const focusable = [...dialog.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], textarea:not(:disabled), summary, [tabindex="0"]')]
        .filter((node) => !node.hidden)
      if (!focusable.length) return
      const first = focusable[0], last = focusable[focusable.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus() }
    }
    document.addEventListener('keydown', onKeyDown, true)
    return () => document.removeEventListener('keydown', onKeyDown, true)
  }, [openRole, ticketOpen])

  useEffect(() => {
    const layer = ticketOpen ? 'ticket' : openRole ? 'role' : null
    if (!layer) { focusedDialogRef.current = null; return }
    if (focusedDialogRef.current === layer) return
    focusedDialogRef.current = layer
    const dialog = layer === 'ticket' ? ticketDialogRef.current : roleDialogRef.current
    const target = dialog?.querySelector<HTMLElement>('button:not(:disabled), [tabindex="-1"]')
    target?.focus()
  }, [openRole, ticketOpen])

  const openRoleDetail = (id: string, event?: MouseEvent<HTMLElement>) => {
    roleRestoreFocusRef.current = event?.currentTarget.closest('.more-pop') ? moreRef.current : event?.currentTarget ?? document.activeElement as HTMLElement
    setMoreOpen(false); setOpenRole(id)
  }
  const openTicketDrawer = (event?: MouseEvent<HTMLElement>) => {
    ticketRestoreFocusRef.current = event?.currentTarget ?? document.activeElement as HTMLElement
    setTicketOpen(true); setMoreOpen(false)
  }

  const doReply = async (ticket: Ticket, answer: string) => {
    if (archived || unavailable || actionBusy) throw new Error(archived ? '会话已归档，只读，不能处理工单。' : '会话状态暂不可确认，不能处理工单。')
    const relation = actionableTicketState.find((item) => item.ticket.id === ticket.id && item.ticket.task_id === ticket.task_id)
    if (!relation) throw new Error('这张工单已不在当前待处理清单中，请刷新状态。')
    setActionBusy(true); setActionError('')
    logRoom('debug', 'session_role_ticket_reply_started', { session: sessionId, task: ticket.task_id, ticket: ticket.id })
    try {
      await replyTicket(ticket.task_id, { ticket_id: ticket.id, answer })
      logRoom('debug', 'session_role_ticket_reply_succeeded', { session: sessionId, task: ticket.task_id, ticket: ticket.id })
    } catch (error) {
      setActionBusy(false); setActionError(errorMessage(error))
      logRoom('error', 'session_role_ticket_reply_failed', { session: sessionId, task: ticket.task_id, ticket: ticket.id, error: errorMessage(error) })
      throw error
    }
    setTicketState((state) => ({ ...state, resolved: state.resolved.filter((item) => !(item.ticket.task_id === ticket.task_id && item.ticket.id === ticket.id)) }))
    onRefresh?.()
    try {
      const detail = await fetchTaskDetail(ticket.task_id)
      const pending = new Map(detail.pending_tickets.map((item) => [item.id, item]))
      setTicketState((state) => ({ ...state, resolved: state.resolved.flatMap((item) => {
        if (item.ticket.task_id !== ticket.task_id) return [item]
        const next = pending.get(item.ticket.id)
        return next ? [{ ...item, ticket: next }] : []
      }) }))
    } catch (error) {
      setActionError(`答复已提交，但刷新待处理事项失败：${errorMessage(error)}`)
      logRoom('warn', 'session_role_ticket_refresh_failed', { session: sessionId, task: ticket.task_id, error: errorMessage(error) })
    } finally { setActionBusy(false) }
  }

  const requestWake = async (role: SessionRoleStatus) => {
    if (!role.card_id || archived || unavailable || actionBusy) return
    setActionBusy(true); setActionError('')
    logRoom('debug', 'session_role_handoff_request_started', { session: sessionId, card: role.card_id })
    try {
      await sendRoomMessage(sessionId, `请接续卡 ${role.card_id}。`, { mentions: [role.card_id] })
      const mode = summary?.cards?.find((card) => card.card_id === role.card_id)?.seat_mode
      setWakeFeedback((current) => ({ ...current, [role.id]: `请求已发送；${mode === 'push' ? '系统会尝试通知当前席位' : mode === 'pull' ? '当前席位需主动查看卡' : '席位接收方式未知'}，尚未确认接续已开始。` }))
      logRoom('debug', 'session_role_handoff_request_succeeded', { session: sessionId, card: role.card_id, seat_mode: mode ?? 'unknown' })
      onRefresh?.()
    } catch (error) {
      setActionError(errorMessage(error))
      logRoom('error', 'session_role_handoff_request_failed', { session: sessionId, card: role.card_id, error: errorMessage(error) })
    } finally { setActionBusy(false) }
  }

  const bannerRole = current
  const effectiveRoleStateClass = (state: SessionRoleStatus['state']) => stateClass(state)

  return <div className="role-status">
    <div className="role-strip" ref={stripRef} role="region" aria-label="会话角色状态">
      <div className="strip-lead" ref={leadRef}>
        {ticketCount > 0 && <button type="button" className="need-cta" title={`需要你处理：${ticketRoleNames.join('、')}`} onClick={(event) => openTicketDrawer(event)} disabled={archived || unavailable} aria-label={`需要你处理 ${ticketCount} 件事，打开待处理事项`}>
          <span className="st-ic" aria-hidden="true">{stateIcon('needs_you')}</span>需要你处理<b>{ticketCount}</b>
        </button>}
        {unavailable && <span role="status" className="unavailable-note">状态读取失败{unavailableReason ? `：${unavailableReason}` : ''}；{projection ? '角色状态未知，保留最后成功观测。' : '尚无成功观测。'}</span>}
        {!unavailable && !projection && <span className="unavailable-note">此服务暂不支持角色状态。</span>}
        {projection && roles.length === 0 && <span className="unavailable-note">当前没有可展示的角色状态。</span>}
        {roles.length > 0 && !roles.some((role) => role.kind === 'coordinator') && <span className="unavailable-note">本会话尚未观察到卡协调者。</span>}
        {ticketState.loading && refs.length > 0 && <span className="unavailable-note">正在核对待处理事项…</span>}
        {ticketState.failures.length > 0 && <><span role="status" className="unavailable-note">待处理事项暂时无法确认</span><button type="button" className="reason-link" onClick={(event) => openTicketDrawer(event)}>查看原因</button></>}
        {bannerRole && <button type="button" className={`banner ${effectiveRoleStateClass(bannerRole.state)} ${['running', 'idle', 'unknown'].includes(bannerRole.state) ? 'quiet' : ''}`}
          title={`${roleName(bannerRole)} · ${stateLabel[bannerRole.state]} · ${formatElapsed(bannerRole.since)} · ${bannerRole.conclusion}`}
          onClick={(event) => openRoleDetail(bannerRole.id, event)}>
          <span className="st-ic" aria-hidden="true">{stateIcon(bannerRole.state)}</span><span className="banner-text">{roleName(bannerRole)}：{bannerRole.conclusion}</span>
        </button>}
      </div>
      <div className="strip-roster" ref={rosterRef}>
        <div className="chips" ref={chipsRef}>
          {ordered.map((role) => <button key={role.id} type="button" data-role-chip={role.id} data-state={role.state} title={`${roleName(role)} · ${stateLabel[role.state]} · ${formatElapsed(role.since)} · ${role.conclusion}`}
            aria-label={`${roleName(role)}，${stateLabel[role.state]}，${formatElapsed(role.since)}。查看详情`}
            hidden={density.foldedIds.includes(role.id)}
            className={`chip ${stateClass(role.state)} ${density.miniIds.includes(role.id) ? 'mini' : ''} ${density.noDurationIds.includes(role.id) ? 'nodur' : ''}`}
            onClick={(event) => openRoleDetail(role.id, event)}>
            <span className="av" aria-hidden="true">{role.kind === 'coordinator' ? '协' : role.kind === 'resident' ? '驻' : '执'}<span className="badge"><span className="st-ic">{stateIcon(role.state)}</span></span></span>
            <span className="chip-tag" aria-hidden="true">{role.kind === 'coordinator' ? role.card_id?.slice(-2) ?? '' : role.kind === 'executor' ? role.task_id?.slice(-2) ?? '' : ''}</span>
            <span className="chip-name">{roleName(role)}</span><span className="chip-state">{stateLabel[role.state]}</span><span className="chip-dur">{formatElapsed(role.since)}</span>
          </button>)}
        </div>
        {hiddenRoles.length > 0 && <button ref={moreRef} type="button" className="more" aria-haspopup="true" aria-expanded={moreOpen} aria-label={`还有 ${hiddenRoles.length} 个角色，查看全部`}
          onClick={() => { setMoreOpen((value) => !value); if (moreOpen) requestAnimationFrame(() => moreRef.current?.focus()) }}>+{hiddenRoles.length}{hiddenRoles.some((role) => role.state === 'exception') && <span className="more-dot err" aria-hidden="true" />}{hiddenRoles.some((role) => role.state === 'needs_you') && <span className="more-dot need" aria-hidden="true" />}</button>}
      </div>
      {observedAt && Number.isFinite(Date.parse(observedAt)) && <span className="observation-time">最后观测 {new Date(observedAt).toLocaleTimeString()}</span>}
    </div>
    {moreOpen && <div className="more-pop" role="menu" aria-label="全部会话角色">
      <div className="pop-head">全部角色<span>按紧急度排序 · {ordered.length} 个</span></div>
      {ordered.map((role) => <button key={role.id} type="button" role="menuitem" className={`pop-row ${stateClass(role.state)}`} onClick={(event) => openRoleDetail(role.id, event)}>
        <span className="av" aria-hidden="true">{role.kind === 'coordinator' ? '协' : role.kind === 'resident' ? '驻' : '执'}</span>
        <span className="pr-name">{roleName(role)}</span><span className="pr-state">{stateIcon(role.state)} {stateLabel[role.state]} · {formatElapsed(role.since)}</span>
        <span className="pr-note">{role.conclusion}</span>
      </button>)}
    </div>}

    {selected && createPortal(<div className="role-status role-status-portal"><div className="role-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) closeRole() }}>
      <section ref={roleDialogRef} tabIndex={-1} role="dialog" aria-modal="true" aria-labelledby="role-status-title" className={`role-dialog ${stateClass(selected.state)}`}>
        <header className="dlg-head"><div className={`av lg ${selected.kind === 'coordinator' ? 'card' : ''}`} aria-hidden="true">{selected.kind === 'coordinator' ? '协' : selected.kind === 'resident' ? '驻' : '执'}<span className="badge"><span className="st-ic">{stateIcon(selected.state)}</span></span></div>
          <div className="dlg-title"><h2 id="role-status-title">{roleName(selected)}</h2><div className="dlg-sub"><span className={`state-pill ${stateClass(selected.state)}`}><span className="st-ic">{stateIcon(selected.state)}</span>{stateLabel[selected.state]}</span><span>{formatElapsed(selected.since)}</span></div></div>
          <button type="button" className="icon-x" aria-label="关闭角色详情（Esc）" onClick={closeRole}>×</button>
        </header>
        <div className="dlg-body">
          <p className="verdict">{selected.conclusion}</p>
          <section className="dsec"><h3>原因</h3><p>{selected.reason || '没有更多原因记录。'}{selected.waiting_for ? ` · 等待：${selected.waiting_for}` : ''}</p></section>
          <section className="dsec"><h3>你可以做什么</h3><p>{archived ? '会话已归档，只读，可查看历史。' : unavailable ? '当前状态读取失败，恢复读取后再操作。' : selected.state === 'exception' ? '查看卡与最近证据；接续失败需要协调者处理。' : selected.state === 'needs_you' ? '打开待处理事项并提交决定。' : selected.can_request_wake ? '可发送接续请求；发送成功不代表席位已接手。' : '目前没有可执行操作。'}</p>
            <div className="acts">
              {selected.card_id && onOpenCard && <button type="button" className="btn" onClick={() => { onOpenCard(selected.card_id!); closeRole() }}>打开卡 {selected.card_id}</button>}
              {selected.ticket_refs?.length ? <button type="button" className="btn need" onClick={(event) => openTicketDrawer(event)}>查看待处理事项</button> : null}
              {selected.can_request_wake && selected.card_id && <button type="button" className="btn primary" disabled={archived || unavailable || actionBusy} onClick={() => void requestWake(selected)}>请求接续</button>}
            </div>
            {wakeFeedback[selected.id] && <p role="status" className="feedback ok">{wakeFeedback[selected.id]}</p>}
            {actionError && <p role="alert" className="feedback">{actionError}</p>}
          </section>
          <section className="dsec"><h3>最近动态</h3>
            {(selected.recent ?? []).length ? <ol className="feed">{[...(selected.recent ?? [])].sort((a, b) => (Date.parse(b.time) || 0) - (Date.parse(a.time) || 0)).slice(0, 5).map((item, index) => <li key={`${item.source}:${item.seq}:${index}`} className={item.tone === 'failed' ? 'f-err' : item.tone === 'needs_you' ? 'f-need' : ''}>
              <time>{Number.isFinite(Date.parse(item.time)) ? new Date(item.time).toLocaleTimeString() : '时间未记录'}</time><span><span className="st-ic" aria-hidden="true">{item.tone === 'failed' ? '▲' : item.tone === 'needs_you' ? '●' : ''}</span>{item.summary}</span>
            </li>)}</ol> : <p className="feed-note">未提供最近动态。</p>}
            {selected.more_activity && <p className="feed-note">还有更早动态未显示。</p>}
          </section>
          <p className="dmeta">{unavailable ? `来源读取失败${unavailableReason ? `：${unavailableReason}` : ''}` : `最后观测于 ${observedAt && Number.isFinite(Date.parse(observedAt)) ? new Date(observedAt).toLocaleString() : '时间未记录'}`}</p>
          <details className="tech"><summary>技术细节</summary><div className="tech-body"><dl>
            {selected.card_id && <><dt>卡</dt><dd>{selected.card_id}</dd></>}{selected.task_id && <><dt>任务</dt><dd>{selected.task_id}</dd></>}
            {selected.target && <><dt>执行目标</dt><dd>{selected.target}</dd></>}
            <dt>角色 ID</dt><dd>{selected.id}</dd>
          </dl>{(selected.recent ?? []).length > 0 && <><h4>最近动态来源标识</h4><ol>{(selected.recent ?? []).slice(0, 5).map((item, index) => <li key={`${item.source}:${item.seq}:${index}`}><time>{item.time}</time><span>{item.source} · seq {item.seq}</span></li>)}</ol></>}</div></details>
        </div>
      </section>
    </div></div>, document.body)}

    {ticketOpen && createPortal(<div className="role-status role-status-portal"><div className="ticket-backdrop" onMouseDown={(event) => { if (event.target === event.currentTarget) closeTicketDrawer() }}>
      <aside ref={ticketDialogRef} tabIndex={-1} role="dialog" aria-modal="true" aria-label="待你处理" className="ticket-sheet">
        <header className="sheet-head"><div className="row"><h2>待你处理</h2><span className={`count ${ticketCount ? '' : 'zero'}`}>{ticketCount}</span><button type="button" className="icon-x" aria-label="关闭待处理事项（Esc）" onClick={closeTicketDrawer}>×</button></div>
          {ticketState.failures.length > 0 && <p className="crumb">部分工单详情读取失败，因此未计入数量。</p>}</header>
        <div className="sheet-body">
          {archived && <p className="mb-2 text-xs text-amber-800">会话已归档，只读；可查看，不能回答。</p>}
          {unavailable && <p className="mb-2 text-xs text-amber-800">会话状态来源读取失败，只能查看最后成功取得的待办；操作已禁用。</p>}
          {ticketState.failures.map((failure) => <p key={failure.taskId} role="alert" className="tk-err">任务 {failure.taskId} 详情读取失败：{failure.error}。该任务未计入待你处理数量。</p>)}
          {actionError && <p role="alert" className="tk-err">{actionError}</p>}
          {ticketState.loading && <p className="mb-2 text-xs text-muted-foreground">正在读取完整待处理事项…</p>}
          {ticketState.failures.length > 0 && visibleTickets.length > 0 && <p className="mb-2 text-xs text-amber-800">失败任务仍显示上次成功读取的内容；未失败的已核验工单仍可处理。</p>}
          <TicketsPanel tickets={visibleTickets} disabled={(ticket) => archived || unavailable || actionBusy || !ticketIsLive(ticket)} onReply={doReply} />
        </div>
      </aside>
    </div></div>, document.body)}
  </div>
}
