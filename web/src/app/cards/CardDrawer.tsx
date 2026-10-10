import { useEffect, useMemo, useRef, useState } from 'react'
import type { ReactNode } from 'react'
import { ChevronLeft, X } from 'lucide-react'
import { ApiError, fetchTaskDetail, replyTicket } from '../../api/client'
import type { Task, TaskDetail, Ticket } from '../../api/types'
import { acceptCard, answerDecision, attachFile, clearCardNeeds, detachFile, fetchCardDetail, moveCard, noteCard, patchCard } from '../../api/ledger'
import type { CardDetail, Decision, LedgerEvent, NodeDef } from '../../api/ledger'
import type { CoordinatorAttachInfo } from '../../api/scheduling'
import { errorMessage } from '../lib/format'
import { TOUCH_BASELINE } from '@/lib/touch'
import { cn } from '@/lib/utils'
import { TicketsPanel } from '../task/TicketsPanel'
import { boardColumnFor, boardColumns, nodeLabelFor, normalizeBoardLayout, type BoardLayout } from './columns'
import { CoordinatorPanel } from './CoordinatorPanel'
// linkedTaskOf/isRunningRow 迁往共享 taskRun.ts（B369.8 §3.2）：Shell 的 scene 档
// 解析链要用同一口径，抽模块避免复制。行为零变化。
import { isRunningRow, linkedTaskOf } from './taskRun'
import { TaskState } from '../board/StateDot'

type Relation = { From: string; To: string; Type: string }

// CardAttention 是抽屉里的「需要你」合一区：等人原因 + 挂卡裁决。
//
// why 两者合成一区：「需要你」在看板上就是等人 ∪ 裁决合一的筛选，抽屉里
// 也该是同一处，否则用户点开一张亮着角标的卡，看到的却是空白——B3 那种只
// 打了等人标记、没有请示的卡尤其明显（2026-08-19 真机看到）。
//
// why 必须在抽屉里而不是只躺在 timeline：请示的候选项与答复入口以前完全
// 没有呈现面——卡上只显示一个「裁决 N」角标，点进抽屉也只在 timeline 里剩
// 一行原文，看不到选什么、也没法答。项目级裁决走顶部收件箱横幅，挂卡的走
// 这里，两条路都能答复。
//
// 答复成功后调 onAnswered 让抽屉重取详情：答案要立刻落到这一处，不能等轮询。
function CardAttention({ cardId, needs, decisions, onAnswered, compact = false, disabled = false }: { cardId: string; needs: string; decisions: Decision[]; onAnswered: () => void; compact?: boolean; disabled?: boolean }) {
  const [drafts, setDrafts] = useState<Record<number, string>>({})
  const [busy, setBusy] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [clearing, setClearing] = useState(false)
  // 撤回等人标记。撤完立刻重取详情，让红旗当场消失——等轮询的话用户会以为没点上。
  const clearNeeds = async () => {
    if (disabled) return
    setClearing(true)
    setError('')
    try {
      await clearCardNeeds(cardId)
      onAnswered()
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setClearing(false)
    }
  }
  const submit = async (decision: Decision) => {
    const text = (drafts[decision.id] ?? '').trim()
    if (!text || disabled) return
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
    <section data-testid="card-attention" className="mb-5">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">⚑ 需要你</h3>
      {needs !== '' && (
        <div className={cn('mb-1.5 flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-2.5 py-2 text-xs text-amber-900', compact && 'flex-col')}>
          <span className="shrink-0 font-semibold">等人</span>
          <span className="min-w-0 flex-1 break-words">{needs}</span>
          <button type="button" disabled={disabled || clearing} onClick={() => void clearNeeds()}
            className={cn('shrink-0 rounded border border-amber-300 px-2 py-0.5 text-[11px] disabled:opacity-50', compact && 'min-h-11 self-start px-3')}>已处理</button>
        </div>
      )}
      {decisions.map((decision) => {
        const open = decision.status === 'open'
        return (
          <div key={decision.id} className={`mb-1.5 rounded-md border px-2.5 py-2 text-xs ${open ? 'border-amber-200 bg-amber-50 text-amber-900' : ''}`}>
            <div className="flex gap-2"><span className="font-mono shrink-0">#{decision.id}</span><span className="min-w-0 flex-1 break-words">{decision.body}</span></div>
            {(decision.options ?? []).length > 0 && (
              <div className="mt-1.5 flex flex-wrap gap-1">
                {(decision.options ?? []).map((option) => (
                  <button key={option} type="button" disabled={disabled || !open} onClick={() => setDrafts((current) => ({ ...current, [decision.id]: option }))}
                    className={cn('rounded-full border px-2 py-0.5 text-[11px] disabled:opacity-60', compact && 'min-h-11 px-3')}>{option}</button>
                ))}
              </div>
            )}
            {open ? (
              <div className="mt-1.5 flex flex-col gap-2">
                {compact ? <textarea rows={3} value={drafts[decision.id] ?? ''} onChange={(event) => setDrafts((current) => ({ ...current, [decision.id]: event.target.value }))}
                  placeholder="答复这条请示…" className="min-h-20 w-full resize-y rounded border bg-background px-3 py-2 text-sm leading-5" /> : <input value={drafts[decision.id] ?? ''} onChange={(event) => setDrafts((current) => ({ ...current, [decision.id]: event.target.value }))}
                  placeholder="答复这条请示…" className="min-w-0 flex-1 rounded border bg-background px-2 py-1 text-xs" />}
                <button type="button" disabled={disabled || busy === decision.id || !(drafts[decision.id] ?? '').trim()} onClick={() => void submit(decision)}
                  className={cn('self-start rounded border px-3 py-2 text-sm disabled:opacity-50', compact && 'min-h-11')}>答复</button>
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
  if (event.type === 'branch_merged') {
    const pushed = payload.pushed_work_branch === true ? '已推工作分支' : '工作分支来自 origin'
    return `合并 ${String(payload.work_branch ?? '')} → ${String(payload.merged_into ?? '')}（${pushed}，已推 ${String(payload.pushed_base ?? '')}）`
  }
  const raw = JSON.stringify(event.payload)
  return raw && raw !== '{}' ? raw : event.type
}

// Verdict 是 review_verdict 事件解出来的裁决正文。findings 里的字段都按可选
// 处理：报文由被审阅的 executor 生成，字段缺失是常态，不能让一个缺 file 的
// finding 把整块渲染打掉。
type VerdictFinding = { severity?: string; summary?: string; file?: string }

// parseVerdict 把 review_verdict 的 payload 解成可渲染的结构，解不动返回 null。
//
// why 需要单独解一层：payload.pass 是布尔，但裁决正文（findings/notes）整个
// 塞在 payload.raw 这个 **JSON 字符串** 里。走 eventSummary 的兜底
// （JSON.stringify(payload)）渲染出来的是转义两遍的裸串——这个看板上最该
// 一眼看清的东西，反而成了最难读的一条（2026-08-20 真机看到）。
function parseVerdict(payload: AnyRecord): { pass: boolean; findings: VerdictFinding[]; notes: string } | null {
  const raw = payload.raw
  if (typeof raw !== 'string' || raw === '') return null
  try {
    const parsed = record(JSON.parse(raw))
    return {
      pass: payload.pass === true,
      findings: Array.isArray(parsed.findings) ? parsed.findings as VerdictFinding[] : [],
      notes: typeof parsed.notes === 'string' ? parsed.notes : '',
    }
  } catch {
    // 解不动就交回上层按原文显示：宁可难看，也不能把裁决吞掉。
    return null
  }
}

// VerdictCard 渲染一条审阅裁决：通过与否 + findings 列表 + 备注。
function VerdictCard({ event }: { event: LedgerEvent }) {
  const verdict = parseVerdict(payloadOf(event))
  if (!verdict) {
    return (
      <div className="flex gap-2 text-xs text-muted-foreground">
        <span className="font-mono">#{event.seq}</span><span className="break-all">{eventSummary(event)}</span>
      </div>
    )
  }
  return (
    <div className={`rounded-lg border px-3 py-2 text-xs ${verdict.pass ? 'border-emerald-200 bg-emerald-50 text-emerald-900' : 'border-rose-200 bg-rose-50 text-rose-900'}`}>
      <div className="mb-1 flex items-center gap-2">
        <span className="font-mono text-[11px] opacity-70">#{event.seq}</span>
        <span className="font-semibold">{verdict.pass ? '审阅通过' : '审阅未过'}</span>
        <span className="text-[11px] opacity-70">{event.actor}</span>
      </div>
      {verdict.findings.length === 0
        ? <p className="opacity-80">没有 findings。</p>
        : (
          <ul className="space-y-1">
            {verdict.findings.map((finding, index) => (
              <li key={index} className="flex gap-1.5">
                <span className="shrink-0 rounded-full border px-1.5 text-[10px] leading-4 opacity-80">{finding.severity ?? '—'}</span>
                <span className="min-w-0 flex-1 break-words">
                  {finding.summary ?? '（无描述）'}
                  {finding.file ? <span className="ml-1 font-mono text-[11px] opacity-70">{finding.file}</span> : null}
                </span>
              </li>
            ))}
          </ul>
        )}
      {verdict.notes !== '' && <p className="mt-1 break-words opacity-80">{verdict.notes}</p>}
    </div>
  )
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

function attachmentsOf(card: unknown): Array<{ kind: string; path: string }> {
  const attachments = value<unknown>(card, 'attachments', [])
  if (!Array.isArray(attachments)) return []
  return attachments.flatMap((item) => {
    const data = record(item)
    const path = data.path ?? data.Path
    const kind = data.kind ?? data.Kind
    return typeof path === 'string' && path !== ''
      ? [{ kind: typeof kind === 'string' ? kind : '', path }]
      : []
  })
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

type DrawerTaskDetail = TaskDetail & { tickets?: Ticket[]; events?: unknown[] }

function pendingTickets(detail: DrawerTaskDetail): Ticket[] {
  return detail.pending_tickets ?? detail.tickets ?? []
}

export interface CardDrawerProps {
  id: string
  onClose: () => void
  onOpenCard: (id: string) => void
  /** Share successful detail metadata with the caller; this does not create another request. */
  onDetailLoaded?: (detail: CardDetail) => void
  workflowStates?: string[]
  boardLayout?: BoardLayout
  initialSection?: 'merge'
  nodes?: NodeDef[]
  tasks?: Task[]
  onJumpToTask?: (taskId: string) => void
  onOpenCoordinatorTerminal?: (info: CoordinatorAttachInfo) => void
  // compact（B369.8 T4）：抽屉全宽 + aria-modal + 焦点移交 + 13 块三层分组。
  // 缺省 false = 桌面 aside 类串与块序逐字节不动（§3.1 的桌面净改动承诺）。
  compact?: boolean
  // B369.10 T8：compact 双跳行第二跳「驾驶会话 → 群聊」的回调（CardsPage 经
  // onOpenSessionForCard 注入，Shell 会话流反查）。缺席不渲染——桌面永不渲染。
  onOpenDriverSession?: () => void
  /** The caller owns session lookup readiness; unknown or expired data cannot be navigated. */
  driverSessionReady?: boolean
  // S4（B426）：compact 抽屉顶部筛选区的内容槽（项目/工作流/搜索三件次级控件，
  // 由 CardsPage 注入 JSX——筛选 state 住调用方，抽屉关闭后筛选保留）。桌面不传
  // 不渲染，aside 结构与块序逐字节不动。
  compactFilters?: ReactNode
  readOnly?: boolean
  // B429 cut-2：compact 列表去⋯后，迁移进详情「更多工作项操作」。
  onMigrate?: () => void
}

export function CardDrawer({
  id,
  onClose,
  onOpenCard,
  onDetailLoaded,
  workflowStates,
  boardLayout,
  initialSection,
  nodes,
  tasks,
  onJumpToTask,
  onOpenCoordinatorTerminal,
  compact = false,
  onOpenDriverSession,
  driverSessionReady = true,
  compactFilters,
  readOnly = false,
  onMigrate,
}: CardDrawerProps) {
  const [detail, setDetail] = useState<CardDetail | null>(null)
  const [error, setError] = useState('')
  const [timelineFilter, setTimelineFilter] = useState<'all' | 'comment' | 'verdict' | 'system'>('all')
  const [note, setNote] = useState('')
  const [noteBusy, setNoteBusy] = useState(false)
  const [noteError, setNoteError] = useState('')
  const [acceptOpen, setAcceptOpen] = useState(false)
  const [acceptEvidence, setAcceptEvidence] = useState('')
  const [acceptBusy, setAcceptBusy] = useState(false)
  const [acceptError, setAcceptError] = useState('')
  const [titleEditing, setTitleEditing] = useState(false)
  const [titleDraft, setTitleDraft] = useState('')
  const [titleBusy, setTitleBusy] = useState(false)
  const [titleError, setTitleError] = useState('')
  const [priorityEditing, setPriorityEditing] = useState(false)
  const [priorityDraft, setPriorityDraft] = useState('')
  const [priorityBusy, setPriorityBusy] = useState(false)
  const [priorityError, setPriorityError] = useState('')
  const [acceptanceEditing, setAcceptanceEditing] = useState(false)
  const [acceptanceDraft, setAcceptanceDraft] = useState('')
  const [acceptanceBusy, setAcceptanceBusy] = useState(false)
  const [acceptanceError, setAcceptanceError] = useState('')
  const [baseEditing, setBaseEditing] = useState(false)
  const [baseDraft, setBaseDraft] = useState('')
  const [baseBusy, setBaseBusy] = useState(false)
  const [baseError, setBaseError] = useState('')
  const [attachmentKind, setAttachmentKind] = useState('plan')
  const [attachmentPath, setAttachmentPath] = useState('')
  const [attachmentBusy, setAttachmentBusy] = useState(false)
  const [attachmentError, setAttachmentError] = useState('')
  const [expandedTask, setExpandedTask] = useState<string | null>(null)
  const [taskDetails, setTaskDetails] = useState<Record<string, DrawerTaskDetail>>({})
  const [taskLoading, setTaskLoading] = useState<string | null>(null)
  const [taskErrors, setTaskErrors] = useState<Record<string, string>>({})
  const [moveTarget, setMoveTarget] = useState('')
  const [moveConfirm, setMoveConfirm] = useState(false)
  const [moveBusy, setMoveBusy] = useState(false)
  const [moveError, setMoveError] = useState('')
  const mergeRef = useRef<HTMLDivElement>(null)
  // 焦点移交（B369.8 §3.1/§4，compact-only）：抽屉挂载即把焦点移入面板
  // （Overlay :32-33 先例，tabIndex={-1} 收焦点）；关闭归还触发钮归
  // CardsPage.closeDrawer。桌面 560px 侧板非模态，不抢焦点不 tabindex。
  const panelRef = useRef<HTMLElement | null>(null)
  useEffect(() => {
    if (compact) panelRef.current?.focus()
  }, [compact])

  const detailCallback = useRef(onDetailLoaded)
  detailCallback.current = onDetailLoaded
  const detailRequest = useRef(0)
  const load = () => {
    const request = ++detailRequest.current
    setError('')
    console.info('cards.detail.start', { card: id })
    void fetchCardDetail(id)
      .then((next) => {
        if (request !== detailRequest.current) return
        setDetail(next)
        if (next) detailCallback.current?.(next)
        console.info('cards.detail.loaded', { card: id, workflow: next?.card.workflow, version: next?.card.workflow_version })
      })
      .catch((cause: unknown) => {
        if (request !== detailRequest.current) return
        console.error('cards.detail.error', { card: id, status: cause instanceof ApiError ? cause.status : 0, cause })
        setError(errorMessage(cause))
      })
  }

  useEffect(() => {
    setDetail(null)
    load()
    // A response for an old ID (or closed drawer) cannot publish metadata into the new target.
    return () => { detailRequest.current += 1 }
  }, [id]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    if (detail && initialSection === 'merge') mergeRef.current?.scrollIntoView({ block: 'start' })
  }, [detail, initialSection])

  const writesBlocked = compact && (readOnly || error !== '' || detail === null)
  const card = detail ? detail.card : null
  // 显式给 string：value<T> 会把字面量实参 '' 推成字面量类型 ""，
  // 那样 status === '已完成' 在类型上恒假，tsc -b 直接报 TS2367
  const status = value<string>(card, 'status', '')
  const states = workflowStates?.length ? workflowStates : [status]
  const resolvedBoardLayout = normalizeBoardLayout(boardLayout, states)
  // 节点详情未加载时不猜节点；状态与节点列表不同属时也只显示真实多对一标签。
  const nodeLabel = nodes ? nodeLabelFor(status, nodes.map((node) => node.name), resolvedBoardLayout) : undefined
  const following = value(card, 'following', '')
  const driverSession = value(card, 'driver_session', '')
  const driverSource = value<string>(card, 'driver_source', '')
  const heartbeat = value(card, 'driver_heartbeat_at', '')
  const driverStale = Boolean(driverSession) && (!heartbeat || Number.isNaN(Date.parse(heartbeat)) || Date.now() - Date.parse(heartbeat) > 5 * 60 * 1000)
  const acceptanceInfo = detail ? acceptance(detail) : { criteria: '', verified: false, evidence: '' }
  const attachments = attachmentsOf(card)
  const ownBase = value<string>(card, 'base_branch', '')
  const effectiveBase = value<string>(detail, 'effective_base_branch', '')
  const baseLabel = ownBase !== ''
    ? `自设 ${ownBase}`
    : effectiveBase !== ''
      ? `继承 ${effectiveBase}`
      : '未设置/回落项目主线'
  // 验收 chip 三态：已验 / 待真机验（活干完了等验）/ 未验（还没干完）。
  // 原来只有两态，把「还在进行中的卡」也显示成「待真机验」——那会让看板上
  // 一片卡都像在等人验，真正等验的那几张反而看不出来
  const acceptanceLabel = acceptanceInfo.verified ? '已验' : status === '已完成' ? '待真机验' : '未验'
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
  // 关联执行的展示序与计数：在跑的排最前（扫一眼就知道有没有活着的），其余按
  // 最后事件序号倒序；平局保持账本返回的原序（Array.prototype.sort 自 ES2019 起
  // 稳定）。tasks===undefined 时不动序也不计数：「在跑」无从判定，标题退回旧
  // 形态——不知道就说不知道，不谎报「0 个在跑」（spec §3.1/§3.2 的诚实降级）。
  const taskRows = useMemo(() => {
    const rows = [...(detail?.task_states ?? [])]
    if (tasks === undefined) return rows
    return rows.sort((left, right) => {
      const leftRunning = isRunningRow(left, tasks)
      const rightRunning = isRunningRow(right, tasks)
      if (leftRunning !== rightRunning) return leftRunning ? -1 : 1
      return right.LastSeq - left.LastSeq
    })
  }, [detail, tasks])
  const runningCount = tasks === undefined ? null : taskRows.filter((row) => isRunningRow(row, tasks)).length
  const terminalTaskRows = compact && tasks !== undefined
    ? taskRows.filter((row) => {
      const linked = linkedTaskOf(row, tasks)
      // Keep unlinked rows visible as unknown; only the shared running classifier can establish history.
      return linked !== undefined && !isRunningRow(row, tasks)
    })
    : []
  const terminalTaskIds = new Set(terminalTaskRows.map((row) => row.TaskID))

  // B369.10 T8 双跳行 hot 判据（与 renderAttention 同源：等人 ∪ 挂卡裁决）且
  // 关联执行存在在跑行——落点取第一个在跑行的 TaskID（taskRows 已按在跑优先
  // 排序），同 taskJumpHref 深链语境。tasks 未到不猜（诚实降级，行不渲染）。
  const needsHot = detail !== null
    && ((detail.needs ?? '') !== '' || (detail.decisions ?? []).some((decision) => decision.status === 'open'))
  const firstRunningTaskId = tasks === undefined ? null : (taskRows.find((row) => isRunningRow(row, tasks))?.TaskID ?? null)

  const beginTitleEdit = () => {
    if (!detail) return
    setTitleDraft(cardTitle(detail))
    setTitleError('')
    setTitleEditing(true)
  }

  const submitTitle = async () => {
    if (writesBlocked) return
    const title = titleDraft.trim()
    if (!title) return
    setTitleBusy(true)
    setTitleError('')
    try {
      await patchCard(id, { title })
      setTitleEditing(false)
      load()
    } catch (err) {
      setTitleError(errorMessage(err))
    } finally {
      setTitleBusy(false)
    }
  }

  const beginPriorityEdit = () => {
    setPriorityDraft(value<string>(card, 'priority', '中'))
    setPriorityError('')
    setPriorityEditing(true)
  }

  const submitPriority = async () => {
    if (writesBlocked) return
    setPriorityBusy(true)
    setPriorityError('')
    try {
      await patchCard(id, { priority: priorityDraft })
      setPriorityEditing(false)
      load()
    } catch (err) {
      setPriorityError(errorMessage(err))
    } finally {
      setPriorityBusy(false)
    }
  }

  const beginAcceptanceEdit = () => {
    setAcceptanceDraft(acceptanceInfo.criteria)
    setAcceptanceError('')
    setAcceptanceEditing(true)
  }

  const submitAcceptance = async () => {
    if (writesBlocked) return
    setAcceptanceBusy(true)
    setAcceptanceError('')
    try {
      await patchCard(id, { acceptance_criteria: acceptanceDraft })
      setAcceptanceEditing(false)
      load()
    } catch (err) {
      setAcceptanceError(errorMessage(err))
    } finally {
      setAcceptanceBusy(false)
    }
  }

  const beginBaseEdit = () => {
    setBaseDraft(ownBase)
    setBaseError('')
    setBaseEditing(true)
  }

  const submitBase = async () => {
    if (writesBlocked) return
    setBaseBusy(true)
    setBaseError('')
    try {
      await patchCard(id, { base_branch: baseDraft })
      setBaseEditing(false)
      load()
    } catch (err) {
      setBaseError(errorMessage(err))
    } finally {
      setBaseBusy(false)
    }
  }

  const submitAttachment = async () => {
    if (writesBlocked) return
    const path = attachmentPath.trim()
    if (!path) return
    setAttachmentBusy(true)
    setAttachmentError('')
    try {
      await attachFile(id, attachmentKind, path)
      setAttachmentPath('')
      load()
    } catch (err) {
      setAttachmentError(errorMessage(err))
    } finally {
      setAttachmentBusy(false)
    }
  }

  const removeAttachment = async (path: string) => {
    setAttachmentBusy(true)
    setAttachmentError('')
    try {
      await detachFile(id, path)
      load()
    } catch (err) {
      setAttachmentError(errorMessage(err))
    } finally {
      setAttachmentBusy(false)
    }
  }

  const loadTaskDetail = async (taskID: string) => {
    setTaskLoading(taskID)
    setTaskErrors((current) => ({ ...current, [taskID]: '' }))
    try {
      const next = await fetchTaskDetail(taskID)
      setTaskDetails((current) => ({ ...current, [taskID]: next as DrawerTaskDetail }))
    } catch (err) {
      setTaskErrors((current) => ({ ...current, [taskID]: errorMessage(err) }))
    } finally {
      setTaskLoading((current) => current === taskID ? null : current)
    }
  }

  const toggleTask = (taskID: string) => {
    if (expandedTask === taskID) {
      setExpandedTask(null)
      return
    }
    setExpandedTask(taskID)
    if (!taskDetails[taskID]) void loadTaskDetail(taskID)
  }

  const replyTaskTicket = async (taskID: string, ticket: Ticket, answer: string) => {
    // TicketsPanel 已用 buildTicketAnswer 按 gate/ask 契约编码 answer；这里负责把
    // 编码后的答复送回 task，并重取详情让工单在抽屉里立即消失或更新。
    await replyTicket(taskID, { ticket_id: ticket.id, answer })
    await loadTaskDetail(taskID)
  }

  const submitNote = async () => {
    if (writesBlocked) return
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

  const submitAccept = async () => {
    if (writesBlocked) return
    const evidence = acceptEvidence.trim()
    if (!evidence) return
    setAcceptBusy(true)
    setAcceptError('')
    try {
      await acceptCard(id, evidence)
      setAcceptOpen(false)
      setAcceptEvidence('')
      load()
    } catch (err) {
      // ApiError.message 是后端的规则原文（如「必须带证据」），逐字保留
      setAcceptError(errorMessage(err))
    } finally {
      setAcceptBusy(false)
    }
  }

  const submitMove = async () => {
    if (writesBlocked) return
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

  // —— 13 块渲染函数（B369.8 T4）：块内部 JSX 与重构前逐字节一致。桌面按原序
  // 展开（renderBlocksDesktop，抽屉既有用例 = 块序未动的证据）；compact 按
  // 三层分组重排（renderBlocksCompact，plan §2 岔口 2）。
  const renderCoordinatorPanel = () => (
    <CoordinatorPanel cardId={id} onOpenTerminal={onOpenCoordinatorTerminal ?? (() => undefined)} />
  )

  const renderCoordinatorSeat = () => (
    <section className="mb-5 rounded-lg border p-3">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">协调者席位</h3>
      {driverSession
        ? <dl className="grid grid-cols-[max-content_minmax(0,1fr)] gap-x-3 gap-y-1 text-xs"><dt className="text-muted-foreground">身份</dt><dd className="break-all font-mono">{driverSession}</dd><dt className="text-muted-foreground">来源</dt><dd>{driverSource === 'bind' ? '坐下' : driverSource === 'coordinate' ? '叫机器人' : '席位异常'}</dd></dl>
        : <p className="text-xs text-muted-foreground">空座：可选择“叫机器人”启动协调者。</p>}
    </section>
  )

  const renderStatusChips = () => (
    <section className="mb-5">
      <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
        {boardColumns(states, resolvedBoardLayout).map((column) => <span key={column} className={`rounded-full border px-2 py-0.5 ${column === boardColumnFor(status, resolvedBoardLayout) ? 'border-primary bg-primary text-primary-foreground' : column === '结束' ? 'border-dashed text-muted-foreground' : 'text-muted-foreground'}`}>{column}</span>)}
      </div>
    </section>
  )

  const renderMeta = () => (
    <section className="mb-5">
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-1 text-xs">
        <dt className="text-muted-foreground">项目</dt><dd>{value(card, 'project', '—')}</dd>
        <dt className="text-muted-foreground">工作流</dt><dd>{value(card, 'workflow', '—')} @ v{value(card, 'workflow_version', 0)}</dd>
        <dt className="text-muted-foreground">优先级</dt>
        <dd>
          {priorityEditing ? (
            <span className="flex items-center gap-1.5">
              <select aria-label="优先级" value={priorityDraft} onChange={(event) => setPriorityDraft(event.target.value)} className="rounded border bg-background px-1.5 py-0.5 text-xs">
                {['高', '中', '低'].map((level) => <option key={level} value={level}>{level}</option>)}
              </select>
              <button type="button" disabled={writesBlocked || priorityBusy} onClick={() => void submitPriority()} className="rounded border px-1.5 py-0.5 text-[11px] disabled:opacity-50">保存优先级</button>
              <button type="button" disabled={writesBlocked || priorityBusy} onClick={() => setPriorityEditing(false)} className="rounded border px-1.5 py-0.5 text-[11px]">取消</button>
            </span>
          ) : (
            <span className="flex items-center gap-1.5"><span>{value(card, 'priority', '—')}</span><button type="button" onClick={beginPriorityEdit} className="rounded border px-1.5 py-0.5 text-[11px]">改优先级</button></span>
          )}
        </dd>
        {priorityError && <dd role="alert" className="col-span-2 break-words text-xs text-destructive">{priorityError}</dd>}
        <dt className="text-muted-foreground">附件</dt><dd>{attachments.map((item) => item.path).join('、') || '—'}</dd>
        <dt className="text-muted-foreground">基线</dt>
        <dd className="font-mono">
          {baseEditing ? (
            <span className="flex flex-wrap items-center gap-1.5 font-sans">
              <input aria-label="基线分支" value={baseDraft} onChange={(event) => setBaseDraft(event.target.value)}
                className="min-w-0 flex-1 rounded border bg-background px-2 py-1 font-mono text-xs" />
              <button type="button" disabled={writesBlocked || baseBusy} onClick={() => void submitBase()}
                className="rounded border px-2 py-1 text-[11px] disabled:opacity-50">保存基线</button>
              <button type="button" disabled={writesBlocked || baseBusy} onClick={() => setBaseEditing(false)}
                className="rounded border px-2 py-1 text-[11px]">取消</button>
            </span>
          ) : (
            <span className="flex flex-wrap items-center gap-1.5">
              <span>{baseLabel}</span>
              <button type="button" onClick={beginBaseEdit} className="font-sans rounded border px-2 py-1 text-[11px]">编辑基线</button>
            </span>
          )}
          {baseError && <span role="alert" className="mt-1 block break-words font-sans text-xs text-destructive">{baseError}</span>}
        </dd>
        {(following || driverStale) && <><dt className="text-muted-foreground">驱动/跟随</dt><dd>{following ? `跟随 ${following}` : `驱动异常：${driverSession}`}</dd></>}
      </dl>
    </section>
  )

  const renderAcceptance = () => (
    <section className="mb-5">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">验收</h3>
      {compact ? (
        /* B369.10 T8 compact 行式：判据/证据/已验三行分行（原型 mobile-card 验收
           区行式版式）。「标记已验…」交互与确认语义原样保留——原型的逐条开关是
           逐条验收语义，wire 的验收是整卡一门（acceptCard(id, evidence)），无逐条
           开关可落，整卡语义保持（偏离记台账，替代形态=行式呈现在此）。 */
        <div className="space-y-2 rounded-lg border p-3 text-xs">
          {acceptanceEditing ? (
            <div className="space-y-1.5">
              <textarea
                value={acceptanceDraft}
                onChange={(event) => setAcceptanceDraft(event.target.value)}
                placeholder="这张卡怎样算做完了…"
                rows={4}
                className="w-full rounded border bg-background px-2 py-1.5 text-xs"
              />
              <div className="flex gap-2">
                <button type="button" disabled={writesBlocked || acceptanceBusy} onClick={() => void submitAcceptance()}
                  className="min-h-11 rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50">保存判据</button>
                <button type="button" disabled={writesBlocked || acceptanceBusy} onClick={() => setAcceptanceEditing(false)}
                  className="min-h-11 rounded-md border px-2.5 py-1 text-xs">取消</button>
              </div>
              {acceptanceError && <p role="alert" className="break-words text-xs text-destructive">{acceptanceError}</p>}
            </div>
          ) : (
            <div data-testid="acceptance-criteria-row" className="flex items-start gap-2">
              <span className="w-8 shrink-0 pt-0.5 text-muted-foreground">判据</span>
              <p className="min-w-0 flex-1 whitespace-pre-wrap leading-5">{acceptanceInfo.criteria || '尚未填写验收判据。'}</p>
              <button type="button" onClick={beginAcceptanceEdit} className="min-h-11 shrink-0 rounded border px-2 py-1 text-[11px] hover:bg-accent">编辑判据</button>
            </div>
          )}
          {acceptanceInfo.evidence !== '' && (
            <div data-testid="acceptance-evidence-row" className="flex items-start gap-2">
              <span className="w-8 shrink-0 pt-0.5 text-muted-foreground">证据</span>
              <p className="min-w-0 flex-1 whitespace-pre-wrap leading-5 text-muted-foreground">{acceptanceInfo.evidence}</p>
            </div>
          )}
          <div data-testid="acceptance-verified-row" className="flex items-start gap-2">
            <span className="w-8 shrink-0 pt-0.5 text-muted-foreground">已验</span>
            <div className="min-w-0 flex-1">
              <p className="leading-5">{acceptanceInfo.verified ? '已验证' : '未验证'}</p>
              {!acceptanceInfo.verified && (
                !acceptOpen ? (
                  <button type="button" onClick={() => setAcceptOpen(true)}
                    className="mt-1.5 min-h-11 rounded-md border px-2.5 py-1 text-xs hover:bg-accent">标记已验…</button>
                ) : (
                  <div className="mt-1.5 space-y-1.5">
                    <textarea value={acceptEvidence} onChange={(event) => setAcceptEvidence(event.target.value)}
                      rows={3} placeholder="证据：怎么验的、在哪台机器、日志在哪"
                      className="w-full rounded border bg-background px-2 py-1 text-xs" />
                    <div className="flex gap-2">
                      <button type="button" disabled={writesBlocked || acceptBusy || !acceptEvidence.trim()} onClick={() => void submitAccept()}
                        className="min-h-11 rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50">确认</button>
                      <button type="button" onClick={() => { setAcceptOpen(false); setAcceptError('') }}
                        className="min-h-11 rounded-md border px-2.5 py-1 text-xs">取消</button>
                    </div>
                    {acceptError && <p role="alert" className="break-words text-xs text-destructive">{acceptError}</p>}
                  </div>
                )
              )}
            </div>
          </div>
        </div>
      ) : (
        <div className="rounded-lg border p-3 text-xs">
          <div className="mb-1.5 font-medium">{acceptanceLabel}</div>
          {acceptanceEditing ? (
            <div className="space-y-1.5">
              <textarea
                value={acceptanceDraft}
                onChange={(event) => setAcceptanceDraft(event.target.value)}
                placeholder="这张卡怎样算做完了…"
                rows={4}
                className="w-full rounded border bg-background px-2 py-1.5 text-xs"
              />
              <div className="flex gap-2">
                <button type="button" disabled={writesBlocked || acceptanceBusy} onClick={() => void submitAcceptance()}
                  className={cn('rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50', compact && 'min-h-11')}>保存判据</button>
                <button type="button" disabled={writesBlocked || acceptanceBusy} onClick={() => setAcceptanceEditing(false)}
                  className="rounded-md border px-2.5 py-1 text-xs">取消</button>
              </div>
              {acceptanceError && <p role="alert" className="break-words text-xs text-destructive">{acceptanceError}</p>}
            </div>
          ) : (
            <>
              <p className="whitespace-pre-wrap leading-5">{acceptanceInfo.criteria || '尚未填写验收判据。'}</p>
              <button type="button" onClick={beginAcceptanceEdit} className="mt-2 rounded-md border px-2.5 py-1 text-xs hover:bg-accent">编辑判据</button>
            </>
          )}
          {acceptanceInfo.evidence && <p className="mt-2 border-l-2 pl-2 leading-5 text-muted-foreground">{acceptanceInfo.evidence}</p>}
          {!acceptanceInfo.verified && (
            !acceptOpen ? (
              <button type="button" onClick={() => setAcceptOpen(true)}
                className="mt-2 rounded-md border px-2.5 py-1 text-xs hover:bg-accent">标记已验…</button>
            ) : (
              <div className="mt-2 space-y-1.5">
                <textarea value={acceptEvidence} onChange={(event) => setAcceptEvidence(event.target.value)}
                  rows={3} placeholder="证据：怎么验的、在哪台机器、日志在哪"
                  className="w-full rounded border bg-background px-2 py-1 text-xs" />
                <div className="flex gap-2">
                  <button type="button" disabled={writesBlocked || acceptBusy || !acceptEvidence.trim()} onClick={() => void submitAccept()}
                    className={cn('rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50', compact && 'min-h-11')}>确认</button>
                  <button type="button" onClick={() => { setAcceptOpen(false); setAcceptError('') }}
                    className="rounded-md border px-2.5 py-1 text-xs">取消</button>
                </div>
                {acceptError && <p role="alert" className="break-words text-xs text-destructive">{acceptError}</p>}
              </div>
            )
          )}
        </div>
      )}
    </section>
  )

  const renderAttachments = () => (
    <section className="mb-5">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">附件管理</h3>
      <div className="space-y-1.5 rounded-lg border p-3 text-xs">
        {attachments.length > 0 ? (
          <ul className="space-y-1">
            {attachments.map((attachment) => (
              <li key={`${attachment.kind}:${attachment.path}`} className="flex items-center gap-2">
                <span className="min-w-0 flex-1 break-all font-mono">{attachment.path}</span>
                <span className="shrink-0 text-muted-foreground">{attachment.kind || '附件'}</span>
                <button
                  type="button"
                  aria-label={`摘掉 ${attachment.path}`}
                  disabled={writesBlocked || attachmentBusy}
                  onClick={() => void removeAttachment(attachment.path)}
                  className="shrink-0 rounded border px-1.5 py-0.5 text-[11px] disabled:opacity-50"
                >摘掉</button>
              </li>
            ))}
          </ul>
        ) : <p className="text-muted-foreground">尚未挂附件。</p>}
        <div className="flex flex-wrap items-center gap-1.5">
          <select aria-label="附件类型" value={attachmentKind} onChange={(event) => setAttachmentKind(event.target.value)} className="rounded border bg-background px-1.5 py-1 text-xs">
            {['spec', 'plan', 'doc'].map((kind) => <option key={kind} value={kind}>{kind}</option>)}
          </select>
          <input
            value={attachmentPath}
            onChange={(event) => setAttachmentPath(event.target.value)}
            placeholder="docs/superpowers/plans/…"
            className="min-w-0 flex-1 rounded border bg-background px-2 py-1 text-xs"
          />
          <button type="button" disabled={writesBlocked || attachmentBusy || !attachmentPath.trim()} onClick={() => void submitAttachment()}
            className="rounded border px-2 py-1 text-xs disabled:opacity-50">挂上</button>
        </div>
        {attachmentError && <p role="alert" className="break-words text-xs text-destructive">{attachmentError}</p>}
      </div>
    </section>
  )

  const renderMergedMembers = () => mergedMembers.length > 0 ? (
    <section ref={mergeRef} className="mb-5">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">⊕ 并入本卡（状态跟随，验收各自保留）</h3>
      {mergedMembers.map((relation) => {
        const memberID = relationValue(relation, 'From')
        return <div key={memberID} className="mb-1 flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs"><button type="button" className="font-mono underline" onClick={() => onOpenCard(memberID)}>{memberID}</button><span className="text-muted-foreground">未验</span><span className="ml-auto rounded-full border px-1.5 text-[10px]">跟随 badge</span><button type="button" disabled title="CLI: handoff card unmerge" className="rounded border px-1.5 text-[10px] text-muted-foreground">拆回</button></div>
      })}
    </section>
  ) : null

  const renderRelations = () => visibleRelations.length > 0 ? (
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
  ) : null

  const renderChildren = () => (detail?.children ?? []).length > 0 ? (
    <section className="mb-5">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">子任务</h3>
      {(detail?.children ?? []).map((child) => (
        <div key={child.id} className="mb-1 flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs">
          <button type="button" className="font-mono underline" onClick={() => onOpenCard(child.id)}>{child.id}</button>
          <span className="min-w-0 flex-1 truncate">{child.title}</span>
          <span className="ml-auto rounded-full border px-1.5 py-0.5 text-[10px] text-muted-foreground">{child.status}</span>
        </div>
      ))}
    </section>
  ) : null

  const renderTaskRow = (row: NonNullable<CardDetail['task_states']>[number]) => {
    const open = expandedTask === row.TaskID
    const taskDetail = taskDetails[row.TaskID]
    const linked = linkedTaskOf(row, tasks)
    const label = linked?.name.trim() || row.TaskID.slice(0, 8)
    return (
      <div key={`${row.Target}/${row.TaskID}`} className="mb-1 rounded-md border text-xs">
        <div role="button" tabIndex={0} aria-expanded={open} title={row.TaskID}
          onClick={() => toggleTask(row.TaskID)} onKeyDown={(event) => {
            if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); toggleTask(row.TaskID) }
          }} className={cn('flex w-full cursor-pointer items-center gap-2 px-2 py-1.5 text-left', compact && 'mobile-card-task-row')}>
          <span className={cn('font-mono', compact && 'mobile-card-task-label')} title={compact ? row.TaskID : undefined}>{compact ? label : row.TaskID}</span>
          {!compact && <span>{row.Purpose}</span>}
          {linked ? <span className={cn('ml-auto', compact && 'mobile-card-task-state')}><TaskState state={linked.state} /></span>
            : <span className={cn('ml-auto text-muted-foreground', compact && 'mobile-card-task-state')}>实况未知{row.LastType !== '' && ` · 最后事件 ${row.LastType}`}</span>}
          <span className={cn('text-muted-foreground', compact && 'mobile-card-task-target')}>{row.Target}</span>
          {onJumpToTask && <button type="button" aria-label={`跳到 ${row.TaskID}`} title="去该任务所在的目录并打开它的 TUI 标签页；目录解析不到时会开在当前目录下"
            onClick={(event) => { event.stopPropagation(); onJumpToTask(row.TaskID) }} className="shrink-0 rounded border px-1.5 py-0.5 text-[11px] hover:bg-accent">↗</button>}
        </div>
        {open && <div className="border-t px-2 py-2">
          {compact && <p className="mb-2 break-all font-mono text-[11px] text-muted-foreground">完整任务 ID：{row.TaskID}</p>}
          {taskLoading === row.TaskID && <p className="text-xs text-muted-foreground">正在读取工单…</p>}
          {taskErrors[row.TaskID] && <p role="alert" className="break-words text-xs text-destructive">{taskErrors[row.TaskID]}</p>}
          {taskDetail && <TicketsPanel bare tickets={pendingTickets(taskDetail)} disabled={writesBlocked} onReply={(ticket, answer) => replyTaskTicket(row.TaskID, ticket, answer)} />}
        </div>}
      </div>
    )
  }

  const renderTaskRows = () => taskRows.length > 0 ? (
    <section className="mb-5">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">
        {/* 计数与行渲染同源（同一个 taskRows/isRunningRow 派生），不会各说各话 */}
        {runningCount === null ? '关联执行（task）' : `关联执行 · ${runningCount} 个在跑 / 共 ${taskRows.length} 个`}
      </h3>
      {taskRows.filter((row) => !terminalTaskIds.has(row.TaskID)).map(renderTaskRow)}
      {terminalTaskRows.length > 0 && (
        <details data-testid="card-task-history" className="mt-2 rounded-md border px-3">
          <summary className="flex min-h-11 cursor-pointer items-center justify-between text-xs font-medium">历史执行 {terminalTaskRows.length}<span aria-hidden="true">⌄</span></summary>
          <div className="pb-2">{terminalTaskRows.map(renderTaskRow)}</div>
        </details>
      )}
    </section>
  ) : null

  const renderAttention = () => ((detail?.decisions ?? []).length > 0 || (detail?.needs ?? '') !== '') ? (
    <CardAttention disabled={writesBlocked} compact={compact} cardId={id} needs={detail?.needs ?? ''} decisions={detail?.decisions ?? []} onAnswered={load} />
  ) : null

  const renderMove = () => (
    <section className="mb-5">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">环节动作</h3>
      <p className="mb-2 text-xs text-muted-foreground">节点执行由协调者生命周期统一接管；此处只保留状态转移。</p>
      {!moveConfirm ? <button type="button" onClick={() => setMoveConfirm(true)} className={cn('rounded-md border px-2.5 py-1 text-xs hover:bg-accent', compact && 'min-h-11')}>转移状态…</button> : <div className="flex flex-wrap items-center gap-2"><select value={moveTarget} onChange={(event) => setMoveTarget(event.target.value)} className="rounded-md border bg-background px-2 py-1 text-xs"><option value="">选择目标态</option>{states.filter((state) => state !== status).map((state) => <option key={state} value={state}>{state}</option>)}</select><button type="button" disabled={writesBlocked || !moveTarget || moveBusy} onClick={() => void submitMove()} className={cn('rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50', compact && 'min-h-11')}>确认转移</button><button type="button" onClick={() => setMoveConfirm(false)} className="rounded-md border px-2.5 py-1 text-xs">取消</button></div>}
      {moveError && <p role="alert" className="mt-1 break-words text-xs text-destructive">{moveError}</p>}
    </section>
  )

  const renderTimeline = () => (
    <section className="mb-5">
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">Timeline</h3>
      <div className="mb-2 flex flex-wrap gap-1"><span className="sr-only">timeline filter</span>{(['all', 'comment', 'verdict', 'system'] as const).map((filter) => <button key={filter} type="button" onClick={() => setTimelineFilter(filter)} className={`rounded-full border px-2 py-0.5 text-[11px] ${timelineFilter === filter ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}>{filter === 'all' ? '全部' : filter === 'comment' ? '评论' : filter === 'verdict' ? '裁决' : '系统'}</button>)}</div>
      <div className="space-y-1.5">
        {groups.map((group, index) => group.kind === 'mirror' ? <details key={`mirror-${index}`} className="text-xs text-muted-foreground"><summary className="cursor-pointer">镜像执行事件（{group.events.length}）</summary><div className="ml-2 border-l pl-2">{group.events.map((event) => <div key={event.seq} className="py-0.5">#{event.seq} {eventSummary(event)}</div>)}</div></details> : group.events.map((event) => event.type === 'review_verdict' ? <VerdictCard key={event.seq} event={event} /> : event.type === 'comment' ? <div key={event.seq} className="rounded-lg bg-muted px-3 py-2 text-xs leading-5"><div className="mb-0.5 text-[11px] text-muted-foreground">{event.actor}</div>{eventSummary(event)}</div> : <div key={event.seq} className="flex gap-2 text-xs text-muted-foreground"><span className="font-mono">#{event.seq}</span><span>{eventSummary(event)}</span></div>))}
        {groups.length === 0 && <p className="text-xs text-muted-foreground">还没有事件。</p>}
      </div>
      <div className="mt-3 flex gap-1.5"><input value={note} onChange={(event) => setNote(event.target.value)} onKeyDown={(event) => { if (event.key === 'Enter' && !event.shiftKey) { event.preventDefault(); void submitNote() } }} placeholder="写评论… 用 #B142 引用其他工作项" className="min-w-0 flex-1 rounded-md border bg-background px-2 py-1.5 text-xs" /><button type="button" disabled={writesBlocked || noteBusy || !note.trim()} onClick={() => void submitNote()} className="rounded-md border px-2.5 py-1 text-xs disabled:opacity-50">发布</button></div>
      {noteError && <p role="alert" className="mt-1 text-xs text-destructive">{noteError}</p>}
      <p className="mt-1 text-[11px] text-muted-foreground">#B 号引用会自动建立关联边。</p>
    </section>
  )

  // 桌面块序（重构前原序，一字不动）：协调者面板 → 席位 → 状态列 → 元数据 →
  // 验收 → 附件 → 并入 → 关系 → 子任务 → 关联执行 → 需要你 → 环节动作 → Timeline。
  const renderBlocksDesktop = () => (
    <>
      {renderCoordinatorPanel()}
      {renderCoordinatorSeat()}
      {renderStatusChips()}
      {renderMeta()}
      {renderAcceptance()}
      {renderAttachments()}
      {renderMergedMembers()}
      {renderRelations()}
      {renderChildren()}
      {renderTaskRows()}
      {renderAttention()}
      {renderMove()}
      {renderTimeline()}
    </>
  )

  // compact 三层块序（plan §2 岔口 2）：工作项（状态地图 + 验收）→ 当前动作
  // （等人/裁决答复 → 环节转移 → 关联执行整块 → 协调者面板）→ 证据（Timeline
  // → 附件 → 并入 → 关系 → 子任务 → 元数据 → 席位）。两个归属裁定：关联执行
  // 行与它展开的工单答复是同一 aria-expanded 行块，整块归当前动作不拆；状态列
  // chips 归工作项组（完成定义语境，与验收同族）。层标题上缘 border-t 分隔。
  const tierHeadingClass = 'mb-3 border-t pt-3 text-xs font-semibold text-muted-foreground'
  // B369.10 T8 双跳行：compact 三层块序之上顶置（原型 mobile-card .jump 行）。
  // ①hot 行=needs 态×在跑行×onJumpToTask 三者齐备；②驾驶会话行=driverSession
  // 在场×onOpenDriverSession 注入。已有行的 ↗ 与详情态会话卡入口不撤。
  const renderJumpRows = () => (
    (needsHot && firstRunningTaskId !== null && onJumpToTask !== undefined)
    || (driverSession !== '' && onOpenDriverSession !== undefined) ? (
      <div className="mb-3 space-y-1.5" data-testid="card-jump-rows">
        {needsHot && firstRunningTaskId !== null && onJumpToTask !== undefined && (
          <button type="button" data-testid="card-jump-task" onClick={() => onJumpToTask(firstRunningTaskId)}
            className="flex min-h-11 w-full items-center gap-2 rounded-md border border-amber-200 bg-amber-50 px-2.5 py-2 text-left text-xs text-amber-900">
            <span className="shrink-0 font-semibold">⚑ 当前节点等你裁决</span>
            <span className="ml-auto shrink-0 text-[11px]">→ 任务现场</span>
          </button>
        )}
        {driverSession !== '' && onOpenDriverSession !== undefined && (
          <button type="button" data-testid="card-jump-session" disabled={!driverSessionReady} onClick={onOpenDriverSession}
            className="flex min-h-11 w-full items-center gap-2 rounded-md border px-2.5 py-2 text-left text-xs disabled:opacity-50">
            <span className="shrink-0 font-semibold">💬 {driverSessionReady ? '驾驶会话' : '驾驶会话（尚未就绪）'}</span>
            <span className="ml-auto shrink-0 text-[11px] text-muted-foreground">{driverSession} · → 群聊</span>
          </button>
        )}
      </div>
    ) : null
  )
  const renderBlocksCompact = () => (
    <>
      {renderJumpRows()}
      <h2 data-testid="card-tier-work" className={tierHeadingClass}>工作项</h2>
      {renderStatusChips()}
      <h2 data-testid="card-tier-action" className={tierHeadingClass}>当前动作</h2>
      {renderAttention()}
      {renderTaskRows()}
      <details data-testid="card-secondary-actions" className="mb-3 border-t pt-3">
        <summary className="min-h-11 cursor-pointer text-sm font-medium">更多工作项操作</summary>
        <div className="pt-3">
          {detail && <button type="button" disabled={writesBlocked} onClick={beginTitleEdit} className="mb-3 min-h-11 rounded border px-3 text-sm">改标题</button>}
          {onMigrate && <button type="button" disabled={writesBlocked} onClick={onMigrate} className="mb-3 min-h-11 rounded border px-3 text-sm">迁移工作项</button>}
          {renderMove()}{renderMeta()}{renderCoordinatorPanel()}{renderCoordinatorSeat()}
        </div>
      </details>
      <h2 data-testid="card-tier-evidence" className={tierHeadingClass}>证据与验收</h2>
      <details data-testid="card-evidence-details" className="rounded-lg border px-3">
        <summary className="min-h-11 cursor-pointer text-sm font-medium">验收、活动历史与相关资料</summary>
        <div className="pt-3">{renderAcceptance()}{renderTimeline()}{renderAttachments()}{renderMergedMembers()}{renderRelations()}{renderChildren()}</div>
      </details>
    </>
  )

  return (
    // 桌面类串逐字节保留（w-[560px] max-w-[92vw]）；compact 全宽（inset-x-0）
    // + 触点基线类。aria-modal/焦点移交 compact-only（§3.1）：桌面是 560px
    // 侧板、看板左半仍可见可点（双栏是设计形态），贴 aria-modal 是向读屏谎报。
    <aside
      ref={panelRef}
      tabIndex={compact ? -1 : undefined}
      className={
        compact
          ? `absolute inset-y-0 right-0 inset-x-0 z-40 flex flex-col border-l bg-background shadow-xl ${TOUCH_BASELINE}`
          : 'absolute inset-y-0 right-0 z-40 flex w-[560px] max-w-[92vw] flex-col border-l bg-background shadow-xl'
      }
      role="dialog"
      aria-label="工作项详情"
      aria-modal={compact ? 'true' : undefined}
    >
      <header className="border-b px-4 py-3">
        <div className={cn('flex items-center gap-2', compact && 'mobile-card-drawer-header')} data-testid="card-drawer-header">
          {compact && <button type="button" aria-label="返回工作项" onClick={onClose} className="size-11 shrink-0 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground">
            <ChevronLeft className="size-5" />
          </button>}
          <span className="font-mono text-xs text-muted-foreground">{value(card, 'id', id)}</span>
          {/* B287：头部状态只渲染一次——单枚深色 chip，节点标签优先、状态回落。 */}
          <span className="rounded-full bg-slate-900 px-2 py-0.5 text-xs text-white">{nodeLabel ?? (status || '加载中')}</span>
          {acceptanceInfo.verified && <span className="rounded-full border border-green-300 bg-green-50 px-2 py-0.5 text-[10px] text-green-700">已验</span>}
          {!compact && <button type="button" aria-label="关闭" onClick={onClose} className="ml-auto rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"><X className="size-4" /></button>}
        </div>
        {titleEditing ? (
          <div className="mt-2 flex items-center gap-2">
            <input
              aria-label="标题"
              value={titleDraft}
              onChange={(event) => setTitleDraft(event.target.value)}
              className="min-w-0 flex-1 rounded border bg-background px-2 py-1 text-sm"
            />
            <button type="button" disabled={writesBlocked || titleBusy || !titleDraft.trim()} onClick={() => void submitTitle()}
              className="rounded border px-2 py-1 text-xs disabled:opacity-50">保存标题</button>
            <button type="button" disabled={writesBlocked || titleBusy} onClick={() => setTitleEditing(false)}
              className="rounded border px-2 py-1 text-xs">取消</button>
          </div>
        ) : (
          <div className="mt-1 flex items-center gap-2">
            <h2 className={cn("min-w-0 flex-1 text-sm font-semibold", !compact && "truncate")}>{detail ? cardTitle(detail) : '工作项详情'}</h2>
            {detail && !compact && <button type="button" disabled={writesBlocked} onClick={beginTitleEdit} className="rounded border px-2 py-1 text-xs">改标题</button>}
          </div>
        )}
        {titleError && <p role="alert" className="mt-1 text-xs text-destructive">{titleError}</p>}
      </header>
      {/* S4（B426）：compact 抽屉顶部筛选区——固定在 header 下、滚动区外（aside
          是 flex-col），作用于背后列表；桌面不渲染。 */}
      {compact && compactFilters}
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {writesBlocked && detail && <p className="mb-3 text-sm text-muted-foreground">当前数据不可用于提交，恢复连接后再处理。</p>}
        {error && <p role="alert" className="mb-3 break-words rounded border border-destructive/40 bg-destructive/5 p-2 text-sm text-destructive">{error}</p>}
        {!detail && !error && <p className="text-sm text-muted-foreground">正在读取账本…</p>}
        {detail && (compact ? renderBlocksCompact() : renderBlocksDesktop())}
      </div>
    </aside>
  )
}
