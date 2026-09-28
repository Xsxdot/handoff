import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError } from '../../api/client'
import { answerDecision, effectiveUnlinkedSummary, fetchCards, fetchDecisions, fetchFlow, fetchFlows, fetchLedgerHealth } from '../../api/ledger'
import { getQueue } from '../../api/scheduling'
import type { CoordinatorAttachInfo } from '../../api/scheduling'
import type { QueueEntry } from '../../api/scheduling'
import type { CardView, Decision, FlowDetail, FlowsResp, NodeDef, UnlinkedSummary } from '../../api/ledger'
import { usePoll } from '../data/usePoll'
import { useUnlinkedSummaryClock } from '../data/useUnlinkedSummaryClock'
import { useTasks } from '../data/useTasks'
import { isDesktopShell, requestOpenCurrentPageInBrowser } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { TOUCH_BASELINE } from '@/lib/touch'
import { cn } from '@/lib/utils'
import { CardDrawer } from './CardDrawer'
import { CardItem } from './CardItem'
import { boardColumns, cardsInColumn, DEFAULT_BOARD_COLUMNS, filterNeeds, mergeStateOrder, mergedLayoutFor, needsAttention, nodeLabelFor, normalizeBoardLayout, visibleColumns } from './columns'
import type { CardLayoutResolver, WorkflowBoardLookup } from './columns'
import { CARD_STATUSES } from './statusVocab'
import { ListView } from './ListView'
import { MigrateDialog } from './MigrateDialog'
import { NewCardDialog } from './NewCardDialog'
import { QueuePanel, queuePositionByCard } from './QueuePanel'

const POLL_MS = 2500
const EMPTY_QUEUE: QueueEntry[] = []

function pinnedWorkflowKey(card: Pick<CardView, 'workflow' | 'workflow_version'>): string | null {
  if (!card.workflow || card.workflow_version === undefined || card.workflow_version <= 0) return null
  return `${card.workflow}@${card.workflow_version}`
}

function projectDecisionCount(decisions: Decision[]): number {
  return decisions.filter((decision) => !decision.card_id).length
}

function UnlinkedRow({ summary }: { summary: UnlinkedSummary }) {
  const now = useUnlinkedSummaryClock(summary.observed_at)
  const effective = effectiveUnlinkedSummary(summary, now)
  const unknownTargets = effective.unknownTargets
  const hasUnknown = unknownTargets.length > 0
  const status = effective.status
  const tasks = effective.tasks
  const count = effective.count
  if (status === 'latest' && count === 0 && !hasUnknown) return null

  const groups = new Map<string, number>()
  for (const task of tasks) groups.set(task.target, (groups.get(task.target) ?? 0) + 1)
  const compact = [...groups.entries()].map(([target, taskCount]) => `${target}×${taskCount}`).join('、')
  const observedLabel = unlinkedObservedLabel(effective.observedAt, now)
  let heading: string
  switch (status) {
    case 'latest':
      heading = `未挂账 task ${count}${compact ? `（${compact}）` : ''}`
      break
    case 'partial':
      heading = `未挂账 task ${count}（部分观测）${compact ? `（${compact}）` : ''}`
      break
    case 'stale':
      heading = `未挂账 task ${count}（上次观测：${observedLabel}）`
      break
    default:
      heading = '未挂账摘要暂无可用观测'
  }
  return (
    <details data-testid="unlinked-summary-row" data-status={status} className="border-y border-amber-200 bg-amber-50 px-4 py-1.5 text-xs text-amber-800">
      <summary className="cursor-pointer">{heading}{hasUnknown ? `／未知目标: ${unknownTargets.join('、')}` : ''}</summary>
      <div className="flex flex-wrap gap-1.5 py-2">
        {status === 'stale' && <span>历史观测时间：<time dateTime={effective.observedAt ?? undefined}>{observedLabel}</time>；当前数量尚未确认。</span>}
        {status === 'unavailable' && <span>系统尚未取得可用摘要，因此不能判断当前是否为零。</span>}
        {tasks.map((task) => <span key={`${task.target}/${task.task_id}`} className="rounded border border-amber-200 bg-background px-2 py-1"><b>{task.target}</b> · <span className="font-mono">{task.task_id}</span> · {task.title} · {task.state}</span>)}
        {hasUnknown && <span>未知目标：{unknownTargets.join('、')}</span>}
      </div>
    </details>
  )
}

function unlinkedObservedLabel(observedAt?: string | null, now = Date.now()): string {
  if (!observedAt) return '时间未知'
  const timestamp = Date.parse(observedAt)
  if (!Number.isFinite(timestamp)) return '时间未知'
  const ageMs = Math.max(0, now - timestamp)
  const age = ageMs < 60_000 ? '不到 1 分钟' : ageMs < 60 * 60_000
    ? `${Math.floor(ageMs / 60_000)} 分钟` : ageMs < 24 * 60 * 60_000
      ? `${Math.floor(ageMs / (60 * 60_000))} 小时` : `${Math.floor(ageMs / (24 * 60 * 60_000))} 天`
  const absolute = new Date(timestamp).toISOString().replace('T', ' ').replace('.000Z', ' UTC').replace(/Z$/, ' UTC')
  return `${absolute}（已过 ${age}）`
}

function ProjectDecisions({ decisions, compact = false }: { decisions: Decision[]; compact?: boolean }) {
  const [answers, setAnswers] = useState<Record<number, string>>({})
  const [busy, setBusy] = useState<number | null>(null)
  const [error, setError] = useState('')
  const answer = async (decision: Decision) => {
    const text = answers[decision.id]?.trim()
    if (!text) return
    setBusy(decision.id)
    setError('')
    try {
      await answerDecision(decision.id, text)
      setAnswers((current) => ({ ...current, [decision.id]: '' }))
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(null)
    }
  }
  return (
    <div className="mx-4 mt-2 space-y-1.5">
      {decisions.map((decision) => <div key={decision.id} className="flex flex-wrap items-center gap-2 rounded-md border border-amber-200 bg-amber-50 px-2.5 py-1.5 text-xs text-amber-900"><span className="shrink-0 rounded-full border border-amber-300 px-1.5 py-0.5 text-[10px]">项目级请示 · 不挂卡</span><span className="font-mono">⚖ #{decision.id}</span><span className="min-w-0 flex-1">{decision.body}</span>{decision.created_by && <span className="shrink-0 text-[10px] text-amber-700/70">{decision.created_by}</span>}<input value={answers[decision.id] ?? ''} onChange={(event) => setAnswers((current) => ({ ...current, [decision.id]: event.target.value }))} placeholder="答复这条请示…" className="w-40 rounded border bg-background px-2 py-1 text-xs" /><button type="button" disabled={busy === decision.id || !(answers[decision.id] ?? '').trim()} onClick={() => void answer(decision)} className={cn('rounded border px-2 py-1 text-xs disabled:opacity-50', compact && 'min-h-11')}>答复</button></div>)}
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </div>
  )
}

/** 可选终端回调由 Shell 注入；工作项页不持有 Workbench 具体实现。 */
export interface CardsPageProps {
  onOpenCoordinatorTerminal?: (info: CoordinatorAttachInfo) => void
  // B369.7 seam：抽屉开关上抛（Shell 紧凑下写 URL：open=nav.openCard、close=
  // nav.closeCard）。缺省不注入 = 桌面与既有单测零改动；注入方负责 URL 收口，
  // 本组件仍维护内部 selected state（URL→state 恢复 effect 不变）。
  onDrawerCardChange?: (cardId: string | null) => void
  // B369.7 seam：任务跳转 href 注入（紧凑下带 from/tab 任务现场返回语境）。
  // 缺省 = 裸 /tasks/:id（桌面原样）。/tasks/:id 作为卡到任务唯一出口不变（B181）。
  taskJumpHref?: (taskId: string) => string
  // compact（B369.8 T3）：紧凑扫描面。缺省 false = 桌面头部/看板/列表逐字节不动。
  // 三个处置：①头部拆三行（主控/词表/次级控制），看板/列表切换不渲染；
  // ②内容区单列 CardItem 纵堆（看板横滚与八列表格在 compact 不作为扫描面）；
  // ③main 根挂触点基线类（24px 次级底线，主动作仍逐枚 min-h-11）。
  compact?: boolean
  // B369.10 T8 seam：卡 → 驾驶会话双跳（Shell 会话流反查，零新端点）。缺席 =
  // 抽屉双跳行第二跳不渲染（桌面原样）。
  onOpenSessionForCard?: (cardId: string) => void
}

/** 参数：协调者终端回调与紧凑 seam；返回：工作项看板/列表与抽屉。 */
export function CardsPage({ onOpenCoordinatorTerminal, onDrawerCardChange, taskJumpHref, compact = false, onOpenSessionForCard }: CardsPageProps = {}) {
  const [searchParams] = useSearchParams()
  const projectFromUrl = searchParams.get('project') ?? ''
  const [view, setView] = useState<'board' | 'list'>('board')
  const [needsOnly, setNeedsOnly] = useState(false)
  // statusFilter：移动「卡」tab 的状态词表筛选（'' = 全部）。词表是受控的
  // CARD_STATUSES（与 Go 逐值一致），不提供自由输入——自由输入等于允许词表外串。
  const [statusFilter, setStatusFilter] = useState('')
  const [selected, setSelected] = useState<string | null>(null)
  const [newCardOpen, setNewCardOpen] = useState(false)
  const [migrateCardId, setMigrateCardId] = useState<string | null>(null)
  const [drawerFocus, setDrawerFocus] = useState<'merge' | undefined>()
  const [project, setProject] = useState(projectFromUrl)
  const [workflow, setWorkflow] = useState('')
  const [search, setSearch] = useState('')
  const [includeArchived, setIncludeArchived] = useState(false)
  const [queueOpen, setQueueOpen] = useState(false)
  const [flows, setFlows] = useState<FlowsResp | null>(null)
  const [flowsError, setFlowsError] = useState('')
  const [drawerNodes, setDrawerNodes] = useState<NodeDef[] | undefined>()
  const [pinnedWorkflows, setPinnedWorkflows] = useState<Record<string, FlowDetail>>({})
  const previousQueueCount = useRef(0)
  const navigate = useNavigate()
  const location = useLocation()
  const cardDeepLink = new URLSearchParams(location.search).get('card') ?? ''
  const cardsPoll = usePoll(() => fetchCards(includeArchived || cardDeepLink !== '' ? 'all=1' : ''), POLL_MS)
  const decisionsPoll = usePoll(() => fetchDecisions(true), POLL_MS)
  const healthPoll = usePoll(fetchLedgerHealth, POLL_MS)
  const queuePoll = usePoll(async () => {
    console.info('queue.poll.start', { intervalMs: 5000 })
    try {
      const response = await getQueue()
      previousQueueCount.current = response.queue.length
      console.info('queue.poll.success', { count: response.queue.length, stale: false })
      return response
    } catch (cause) {
      const status = cause instanceof ApiError ? cause.status : 0
      if (status === 401) console.warn('queue.poll.expired', { status })
      console.error('queue.poll.error', { count: previousQueueCount.current, stale: true, status, cause })
      throw cause
    }
  }, 5000)
  const showOpenInBrowser = isDesktopShell()
  useEffect(() => { setProject(projectFromUrl) }, [projectFromUrl])
  // 任务实况走页面级那条 2.5s 流（useTasks），抽屉只吃结果、不自起轮询：
  // 同页两条流会各自跳动，卡上与看板会在不同时刻更新（spec §5）。首拉未回
  // 时给 undefined，抽屉按「计数不可知」显示旧标题，不谎报「0 个在跑」。
  const tasksPoll = useTasks()

  useEffect(() => {
    let cancelled = false
    void fetchFlows()
      .then((result) => {
        if (cancelled) return
        setFlows(result)
        console.info('cards.flows.loaded', {
          workflows: result.workflows.length,
          withBoard: result.workflows.filter((flow) => flow.def.board).length,
        })
      })
      .catch((err: unknown) => { if (!cancelled) setFlowsError(errorMessage(err)) })
    return () => { cancelled = true }
  }, [])

  useEffect(() => { cardsPoll.refresh() }, [includeArchived, cardDeepLink]) // eslint-disable-line react-hooks/exhaustive-deps

  const cards = useMemo(() => cardsPoll.data?.cards ?? [], [cardsPoll.data])
  const queueEntries = useMemo(() => queuePoll.data?.queue ?? EMPTY_QUEUE, [queuePoll.data])
  const queuePositions = useMemo(() => queuePositionByCard(queueEntries), [queueEntries])
  const decisions = decisionsPoll.data ?? []
  const projectOptions = useMemo(() => [...new Set(cards.map((card) => card.project).filter(Boolean))].sort(), [cards])
  const selectedWorkflow = workflow ? flows?.workflows.find((flow) => flow.name === workflow) : undefined
  const workflowStates = useMemo(() => workflow
    ? selectedWorkflow?.def.states ?? []
    // 多条流的列序按流程先后拓扑合并——取并集会把某条流独有的后置状态
    // 甩到另一条流的「已完成」后面（见 mergeStateOrder）
    : mergeStateOrder(flows?.workflows.map((flow) => flow.def.states) ?? []),
  [flows, selectedWorkflow, workflow])
  const workflowOptions = flows?.workflows ?? []
  const boardLayout = useMemo(
    () => normalizeBoardLayout(workflow ? selectedWorkflow?.def.board : undefined, workflowStates),
    [selectedWorkflow, workflow, workflowStates],
  )
  // 呈现布局按流**当前版本**的 board 解析（D1）：合并视图逐卡解析，单流视图所有卡用选中流。
  const layoutForWorkflow = useMemo<WorkflowBoardLookup>(
    () => (name) => {
      const flow = flows?.workflows.find((item) => item.name === name)
      return flow ? normalizeBoardLayout(flow.def.board, flow.def.states) : undefined
    },
    [flows],
  )
  const cardLayoutResolver = useMemo<CardLayoutResolver>(
    () => (workflow ? () => boardLayout : (card) => mergedLayoutFor(card, layoutForWorkflow)),
    [boardLayout, layoutForWorkflow, workflow],
  )
  // 合并视图列集合恒默认五列（D2）；单流视图沿用该流自身列名（现状不变）。
  const displayedColumns = useMemo(
    () => (workflow ? boardColumns(workflowStates, boardLayout) : [...DEFAULT_BOARD_COLUMNS]),
    [boardLayout, workflow, workflowStates],
  )
  // 卡挂了 flows 里没有的流（已删/改名）时留一条可查告警；归列仍按默认映射诚实回落。
  const missingWorkflows = useMemo(() => {
    if (!flows) return []
    const known = new Set(flows.workflows.map((flow) => flow.name))
    return [...new Set(cards.map((card) => card.workflow).filter((name) => name !== '' && !known.has(name)))]
  }, [cards, flows])
  useEffect(() => {
    if (missingWorkflows.length > 0) console.warn('cards.board.workflow.missing', { workflows: missingWorkflows })
  }, [missingWorkflows])
  const healthRows = healthPoll.data?.mirror ?? []
  // 滞后要点名是哪台：判据⑦ 判的是「断链期看板该 target 亮事件流滞后」，
  // 只报一个全局「镜像异常」等于告诉你「有台机器哑了，自己猜是哪台」。
  // Live === false 是「挂账全归档、没东西可镜像」——心跳停在最后一条是正常静默，
  // 不当断链。字段缺席（旧 agentd）按仍在飞处理，避免把真断链藏掉。
  const staleTargets = healthRows
    .filter((row) => row.Live !== false && Date.now() - Date.parse(row.UpdatedAt) > 60_000)
    .map((row) => row.Target)
  const healthStale = healthPoll.disconnected || staleTargets.length > 0
  const healthLabel = healthPoll.disconnected
    ? '看板离线'
    : staleTargets.length > 0
      ? `事件流滞后: ${staleTargets.join('、')}`
      : ''

  const filtered = useMemo(() => {
    const query = search.trim().toLowerCase()
    const base = cards.filter((card) => {
      if (project && card.project !== project) return false
      if (workflow && card.workflow !== workflow) return false
      return query === '' || card.id.toLowerCase().includes(query) || card.title.toLowerCase().includes(query)
    })
    const byStatus = statusFilter === '' ? base : base.filter((card) => card.status === statusFilter)
    return filterNeeds(byStatus, needsOnly)
  }, [cards, needsOnly, project, search, workflow, statusFilter])
  const attentionCount = cards.filter(needsAttention).length + projectDecisionCount(decisions)
  // 项目级请示不跟筛选走：它被算进了「需要你」徽标，只在筛选态显示等于
  // 徽标数字有一部分永远看不见（同一类毛病见 visibleColumns 的注释）
  const projectDecisions = decisions.filter((decision) => !decision.card_id)
  const selectedCard = selected ? cards.find((card) => card.id === selected) : undefined
  const selectedWorkflowName = selectedCard?.workflow ?? ''
  const selectedWorkflowVersion = selectedCard?.workflow_version
  const selectedPinnedKey = selectedCard ? pinnedWorkflowKey(selectedCard) : null
  const selectedPinnedWorkflow = selectedPinnedKey ? pinnedWorkflows[selectedPinnedKey] : undefined

  useEffect(() => {
    const requested = new Map<string, { name: string; version: number }>()
    for (const card of cards) {
      const key = pinnedWorkflowKey(card)
      if (key && !pinnedWorkflows[key]) requested.set(key, { name: card.workflow, version: card.workflow_version as number })
    }
    if (requested.size === 0) return
    let cancelled = false
    void Promise.all([...requested.entries()].map(async ([key, target]) => [key, await fetchFlow(target.name, target.version)] as const))
      .then((details) => {
        if (cancelled) return
        setPinnedWorkflows((current) => Object.fromEntries([...Object.entries(current), ...details]))
        console.info('cards.workflow.pinned.done', { count: details.length })
      })
      .catch((cause: unknown) => {
        if (!cancelled) console.error('cards.workflow.pinned.error', { requested: [...requested.keys()], cause })
      })
    return () => { cancelled = true }
  }, [cards, pinnedWorkflows])

  useEffect(() => {
    if (!cardDeepLink || !cards.some((card) => card.id === cardDeepLink)) return
    setSelected(cardDeepLink)
  }, [cardDeepLink, cards])

  useEffect(() => {
    if (!selected || !selectedWorkflowName) {
      setDrawerNodes(undefined)
      return
    }
    let cancelled = false
    setDrawerNodes(undefined)
    if (selectedPinnedWorkflow) {
      setDrawerNodes(selectedPinnedWorkflow.nodes)
      return () => { cancelled = true }
    }
    if (selectedWorkflowVersion === undefined || selectedWorkflowVersion <= 0) {
      console.warn('cards.workflow.drawer.unversioned', { card: selected, workflow: selectedWorkflowName })
      return () => { cancelled = true }
    }
    // 抽屉节点动作同样必须使用卡片钉住的版本；没有版本的旧列表数据不猜节点集。
    void fetchFlow(selectedWorkflowName, selectedWorkflowVersion)
      .then((flow) => { if (!cancelled) setDrawerNodes(flow.nodes) })
      .catch((cause: unknown) => {
        if (!cancelled) {
          console.error('cards.workflow.drawer.error', { card: selected, workflow: selectedWorkflowName, version: selectedWorkflowVersion, cause })
          setDrawerNodes(undefined)
        }
      })
    return () => { cancelled = true }
  }, [selected, selectedWorkflowName, selectedWorkflowVersion, selectedPinnedWorkflow])
  // drawerTriggerRef 记「打开抽屉时焦点在哪」（B369.8 §4，compact-only）：
  // openDrawer 时记 document.activeElement，closeDrawer 归还（isConnected 守卫
  // 兜住触发钮已被重渲染/卸载摘走的情形）。桌面双栏非模态，不记不还（零变化）。
  const drawerTriggerRef = useRef<HTMLElement | null>(null)
  const openDrawer = (id: string, focus?: 'merge') => {
    if (compact) {
      drawerTriggerRef.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    }
    setSelected(id)
    setDrawerFocus(focus)
    onDrawerCardChange?.(id)
  }
  const closeDrawer = () => {
    setSelected(null)
    setDrawerFocus(undefined)
    onDrawerCardChange?.(null)
    if (compact) {
      const trigger = drawerTriggerRef.current
      drawerTriggerRef.current = null
      if (trigger && trigger.isConnected) trigger.focus()
    }
    // card 参数的 URL 清理归注入 seam 的层收口（紧凑 closeCard 会保留 project）；
    // 未注入 seam（桌面）维持既有 replace 行为。
    if (!onDrawerCardChange && new URLSearchParams(location.search).has('card')) navigate('/cards', { replace: true })
  }
  const newCardWorkflows = flows?.workflows.map((item) => item.name) ?? []
  // 卡到任务的唯一出口是 /tasks/:id 深链：目录解析、开 TUI tab、跨机全由
  // Shell 既有的 TaskDeepLink 完成，这里绝不顺手做目录切换（spec §3.3 明令
  // 禁止复制那套逻辑）。跳转即离开 /cards 是接受的代价（spec §3.3 已弃选回退机制）。
  const jumpToTask = (taskId: string) => {
    console.debug('[cards] 从卡跳转任务深链', taskId)
    navigate(taskJumpHref?.(taskId) ?? `/tasks/${taskId}`)
  }

  // renderCardItem 是 compact 单列的 CardItem 传参（B369.8 T3）。pinned-workflow
  // 解析与看板列内（下方 JSX）完全一致：queuePosition/nodeTag/onOpen/onMigrate
  // 同源同值。刻意不复用到看板分支——桌面 JSX 一字不动是本卡的桌面净改动承诺。
  const renderCardItem = (card: CardView) => {
    const pinned = pinnedWorkflowKey(card)
    const detail = pinned ? pinnedWorkflows[pinned] : undefined
    const cardStates = detail?.states ?? []
    const cardBoard = detail ? normalizeBoardLayout(detail.board, cardStates) : boardLayout
    const cardNodes = detail?.nodes?.map((node) => node.name) ?? []
    return (
      <CardItem
        key={card.id}
        card={card}
        queuePosition={queuePositions.get(card.id)}
        nodeTag={detail ? nodeLabelFor(card.status, cardNodes, cardBoard) : undefined}
        onOpen={(focus) => openDrawer(card.id, focus)}
        onMigrate={() => setMigrateCardId(card.id)}
      />
    )
  }

  // 卡面内容（除抽屉与对话框外的全部）。compact 时包进 display:contents 的
  // cards-surface 包装（§4）：包装盒不生成、布局零变化；抽屉覆盖期整块
  // aria-hidden + inert——display:contents 无盒，pointer-events 类无意义且
  // 不需要，inert 在平面树上断指针与焦点。桌面不包（双栏可见可点是设计形态）。
  const surfaceContent = (
    <>
      <QueuePanel
        entries={queueEntries}
        open={queueOpen}
        loading={queuePoll.data === null && !queuePoll.disconnected && !queuePoll.sessionExpired}
        disconnected={queuePoll.disconnected}
        sessionExpired={queuePoll.sessionExpired}
        errorText={queuePoll.errorText}
        onToggle={() => setQueueOpen((current) => !current)}
        onOpenCard={(id) => openDrawer(id)}
      />
      {flowsError && <p role="alert" className="mx-4 mt-2 text-xs text-destructive">流程读取失败：{flowsError}</p>}
      {projectDecisions.length > 0 && <ProjectDecisions decisions={projectDecisions} compact={compact} />}
      {cardsPoll.data?.unlinked && <UnlinkedRow summary={cardsPoll.data.unlinked} />}
      {cardsPoll.data === null ? (
        <p className="p-4 text-sm text-muted-foreground">正在读取账本…</p>
      ) : compact ? (
        // B369.8 T3：compact 单列扫描面——看板横滚与八列表格不作为扫描面
        //（view 切换控件在 compact 头部不渲染，view 恒 'board'，这里不再判它）。
        // 传参经 renderCardItem 与看板列内完全一致。
        <div data-testid="cards-single-column" className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto px-3 py-3">
          {filtered.map(renderCardItem)}
          {filtered.length === 0 && <p className="px-1 py-2 text-xs text-muted-foreground">（没有匹配的工作项）</p>}
        </div>
      ) : view === 'list' ? <ListView cards={filtered} includeArchived={includeArchived} onIncludeArchivedChange={setIncludeArchived} onOpen={(id) => openDrawer(id)} /> : <div className="flex min-h-0 flex-1 gap-2 overflow-x-auto px-4 py-3">{visibleColumns(displayedColumns, filtered, needsOnly, cardLayoutResolver).map((column) => { const inColumn = cardsInColumn(filtered, column, cardLayoutResolver); return <section key={column} className="flex min-h-0 w-60 shrink-0 flex-col"><header className="flex items-center gap-1.5 px-1 pb-2 text-xs font-semibold"><span>{column}</span><span className="font-normal text-muted-foreground">{inColumn.length}</span></header><div className="min-h-0 flex-1 space-y-2 overflow-y-auto pb-2">{inColumn.map((card) => { const pinned = pinnedWorkflowKey(card); const detail = pinned ? pinnedWorkflows[pinned] : undefined; const cardLayout = cardLayoutResolver(card); const cardNodes = detail?.nodes?.map((node) => node.name) ?? []; return <CardItem key={card.id} card={card} queuePosition={queuePositions.get(card.id)} nodeTag={detail ? nodeLabelFor(card.status, cardNodes, cardLayout) : undefined} onOpen={(focus) => openDrawer(card.id, focus)} onMigrate={() => setMigrateCardId(card.id)} /> })}{inColumn.length === 0 && <p className="px-1 py-2 text-xs text-muted-foreground">（空）</p>}</div></section> })}</div>}
      {cardsPoll.disconnected && <p className="border-t bg-amber-50 px-4 py-1.5 text-xs text-amber-800">已断开：{cardsPoll.errorText}（保留最后一次账本数据）</p>}
    </>
  )

  return (
    <main className={cn('relative flex h-full min-h-0 w-full flex-col bg-background', compact && TOUCH_BASELINE)}>
      {compact ? (
        // B369.8 review 必修：头部三行与内容面同在 cards-surface 硬闸内。
        // compact 抽屉全宽不透明覆盖，覆盖期头部控件（⚑需要你/chips/两个
        // select/搜索/+新建）必须与内容一起退出读屏树/Tab 序/指针命中
        // （plan §4 被盖面含 header；验收判据「覆盖层后的后台内容对读屏与
        // 键盘不可达」在头部切片同样成立）。contents 包装不生成盒，三行与
        // surfaceContent 的子元素仍是 main 的有效 flex 子项，拓扑零变化。
        <div
          data-testid="cards-surface"
          className="contents"
          aria-hidden={selected !== null}
          {...(selected !== null ? { inert: true } : {})}
        >
          {/* B369.8 岔口 6：compact 头部三行、全部常驻不折叠（390 宽下常驻比
              折叠少一次点击）。桌面 header（下方分支）逐字节不动。 */}
          <div className="flex min-h-11 flex-wrap items-center gap-2 border-b px-3 py-1.5">
            <span className="text-sm font-semibold">工作项</span>
            <span
              className={`flex items-center gap-1 text-[11px] ${healthStale ? 'text-amber-700' : 'text-green-600'}`}
              title={healthStale ? `${healthLabel}——该机器的事件已停止镜像，卡上的 task 实况可能是陈的` : '镜像正常'}
            >{healthStale ? healthLabel : '●'}</span>
            <button
              type="button"
              onClick={() => setNeedsOnly((current) => !current)}
              className={`ml-auto inline-flex min-h-11 items-center rounded-md border px-2.5 py-1 text-xs ${needsOnly ? 'border-amber-400 bg-amber-50 text-amber-800' : 'text-amber-700'}`}
            >⚑ 需要你 {attentionCount}</button>
          </div>
          {/* 行 2（词表）：最高频的筛选单独成行，与次级配置（项目/工作流）不同频 */}
          <span data-testid="card-status-filter" className="flex flex-wrap items-center gap-1 border-b px-3 py-1.5">
            {CARD_STATUSES.map((status) => (
              <button
                key={status}
                type="button"
                data-testid={`card-status-${status}`}
                aria-pressed={statusFilter === status}
                onClick={() => {
                  const next = statusFilter === status ? '' : status
                  console.debug('cards.status_filter', { status: next })
                  setStatusFilter(next)
                }}
                className={`rounded-full border px-2 py-0.5 text-[11px] ${statusFilter === status ? 'border-foreground bg-accent font-medium' : 'text-muted-foreground'}`}
              >
                {status}
              </button>
            ))}
          </span>
          {/* 行 3（次级控制行）：全行控件统一 min-h-11（44px 触控）；看板/列表
              切换不渲染——compact 的扫描面只有单列，切换是无消费者的死控件 */}
          <div data-testid="cards-controls-secondary" className="flex flex-wrap items-center gap-2 border-b px-3 py-1.5">
            <select aria-label="项目" value={project} onChange={(event) => setProject(event.target.value)} className="min-h-11 rounded-md border bg-background px-2 py-1 text-xs"><option value="">全部项目</option>{projectOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select>
            <select aria-label="工作流" value={workflow} onChange={(event) => setWorkflow(event.target.value)} className="min-h-11 rounded-md border bg-background px-2 py-1 text-xs"><option value="">全部工作流</option>{workflowOptions.map((item) => <option key={item.name} value={item.name}>{item.name} v{item.version}</option>)}</select>
            <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜 B 号 / 标题" className="min-h-11 min-w-0 flex-1 rounded-md border bg-background px-2 py-1 text-xs" />
            <button type="button" onClick={() => setNewCardOpen(true)} className="min-h-11 rounded-md border px-2.5 py-1 text-xs">+ 新建</button>
            {showOpenInBrowser && (
              <button
                type="button"
                aria-label="从浏览器打开"
                title="从浏览器打开当前工作项页"
                onClick={() => { requestOpenCurrentPageInBrowser() }}
                className="rounded-md border px-2.5 py-1 text-xs"
              >
                从浏览器打开
              </button>
            )}
          </div>
          {surfaceContent}
        </div>
      ) : (
        <>
        <header className="flex flex-wrap items-center gap-2 border-b px-4 py-2.5">
          <span className="text-sm font-semibold">工作项</span>
          <div className="inline-flex overflow-hidden rounded-md border"><button type="button" onClick={() => setView('board')} className={`px-2.5 py-1 text-xs ${view === 'board' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}>看板</button><button type="button" onClick={() => setView('list')} className={`px-2.5 py-1 text-xs ${view === 'list' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}>列表</button></div>
          <select aria-label="项目" value={project} onChange={(event) => setProject(event.target.value)} className="rounded-md border bg-background px-2 py-1 text-xs"><option value="">全部项目</option>{projectOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select>
          <select aria-label="工作流" value={workflow} onChange={(event) => setWorkflow(event.target.value)} className="rounded-md border bg-background px-2 py-1 text-xs"><option value="">全部工作流</option>{workflowOptions.map((item) => <option key={item.name} value={item.name}>{item.name} v{item.version}</option>)}</select>
          <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜 B 号 / 标题" className="w-40 rounded-md border bg-background px-2 py-1 text-xs" />
          <button type="button" onClick={() => setNewCardOpen(true)} className="rounded-md border px-2.5 py-1 text-xs">+ 新建</button>
          <button type="button" onClick={() => setNeedsOnly((current) => !current)} className={`rounded-md border px-2.5 py-1 text-xs ${needsOnly ? 'border-amber-400 bg-amber-50 text-amber-800' : 'text-amber-700'}`}>⚑ 需要你 {attentionCount}</button>
          <span data-testid="card-status-filter" className="flex items-center gap-1">
            {CARD_STATUSES.map((status) => (
              <button
                key={status}
                type="button"
                data-testid={`card-status-${status}`}
                aria-pressed={statusFilter === status}
                onClick={() => {
                  const next = statusFilter === status ? '' : status
                  console.debug('cards.status_filter', { status: next })
                  setStatusFilter(next)
                }}
                className={`rounded-full border px-2 py-0.5 text-[11px] ${statusFilter === status ? 'border-foreground bg-accent font-medium' : 'text-muted-foreground'}`}
              >
                {status}
              </button>
            ))}
          </span>
          {showOpenInBrowser && (
            <button
              type="button"
              aria-label="从浏览器打开"
              title="从浏览器打开当前工作项页"
              // 只发送当前地址的 origin/path/query，并不 navigate，保证桌面仍停在本页。
              onClick={() => { requestOpenCurrentPageInBrowser() }}
              className="ml-auto rounded-md border px-2.5 py-1 text-xs"
            >
              从浏览器打开
            </button>
          )}
          <span className={`${showOpenInBrowser ? '' : 'ml-auto'} flex items-center gap-1 text-[11px] ${healthStale ? 'text-amber-700' : 'text-green-600'}`} title={healthStale ? `${healthLabel}——该机器的事件已停止镜像，卡上的 task 实况可能是陈的` : '镜像正常'}>{healthStale ? healthLabel : '●'}</span>
        </header>
          {surfaceContent}
        </>
      )}
      {selected && <CardDrawer id={selected} onClose={closeDrawer} onOpenCard={(id) => openDrawer(id)} workflowStates={selectedPinnedWorkflow?.states ?? (selectedWorkflowVersion !== undefined && selectedWorkflowVersion > 0 ? workflowStates : undefined)} boardLayout={selectedCard ? cardLayoutResolver(selectedCard) : undefined} initialSection={drawerFocus} nodes={drawerNodes} tasks={tasksPoll.data ?? undefined} onJumpToTask={jumpToTask} onOpenCoordinatorTerminal={onOpenCoordinatorTerminal} compact={compact} onOpenDriverSession={onOpenSessionForCard ? () => onOpenSessionForCard(selected) : undefined} />}
      <NewCardDialog
        open={newCardOpen} project={project} cardProjects={projectOptions} workflows={newCardWorkflows}
        onClose={() => setNewCardOpen(false)}
        onCreated={(id) => { setNewCardOpen(false); cardsPoll.refresh(); openDrawer(id) }}
      />
      <MigrateDialog
        open={migrateCardId !== null}
        cardId={migrateCardId ?? ''}
        onClose={() => setMigrateCardId(null)}
        onMigrated={() => { setMigrateCardId(null); cardsPoll.refresh() }}
      />
    </main>
  )
}
