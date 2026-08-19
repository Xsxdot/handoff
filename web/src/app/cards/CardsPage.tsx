import { useEffect, useMemo, useState } from 'react'
import { answerDecision, fetchCards, fetchDecisions, fetchFlows, fetchLedgerHealth } from '../../api/ledger'
import type { Decision, FlowsResp, UnlinkedSummary } from '../../api/ledger'
import { usePoll } from '../data/usePoll'
import { errorMessage } from '../lib/format'
import { CardDrawer } from './CardDrawer'
import { CardItem } from './CardItem'
import { boardColumns, cardsInColumn, filterNeeds, needsAttention } from './columns'
import { ListView } from './ListView'

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
      {decisions.map((decision) => <div key={decision.id} className="flex flex-wrap items-center gap-2 rounded-md border border-amber-200 bg-amber-50 px-2.5 py-1.5 text-xs text-amber-900"><span className="font-mono">⚖ #{decision.id}</span><span className="min-w-0 flex-1">{decision.body}</span><input value={answers[decision.id] ?? ''} onChange={(event) => setAnswers((current) => ({ ...current, [decision.id]: event.target.value }))} placeholder="答复" className="w-36 rounded border bg-background px-2 py-1 text-xs" /><button type="button" disabled={busy === decision.id || !(answers[decision.id] ?? '').trim()} onClick={() => void answer(decision)} className="rounded border px-2 py-1 text-xs disabled:opacity-50">答复</button></div>)}
      {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
    </div>
  )
}

export function CardsPage() {
  const [view, setView] = useState<'board' | 'list'>('board')
  const [needsOnly, setNeedsOnly] = useState(false)
  const [selected, setSelected] = useState<string | null>(null)
  const [drawerFocus, setDrawerFocus] = useState<'merge' | undefined>()
  const [project, setProject] = useState('')
  const [workflow, setWorkflow] = useState('')
  const [search, setSearch] = useState('')
  const [includeArchived, setIncludeArchived] = useState(false)
  const [flows, setFlows] = useState<FlowsResp | null>(null)
  const [flowsError, setFlowsError] = useState('')
  const cardsPoll = usePoll(() => fetchCards(includeArchived ? 'all=1' : ''), POLL_MS)
  const decisionsPoll = usePoll(() => fetchDecisions(true), POLL_MS)
  const healthPoll = usePoll(fetchLedgerHealth, POLL_MS)

  useEffect(() => {
    let cancelled = false
    void fetchFlows().then((result) => { if (!cancelled) setFlows(result) }).catch((err: unknown) => { if (!cancelled) setFlowsError(errorMessage(err)) })
    return () => { cancelled = true }
  }, [])

  useEffect(() => { cardsPoll.refresh() }, [includeArchived]) // eslint-disable-line react-hooks/exhaustive-deps

  const cards = useMemo(() => cardsPoll.data?.cards ?? [], [cardsPoll.data])
  const decisions = decisionsPoll.data ?? []
  const projectOptions = useMemo(() => [...new Set(cards.map((card) => card.project).filter(Boolean))].sort(), [cards])
  const workflowStates = workflow
    ? flows?.workflows.find((flow) => flow.name === workflow)?.def.states ?? []
    : [...new Set(flows?.workflows.flatMap((flow) => flow.def.states) ?? [])]
  const workflowOptions = flows?.workflows ?? []
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
  const projectDecisions = needsOnly ? decisions.filter((decision) => !decision.card_id) : []
  const openDrawer = (id: string, focus?: 'merge') => { setSelected(id); setDrawerFocus(focus) }
  const closeDrawer = () => { setSelected(null); setDrawerFocus(undefined) }

  return (
    <main className="flex h-full min-h-0 w-full flex-col bg-background">
      <header className="flex flex-wrap items-center gap-2 border-b px-4 py-2.5">
        <span className="text-sm font-semibold">工作项</span>
        <div className="inline-flex overflow-hidden rounded-md border"><button type="button" onClick={() => setView('board')} className={`px-2.5 py-1 text-xs ${view === 'board' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}>看板</button><button type="button" onClick={() => setView('list')} className={`px-2.5 py-1 text-xs ${view === 'list' ? 'bg-primary text-primary-foreground' : 'text-muted-foreground'}`}>列表</button></div>
        <select aria-label="项目" value={project} onChange={(event) => setProject(event.target.value)} className="rounded-md border bg-background px-2 py-1 text-xs"><option value="">全部项目</option>{projectOptions.map((item) => <option key={item} value={item}>{item}</option>)}</select>
        <select aria-label="工作流" value={workflow} onChange={(event) => setWorkflow(event.target.value)} className="rounded-md border bg-background px-2 py-1 text-xs"><option value="">全部工作流</option>{workflowOptions.map((item) => <option key={item.name} value={item.name}>{item.name} v{item.version}</option>)}</select>
        <input value={search} onChange={(event) => setSearch(event.target.value)} placeholder="搜 B 号 / 标题" className="w-40 rounded-md border bg-background px-2 py-1 text-xs" />
        <button type="button" onClick={() => setNeedsOnly((current) => !current)} className={`rounded-md border px-2.5 py-1 text-xs ${needsOnly ? 'border-amber-400 bg-amber-50 text-amber-800' : 'text-amber-700'}`}>⚑ 需要你 {attentionCount}</button>
        <span className={`ml-auto flex items-center gap-1 text-[11px] ${healthStale ? 'text-amber-700' : 'text-green-600'}`} title={healthStale ? `${healthLabel}——该机器的事件已停止镜像，卡上的 task 实况可能是陈的` : '镜像正常'}>{healthStale ? healthLabel : '●'}</span>
      </header>
      {flowsError && <p role="alert" className="mx-4 mt-2 text-xs text-destructive">流程读取失败：{flowsError}</p>}
      {needsOnly && projectDecisions.length > 0 && <ProjectDecisions decisions={projectDecisions} />}
      <UnlinkedRow summary={cardsPoll.data?.unlinked ?? { count: 0, tasks: [], unknown_targets: [] }} />
      {cardsPoll.data === null ? <p className="p-4 text-sm text-muted-foreground">正在读取账本…</p> : view === 'list' ? <ListView cards={filtered} includeArchived={includeArchived} onIncludeArchivedChange={setIncludeArchived} onOpen={(id) => openDrawer(id)} /> : <div className="flex min-h-0 flex-1 gap-2 overflow-x-auto px-4 py-3">{boardColumns(workflowStates.length ? workflowStates : [...new Set(cards.map((card) => card.status))]).map((status) => { const inColumn = cardsInColumn(filtered, status); return <section key={status} className="flex min-h-0 w-60 shrink-0 flex-col"><header className="flex items-center gap-1.5 px-1 pb-2 text-xs font-semibold"><span>{status}</span><span className="font-normal text-muted-foreground">{inColumn.length}</span></header><div className="min-h-0 flex-1 space-y-2 overflow-y-auto pb-2">{inColumn.map((card) => <CardItem key={card.id} card={card} onOpen={(focus) => openDrawer(card.id, focus)} />)}{inColumn.length === 0 && <p className="px-1 py-2 text-xs text-muted-foreground">（空）</p>}</div></section> })}</div>}
      {cardsPoll.disconnected && <p className="border-t bg-amber-50 px-4 py-1.5 text-xs text-amber-800">已断开：{cardsPoll.errorText}（保留最后一次账本数据）</p>}
      {selected && <CardDrawer id={selected} onClose={closeDrawer} onOpenCard={(id) => openDrawer(id)} workflowStates={workflowStates} initialSection={drawerFocus} />}
    </main>
  )
}
