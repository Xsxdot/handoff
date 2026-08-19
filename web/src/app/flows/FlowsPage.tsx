import { useEffect, useMemo, useState } from 'react'
import { fetchFlows } from '../../api/ledger'
import type { FlowsResp, TemplateWire, WorkflowWire } from '../../api/ledger'

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

function WorkflowCard({ workflow }: { workflow: WorkflowWire }) {
  const gates = workflow.def.gates ?? {}
  return (
    <section className="rounded-lg border p-4">
      <div className="flex items-center gap-2">
        <h3 className="font-medium">{workflow.name} 流</h3>
        <span className="rounded bg-muted px-2 py-0.5 text-xs">当前 v{workflow.version}</span>
        <span className="ml-auto text-xs text-muted-foreground">只读 · 编辑请使用 CLI</span>
      </div>
      <div className="mt-3">
        <Pipeline workflow={workflow} />
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
        <dd className="font-mono">v{workflow.version}（当前 API 返回最新版）</dd>
      </dl>
      <p className="mt-3 border-l-2 pl-2 text-xs text-muted-foreground">
        工作项会钉住创建时的工作流版本；改形状用 <code>handoff workflow put</code>，把卡迁到新版用 <code>handoff workflow migrate</code>。
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
              {workflows.map((workflow) => <WorkflowCard key={workflow.name} workflow={workflow} />)}
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
