import { useEffect, useMemo, useRef, useState } from 'react'
import { X } from 'lucide-react'
import { fetchTaskDetail, replyTicket } from '../../api/client'
import type { Task, TaskDetail, Ticket } from '../../api/types'
import { acceptCard, answerDecision, attachFile, clearCardNeeds, detachFile, fetchCardDetail, moveCard, noteCard, patchCard, runCardStep } from '../../api/ledger'
import type { CardDetail, Decision, LedgerEvent, NodeDef, TaskStateRow } from '../../api/ledger'
import { errorMessage } from '../lib/format'
import { TicketsPanel } from '../task/TicketsPanel'
import { boardColumnFor, boardColumns, normalizeBoardLayout, type BoardLayout } from './columns'
import { TaskState } from '../board/StateDot'
import { isTerminalState } from '../workbench/TaskPickerDialog'

type Relation = { From: string; To: string; Type: string }

function logCardStep(level: 'debug' | 'warn' | 'error', event: string, fields: Record<string, unknown>): void {
  const payload = { subsystem: 'cards', event, ...fields }
  if (level === 'error') console.error(payload)
  else if (level === 'warn') console.warn(payload)
  else console.debug(payload)
}

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
function CardAttention({ cardId, needs, decisions, onAnswered }: { cardId: string; needs: string; decisions: Decision[]; onAnswered: () => void }) {
  const [drafts, setDrafts] = useState<Record<number, string>>({})
  const [busy, setBusy] = useState<number | null>(null)
  const [error, setError] = useState('')
  const [clearing, setClearing] = useState(false)
  // 撤回等人标记。撤完立刻重取详情，让红旗当场消失——等轮询的话用户会以为没点上。
  const clearNeeds = async () => {
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
      <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">⚑ 需要你</h3>
      {needs !== '' && (
        <div className="mb-1.5 flex items-start gap-2 rounded-md border border-amber-200 bg-amber-50 px-2.5 py-2 text-xs text-amber-900">
          <span className="shrink-0 font-semibold">等人</span>
          <span className="min-w-0 flex-1 break-words">{needs}</span>
          <button type="button" disabled={clearing} onClick={() => void clearNeeds()}
            className="shrink-0 rounded border border-amber-300 px-2 py-0.5 text-[11px] disabled:opacity-50">已处理</button>
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

// linkedTaskOf 把账本挂账行关联到任务流里的真实任务；关联不上返回 undefined。
// 关联不上是真实情形（任务已归档清出流 / 流首拉未回），调用方按「实况未知」
// 如实降级，不猜不冒充——与 internal/ledger/taskstate.go 文件头「滞后要显性化，
// 不拿陈旧实况冒充新鲜」是同一纪律在前端的落法。
function linkedTaskOf(row: TaskStateRow, tasks: Task[] | undefined): Task | undefined {
  return tasks?.find((task) => task.id === row.TaskID)
}

// isRunningRow 判「这一行此刻在不在跑」。口径刻意与看板分栏、任务选择弹层同源：
// 非 isTerminalState 即在跑（waiting_answer/waiting_review 是「等你动手」，不是
// 「结束」；spec §5 明令复用这一个终态集合，不许自造第三套）。关联不上的一律
// 不算在跑：不知道的事不能报成「活着」。n 是几十量级，不做 memo 化。
function isRunningRow(row: TaskStateRow, tasks: Task[] | undefined): boolean {
  const live = linkedTaskOf(row, tasks)
  return live !== undefined && !isTerminalState(live.state)
}

function pendingTickets(detail: DrawerTaskDetail): Ticket[] {
  return detail.pending_tickets ?? detail.tickets ?? []
}

export function CardDrawer({
  id,
  onClose,
  onOpenCard,
  workflowStates,
  boardLayout,
  initialSection,
  nodes,
  tasks,
  onJumpToTask,
}: {
  id: string
  onClose: () => void
  onOpenCard: (id: string) => void
  workflowStates?: string[]
  // 看板列是工作流的投影，抽屉沿用同一映射避免状态与列名各说各话。
  boardLayout?: BoardLayout
  initialSection?: 'merge'
  nodes?: NodeDef[]
  // 任务实况快照：调用方（CardsPage）把页面级 useTasks() 的结果原样传下来。
  // 抽屉不自起第二条轮询——同页两条 2.5s 流会各自跳动，卡上的状态会和看板
  // 在不同时刻更新（spec §5）。undefined = 流未接入或首拉未回。
  tasks?: Task[]
  // 提供时每行渲染 ↗ 跳转按钮。语义固定为深链 navigate('/tasks/{taskId}')，
  // 由调用方注入（CardsPage 用 useNavigate 实现）；抽屉自己绝不解析目录或切
  // tab——那是 Shell 既有 TaskDeepLink 的职责，复制它等于养第二份会漂移的逻辑
  // （spec §3.3 明令禁止）。缺省不画按钮。
  onJumpToTask?: (taskId: string) => void
}) {
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
  const [stepBusy, setStepBusy] = useState<string | null>(null)
  const [stepStarted, setStepStarted] = useState<string | null>(null)
  const [stepError, setStepError] = useState('')
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
  // 显式给 string：value<T> 会把字面量实参 '' 推成字面量类型 ""，
  // 那样 status === '已完成' 在类型上恒假，tsc -b 直接报 TS2367
  const status = value<string>(card, 'status', '')
  const states = workflowStates?.length ? workflowStates : [status]
  const coordinatorNode = nodes?.find((node) => node.name === status)
  const coordinatorReady = coordinatorNode?.dispatch === true
  const resolvedBoardLayout = normalizeBoardLayout(boardLayout, states)
  const following = value(card, 'following', '')
  const driverSession = value(card, 'driver_session', '')
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

  const beginTitleEdit = () => {
    if (!detail) return
    setTitleDraft(cardTitle(detail))
    setTitleError('')
    setTitleEditing(true)
  }

  const submitTitle = async () => {
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

  const startStep = async (step: string) => {
    setStepBusy(step)
    setStepError('')
    logCardStep('debug', 'coordinator_step_started', { card: id, step })
    try {
      await runCardStep(id, step)
      // 受理即置灰：环节是异步的，再点一次只会撞 409。进展在 Timeline 上
      setStepStarted(step)
      load()
      logCardStep('debug', 'coordinator_step_succeeded', { card: id, step })
    } catch (err) {
      // 409 的冲突原因是后端写的原文（哪张卡的什么环节在跑），逐字显示
      const cause = errorMessage(err)
      setStepError(cause)
      logCardStep('error', 'coordinator_step_failed', { card: id, step, cause })
    } finally {
      setStepBusy(null)
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
    <aside className="absolute inset-y-0 right-0 z-40 flex w-[560px] max-w-[92vw] flex-col border-l bg-background shadow-xl" role="dialog" aria-label="工作项详情">
      <header className="border-b px-4 py-3">
        <div className="flex items-center gap-2">
          <span className="font-mono text-xs text-muted-foreground">{value(card, 'id', id)}</span>
          <span className="rounded-full border px-2 py-0.5 text-xs">{status || '加载中'}</span>
          {acceptanceInfo.verified && <span className="rounded-full border border-green-300 bg-green-50 px-2 py-0.5 text-[10px] text-green-700">已验</span>}
          <button type="button" aria-label="关闭" onClick={onClose} className="ml-auto rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"><X className="size-4" /></button>
        </div>
        {titleEditing ? (
          <div className="mt-2 flex items-center gap-2">
            <input
              aria-label="标题"
              value={titleDraft}
              onChange={(event) => setTitleDraft(event.target.value)}
              className="min-w-0 flex-1 rounded border bg-background px-2 py-1 text-sm"
            />
            <button type="button" disabled={titleBusy || !titleDraft.trim()} onClick={() => void submitTitle()}
              className="rounded border px-2 py-1 text-xs disabled:opacity-50">保存标题</button>
            <button type="button" disabled={titleBusy} onClick={() => setTitleEditing(false)}
              className="rounded border px-2 py-1 text-xs">取消</button>
          </div>
        ) : (
          <div className="mt-1 flex items-center gap-2">
            <h2 className="min-w-0 flex-1 truncate text-sm font-semibold">{detail ? cardTitle(detail) : '工作项详情'}</h2>
            {detail && <button type="button" onClick={beginTitleEdit} className="rounded border px-2 py-1 text-xs">改标题</button>}
          </div>
        )}
        {titleError && <p role="alert" className="mt-1 text-xs text-destructive">{titleError}</p>}
      </header>
      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {error && <p role="alert" className="mb-3 break-words rounded border border-destructive/40 bg-destructive/5 p-2 text-sm text-destructive">{error}</p>}
        {!detail && !error && <p className="text-sm text-muted-foreground">正在读取账本…</p>}
        {detail && (
          <>
            <section className="mb-5" aria-label="协调者动作">
              <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">协调者</h3>
              <button
                type="button"
                disabled={!coordinatorReady || stepBusy !== null || stepStarted !== null}
                title={!coordinatorReady ? '当前状态没有可派发的协调者节点' : undefined}
                onClick={() => { if (coordinatorNode) void startStep(coordinatorNode.name) }}
                className="rounded-md border px-2.5 py-1 text-xs hover:bg-accent disabled:opacity-50"
              >
                ▶ 拉起协调者
              </button>
              {!coordinatorReady && <p className="mt-1 text-xs text-muted-foreground">当前状态未配置可派发节点。</p>}
              {stepStarted === status && <p className="mt-1 text-xs text-muted-foreground">已受理，进展见 Timeline。</p>}
            </section>

            <section className="mb-5">
              <div className="flex flex-wrap items-center gap-1.5 text-[11px]">
                {boardColumns(states, resolvedBoardLayout).map((column) => <span key={column} className={`rounded-full border px-2 py-0.5 ${column === boardColumnFor(status, resolvedBoardLayout) ? 'border-primary bg-primary text-primary-foreground' : column === '结束' ? 'border-dashed text-muted-foreground' : 'text-muted-foreground'}`}>{column}</span>)}
              </div>
            </section>

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
                      <button type="button" disabled={priorityBusy} onClick={() => void submitPriority()} className="rounded border px-1.5 py-0.5 text-[11px] disabled:opacity-50">保存优先级</button>
                      <button type="button" disabled={priorityBusy} onClick={() => setPriorityEditing(false)} className="rounded border px-1.5 py-0.5 text-[11px]">取消</button>
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
                      <button type="button" disabled={baseBusy} onClick={() => void submitBase()}
                        className="rounded border px-2 py-1 text-[11px] disabled:opacity-50">保存基线</button>
                      <button type="button" disabled={baseBusy} onClick={() => setBaseEditing(false)}
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

            <section className="mb-5">
              <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">验收</h3>
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
                      <button type="button" disabled={acceptanceBusy} onClick={() => void submitAcceptance()}
                        className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50">保存判据</button>
                      <button type="button" disabled={acceptanceBusy} onClick={() => setAcceptanceEditing(false)}
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
                        <button type="button" disabled={acceptBusy || !acceptEvidence.trim()} onClick={() => void submitAccept()}
                          className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50">确认</button>
                        <button type="button" onClick={() => { setAcceptOpen(false); setAcceptError('') }}
                          className="rounded-md border px-2.5 py-1 text-xs">取消</button>
                      </div>
                      {acceptError && <p role="alert" className="break-words text-xs text-destructive">{acceptError}</p>}
                    </div>
                  )
                )}
              </div>
            </section>

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
                          disabled={attachmentBusy}
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
                  <button type="button" disabled={attachmentBusy || !attachmentPath.trim()} onClick={() => void submitAttachment()}
                    className="rounded border px-2 py-1 text-xs disabled:opacity-50">挂上</button>
                </div>
                {attachmentError && <p role="alert" className="break-words text-xs text-destructive">{attachmentError}</p>}
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

            {(detail.children ?? []).length > 0 && (
              <section className="mb-5">
                <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">子任务</h3>
                {(detail.children ?? []).map((child) => (
                  <div key={child.id} className="mb-1 flex items-center gap-2 rounded-md border px-2 py-1.5 text-xs">
                    <button type="button" className="font-mono underline" onClick={() => onOpenCard(child.id)}>{child.id}</button>
                    <span className="min-w-0 flex-1 truncate">{child.title}</span>
                    <span className="ml-auto rounded-full border px-1.5 py-0.5 text-[10px] text-muted-foreground">{child.status}</span>
                  </div>
                ))}
              </section>
            )}

            {taskRows.length > 0 && (
              <section className="mb-5">
                <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">
                  {/* 计数与行渲染同源（同一个 taskRows/isRunningRow 派生），不会各说各话 */}
                  {runningCount === null ? '关联执行（task）' : `关联执行 · ${runningCount} 个在跑 / 共 ${taskRows.length} 个`}
                </h3>
                {taskRows.map((row) => {
                  const open = expandedTask === row.TaskID
                  const taskDetail = taskDetails[row.TaskID]
                  const linked = linkedTaskOf(row, tasks)
                  return (
                    <div key={`${row.Target}/${row.TaskID}`} className="mb-1 rounded-md border text-xs">
                      {/* 整行点击=展开工单（现状职责，spec §3.3 不动它）。外层从
                          <button> 换成 div[role=button] 是为了容纳行内的 ↗ 真
                          按钮（button 不能嵌 button）；role/tabIndex/键盘处理
                          照抄 CardItem.tsx:35-44 的行内可点先例，cursor-pointer
                          补回原生 button 自带的指针。 */}
                      <div
                        role="button"
                        tabIndex={0}
                        aria-expanded={open}
                        onClick={() => toggleTask(row.TaskID)}
                        onKeyDown={(event) => {
                          if (event.key === 'Enter' || event.key === ' ') {
                            event.preventDefault()
                            toggleTask(row.TaskID)
                          }
                        }}
                        className="flex w-full cursor-pointer items-center gap-2 px-2 py-1.5 text-left"
                      >
                        {/* 实况来自页面级 2.5s 任务流的真 state，渲染与看板同一套
                            圆点+文案。LastType 只是镜像事件的类型不是状态：
                            turn_failed 可 continue、completed 事件早于落态，拿它判
                            「跑没跑完」会和看板得出相反结论（spec §3.1）。关联不上
                            就写「实况未知」并把 LastType 当线索列出。 */}
                        <span className="font-mono">{row.TaskID}</span><span>{row.Purpose}</span>
                        {linked ? (
                          <span className="ml-auto"><TaskState state={linked.state} /></span>
                        ) : (
                          <span className="ml-auto text-muted-foreground">实况未知{row.LastType !== '' && ` · 最后事件 ${row.LastType}`}</span>
                        )}
                        <span className="text-muted-foreground">{row.Target}</span>
                        {onJumpToTask && (
                          <button
                            type="button"
                            aria-label={`跳到 ${row.TaskID}`}
                            title="去该任务所在的目录并打开它的 TUI 标签页；目录解析不到时会开在当前目录下"
                            onClick={(event) => {
                              // 跳转必须掐掉冒泡：整行的点击语义是展开工单，
                              // 一次点击不能又跳走又把面板拉出来（spec §3.3；
                              // 验收含「去掉 stopPropagation 必须红」的变异复验）
                              event.stopPropagation()
                              onJumpToTask(row.TaskID)
                            }}
                            className="shrink-0 rounded border px-1.5 py-0.5 text-[11px] hover:bg-accent"
                          >↗</button>
                        )}
                      </div>
                      {open && (
                        <div className="border-t px-2 py-2">
                          {/* 远程 task 的工单在这里也答得了：agentd 的 byTask 中间件会把
                              /api/tasks/{id}/* 透明代理到该 task 的属主机器。所以这一段
                              是纯前端复用，不需要任何新后端。 */}
                          {taskLoading === row.TaskID && <p className="text-xs text-muted-foreground">正在读取工单…</p>}
                          {taskErrors[row.TaskID] && <p role="alert" className="break-words text-xs text-destructive">{taskErrors[row.TaskID]}</p>}
                          {taskDetail && (
                            <TicketsPanel
                              bare
                              tickets={pendingTickets(taskDetail)}
                              disabled={false}
                              onReply={(ticket, answer) => replyTaskTicket(row.TaskID, ticket, answer)}
                            />
                          )}
                        </div>
                      )}
                    </div>
                  )
                })}
              </section>
            )}

            {((detail.decisions ?? []).length > 0 || (detail.needs ?? '') !== '') && (
              <CardAttention cardId={id} needs={detail.needs ?? ''} decisions={detail.decisions ?? []} onAnswered={load} />
            )}

            <section className="mb-5">
              <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">环节动作</h3>
              <div className="mb-2 flex flex-wrap gap-2">
                {nodes?.filter((node) => node.dispatch).map((node) => {
                  const base = value<string>(detail, 'effective_base_branch', '')
                  const humanOnly = base !== '' && (node.human_bases ?? []).includes(base)
                  return (
                    <button
                      key={node.name}
                      type="button"
                      title={humanOnly ? `基线 ${base} 在本节点的人工清单里：点了也不会自动跑，会直接转「需要你」` : undefined}
                      disabled={stepBusy !== null || stepStarted !== null}
                      onClick={() => void startStep(node.name)}
                      className={`rounded-md border px-2.5 py-1 text-xs hover:bg-accent disabled:opacity-50 ${humanOnly ? 'text-muted-foreground' : ''}`}
                    >跑「{node.name}」</button>
                  )
                })}
              </div>
              {stepStarted && <p className="mb-2 text-xs text-muted-foreground">已发起，进展见下方 Timeline。</p>}
              {stepError && <p role="alert" className="mb-2 break-words text-xs text-destructive">{stepError}</p>}
              {!moveConfirm ? <button type="button" onClick={() => setMoveConfirm(true)} className="rounded-md border px-2.5 py-1 text-xs hover:bg-accent">转移状态…</button> : <div className="flex flex-wrap items-center gap-2"><select value={moveTarget} onChange={(event) => setMoveTarget(event.target.value)} className="rounded-md border bg-background px-2 py-1 text-xs"><option value="">选择目标态</option>{states.filter((state) => state !== status).map((state) => <option key={state} value={state}>{state}</option>)}</select><button type="button" disabled={!moveTarget || moveBusy} onClick={() => void submitMove()} className="rounded-md bg-primary px-2.5 py-1 text-xs text-primary-foreground disabled:opacity-50">确认转移</button><button type="button" onClick={() => setMoveConfirm(false)} className="rounded-md border px-2.5 py-1 text-xs">取消</button></div>}
              {moveError && <p role="alert" className="mt-1 break-words text-xs text-destructive">{moveError}</p>}
            </section>

            <section className="mb-5">
              <h3 className="mb-1.5 text-xs font-semibold text-muted-foreground">Timeline</h3>
              <div className="mb-2 flex flex-wrap gap-1"><span className="sr-only">timeline filter</span>{(['all', 'comment', 'verdict', 'system'] as const).map((filter) => <button key={filter} type="button" onClick={() => setTimelineFilter(filter)} className={`rounded-full border px-2 py-0.5 text-[11px] ${timelineFilter === filter ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}>{filter === 'all' ? '全部' : filter === 'comment' ? '评论' : filter === 'verdict' ? '裁决' : '系统'}</button>)}</div>
              <div className="space-y-1.5">
                {groups.map((group, index) => group.kind === 'mirror' ? <details key={`mirror-${index}`} className="text-xs text-muted-foreground"><summary className="cursor-pointer">镜像执行事件（{group.events.length}）</summary><div className="ml-2 border-l pl-2">{group.events.map((event) => <div key={event.seq} className="py-0.5">#{event.seq} {eventSummary(event)}</div>)}</div></details> : group.events.map((event) => event.type === 'review_verdict' ? <VerdictCard key={event.seq} event={event} /> : event.type === 'comment' ? <div key={event.seq} className="rounded-lg bg-muted px-3 py-2 text-xs leading-5"><div className="mb-0.5 text-[11px] text-muted-foreground">{event.actor}</div>{eventSummary(event)}</div> : <div key={event.seq} className="flex gap-2 text-xs text-muted-foreground"><span className="font-mono">#{event.seq}</span><span>{eventSummary(event)}</span></div>))}
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
