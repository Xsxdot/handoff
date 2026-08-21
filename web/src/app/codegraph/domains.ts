// domains —— 领域层的纯算法：路径、祖先链、子领域、全景聚合、叶子领域裁剪。
//
// 职责：全部确定性纯函数，组件只做渲染与事件
// 边界：不发请求、不碰 DOM；领域一律读自数据的 domains 段，**绝不按包名推导**
// ——推导出来的层级会被人和 agent 当成真实架构读（spec §3.1）。
// 与 Go 侧 internal/codegraph/domains.go 的「跨领域边 / 对外接口」判定规则必须
// 一致（跨领域 = 两端叶子领域不同；接口 = 跨领域边的被调端），两侧分叉就是 bug。
import type { CgView, ViewEdge } from './graphmath'
import { scannedEntries } from './graphmath'

// DomainCard 是全景里的一张卡。ext=true 表示它是**本级之外**的领域占位卡：
// 只为保住跨界连线而画，点击横跳过去，不显示统计。
export interface DomainCard {
  id: string
  ext: boolean
  containers: string[]
  entries: string[]
  nodes: string[]
}

// DomainEdge 是两个领域之间的调用关系；pairs 保留底层方法对，供「谁调用了谁的接口」。
export interface DomainEdge {
  from: string
  to: string
  pairs: ViewEdge[]
}

// DomainAgg 是一次全景聚合的结果。ifaces[领域][被调节点] = 调用方领域集合。
export interface DomainAgg {
  cards: Record<string, DomainCard>
  edges: Map<string, DomainEdge>
  ifaces: Record<string, Map<string, Set<string>>>
}

// hasDomains 判断该图带不带领域段；false 时页面降级为单领域视图并给出提示。
export function hasDomains(v: CgView): boolean {
  return Object.keys(v.domains).length > 0
}

// domainPathOf 返回容器所属领域从顶层到叶子的路径；未归属或领域缺失返回 []。
// 用 seen 防成环——Validate 会拦下环，但坏数据不该让界面死循环。
export function domainPathOf(v: CgView, containerId: string): string[] {
  const path: string[] = []
  const seen = new Set<string>()
  let id = v.containers[containerId]?.domain ?? ''
  while (id && v.domains[id] && !seen.has(id)) {
    seen.add(id)
    path.unshift(id)
    id = v.domains[id].parent ?? ''
  }
  return path
}

// nodeDomainPathOf 同 domainPathOf，按节点取。
export function nodeDomainPathOf(v: CgView, nodeId: string): string[] {
  const n = v.nodes[nodeId]
  return n ? domainPathOf(v, n.container) : []
}

// domainAncestors 返回顶层到 scope 的完整路径（含 scope 自身），面包屑用。
// **按 parent 链走**，不要拆 id 字符串——领域 id 是不透明的，带不带斜杠都合法。
export function domainAncestors(v: CgView, scope: string): string[] {
  const path: string[] = []
  const seen = new Set<string>()
  let id = scope
  while (id && v.domains[id] && !seen.has(id)) {
    seen.add(id)
    path.unshift(id)
    id = v.domains[id].parent ?? ''
  }
  return path
}

// childDomainsOf 返回 scope 的直接子领域（scope=null 即顶层领域），按 id 升序。
// 返回空数组 = scope 是叶子领域，页面据此切换到树+图视图。
export function childDomainsOf(v: CgView, scope: string | null): string[] {
  const want = scope ?? ''
  return Object.entries(v.domains)
    .filter(([, d]) => (d.parent ?? '') === want)
    .map(([id]) => id)
    .sort()
}

// inScope 判断节点是否落在 scope 领域内（含其子领域）；scope=null 恒真。
export function inScope(v: CgView, nodeId: string, scope: string | null): boolean {
  if (!scope) return true
  return nodeDomainPathOf(v, nodeId).includes(scope)
}

// domainAgg 把视图聚合成一层领域全景。
// scope=null 聚到顶层领域；否则聚到 scope 的直接子领域，本级之外的容器归入
// "ext:<顶层领域>" 占位卡。两端都在域外的边不画——那是别人家的事。
export function domainAgg(v: CgView, scope: string | null): DomainAgg {
  const cards: Record<string, DomainCard> = {}
  const byContainer: Record<string, string> = {}
  const put = (cardId: string, cid: string, ext: boolean) => {
    const card = (cards[cardId] ??= { id: cardId, ext, containers: [], entries: [], nodes: [] })
    card.containers.push(cid)
    byContainer[cid] = cardId
  }
  for (const cid of Object.keys(v.containers)) {
    const path = domainPathOf(v, cid)
    if (!path.length) continue
    if (scope === null) {
      put(path[0], cid, false)
      continue
    }
    const i = path.indexOf(scope)
    if (i < 0) {
      put('ext:' + path[0], cid, true)
      continue
    }
    // path[i+1] 缺席 = 本级直属成员（叶子领域的内容），不进全景
    if (path[i + 1]) put(path[i + 1], cid, false)
  }
  for (const [id, n] of Object.entries(v.nodes)) {
    const card = cards[byContainer[n.container]]
    if (!card) continue
    if (n.kind === 'entry') card.entries.push(id)
    else card.nodes.push(id)
  }
  const edges = new Map<string, DomainEdge>()
  const ifaces: Record<string, Map<string, Set<string>>> = {}
  for (const e of v.edges) {
    const a = byContainer[v.nodes[e.from]?.container ?? '']
    const b = byContainer[v.nodes[e.to]?.container ?? '']
    if (!a || !b || a === b) continue
    if (a.startsWith('ext:') && b.startsWith('ext:')) continue
    const key = a + '|' + b
    const de = edges.get(key) ?? { from: a, to: b, pairs: [] }
    de.pairs.push(e)
    edges.set(key, de)
    if (e.status !== 'deleted' && !b.startsWith('ext:')) {
      const m = (ifaces[b] ??= new Map())
      if (!m.has(e.to)) m.set(e.to, new Set())
      m.get(e.to)!.add(a)
    }
  }
  return { cards, edges, ifaces }
}

// leafRoots 是叶子领域内部树的根：本域已扫描入口 + 被域外调用的接口。
// 都没有时（纯被内部使用的领域）退而取本域第一个非入口节点——空白页分不清
// 「这个领域是空的」和「渲染坏了」。
export function leafRoots(v: CgView, scope: string): string[] {
  const ifs = new Set<string>()
  for (const e of v.edges) {
    if (e.status === 'deleted') continue
    if (inScope(v, e.to, scope) && !inScope(v, e.from, scope) && !v.nodes[e.to]?.unscanned) ifs.add(e.to)
  }
  const roots = [
    ...scannedEntries(v).filter((id) => inScope(v, id, scope)),
    ...[...ifs].sort(),
  ]
  const uniq = [...new Set(roots)]
  if (uniq.length) return uniq
  return Object.keys(v.nodes)
    .filter((id) => inScope(v, id, scope) && v.nodes[id].kind !== 'entry')
    .sort()
    .slice(0, 1)
}
