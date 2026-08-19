// CodegraphPage —— 代码图页（/codegraph）：工具条 + 左树/中图/右详情三栏。
//
// 布局契约（spec §5）：左树 320px 固定、右详情 340px 固定、中间自适应。
// 状态机：foci（焦点集合，单选=1 个）、hist/histIdx（焦点历史，语义同浏览器
// 历史——新选择截断前进分支）、depth 默认 2、viewName 默认 baseline。
import { useMemo, useState } from 'react'
import { useProjectTree } from '../data/useProjectTree'
import { CallTree } from './CallTree'
import { DetailPanel } from './DetailPanel'
import { FocusGraph } from './FocusGraph'
import { SeqView } from './SeqView'
import { mergeView, scannedEntries } from './graphmath'
import { useCodegraph } from './useCodegraph'

export function CodegraphPage() {
  const tree = useProjectTree()
  const projects = useMemo(() => (tree.data?.projects ?? []).map((p) => p.name), [tree.data])
  const [project, setProject] = useState('')
  const active = project || projects[0] || ''
  const { data, error, loading, reload } = useCodegraph(active)

  const [viewName, setViewName] = useState('baseline')
  const [mode, setMode] = useState<'combo' | 'seq'>('combo')
  const [depth, setDepth] = useState(2)
  const [foci, setFoci] = useState<string[]>([])
  const [hist, setHist] = useState<string[][]>([])
  const [histIdx, setHistIdx] = useState(-1)
  const [open, setOpen] = useState<Set<string>>(new Set())
  const [selected, setSelected] = useState('')

  const view = useMemo(() => {
    if (!data) return null
    const d = viewName === 'baseline' ? undefined : data.views[viewName]
    return mergeView(data.baseline, d)
  }, [data, viewName])
  const staleIds = useMemo(() => new Set((data?.stale ?? []).map((s) => s.id)), [data])

  const effFoci = useMemo(() => {
    if (!view) return []
    const ok = foci.filter((f) => view.nodes[f] && view.nodes[f].status !== 'deleted')
    return ok.length ? ok : scannedEntries(view).slice(0, 1)
  }, [view, foci])

  const setFociWithHist = (next: string[], fromHist = false) => {
    if (next.join('|') === effFoci.join('|')) return
    if (!fromHist) {
      const h = [...hist.slice(0, histIdx + 1), next]
      setHist(h)
      setHistIdx(h.length - 1)
    }
    setFoci(next)
    setSelected(next[next.length - 1] ?? '')
  }
  const onFocus = (id: string, additive: boolean) => {
    if (additive) {
      const s = effFoci.includes(id) ? effFoci.filter((x) => x !== id) : [...effFoci, id]
      if (s.length) setFociWithHist(s)
    } else setFociWithHist([id])
  }

  if (error) return <div className="p-6 text-sm text-red-600">{error}</div>
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-2 text-sm">
        <label className="text-muted-foreground">项目</label>
        <select value={active} onChange={(e) => setProject(e.target.value)} className="rounded border px-1.5 py-0.5">
          {projects.map((p) => <option key={p}>{p}</option>)}
        </select>
        <div className="flex overflow-hidden rounded border">
          {(['combo', 'seq'] as const).map((m) => (
            <button key={m} onClick={() => setMode(m)}
              className={`px-2.5 py-0.5 ${mode === m ? 'bg-primary text-primary-foreground' : ''}`}>
              {m === 'combo' ? '树+图' : '时序图'}
            </button>
          ))}
        </div>
        <label className="text-muted-foreground">视图</label>
        <select value={viewName} onChange={(e) => setViewName(e.target.value)} className="rounded border px-1.5 py-0.5">
          <option value="baseline">基准 · {data?.baseline.meta.branch ?? ''}</option>
          {Object.entries(data?.views ?? {}).map(([k, v]) => <option key={k} value={k}>{v.view}</option>)}
        </select>
        {data && data.stale.length > 0 && (
          <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-700"
            title={data.stale.map((s) => `${s.id}: ${s.reason}`).join('\n')}>
            ⚠ {data.stale.length} 个节点疑似失鲜
          </span>
        )}
        <button onClick={reload} className="ml-auto rounded border px-2 py-0.5 text-xs">刷新</button>
      </div>
      {loading || !view ? (
        <div className="p-6 text-sm text-muted-foreground">{loading ? '加载中…' : '该项目未生成代码图'}</div>
      ) : (
        <div className="flex min-h-0 flex-1">
          <CallTree view={view} foci={effFoci} open={open}
            onToggle={(id, o) => setOpen((s) => {
              const n = new Set(s)
              if (o) n.add(id)
              else n.delete(id)
              return n
            })}
            onFocus={onFocus} />
          {mode === 'combo' ? (
            <FocusGraph view={view} foci={effFoci} depth={depth} staleIds={staleIds}
              onDepth={setDepth} onFocus={onFocus} onSelect={setSelected}
              canBack={histIdx > 0} canFwd={histIdx < hist.length - 1}
              onBack={() => { setHistIdx(histIdx - 1); setFociWithHist(hist[histIdx - 1], true) }}
              onFwd={() => { setHistIdx(histIdx + 1); setFociWithHist(hist[histIdx + 1], true) }} />
          ) : (
            <SeqView view={view} entry={effFoci[0]} onSelect={setSelected} />
          )}
          <DetailPanel project={active} view={view} nodeId={selected || effFoci[effFoci.length - 1] || ''}
            stale={staleIds} onJump={(id) => setFociWithHist([id])} />
        </div>
      )}
    </div>
  )
}
