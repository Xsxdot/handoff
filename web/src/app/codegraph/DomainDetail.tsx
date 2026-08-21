// DomainDetail —— 领域全景的右详情：领域卡详情 / 领域间连线详情。
//
// 领域详情回答「这个领域负责什么、对外开了哪些口子、外面从哪些入口进来」；
// 连线详情回答「谁调用了谁的接口」——逐对列出底层方法，点任一端下钻过去。
// 与 DetailPanel（方法/实体详情）是两个东西：那个跟着节点走，这个跟着领域走。
import { domainAgg } from './domains'
import type { CgView } from './graphmath'

interface DomainDetailProps {
  view: CgView
  scope: string | null
  domainId: string   // 与 edgeKey 互斥
  edgeKey: string
  onEnterNode: (id: string) => void
  onEnterDomain: (id: string) => void
}

function Sec({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-3.5">
      <div className="mb-1 text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
      {children}
    </div>
  )
}

// NodeLink 一行可点的节点：点了就下钻到它所在的叶子领域并聚焦。
function NodeLink({ view, id, suffix, onEnterNode }: {
  view: CgView; id: string; suffix?: string; onEnterNode: (id: string) => void
}) {
  const n = view.nodes[id]
  if (!n) return null
  return (
    <div className="cursor-pointer py-0.5 font-mono text-xs hover:underline" onClick={() => onEnterNode(id)}>
      {n.name}{n.kind === 'func' ? '()' : ''}
      <span className="ml-1.5 font-sans text-[10.5px] text-muted-foreground">
        {view.containers[n.container]?.label}{suffix ? ' ' + suffix : ''}
      </span>
    </div>
  )
}

export function DomainDetail(props: DomainDetailProps) {
  const { view, scope, domainId, edgeKey, onEnterNode, onEnterDomain } = props
  const agg = domainAgg(view, scope)
  const shell = 'w-[340px] shrink-0 overflow-y-auto border-l p-3.5 text-sm'

  if (edgeKey) {
    const de = agg.edges.get(edgeKey)
    if (!de) return <aside className={shell} />
    const label = (cardId: string) => view.domains[cardId.startsWith('ext:') ? cardId.slice(4) : cardId]?.label ?? cardId
    return (
      <aside className={shell}>
        <h3 className="font-mono text-sm font-semibold">{label(de.from)} → {label(de.to)}</h3>
        <div className="mb-2.5 font-mono text-[11px] text-muted-foreground">{de.pairs.length} 处跨领域调用</div>
        <Sec label="谁调用了谁的接口">
          {de.pairs.map((p, i) => (
            <div key={i} className="border-t py-1 text-xs">
              <NodeLink view={view} id={p.from} onEnterNode={onEnterNode} />
              <div className="pl-2 text-[11px] text-muted-foreground">
                ↓ 调用{p.status === 'added' ? ' （本视图新增）' : p.status === 'deleted' ? ' （本视图删除）' : ''}
              </div>
              <NodeLink view={view} id={p.to} onEnterNode={onEnterNode} />
            </div>
          ))}
        </Sec>
      </aside>
    )
  }

  const meta = view.domains[domainId]
  const card = agg.cards[domainId]
  if (!meta || !card) return <aside className={shell} />
  const ifs = agg.ifaces[domainId]
  const scannedEntry = card.entries.filter((id) => !view.nodes[id]?.unscanned)
  const unscanned = card.entries.length - scannedEntry.length
  return (
    <aside className={shell}>
      <h3 className="font-mono text-sm font-semibold">
        {meta.label} <span className="font-sans text-[11px] font-normal text-muted-foreground">{meta.kind}</span>
      </h3>
      <div className="mb-2.5 font-mono text-[11px] text-muted-foreground">
        领域 · {card.containers.filter((cid) => !view.containers[cid]?.entry).length} 个类/分组
      </div>
      {meta.summary && <Sec label="职责"><div>{meta.summary}</div></Sec>}
      {meta.desc && <Sec label="内部逻辑"><div className="text-[12.5px] leading-relaxed text-muted-foreground">{meta.desc}</div></Sec>}
      <Sec label="对外开放接口（被其他领域调用）">
        {ifs && ifs.size ? [...ifs.entries()].map(([nid, callers]) => (
          <NodeLink key={nid} view={view} id={nid} onEnterNode={onEnterNode}
            suffix={'← ' + [...callers].map((c) => view.domains[c.startsWith('ext:') ? c.slice(4) : c]?.label ?? c).join('、')} />
        )) : <div className="text-xs text-muted-foreground">无——没有其他领域调用它的方法</div>}
      </Sec>
      {card.entries.length ? (
        <Sec label="对外入口（CLI / HTTP / WS）">
          {scannedEntry.map((id) => <NodeLink key={id} view={view} id={id} onEnterNode={onEnterNode} />)}
          {/* 未扫描入口只报数不列出：列出来会让人以为它们已经在图里了 */}
          {unscanned ? <div className="mt-1 text-[11.5px] text-muted-foreground">…另有 {unscanned} 个未扫描入口</div> : null}
        </Sec>
      ) : null}
      <button className="rounded border px-2 py-0.5 text-xs" onClick={() => onEnterDomain(domainId)}>
        进入领域内部 ▸
      </button>
    </aside>
  )
}
