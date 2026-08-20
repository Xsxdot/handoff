// NodeEditor —— 单个工作流节点的编辑器。
//
// 职责：把一个 NodeDef 展开成一组控件（模板引用、单字段覆盖、能力开关、路由、
// 门槛、人工基线清单），改动整体冒泡给上层，本组件不持有节点数组。
//
// 边界：
//   - 不保存——保存是整条工作流一次性发新版本，那在 FlowsPage
//   - 不做跨节点校验（路由悬空、模板不存在）——那是后端 PutWorkflow 的职责，
//     前端重复实现一遍只会漂移；这里只把候选项限制成合法集合，降低出错概率
//   - **不预设节点类型**：界面上不出现「审阅节点」「合并节点」这样的词，
//     语义由用户开出来的开关组合决定
import type { NodeDef, NodeOverride } from '../../api/ledger'

export interface NodeEditorProps {
  node: NodeDef
  templates: string[]
  disciplines: string[]
  nodeNames: string[]
  onChange: (node: NodeDef) => void
  onRemove: () => void
}

const inputClass = 'mt-1 w-full rounded border px-2 py-1.5 text-sm'
const labelClass = 'block text-xs text-muted-foreground'

function controlID(name: string, suffix: string): string {
  const safe = name.replace(/[^a-zA-Z0-9_-]+/g, '-') || 'node'
  return `flow-node-${safe}-${suffix}`
}

function routeOptions(node: NodeDef, nodeNames: string[]): string[] {
  return nodeNames.filter((name) => name !== node.name)
}

export function NodeEditor({
  node, templates, disciplines, nodeNames, onChange, onRemove,
}: NodeEditorProps) {
  const id = (suffix: string) => controlID(node.name, suffix)
  const routes = routeOptions(node, nodeNames)
  const templateNames = node.template && !templates.includes(node.template)
    ? [node.template, ...templates]
    : templates

  const update = (patch: Partial<NodeDef>) => onChange({ ...node, ...patch })

  const updateOverride = (key: keyof NodeOverride, value: string) => {
    const override = { ...(node.override ?? {}) }
    if (value === '') delete override[key]
    else override[key] = value
    update({ override: Object.keys(override).length > 0 ? override : undefined })
  }

  const updateGate = (patch: NonNullable<NodeDef['gate']>) => {
    const gate = { ...(node.gate ?? {}), ...patch }
    const cleanedGate = Object.fromEntries(
      Object.entries(gate).filter(([, value]) => value !== undefined),
    ) as NonNullable<NodeDef['gate']>
    update({ gate: Object.keys(cleanedGate).length > 0 ? cleanedGate : undefined })
  }

  const setDispatch = (enabled: boolean) => {
    if (!enabled) {
      // 派发关掉后，下面这些字段没有执行对象就不会生效；一起清掉还能让保存的
      // JSON 与界面保持一致，避免用户以后重新打开时误以为它们仍然有效。
      update({
        dispatch: false,
        verdict: false,
        template: undefined,
        override: undefined,
        max_rounds: undefined,
        on_fail: undefined,
      })
      return
    }
    update({ dispatch: true })
  }

  const setVerdict = (enabled: boolean) => {
    update({
      verdict: enabled,
      ...(enabled ? {} : { max_rounds: undefined, on_fail: undefined }),
    })
  }

  const setHumanBases = (value: string) => {
    const humanBases = value.split(',').map((base) => base.trim()).filter(Boolean)
    update({ human_bases: humanBases.length > 0 ? humanBases : undefined })
  }

  return (
    <article className="rounded border bg-background p-3">
      <div className="flex items-end gap-3">
        <div className="min-w-0 flex-1">
          <label className={labelClass} htmlFor={id('name')}>列名</label>
          <input
            id={id('name')}
            className={inputClass}
            value={node.name}
            onChange={(event) => update({ name: event.target.value })}
          />
        </div>
        <button
          type="button"
          className="rounded border px-2 py-1.5 text-xs text-destructive"
          onClick={onRemove}
        >
          删除本列
        </button>
      </div>

      <div className="mt-3 flex flex-wrap gap-x-4 gap-y-2 text-sm">
        <div className="flex items-center gap-2">
          <input
            id={id('dispatch')}
            type="checkbox"
            checked={node.dispatch === true}
            onChange={(event) => setDispatch(event.target.checked)}
          />
          <label htmlFor={id('dispatch')}>派发</label>
        </div>
        {node.dispatch === true && (
          <>
            <div className="flex items-center gap-2">
              <input
                id={id('verdict')}
                type="checkbox"
                checked={node.verdict === true}
                onChange={(event) => setVerdict(event.target.checked)}
              />
              <label htmlFor={id('verdict')}>裁决</label>
            </div>
            <div className="flex items-center gap-2">
              <input
                id={id('carry-card-context')}
                type="checkbox"
                checked={node.carry_card_context === true}
                onChange={(event) => update({ carry_card_context: event.target.checked })}
              />
              <label htmlFor={id('carry-card-context')}>携带卡上下文</label>
            </div>
          </>
        )}
      </div>

      {node.dispatch === true && (
        <div className="mt-3 grid gap-3 sm:grid-cols-2">
          <div>
            <label className={labelClass} htmlFor={id('template')}>模板</label>
            <select
              id={id('template')}
              className={inputClass}
              value={node.template ?? ''}
              onChange={(event) => update({ template: event.target.value || undefined })}
            >
              <option value="">（不指定模板）</option>
              {templateNames.map((template) => <option key={template} value={template}>{template}</option>)}
            </select>
          </div>
          <div>
            <label className={labelClass} htmlFor={id('discipline')}>纪律块</label>
            <select
              id={id('discipline')}
              className={inputClass}
              value={node.override?.discipline ?? ''}
              onChange={(event) => updateOverride('discipline', event.target.value)}
            >
              <option value="">（沿用模板）</option>
              {disciplines.map((discipline) => <option key={discipline} value={discipline}>{discipline}</option>)}
            </select>
          </div>
          {(['executor', 'target', 'model'] as const).map((key) => {
            const labels = { executor: '执行者', target: '目标机', model: '模型' }
            return (
              <div key={key}>
                <label className={labelClass} htmlFor={id(key)}>{labels[key]}</label>
                <input
                  id={id(key)}
                  className={inputClass}
                  value={node.override?.[key] ?? ''}
                  placeholder="（沿用模板）"
                  onChange={(event) => updateOverride(key, event.target.value)}
                />
              </div>
            )
          })}
        </div>
      )}

      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <div>
          <label className={labelClass} htmlFor={id('next')}>通过后去</label>
          <select
            id={id('next')}
            className={inputClass}
            value={node.next ?? ''}
            onChange={(event) => update({ next: event.target.value || undefined })}
          >
            <option value="">（停在本列）</option>
            {routes.map((route) => <option key={route} value={route}>{route}</option>)}
          </select>
        </div>
        {node.dispatch === true && node.verdict === true && (
          <div>
            <label className={labelClass} htmlFor={id('max-rounds')}>最大轮次</label>
            <input
              id={id('max-rounds')}
              type="number"
              min="1"
              className={inputClass}
              value={node.max_rounds ?? ''}
              onChange={(event) => update({ max_rounds: event.target.value ? Number(event.target.value) : undefined })}
            />
          </div>
        )}
        {node.dispatch === true && node.verdict === true && (
          <div>
            <label className={labelClass} htmlFor={id('on-fail')}>失败后去</label>
            <select
              id={id('on-fail')}
              className={inputClass}
              value={node.on_fail ?? ''}
              onChange={(event) => update({ on_fail: event.target.value || undefined })}
            >
              <option value="">（停在本列）</option>
              {routes.map((route) => <option key={route} value={route}>{route}</option>)}
            </select>
          </div>
        )}
      </div>

      <div className="mt-3 grid gap-3 sm:grid-cols-2">
        <div>
          <label className={labelClass} htmlFor={id('attachment')}>需要附件</label>
          <select
            id={id('attachment')}
            className={inputClass}
            value={node.gate?.require_attachment ?? ''}
            onChange={(event) => updateGate({ require_attachment: event.target.value || undefined })}
          >
            <option value="">（不要求）</option>
            <option value="spec">spec</option>
            <option value="plan">plan</option>
            <option value="doc">doc</option>
          </select>
        </div>
        <div className="flex items-center gap-2 self-end pb-2 text-sm">
          <input
            id={id('acceptance')}
            type="checkbox"
            checked={node.gate?.require_acceptance === true}
            onChange={(event) => updateGate({ require_acceptance: event.target.checked || undefined })}
          />
          <label htmlFor={id('acceptance')}>要求验收判据</label>
        </div>
      </div>

      <div className="mt-3">
        <label className={labelClass} htmlFor={id('human-bases')}>人工基线清单</label>
        <input
          id={id('human-bases')}
          className={inputClass}
          value={(node.human_bases ?? []).join(', ')}
          placeholder="例如 main, release"
          onChange={(event) => setHumanBases(event.target.value)}
        />
        <p className="mt-1 text-xs text-muted-foreground">
          卡的有效基线落在其中时本节点不自动跑，直接转「需要你」。
        </p>
      </div>
    </article>
  )
}
