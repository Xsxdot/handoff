import { useEffect, useMemo, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { answerDecision, fetchCardDetail, moveCard, noteCard } from '../../api/ledger'
import type { CardDetail, Decision, LedgerEvent } from '../../api/ledger'
import { errorMessage } from '../lib/format'
import { boardColumns } from './columns'

type Relation = { From: string; To: string; Type: string }

// CardDecisions 是挂卡裁决的呈现与答复区。
//
// why 它必须在抽屉里而不是只躺在 timeline：请示的候选项与答复入口以前完全
// 没有呈现面——卡上只显示一个「裁决 N」角标，点进抽屉也只在 timeline 里剩
// 一行原文，看不到选什么、也没法答（2026-08-19 真机看到）。项目级裁决走顶部
// 收件箱横幅，挂卡的走这里，两条路都能答复。
//
// 答复成功后调 onAnswered 让抽屉重取详情：答案要立刻落到这一处，不能等轮询。
function CardDecisions({ decisions, onAnswered }: { decisions: Decision[]; onAnswered: () => void }) {
  const [drafts, setDrafts] = useState<Record<number, string>>({})
  const [busy, setBusy] = useState<number | null>(null)
  const [error, setError] = useState('')
  const submit = async (decision: Decision) => {
    const text = (drafts[decision.id] ?? '').trim()
    if (!text) return
    setBusy(decision.id)
    setError('')
    try {
      await answerDecision(decision.id, text)
      setDrafts((current) => ({ ...current, [decision.id]: '' }))
      onAnswered()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }
  return (
    <section className="mb-5">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">⚖ 裁决</h3>
      {decisions.map((decision) => {
        const open = decision.status === 'open'
        return (
          <div key={decision.id} className={`mb-1.5 rounded-md border px-2.5 py-2 text-xs ${open ? 'border-amber-200 bg-amber-50 text-amber-900' : ''}`}>
            <div className="flex gap-2"><span className="font-mono shrink-0">#{decision.id}</span><span className="min-w-0 flex-1 break-words">{decision.body}</span></div>
            {(decision.options ?? []).length > 0 && (
              <div className="mt-1.5 flex flex-wrap gap-1">
                {(decision.options ?? []).map((option) => (
                  <button key={option} type="button" disabled={!open} onClick={() => setDrafts((current) => ({ ...current, [decision.id]: option }))}
                    className="rounded-full border px-2 py-0.5 text-[11px] disabled:opacity-60">{option}</button>
                ))}
              </div>
            )}
            {open ? (
              <div className="mt-1.5 flex items-center gap-2">
                <input value={drafts[decision.id] ?? ''} onChange={(event) => setDrafts((current) => ({ ...current, [decision.id]: event.target.value }))}
                  placeholder="答复这条请示…" className="min-w-0 flex-1 rounded border bg-background px-2 py-1 text-xs" />
                <button type="button" disabled={busy === decision.id || !(drafts[decision.id] ?? '').trim()} onClick={() => void submit(decision)}
                  className="rounded border px-2 py-1 text-xs disabled:opacity-50">答复</button>
              </div>
            ) : (
              <p className="mt-1.5 text-muted-foreground">已答复：{decision.answer}</p>
            )}
          </div>
        )
      })}
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </section>
  )
}

type AnyRecord = Record<string, unknown>

function record(value: unknown): AnyRecord {
  return value !== null && typeof value === 'object' ? value as AnyRecord : {}
}

function value<T>(source: unknown, key: string, fallback: T): T {
  const data = record(source)
  const direct = data[key]
  if (direct !== undefined) return direct as T
  const upper = data[key.slice(0, 1).toUpperCase() + key.slice(1)]
  return upper === undefined ? fallback : upper as T
}

function relationValue(relation: Relation, key: 'From' | 'To' | 'Type'): string {
  const data = relation as unknown as AnyRecord
  return String(data[key] ?? data[key.toLowerCase()] ?? '')
}

function payloadOf(event: LedgerEvent): AnyRecord {
  return record(event.payload)
}

function eventSummary(event: LedgerEvent): string {
  const payload = payloadOf(event)
  for (const key of ['body', 'reason', 'text', 'note', 'answer', 'task_type']) {
    if (typeof payload[key] === 'string' && payload[key] !== '') return payload[key] as string
  }
  if (event.type === 'status_moved') {
    return `${String(payload.from ?? '')} → ${String(payload.to ?? '')}`
  }
  const raw = JSON.stringify(event.payload)
  return raw && raw !== '{}' ? raw : event.type
}

function eventKind(type: string): 'comment' | 'verdict' | 'system' | 'mirror' {
  if (type === 'comment') return 'comment'
  if (type === 'review_verdict' || type === 'decision_opened' || type === 'decision_answered') return 'verdict'
  if (type === 'task_mirrored') return 'mirror'
  return 'system'
}

function cardTitle(detail: CardDetail): string {
  return value(detail.card, 'title', '工作项')
}

function attachmentsOf(card: unknown): Array<{ path?: string }> {
  const attachments = value<unknown>(card, 'attachments', [])
  return Array.isArray(attachments) ? attachments as Array<{ path?: string }> : []
}

function acceptance(detail: CardDetail): { criteria: string; verified: boolean; evidence: string } {
  const criteria = value(detail.card, 'acceptance_criteria', '')
  const events = (detail.events ?? []).filter((event) => event.type === 'acceptance_recorded')
  const latest = events.at(-1)
  const payload = latest ? payloadOf(latest) : {}
  return {
    criteria,
    verified: payload.verified_on_real_machine === true,
    evidence: typeof payload.evidence === 'string' ? payload.evidence : '',
  }
}

function timelineGroups(events: LedgerEvent[]): Array<{ kind: 'mirror' | 'event'; events: LedgerEvent[] }> {
  const groups: Array<{ kind: 'mirror' | 'event'; events: LedgerEvent[] }> = []
  for (const event of events) {
    const kind = event.type === 'task_mirrored' ? 'mirror' : 'event'
    const last = groups.at(-1)
    if (kind === 'mirror' && last?.kind === 'mirror') last.events.push(event)
    else groups.push({ kind, events: [event] })
  }
  return groups
}

export function CardDrawer({
  id,
  onClose,
  onOpenCard,
  workflowStates,
  initialSection,
}: {
  id: string
  onClose: () => void
  onOpenCard: (id: string) => void
  workflowStates?: string[]
  initialSection?: 'merge'
}) {
  const [detail, setDetail] = useState<CardDetail | null>(null)
  const [error, setError] = useState('')
  const [timelineFilter, setTimelineFilter] = useState<'all' | 'comment' | 'verdict' | 'system'>('all')
  const [note, setNote] = useState('')
  const [noteBusy, setNoteBusy] = useState(false)
  const [noteError, setNoteError] = useState('')
  const [moveTarget, setMoveTarget] = useState('')
  const [moveConfirm, setMoveConfirm] = useState(false)
  const [moveBusy, setMoveBusy] = useState(false)
  const [moveError, setMoveError] = useState('')
  const mergeRef = useRef<HTMLDivElement>(null)

  const load = () => {
    setError('')
    void fetchCardDetail(id).then(setDetail).catch((err: unknown) => setError(errorMessage(err)))
  }

  useEffect(() => {
    let cancelled = false
    setDetail(null)
    setError('')
    void fetchCardDetail(id)
      .then((next) => { if (!cancelled) setDetail(next) })
      .catch((err: unknown) => { if (!cancelled) setError(errorMessage(err)) })
    return () => { cancelled = true }
  }, [id])

  useEffect(() => {
    if (detail && initialSection === 'merge') mergeRef.current?.scrollIntoView({ block: 'start' })
  }, [detail, initialSection])

  const card = detail ? detail.card : null
  const status = value(card, 'status', '')
  const states = workflowStates?.length ? workflowStates : [status]
  const following = value(card, 'following', '')
  const driverSession = value(card, 'driver_session', '')
  const heartbeat = value(card, 'driver_heartbeat_at', '')
  const driverStale = Boolean(driverSession) && (!heartbeat || Number.isNaN(Date.parse(heartbeat)) || Date.now() - Date.parse(heartbeat) > 5 * 60 * 1000)
  const acceptanceInfo = detail ? acceptance(detail) : { criteria: '', verified: false, evidence: '' }
  const relations = detail?.relations ?? []
  const mergedMembers = relations.filter((relation) => relationValue(relation, 'Type') === 'merged_into' && relationValue(relation, 'To') === id)
  const visibleRelations = relations.filter((relation) => relationValue(relation, 'Type') !== 'merged_into')
  const filteredEvents = useMemo(() => {
    if (!detail) return []
    const events = detail.events ?? []
    if (timelineFilter === 'all') return events
    return events.filter((event) => {
      const kind = eventKind(event.type)
      return timelineFilter === 'system' ? kind === 'system' || kind === 'mirror' : kind === timelineFilter
    })
  }, [detail, timelineFilter])
  const groups = timelineGroups(filteredEvents)

  const submitNote = async () => {
    if (!note.trim()) return
    setNoteBusy(true)
    setNoteError('')
    try {
      await noteCard(id, note.trim())
      setNote('')
      load()
    } catch (err) {
      setNoteError(errorMessage(err))
    } finally {
      setNoteBusy(false)
    }
  }

  const submitMove = async () => {
    if (!moveTarget) return
    setMoveBusy(true)
    setMoveError('')
    try {
      await moveCard(id, moveTarget)
      setMoveConfirm(false)
      setMoveTarget('')
      load()
    } catch (err) {
      // ApiError.message is the service-side gate/CAS message; preserve it verbatim.
      setMoveError(errorMessage(err))
    } finally {
      setMoveBusy(false)
    }
  }

  return (
    <aside className="fixed inset-y-0 right-0 z-40 flex w-[560px] max-w-[92vw] flex-col border-l bg-background shadow-xl" role="dialog" aria-label="工作项详情">
      <header className="border-b px-4 py-3">
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs text-muted-foreground">{value(card, 'id', id)}</span>
          <span className="rounded-full border px-2 py-0.5 text-xs">{status || '加载中'}</span>
          {acceptanceInfo.verified && <span className="rounded-full border border-green-300 bg-green-50 px-2 py-0.5 text-[10px] text-green-700">已验</span>}
          <button type="button" aria-label="关闭" onClick={onClose} className="ml-auto rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"><X className="size-4" /></button>
        </div>
        <h2 className="mt-1 text-sm font-semibold">{detail ? cardTitle(detail) : '工作项详情'}</h2>
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {error && <p role="alert" className="mb-3 break-words rounded border border-destructive/40 bg-destructive/5 p-2 text-sm text-destructive">{error}</p>}
        {!detail && !error && <p className="text-sm text-muted-foreground">正在读取账本…</p>}
        {detail && (
          <>
            <section className="mb-5">
              <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
                {boardColumns(states).map((state) => <span key={state} className={`rounded-full border px-2 py-0.5 ${state === status ? 'border-primary bg-primary text-primary-foreground' : state === '终止' ? 'border-dashed text-muted-foreground' : 'text-muted-foreground'}`}>{state}</span>)}
              </div>
            </section>

            <section className="mb-5">
              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
                <dt className="text-muted-foreground">项目</dt><dd>{value(card, 'project', '—')}</dd>
                <dt className="text-muted-foreground">工作流</dt><dd>{value(card, 'workflow', '—')} @ v{value(card, 'workflow_version', 0)}</dd>
                <dt className="text-muted-foreground">附件</dt><dd>{attachmentsOf(card).map((item) => item.path).filter(Boolean).join('、') || '—'}</dd>
                <dt className="text-muted-foreground">基线</dt><dd className="font-mono">{value(detail, 'effective_base_branch', '') || '—'}</dd>
                {(following || driverStale) && <><dt className="text-muted-foreground">驱动/跟随</dt><dd>{following ? `跟随 ${following}` : `驱动异常：${driverSession}`}</dd></>}
              </dl>
            </section>

            <section className="mb-5">
              <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">验收</h3>
              <div className="rounded-lg border p-3 text-xs">
                <div className="mb-1.5 font-medium">{acceptanceInfo.verified ? '已验' : '待真机验'}</div>
                <p className="whitespace-pre-wrap leading-5">{acceptanceInfo.criteria || '尚未填写验收判据。'}</p>
                {acceptanceInfo.evidence && <p className="mt-2 border-l-2 pl-2 leading-5 text-muted-foreground">{acceptanceInfo.evidence}</p>}
              </div>
            </section>

            {mergedMembers.length > 0 && (
              <section ref={mergeRef} className="mb-5">
                <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">⊕ 并入本卡（状态跟随，验收各自保留）</h3>
                {mergedMembers.map((relation) => {
                  const memberID = relationValue(relation, 'From')
                  return <div key={memberID} className="mb-1 flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs"><button type="button" className="font-mono underline" onClick={() => onOpenCard(memberID)}>{memberID}</button><span className="text-muted-foreground">未验</span><span className="ml-auto rounded-full border px-1.5 text-[10px]">跟随 badge</span><button type="button" disabled title="CLI: handoff card unmerge" className="rounded border px-1.5 text-[10px] text-muted-foreground">拆回</button></div>
                })}
              </section>
            )}

            {visibleRelations.length > 0 && (
              <section className="mb-5">
                <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">关系</h3>
                {visibleRelations.map((relation, index) => {
                  const from = relationValue(relation, 'From')
                  const to = relationValue(relation, 'To')
                  const type = relationValue(relation, 'Type')
                  const label: Record<string, string> = { blocks: '阻塞', discovered_from: '发现自', split_from: '拆分自', relates: '关联' }
                  return <div key={`${from}-${to}-${type}-${index}`} className="mb-1 text-xs"><span className="mr-1.5 text-muted-foreground">{label[type] ?? type}</span><button type="button" className="font-mono underline" onClick={() => onOpenCard(from === id ? to : from)}>{from === id ? to : from}</button></div>
                })}
              </section>
            )}

            {(detail.task_states ?? []).length > 0 && (
              <section className="mb-5">
                <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">关联执行（task）</h3>
                {(detail.task_states ?? []).map((task) => <div key={`${task.Target}/${task.TaskID}`} className="mb-1 flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs"><span className="font-mono">{task.TaskID}</span><span>{task.Purpose}</span><span className="ml-auto text-muted-foreground">{task.LastType || '未知'}</span><span className="text-muted-foreground">{task.Target}</span></div>)}
              </section>
            )}

            {(detail.decisions ?? []).length > 0 && <CardDecisions decisions={detail.decisions ?? []} onAnswered={load} />}

            <section className="mb-5">
              <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">节点动作</h3>
              {!moveConfirm ? <button type="button" onClick={() => setMoveConfirm(true)} className="rounded-md border px-2.5 py-1 text-xs hover:bg-accent">转移状态…</button> : <div className="flex flex-wrap items-center gap-2"><select value={moveTarget} onChange={(event) => setMoveTarget(event.target.value)} className="rounded-md border bg-background px-2 py-1 text-xs"><option value="">选择目标态</option>{states.filter((state) => state !== status).map((state) => <option key={state} value={state}>{state}</option>)}</select><button type="button" disabled={!moveTarget || moveBusy} onClick={() => void submitMove()} className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50">确认转移</button><button type="button" onClick={() => setMoveConfirm(false)} className="rounded-md border px-2.5 py-1 text-xs">取消</button></div>}
              {moveError && <p role="alert" className="mt-1 break-words text-xs text-destructive">{moveError}</p>}
            </section>

            <section className="mb-5">
              <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">Timeline</h3>
              <div className="mb-2 flex flex-wrap gap-1"><span className="sr-only">timeline filter</span>{(['all', 'comment', 'verdict', 'system'] as const).map((filter) => <button key={filter} type="button" onClick={() => setTimelineFilter(filter)} className={`rounded-full border px-2 py-0.5 text-[11px] ${timelineFilter === filter ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}>{filter === 'all' ? '全部' : filter === 'comment' ? '评论' : filter === 'verdict' ? '裁决' : '系统'}</button>)}</div>
              <div className="space-y-1.5">
                {groups.map((group, index) => group.kind === 'mirror' ? <details key={`mirror-${index}`} className="text-xs text-muted-foreground"><summary className="cursor-pointer">镜像执行事件（{group.events.length}）</summary><div className="ml-2 border-l pl-2">{group.events.map((event) => <div key={event.seq} className="py-0.5">#{event.seq} {eventSummary(event)}</div>)}</div></details> : group.events.map((event) => event.type === 'comment' ? <div key={event.seq} className="rounded-lg bg-muted px-3 py-2 text-xs leading-5"><div className="mb-0.5 text-[11px] text-muted-foreground">{event.actor}</div>{eventSummary(event)}</div> : <div key={event.seq} className="flex gap-2 text-xs text-muted-foreground"><span className="font-mono">#{event.seq}</span><span>{eventSummary(event)}</span></div>))}
                {groups.length === 0 && <p className="text-xs text-muted-foreground">还没有事件。</p>}
              </div>
              <div className="mt-3 flex gap-1.5"><input value={note} onChange={(event) => setNote(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); void submitNote() } }} placeholder="写评论… 用 #B142 引用其他工作项" className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1.5 text-xs" /><button type="button" disabled={noteBusy || !note.trim()} onClick={() => void submitNote()} className="rounded-md border px-2.5 py-1 text-xs disabled:opacity-50">发布</button></div>
              {noteError && <p role="alert" className="mt-1 text-xs text-destructive">{noteError}</p>}
              <p className="mt-1 text-[11px] text-muted-foreground">#B 号引用会自动建立关联边。</p>
            </section>
          </>
        )}
      </div>
    </aside>
  )
}
