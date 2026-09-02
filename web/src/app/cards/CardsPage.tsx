import { useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { ApiError } from '../../api/client'
import { answerDecision, fetchCards, fetchDecisions, fetchFlow, fetchFlows, fetchLedgerHealth } from '../../api/ledger'
import { getQueue } from '../../api/scheduling'
import type { CoordinatorAttachInfo } from '../../api/scheduling'
import type { QueueEntry } from '../../api/scheduling'
import type { CardView, Decision, FlowDetail, FlowsResp, NodeDef, UnlinkedSummary } from '../../api/ledger'
import { usePoll } from '../data/usePoll'
import { useTasks } from '../data/useTasks'
import { isDesktopShell, requestOpenCurrentPageInBrowser } from '../lib/desktopShell'
import { errorMessage } from '../lib/format'
import { CardDrawer } from './CardDrawer'
import { CardItem } from './CardItem'
import { boardColumns, cardsInColumn, filterNeeds, mergeStateOrder, needsAttention, nodeLabelFor, normalizeBoardLayout, visibleColumns } from './columns'
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
  const hasUnknown = (summary.unknown_targets?.length ?? 0) > 0
  if (summary.count === 0 && !hasUnknown) return null
  const groups = new Map<string, number>()
  for (const task of summary.tasks ?? []) groups.set(task.target, (groups.get(task.target) ?? 0) + 1)
  const compact = [...groups.entries()].map(([target, count]) => `${target}×${count}`).join('、')
  return (
    <details className="border-y border-amber-200 bg-amber-50 px-4 py-1.5 text-xs text-amber-800">
      <summary className="cursor-pointer">未挂账 task {summary.count}{compact ? `（${compact}）` : ''}{hasUnknown ? `／不可达: ${summary.unknown_targets?.join('、')}` : ''}</summary>
      <div className="flex flex-wrap gap-1.5 py-2">
        {(summary.tasks ?? []).map((task) => <span key={`${task.target}/${task.task_id}`} className="rounded border border-amber-200 bg-background px-2 py-1"><b>{task.target}</b> · <span className="font-mono">{task.task_id}</span> · {task.title} · {task.state}</span>)}
        {hasUnknown && <span>不可达：{summary.unknown_targets?.join('、')}</span>}
      </div>
    </details>
  )
}

function ProjectDecisions({ decisions }: { decisions: Decision[] }) {
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
      {decisions.map((decision) => <div key={decision.id} className="flex flex-wrap items-center gap-2 rounded-md border border-amber-200 bg-amber-50 px-2.5 py-1.5 text-xs text-amber-900"><span className="shrink-0 rounded-full border border-amber-300 px-1.5 py-0.5 text-[10px]">项目级请示 · 不挂卡</span><span className="font-mono">⚖ #{decision.id}</span><span className="min-w-0 flex-1">{decision.body}</span>{decision.created_by && <span className="shrink-0 text-[10px] text-amber-700/70">{decision.created_by}</span>}<input value={answers[decision.id] ?? ''} onChange={(event) => setAnswers((current) => ({ ...current, [decision.id]: event.target.value }))} placeholder="答复这条请示…" className="w-40 rounded border bg-background px-2 py-1 text-xs" /><button type="button" disabled={busy === decision.id || !(answers[decision.id] ?? '').trim()} onClick={() => void answer(decision)} className="rounded border px-2 py-1 text-xs disabled:opacity-50">答复</button></div>)}
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </div>
  )
}

/** 可选终端回调由 Shell 注入；工作项页不持有 Workbench 具体实现。 */
export interface CardsPageProps {
  onOpenCoordinatorTerminal?: (info: CoordinatorAttachInfo) => void
}

/** 参数：协调者终端回调；返回：工作项看板/列表与抽屉。 */
export function CardsPage({ onOpenCoordinatorTerminal }: CardsPageProps = {}) {
  const [searchParams] = useSearchParams()
  const projectFromUrl = searchParams.get('project') ?? ''
  const [view, setView] = useState<'board' | 'list'>('board')
  const [needsOnly, setNeedsOnly] = useState(false)
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
    void fetchFlows().then((result) => { if (!cancelled) setFlows(result) }).catch((err: unknown) => { if (!cancelled) setFlowsError(errorMessage(err)) })
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
  const displayedColumns = useMemo(() => boardColumns(workflowStates, boardLayout), [boardLayout, workflowStates])
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
    return filterNeeds(base, needsOnly)
  }, [cards, needsOnly, project, search, workflow])
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
  const openDrawer = (id: string, focus?: 'merge') => { setSelected(id); setDrawerFocus(focus) }
  const closeDrawer = () => {
    setSelected(null)
    setDrawerFocus(undefined)
    if (new URLSearchParams(location.search).has('card')) navigate('/cards', { replace: true })
  }
  const newCardWorkflows = flows?.workflows.map((item) => item.name) ?? []
  // 卡到任务的唯一出口是 /tasks/:id 深链：目录解析、开 TUI tab、跨机全由
  // Shell 既有的 TaskDeepLink 完成，这里绝不顺手做目录切换（spec §3.3 明令
  // 禁止复制那套逻辑）。跳转即离开 /cards 是接受的代价（spec §3.3 已弃选回退机制）。
  const jumpToTask = (taskId: string) => {
    console.debug('[cards] 从卡跳转任务深链', taskId)
    navigate(`/tasks/${taskId}`)
  }

  return (
    <main className="relative flex h-full min-h-0 w-full flex-col bg-background">
      <header className="flex flex-wrap items-center gap-2 border-b px-4 py-2.5">
        <span className="text-sm font-semibold">工作项</span>
        <div className="inline-flex overflow-hidden rounded-md border"><button type="button" onClick={() => setView('board')} className={`px-2.5 py-1 text-xs ${view === 'board' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}>看板</button><button type="button" onClick={() => setView('list')} className={`px-2.5 py-1 text-xs ${view === 'list' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}>列表</button></div>
        <select aria-label="项目" value={project} onChange={(event) => setProject(event.target.value)} className="rounded-md border bg-background px-2 py-1 text-xs"><option value="">全部项目</option>{projectOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select>
        <select aria-label="工作流" value={workflow} onChange={(event) => setWorkflow(event.target.value)} className="rounded-md border bg-background px-2 py-1 text-xs"><option value="">全部工作流</option>{workflowOptions.map((item) => <option key={item.name} value={item.name}>{item.name} v{item.version}</option>)}</select>
        <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜 B 号 / 标题" className="w-40 rounded-md border bg-background px-2 py-1 text-xs" />
        <button type="button" onClick={() => setNewCardOpen(true)} className="rounded-md border px-2.5 py-1 text-xs">+ 新建</button>
        <button type="button" onClick={() => setNeedsOnly((current) => !current)} className={`rounded-md border px-2.5 py-1 text-xs ${needsOnly ? 'border-amber-400 bg-amber-50 text-amber-800' : 'text-amber-700'}`}>⚑ 需要你 {attentionCount}</button>
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
      {projectDecisions.length > 0 && <ProjectDecisions decisions={projectDecisions} />}
      <UnlinkedRow summary={cardsPoll.data?.unlinked ?? { count: 0, tasks: [], unknown_targets: [] }} />
      {cardsPoll.data === null ? <p className="p-4 text-sm text-muted-foreground">正在读取账本…</p> : view === 'list' ? <ListView cards={filtered} includeArchived={includeArchived} onIncludeArchivedChange={setIncludeArchived} onOpen={(id) => openDrawer(id)} /> : <div className="flex min-h-0 flex-1 gap-2 overflow-x-auto px-4 py-3">{visibleColumns(displayedColumns, filtered, needsOnly, boardLayout).map((column) => { const inColumn = cardsInColumn(filtered, column, boardLayout); return <section key={column} className="flex min-h-0 w-60 shrink-0 flex-col"><header className="flex items-center gap-1.5 px-1 pb-2 text-xs font-semibold"><span>{column}</span><span className="font-normal text-muted-foreground">{inColumn.length}</span></header><div className="min-h-0 flex-1 space-y-2 overflow-y-auto pb-2">{inColumn.map((card) => { const pinned = pinnedWorkflowKey(card); const detail = pinned ? pinnedWorkflows[pinned] : undefined; const cardStates = detail?.states ?? []; const cardBoard = detail ? normalizeBoardLayout(detail.board, cardStates) : boardLayout; const cardNodes = detail?.nodes?.map((node) => node.name) ?? []; return <CardItem key={card.id} card={card} queuePosition={queuePositions.get(card.id)} nodeTag={detail ? nodeLabelFor(card.status, cardNodes, cardBoard) : undefined} onOpen={(focus) => openDrawer(card.id, focus)} onMigrate={() => setMigrateCardId(card.id)} /> })}{inColumn.length === 0 && <p className="px-1 py-2 text-xs text-muted-foreground">（空）</p>}</div></section> })}</div>}
      {cardsPoll.disconnected && <p className="border-t bg-amber-50 px-4 py-1.5 text-xs text-amber-800">已断开：{cardsPoll.errorText}（保留最后一次账本数据）</p>}
      {selected && <CardDrawer id={selected} onClose={closeDrawer} onOpenCard={(id) => openDrawer(id)} workflowStates={selectedPinnedWorkflow?.states ?? (selectedWorkflowVersion !== undefined && selectedWorkflowVersion > 0 ? workflowStates : undefined)} boardLayout={selectedPinnedWorkflow ? normalizeBoardLayout(selectedPinnedWorkflow.board, selectedPinnedWorkflow.states) : selectedWorkflowVersion !== undefined && selectedWorkflowVersion > 0 ? boardLayout : undefined} initialSection={drawerFocus} nodes={drawerNodes} tasks={tasksPoll.data ?? undefined} onJumpToTask={jumpToTask} onOpenCoordinatorTerminal={onOpenCoordinatorTerminal} />}
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
