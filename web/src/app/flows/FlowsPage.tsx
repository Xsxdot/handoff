import { useEffect, useMemo, useState } from 'react'
import { fetchDisciplineNames, fetchFlow, fetchFlows, putFlow } from '../../api/ledger'
import type { FlowsResp, NodeDef, TemplateWire, WorkflowWire } from '../../api/ledger'
import { errorMessage } from '../lib/format'
import { NodeEditor } from './NodeEditor'

type Tab = 'workflows' | 'templates'

const INSERTED_STATES = new Set(['已出spec', '已出 spec', '待合并'])

function display(value: unknown, fallback = '—'): string {
  if (value === undefined || value === null || value === '') return fallback
  return String(value)
}

function formatJSON(value: unknown): string {
  return JSON.stringify(value, null, 2)
}

function Pipeline({ workflow }: { workflow: WorkflowWire }) {
  const gates = workflow.def.gates ?? {}
  return (
    <>
      <div className="flex flex-wrap items-center gap-1 text-xs">
        {workflow.def.states.map((state, index) => (
          <span key={`${state}-${index}`} className="contents">
            {index > 0 && <span className="text-muted-foreground">→</span>}
            <span
              className={
                INSERTED_STATES.has(state)
                  ? 'rounded-full border border-dashed px-2 py-0.5'
                  : 'rounded-full border bg-muted px-2 py-0.5 font-medium'
              }
              title={gates[state] ? `进入此状态前：${formatJSON(gates[state])}` : undefined}
            >
              {state}
            </span>
          </span>
        ))}
      </div>
      <p className="mt-2 text-xs text-muted-foreground">
        实线 = 骨架锚点；虚线 = 插入状态。{Object.keys(gates).length > 0 && '带门条件的状态悬停可查看要求。'}
      </p>
    </>
  )
}

function nodesFromStates(states: string[]): NodeDef[] {
  return states.map((name, index) => ({
    name,
    ...(index + 1 < states.length ? { next: states[index + 1] } : {}),
  }))
}

function WorkflowCard({ workflow, templates }: { workflow: WorkflowWire; templates: string[] }) {
  const [editing, setEditing] = useState(false)
  const [loading, setLoading] = useState(false)
  const [busy, setBusy] = useState(false)
  const [nodes, setNodes] = useState<NodeDef[]>(() => nodesFromStates(workflow.def.states))
  const [disciplines, setDisciplines] = useState<string[]>([])
  const [version, setVersion] = useState(workflow.version)
  const [loadError, setLoadError] = useState('')
  const [saveError, setSaveError] = useState('')

  const beginEdit = () => {
    setEditing(true)
    setLoading(true)
    setLoadError('')
    setSaveError('')
    setNodes(nodesFromStates(workflow.def.states))
    void Promise.all([fetchFlow(workflow.name), fetchDisciplineNames()])
      .then(([detail, names]) => {
        setNodes(detail.nodes)
        setDisciplines(names)
      })
      .catch((err: unknown) => setLoadError(errorMessage(err)))
      .finally(() => setLoading(false))
  }

  const updateNode = (index: number, node: NodeDef) => {
    setNodes((current) => current.map((item, itemIndex) => itemIndex === index ? node : item))
  }

  const removeNode = (index: number) => {
    setNodes((current) => current.filter((_, itemIndex) => itemIndex !== index))
  }

  const moveNode = (index: number, offset: number) => {
    setNodes((current) => {
      const target = index + offset
      if (target < 0 || target >= current.length) return current
      const next = [...current]
      const [item] = next.splice(index, 1)
      next.splice(target, 0, item)
      return next
    })
  }

  const addNode = () => {
    setNodes((current) => [...current, { name: `新列${current.length + 1}` }])
  }

  const save = async () => {
    setBusy(true)
    setSaveError('')
    try {
      const result = await putFlow(workflow.name, nodes)
      setVersion(result.version)
      setEditing(false)
    } catch (err) {
      setSaveError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  if (editing) {
    return (
      <section className="rounded-lg border p-4">
        <div className="flex items-center gap-2">
          <h3 className="font-medium">编辑 {workflow.name} 流</h3>
          <span className="rounded bg-muted px-2 py-0.5 text-xs">当前版本 {version}</span>
          <button
            type="button"
            className="ml-auto rounded border px-3 py-1.5 text-xs"
            onClick={() => setEditing(false)}
          >
            取消编辑
          </button>
        </div>
        <p className="mt-3 rounded border border-blue-500/30 bg-blue-500/10 p-3 text-xs">
          保存会发布一个新版本；已有的卡仍走各自钉住的版本；要让老卡用新流程，用 <code>handoff workflow migrate</code> 显式迁。
        </p>
        {loadError !== '' && <p className="mt-3 rounded border border-amber-500/40 bg-amber-500/10 p-3 text-xs">读取节点定义失败：{loadError}</p>}
        {saveError !== '' && <p className="mt-3 rounded border border-amber-500/40 bg-amber-500/10 p-3 text-xs">{saveError}</p>}
        {loading && <p className="mt-3 text-xs text-muted-foreground">正在读取完整节点定义…</p>}
        <div className="mt-3 space-y-3">
          {nodes.map((node, index) => (
            <div key={`${node.name}-${index}`}>
              <NodeEditor
                node={node}
                index={index}
                templates={templates}
                disciplines={disciplines}
                nodeNames={nodes.map((item) => item.name)}
                onChange={(next) => updateNode(index, next)}
                onRemove={() => removeNode(index)}
              />
              <div className="mt-1 flex gap-1 pl-1">
                <button
                  type="button"
                  className="rounded border px-2 py-1 text-xs disabled:opacity-50"
                  disabled={index === 0}
                  onClick={() => moveNode(index, -1)}
                >
                  上移
                </button>
                <button
                  type="button"
                  className="rounded border px-2 py-1 text-xs disabled:opacity-50"
                  disabled={index === nodes.length - 1}
                  onClick={() => moveNode(index, 1)}
                >
                  下移
                </button>
              </div>
            </div>
          ))}
        </div>
        <div className="mt-4 flex flex-wrap gap-2">
          <button type="button" className="rounded border px-3 py-1.5 text-sm" onClick={addNode}>加一列</button>
          <button
            type="button"
            className="rounded bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50"
            disabled={busy}
            onClick={() => void save()}
          >
            {busy ? '保存中…' : '保存为新版本'}
          </button>
        </div>
      </section>
    )
  }

  const displayWorkflow = { ...workflow, version }
  const gates = workflow.def.gates ?? {}
  return (
    <section className="rounded-lg border p-4">
      <div className="flex items-center gap-2">
        <h3 className="font-medium">{workflow.name} 流</h3>
        <span className="rounded bg-muted px-2 py-0.5 text-xs">当前版本 {version}</span>
        <span className="ml-auto text-xs text-muted-foreground">可在此编辑并发布新版本</span>
        <button type="button" className="rounded border px-3 py-1.5 text-xs" onClick={beginEdit}>编辑</button>
      </div>
      <div className="mt-3">
        <Pipeline workflow={displayWorkflow} />
      </div>
      <dl className="mt-4 grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
        <dt className="text-muted-foreground">状态数</dt>
        <dd>{workflow.def.states.length}</dd>
        <dt className="text-muted-foreground">门条件</dt>
        <dd>
          {Object.keys(gates).length === 0
            ? '无'
            : Object.entries(gates)
                .map(([state, gate]) => `${state}: ${formatJSON(gate)}`)
                .join(' · ')}
        </dd>
        <dt className="text-muted-foreground">版本</dt>
        <dd className="font-mono">v{version}（当前 API 返回最新版）</dd>
      </dl>
      <p className="mt-3 border-l-2 pl-2 text-xs text-muted-foreground">
        工作项会钉住创建时的工作流版本；要让老卡用新流程，用 <code>handoff workflow migrate</code> 显式迁移。
      </p>
    </section>
  )
}

function TemplateDetails({ template }: { template: TemplateWire }) {
  const def = template.def
  const modelByTarget = typeof def.model_by_target === 'object' && def.model_by_target !== null
    ? (def.model_by_target as Record<string, unknown>)
    : {}
  const scalarEntries = Object.entries(def).filter(([key]) => key !== 'model_by_target')
  return (
    <>
      <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-xs">
        {scalarEntries.map(([key, value]) => (
          <span key={key} className="contents">
            <dt className="text-muted-foreground">{key}</dt>
            <dd className="break-words">{display(value)}</dd>
          </span>
        ))}
        <dt className="text-muted-foreground">模型覆盖</dt>
        <dd>
          {Object.keys(modelByTarget).length === 0 ? (
            '无'
          ) : (
            <div className="overflow-x-auto">
              <table className="min-w-[320px] border-collapse text-left">
                <thead>
                  <tr>
                    <th className="border px-2 py-1 font-normal text-muted-foreground">目标机</th>
                    <th className="border px-2 py-1 font-normal text-muted-foreground">模型</th>
                  </tr>
                </thead>
                <tbody>
                  {Object.entries(modelByTarget).map(([target, model]) => (
                    <tr key={target}>
                      <td className="border px-2 py-1">{target}</td>
                      <td className="border px-2 py-1 font-mono">{display(model)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </dd>
      </dl>
      <details className="mt-3 overflow-hidden rounded border">
        <summary className="cursor-pointer bg-sidebar px-3 py-2 text-xs">
          纪律块 / 模板定义 · v{template.version}
        </summary>
        <pre className="max-h-64 overflow-auto whitespace-pre-wrap p-3 font-mono text-[11px] leading-relaxed text-muted-foreground">
          {formatJSON(def)}
        </pre>
      </details>
      <p className="mt-3 border-l-2 pl-2 text-xs text-muted-foreground">
        派发会快照模板版本与纪律块 hash；改模板用 <code>handoff template put</code>，某次派发用了哪版在卡的 dispatched 事件里。
      </p>
    </>
  )
}

function TemplateCard({ template }: { template: TemplateWire }) {
  return (
    <section className="rounded-lg border p-4">
      <div className="flex items-center gap-2">
        <h3 className="font-medium">{template.name}</h3>
        <span className="rounded bg-muted px-2 py-0.5 text-xs">当前 v{template.version}</span>
        <span className="ml-auto text-xs text-muted-foreground">只读 · 编辑请使用 CLI</span>
      </div>
      <div className="mt-3">
        <TemplateDetails template={template} />
      </div>
    </section>
  )
}

export function FlowsPage() {
  const [tab, setTab] = useState<Tab>('workflows')
  const [data, setData] = useState<FlowsResp | null>(null)
  const [error, setError] = useState('')

  useEffect(() => {
    let cancelled = false
    fetchFlows()
      .then((response) => {
        if (!cancelled) setData(response)
      })
      .catch((err: unknown) => {
        if (!cancelled) setError(err instanceof Error ? err.message : String(err))
      })
    return () => {
      cancelled = true
    }
  }, [])

  const workflows = useMemo(() => data?.workflows ?? [], [data])
  const templates = useMemo(() => data?.templates ?? [], [data])

  return (
    <div className="flex h-full min-h-0">
      <nav className="w-44 shrink-0 border-r p-3">
        <button
          type="button"
          className={`block w-full rounded px-3 py-2 text-left text-sm ${tab === 'workflows' ? 'bg-muted font-medium' : 'hover:bg-accent'}`}
          onClick={() => setTab('workflows')}
        >
          工作流
        </button>
        <button
          type="button"
          className={`mt-1 block w-full rounded px-3 py-2 text-left text-sm ${tab === 'templates' ? 'bg-muted font-medium' : 'hover:bg-accent'}`}
          onClick={() => setTab('templates')}
        >
          派发模板
        </button>
      </nav>
      <div className="min-w-0 flex-1 overflow-y-auto p-5">
        {error !== '' && <p className="mb-4 rounded border border-amber-500/40 bg-amber-500/10 p-3 text-sm">流程数据加载失败：{error}</p>}
        {tab === 'workflows' ? (
          <>
            <h1 className="text-lg font-semibold">工作流（状态机形状）</h1>
            <p className="mb-5 mt-1 text-sm text-muted-foreground">
              只管状态集合与转移合法性。不可变版本化；工作项钉旧版继续走，显式迁移。
            </p>
            <div className="space-y-4">
              {workflows.length === 0 && data && <p className="text-sm text-muted-foreground">暂无工作流。</p>}
              {workflows.map((workflow) => (
                <WorkflowCard
                  key={workflow.name}
                  workflow={workflow}
                  templates={templates.map((template) => template.name)}
                />
              ))}
            </div>
          </>
        ) : (
          <>
            <h1 className="text-lg font-semibold">派发模板（派发配方）</h1>
            <p className="mb-5 mt-1 text-sm text-muted-foreground">
              executor、目标机、模型覆盖、纪律块与分支策略均带版本号。页面只读，编辑走 CLI。
            </p>
            <div className="space-y-4">
              {templates.length === 0 && data && <p className="text-sm text-muted-foreground">暂无派发模板。</p>}
              {templates.map((template) => <TemplateCard key={template.name} template={template} />)}
            </div>
          </>
        )}
        {!data && error === '' && <p className="text-sm text-muted-foreground">正在加载流程数据…</p>}
      </div>
    </div>
  )
}
