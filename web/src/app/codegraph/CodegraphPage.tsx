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

  // 出错时**不能整页替换**：项目下拉在工具条里，把工具条一起换掉，选中一个
  // 没扫过图的项目后就再也换不回去了（本页没有别的项目入口，等于卡死）。
  // 所以错误只占内容区，工具条恒在。
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
      {loading || error || !view ? (
        <CodegraphPlaceholder loading={loading} error={error} project={active} onRetry={reload} />
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

// NOT_SCANNED 是 agentd 对「这个项目还没扫过图」的应答特征（codegraph.go 的 404
// 文案）。按文案判而不是按状态码：useCodegraph 只把 Error.message 传出来，
// 状态码在那一层就丢了。改 agentd 那句文案时这里要一起改——两处都在提「未生成
// 代码图」，grep 得到。
const NOT_SCANNED = '未生成代码图'

/** CodegraphPlaceholder 是内容区的三种非图状态：加载中 / 没扫过 / 真出错。 */
function CodegraphPlaceholder({ loading, error, project, onRetry }: {
  loading: boolean
  error: string
  project: string
  onRetry: () => void
}) {
  if (loading) return <div className="p-6 text-sm text-muted-foreground">加载中…</div>

  // 「没扫过」不是故障，是这个项目还没做过的一件事——给命令，别给红字。
  const notScanned = !error || error.includes(NOT_SCANNED)
  return (
    <div className="flex min-h-0 flex-1 flex-col items-center justify-center gap-3 p-6 text-center">
      {notScanned ? (
        <>
          <p className="text-sm font-medium">{project ? `项目 ${project} 还没有代码图` : '还没有代码图'}</p>
          {/* 这里不给「跑一句命令」的暗示：本仓没有 graph scan 子命令，
              基线是派 executor 按 docs/codegraph-scan-recipe.md 扫出来的
              （handoff graph 一族全是本地只读查询，见 cmd/graph.go） */}
          <p className="max-w-md text-xs text-muted-foreground">
            代码图是扫描产物，落在项目仓库的
            <code className="mx-1 rounded bg-muted px-1 py-0.5 font-mono">codegraph/baseline.json</code>。
            按 <code className="mx-1 rounded bg-muted px-1 py-0.5 font-mono">docs/codegraph-scan-recipe.md</code>
            派一次扫描任务，文件落盘后回来点「刷新」。
          </p>
        </>
      ) : (
        <>
          <p className="text-sm font-medium text-destructive">取代码图失败</p>
          {/* 报错原文照抄，不翻译不概括：这里最常见的是网络/权限，改写会让人查错方向 */}
          <p className="max-w-md break-all text-xs text-muted-foreground">{error}</p>
        </>
      )}
      <button type="button" onClick={onRetry} className="rounded border px-2.5 py-1 text-xs hover:bg-accent/60">
        重试
      </button>
    </div>
  )
}
