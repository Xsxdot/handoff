// graphmath —— 代码图的纯算法层：合并、邻域 BFS、竖向分层布局、调用树。
//
// 职责：全部确定性纯函数，组件只做渲染与事件
// 边界：不发请求、不碰 DOM；与 Go 侧 internal/codegraph 语义一致
//（deleted 不参与遍历、下游正距上游负距、多焦点并集）——两侧行为分叉
// 就是 bug，以 spec §3/§5 为准
import type { CgDiff, CgGraph, CgNode } from '../../api/types'

export type Status = '' | 'added' | 'modified' | 'deleted'
export interface ViewNode extends CgNode { status: Status }
export interface ViewEdge { from: string; to: string; status: Status }
export interface CgView {
  name: string
  // 恒为对象（空对象 = 该图没有领域段）：调用方不必到处判 undefined
  domains: NonNullable<CgGraph['domains']>
  containers: CgGraph['containers']
  nodes: Record<string, ViewNode>
  edges: ViewEdge[]
}

// mergeView 把基线与 diff 合并成视图；无 diff 即纯基准。
export function mergeView(g: CgGraph, d?: CgDiff): CgView {
  const nodes: Record<string, ViewNode> = {}
  for (const [id, n] of Object.entries(g.nodes)) nodes[id] = { ...n, status: '' }
  const edges: ViewEdge[] = g.edges.map(([from, to]) => ({ from, to, status: '' as Status }))
  if (!d) return { name: 'baseline', domains: g.domains ?? {}, containers: g.containers, nodes, edges }
  for (const [id, n] of Object.entries(d.nodesAdded ?? {})) nodes[id] = { ...n, status: 'added' }
  for (const [id, n] of Object.entries(d.nodesModified ?? {})) if (nodes[id]) nodes[id] = { ...n, status: 'modified' }
  for (const id of d.nodesDeleted ?? []) if (nodes[id]) nodes[id] = { ...nodes[id], status: 'deleted' }
  const del = new Set((d.edgesDeleted ?? []).map(([a, b]) => `${a}\u0000${b}`))
  for (const e of edges) if (del.has(`${e.from}\u0000${e.to}`)) e.status = 'deleted'
  for (const [from, to] of d.edgesAdded ?? []) edges.push({ from, to, status: 'added' })
  return { name: d.view, domains: g.domains ?? {}, containers: g.containers, nodes, edges }
}

// scannedEntries 返回已扫描入口 id（unscanned/deleted 不进左树），按 order+name 稳定排序。
export function scannedEntries(v: CgView): string[] {
  return Object.entries(v.nodes)
    .filter(([, n]) => n.kind === 'entry' && !n.unscanned && n.status !== 'deleted')
    .sort((a, b) => (a[1].order ?? 99) - (b[1].order ?? 99) || a[1].name.localeCompare(b[1].name))
    .map(([id]) => id)
}

// buildAdj 建正反邻接表；deleted 节点/边不参与（渲染残影另算）。
export function buildAdj(v: CgView): { adj: Record<string, string[]>; radj: Record<string, string[]> } {
  const adj: Record<string, string[]> = {}
  const radj: Record<string, string[]> = {}
  for (const e of v.edges) {
    if (e.status === 'deleted' || v.nodes[e.from]?.status === 'deleted' || v.nodes[e.to]?.status === 'deleted') continue
    ;(adj[e.from] ??= []).push(e.to)
    ;(radj[e.to] ??= []).push(e.from)
  }
  return { adj, radj }
}

// neighborhood 多源 BFS：焦点 0 层、下游正、上游负；depth=0 不限。
// expand 可选：返回 false 的节点仍会进入结果（要显示），但不再从它继续扩展——
// 领域下钻靠它把邻域裁在领域边界上，否则一跳就跑到别的领域去了。
export function neighborhood(
  v: CgView, foci: string[], depth: number, expand?: (id: string) => boolean,
): Record<string, number> {
  const { adj, radj } = buildAdj(v)
  const dist: Record<string, number> = {}
  for (const f of foci) dist[f] = 0
  const sweep = (next: Record<string, string[]>, step: number) => {
    let frontier = [...foci]
    while (frontier.length) {
      const nx: string[] = []
      for (const id of frontier) {
        const d = dist[id] + step
        if (depth > 0 && Math.abs(d) > depth) continue
        for (const t of next[id] ?? []) {
          if (t in dist) continue
          dist[t] = d
          if (!expand || expand(t)) nx.push(t)
        }
      }
      frontier = nx
    }
  }
  sweep(adj, 1)
  sweep(radj, -1)
  return dist
}

// layoutBands 竖向分层：每个 dist 一行，行内先名字序、再由内向外按邻居均值
// 重排减少交叉（原型验证过的布局，常量与 spec §5 基准一致）。
export function layoutBands(
  v: CgView, dist: Record<string, number>,
  NODE_W = 156, XSP = 180, YSTEP = 112, PADX = 60, PADY = 70,
): { px: Record<string, number>; py: Record<string, number>; W: number; H: number; order: number[] } {
  const { adj, radj } = buildAdj(v)
  const bands = new Map<number, string[]>()
  for (const [id, d] of Object.entries(dist)) {
    if (!bands.has(d)) bands.set(d, [])
    bands.get(d)!.push(id)
  }
  const order = [...bands.keys()].sort((a, b) => a - b)
  const maxCnt = Math.max(...order.map((d) => bands.get(d)!.length))
  const xStep = Math.max(XSP, NODE_W)
  const W = PADX * 2 + maxCnt * xStep
  const H = PADY * 2 + (order.length - 1) * YSTEP + 46
  const px: Record<string, number> = {}
  const py: Record<string, number> = {}
  const place = (d: number) => {
    const list = bands.get(d)!
    const off = PADX + ((maxCnt - list.length) * xStep) / 2
    list.forEach((id, i) => { px[id] = off + i * xStep; py[id] = PADY + (d - order[0]) * YSTEP })
  }
  const meanX = (id: string) => {
    const nb = [...(adj[id] ?? []), ...(radj[id] ?? [])].filter((t) => px[t] !== undefined)
    return nb.length ? nb.reduce((s, t) => s + px[t], 0) / nb.length : 0
  }
  for (const d of order) bands.get(d)!.sort((a, b) => v.nodes[a].name.localeCompare(v.nodes[b].name))
  place(0)
  for (let d = 1; d <= order[order.length - 1]; d++) if (bands.has(d)) { bands.get(d)!.sort((a, b) => meanX(a) - meanX(b)); place(d) }
  for (let d = -1; d >= order[0]; d--) if (bands.has(d)) { bands.get(d)!.sort((a, b) => meanX(a) - meanX(b)); place(d) }
  return { px, py, W, H, order }
}

// ChainTreeNode 是左树 CallTree 使用的 DFS 展开结果；cycle=true 表示此处截断回边。
export interface ChainTreeNode { id: string; cycle?: boolean; children: ChainTreeNode[] }

// chainTree 从入口 DFS 展开调用树，路径内重现即标 cycle 截断，深度上限防爆栈。
export function chainTree(v: CgView, entry: string, maxDepth = 10): ChainTreeNode {
  const { adj } = buildAdj(v)
  const walk = (id: string, path: Set<string>, depth: number): ChainTreeNode => {
    const kids = depth >= maxDepth ? [] : (adj[id] ?? [])
    return {
      id,
      children: kids.map((t) =>
        path.has(t) ? { id: t, cycle: true, children: [] }
          : walk(t, new Set([...path, t]), depth + 1)),
    }
  }
  return walk(entry, new Set([entry]), 0)
}
