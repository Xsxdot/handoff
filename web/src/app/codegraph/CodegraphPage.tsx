// CodegraphPage —— 代码图页（/codegraph）：领域图三级下钻。
//
// 三态（spec §5 定稿形态）：
//   scope=null 且图里有领域 → 领域全景
//   scope 还有子领域        → 子领域全景（域外领域画成占位卡）
//   scope 是叶子领域        → 树+图视图（左树 320 / 中图自适应 / 右详情 340）
// 整图没有领域段时降级为单领域视图并明示提示——不按包名伪造领域。
import { useMemo, useState } from 'react'
import { useProjectTree } from '../data/useProjectTree'
import { CallTree } from './CallTree'
import { DetailPanel } from './DetailPanel'
import { DomainDetail } from './DomainDetail'
import { DomainPanorama } from './DomainPanorama'
import { FocusGraph } from './FocusGraph'
import { childDomainsOf, domainAncestors, hasDomains, leafRoots, nodeDomainPathOf } from './domains'
import { mergeView, scannedEntries } from './graphmath'
import { useCodegraph } from './useCodegraph'

export function CodegraphPage() {
  const tree = useProjectTree()
  const projects = useMemo(() => (tree.data?.projects ?? []).map((p) => p.name), [tree.data])
  const [project, setProject] = useState('')
  const active = project || projects[0] || ''
  const { data, error, loading, reload } = useCodegraph(active)

  const [viewName, setViewName] = useState('baseline')
  const [scope, setScope] = useState<string | null>(null)
  const [selDomain, setSelDomain] = useState('')
  const [selEdge, setSelEdge] = useState('')
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

  const single = !!view && !hasDomains(view)               // 旧图：整张图当一个领域看
  const pano = !!view && !single && (scope === null || childDomainsOf(view, scope).length > 0)
  const leafScope = single ? null : scope

  const effFoci = useMemo(() => {
    if (!view) return []
    const ok = foci.filter((f) => view.nodes[f] && view.nodes[f].status !== 'deleted')
    if (ok.length) return ok
    // 默认焦点必须落在当前领域内：goScope 会清空 foci，若这里回落到全图第一个
    // 已扫描入口，进领域后左树列的是本域的根、焦点图却停在域外节点上，两栏各说各话。
    return leafScope ? leafRoots(view, leafScope).slice(0, 1) : scannedEntries(view).slice(0, 1)
  }, [view, foci, leafScope])

  const setFociWithHist = (next: string[], fromHist = false) => {
    if (next.join('|') === effFoci.join('|')) return
    if (!fromHist) {
      // 历史为空时先把当前（默认）焦点垫底：否则第一次换焦点后「后退」无处可退
      const base = hist.length ? hist.slice(0, histIdx + 1) : [effFoci]
      const h = [...base, next]
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

  // 换一层领域：焦点历史与展开状态都作废——它们是上一层的语境，带过去只会误导
  const goScope = (next: string | null) => {
    setScope(next)
    setSelDomain('')
    setSelEdge('')
    setFoci([])
    setHist([])
    setHistIdx(-1)
    setOpen(new Set())
    setSelected('')
  }
  // 横跳：落到目标节点所在的叶子领域并把它设为焦点
  const enterNode = (id: string) => {
    if (!view) return
    const path = nodeDomainPathOf(view, id)
    goScope(path.length ? path[path.length - 1] : null)
    setFoci([id])
    setHist([[id]])
    setHistIdx(0)
    setSelected(id)
  }

  if (error) return <div className="p-6 text-sm text-red-600">{error}</div>
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-2 text-sm">
        <label className="text-muted-foreground">项目</label>
        <select value={active} onChange={(e) => { setProject(e.target.value); goScope(null) }}
          className="rounded border px-1.5 py-0.5">
          {projects.map((p) => <option key={p}>{p}</option>)}
        </select>
        <label className="text-muted-foreground">视图</label>
        <select value={viewName} onChange={(e) => { setViewName(e.target.value); goScope(null) }}
          className="rounded border px-1.5 py-0.5">
          <option value="baseline">基准 · {data?.baseline.meta.branch ?? ''}</option>
          {Object.entries(data?.views ?? {}).map(([k, v]) => <option key={k} value={k}>{v.view}</option>)}
        </select>
        {data && data.stale.length > 0 && (
          <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-700"
            title={data.stale.map((s) => `${s.id}: ${s.reason}`).join('\n')}>
            ⚠ {data.stale.length} 个节点疑似失鲜
          </span>
        )}
        {single && (
          <span className="rounded-full bg-amber-100 px-2 py-0.5 text-xs text-amber-700">
            该图未包含领域划分（扫描版本较旧）：重扫可获得领域全景
          </span>
        )}
        <button onClick={reload} className="ml-auto rounded border px-2 py-0.5 text-xs">刷新</button>
      </div>
      {loading || !view ? (
        <div className="p-6 text-sm text-muted-foreground">{loading ? '加载中…' : '该项目未生成代码图'}</div>
      ) : (
        <div className="relative flex min-h-0 flex-1">
          {!single && (
            <div className="absolute left-3.5 top-2.5 z-30 inline-flex items-center gap-2 rounded-full border bg-background px-3.5 py-1 text-xs shadow-sm">
              {scope === null ? (
                <>
                  <b>领域全景</b>
                  <span className="text-[11px] text-muted-foreground">点卡片看职责 · 点连线看谁调谁 · 进入 ▸ 下钻 · 空白拖动平移 · ⌘/⌃+滚轮缩放</span>
                </>
              ) : (
                <>
                  <span className="cursor-pointer text-muted-foreground hover:underline" onClick={() => goScope(null)}>◀ 领域全景</span>
                  {domainAncestors(view, scope).map((id, i, arr) => (
                    <span key={id} className="inline-flex items-center gap-2">
                      <span className="text-muted-foreground">▸</span>
                      {i === arr.length - 1 ? (
                        <>
                          <b>{view.domains[id]?.label}</b>
                          <span className="text-[11px] text-muted-foreground">{view.domains[id]?.kind}</span>
                        </>
                      ) : (
                        <span className="cursor-pointer text-muted-foreground hover:underline" onClick={() => goScope(id)}>
                          {view.domains[id]?.label}
                        </span>
                      )}
                    </span>
                  ))}
                </>
              )}
            </div>
          )}
          {pano ? (
            <>
              <DomainPanorama view={view} scope={scope} selectedDomain={selDomain} selectedEdge={selEdge}
                onSelectDomain={(id) => { setSelDomain(id); setSelEdge('') }}
                onSelectEdge={(k) => { setSelEdge(k); setSelDomain('') }}
                onEnter={goScope} />
              <DomainDetail view={view} scope={scope} domainId={selDomain} edgeKey={selEdge}
                onEnterNode={enterNode} onEnterDomain={goScope} />
            </>
          ) : (
            <>
              <CallTree view={view} foci={effFoci} open={open} scope={leafScope}
                onToggle={(id, o) => setOpen((s) => {
                  const n = new Set(s)
                  if (o) n.add(id)
                  else n.delete(id)
                  return n
                })}
                onFocus={onFocus} onCrossJump={enterNode} />
              <FocusGraph view={view} foci={effFoci} depth={depth} staleIds={staleIds} scope={leafScope}
                onDepth={setDepth} onFocus={onFocus} onSelect={setSelected} onCrossJump={enterNode}
                canBack={histIdx > 0} canFwd={histIdx < hist.length - 1}
                onBack={() => { setHistIdx(histIdx - 1); setFociWithHist(hist[histIdx - 1], true) }}
                onFwd={() => { setHistIdx(histIdx + 1); setFociWithHist(hist[histIdx + 1], true) }} />
              <DetailPanel project={active} view={view} nodeId={selected || effFoci[effFoci.length - 1] || ''}
                stale={staleIds} onJump={enterNode} />
            </>
          )}
        </div>
      )}
    </div>
  )
}
