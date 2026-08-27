import { useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { answerDecision, fetchCards, fetchDecisions, fetchFlow, fetchFlows, fetchLedgerHealth } from '../../api/ledger'
import type { Decision, FlowsResp, NodeDef, UnlinkedSummary } from '../../api/ledger'
import { usePoll } from '../data/usePoll'
import { useTasks } from '../data/useTasks'
import { errorMessage } from '../lib/format'
import { CardDrawer } from './CardDrawer'
import { CardItem } from './CardItem'
import { boardColumns, cardsInColumn, filterNeeds, mergeStateOrder, needsAttention, normalizeBoardLayout, visibleColumns } from './columns'
import { ListView } from './ListView'
import { MigrateDialog } from './MigrateDialog'
import { NewCardDialog } from './NewCardDialog'

const POLL_MS = 2500

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

export function CardsPage() {
  const [view, setView] = useState<'board' | 'list'>('board')
  const [needsOnly, setNeedsOnly] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)
  const [newCardOpen, setNewCardOpen] = useState(false)
  const [migrateCardId, setMigrateCardId] = useState<string | null>(null)
  const [drawerFocus, setDrawerFocus] = useState<'merge' | undefined>()
  const [project, setProject] = useState('')
  const [workflow, setWorkflow] = useState('')
  const [search, setSearch] = useState('')
  const [includeArchived, setIncludeArchived] = useState(false)
  const [flows, setFlows] = useState<FlowsResp | null>(null)
  const [flowsError, setFlowsError] = useState('')
  const [drawerNodes, setDrawerNodes] = useState<NodeDef[] | undefined>()
  const cardsPoll = usePoll(() => fetchCards(includeArchived ? 'all=1' : ''), POLL_MS)
  const decisionsPoll = usePoll(() => fetchDecisions(true), POLL_MS)
  const healthPoll = usePoll(fetchLedgerHealth, POLL_MS)
  const navigate = useNavigate()
  // 任务实况走页面级那条 2.5s 流（useTasks），抽屉只吃结果、不自起轮询：
  // 同页两条流会各自跳动，卡上与看板会在不同时刻更新（spec §5）。首拉未回
  // 时给 undefined，抽屉按「计数不可知」显示旧标题，不谎报「0 个在跑」。
  const tasksPoll = useTasks()

  useEffect(() => {
    let cancelled = false
    void fetchFlows().then((result) => { if (!cancelled) setFlows(result) }).catch((err: unknown) => { if (!cancelled) setFlowsError(errorMessage(err)) })
    return () => { cancelled = true }
  }, [])

  useEffect(() => { cardsPoll.refresh() }, [includeArchived]) // eslint-disable-line react-hooks/exhaustive-deps

  const cards = useMemo(() => cardsPoll.data?.cards ?? [], [cardsPoll.data])
  const decisions = decisionsPoll.data ?? []
  const projectOptions = useMemo(() => [...new Set(cards.map((card) => card.project).filter(Boolean))].sort(), [cards])
  const selectedWorkflow = workflow ? flows?.workflows.find((flow) => flow.name === workflow) : undefined
  const workflowStates = workflow
    ? selectedWorkflow?.def.states ?? []
    // 多条流的列序按流程先后拓扑合并——取并集会把某条流独有的后置状态
    // 甩到另一条流的「已完成」后面（见 mergeStateOrder）
    : mergeStateOrder(flows?.workflows.map((flow) => flow.def.states) ?? [])
  const workflowOptions = flows?.workflows ?? []
  const boardLayout = useMemo(
    () => normalizeBoardLayout(workflow ? selectedWorkflow?.def.board : undefined, workflowStates),
    [selectedWorkflow, workflow, workflowStates],
  )
  const displayedColumns = useMemo(() => boardColumns(workflowStates, boardLayout), [boardLayout, workflowStates])
  const healthRows = healthPoll.data?.mirror ?? []
  // 滞后要点名是哪台：判据⑦ 判的是「断链期看板该 target 亮事件流滞后」，
  // 只报一个全局「镜像异常」等于告诉你「有台机器哑了，自己猜是哪台」
  const staleTargets = healthRows
    .filter((row) => Date.now() - Date.parse(row.UpdatedAt) > 60_000)
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

  useEffect(() => {
    if (!selected || !selectedWorkflowName) {
      setDrawerNodes(undefined)
      return
    }
    let cancelled = false
    setDrawerNodes(undefined)
    // 已知缺口：fetchFlow 拿的是工作流最新版，而卡钉的是建卡时那版。两者不同时，
    // 这里画出来的按钮可能多一个或少一个——真正的合法性由后端按卡钉的版本判，
    // 点了不存在的节点会拿到「节点 %q 不在卡 %s 的工作流 … 里」。先接受这个偏差：
    // 修它要给 /api/cards/{id} 的响应带上卡自己那版的节点，属于另一个改动。
    void fetchFlow(selectedWorkflowName)
      .then((flow) => { if (!cancelled) setDrawerNodes(flow.nodes) })
      .catch(() => { if (!cancelled) setDrawerNodes(undefined) })
    return () => { cancelled = true }
  }, [selected, selectedWorkflowName])
  const openDrawer = (id: string, focus?: 'merge') => { setSelected(id); setDrawerFocus(focus) }
  const closeDrawer = () => { setSelected(null); setDrawerFocus(undefined) }
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
        <span className={`ml-auto flex items-center gap-1 text-[11px] ${healthStale ? 'text-amber-700' : 'text-green-600'}`} title={healthStale ? `${healthLabel}——该机器的事件已停止镜像，卡上的 task 实况可能是陈的` : '镜像正常'}>{healthStale ? healthLabel : '●'}</span>
      </header>
      {flowsError && <p role="alert" className="mx-4 mt-2 text-xs text-destructive">流程读取失败：{flowsError}</p>}
      {projectDecisions.length > 0 && <ProjectDecisions decisions={projectDecisions} />}
      <UnlinkedRow summary={cardsPoll.data?.unlinked ?? { count: 0, tasks: [], unknown_targets: [] }} />
      {cardsPoll.data === null ? <p className="p-4 text-sm text-muted-foreground">正在读取账本…</p> : view === 'list' ? <ListView cards={filtered} includeArchived={includeArchived} onIncludeArchivedChange={setIncludeArchived} onOpen={(id) => openDrawer(id)} /> : <div className="flex min-h-0 flex-1 gap-2 overflow-x-auto px-4 py-3">{visibleColumns(displayedColumns, filtered, needsOnly, boardLayout).map((column) => { const inColumn = cardsInColumn(filtered, column, boardLayout); return <section key={column} className="flex min-h-0 w-60 shrink-0 flex-col"><header className="flex items-center gap-1.5 px-1 pb-2 text-xs font-semibold"><span>{column}</span><span className="font-normal text-muted-foreground">{inColumn.length}</span></header><div className="min-h-0 flex-1 space-y-2 overflow-y-auto pb-2">{inColumn.map((card) => <CardItem key={card.id} card={card} onOpen={(focus) => openDrawer(card.id, focus)} onMigrate={() => setMigrateCardId(card.id)} />)}{inColumn.length === 0 && <p className="px-1 py-2 text-xs text-muted-foreground">（空）</p>}</div></section> })}</div>}
      {cardsPoll.disconnected && <p className="border-t bg-amber-50 px-4 py-1.5 text-xs text-amber-800">已断开：{cardsPoll.errorText}（保留最后一次账本数据）</p>}
      {selected && <CardDrawer id={selected} onClose={closeDrawer} onOpenCard={(id) => openDrawer(id)} workflowStates={workflowStates} boardLayout={boardLayout} initialSection={drawerFocus} nodes={drawerNodes} tasks={tasksPoll.data ?? undefined} onJumpToTask={jumpToTask} />}
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
