// CallTree —— 代码图左树：已扫描入口的调用树导航。
// 展开状态由父组件持有（Set<string>），点名字换焦点、⌘/Ctrl+点做并集追加。
import type { ChainTreeNode, CgView } from './graphmath'
import { chainTree, scannedEntries } from './graphmath'
import { inScope, leafRoots, nodeDomainPathOf } from './domains'

const STATUS_BADGE: Record<string, { text: string; cls: string }> = {
  added: { text: '加', cls: 'bg-green-600' },
  modified: { text: '改', cls: 'bg-amber-500' },
  deleted: { text: '删', cls: 'bg-red-600' },
}

function Row({ view, node, foci, open, scope, onToggle, onFocus, onCrossJump }: {
  view: CgView; node: ChainTreeNode; foci: string[]; open: Set<string>
  scope: string | null; onCrossJump: (id: string) => void
  onToggle: (id: string, open: boolean) => void
  onFocus: (id: string, additive: boolean) => void
}) {
  const n = view.nodes[node.id]
  if (!n) return null
  if (node.cycle) return <div className="pl-4 text-xs text-muted-foreground">↻ {n.name}</div>
  const badge = STATUS_BADGE[n.status]
  return (
    <details open={open.has(node.id)}
      onToggle={(e) => onToggle(node.id, (e.target as HTMLDetailsElement).open)}
      className="pl-3.5">
      <summary className="flex cursor-pointer items-center gap-1.5 rounded px-1 py-0.5 hover:bg-muted">
        <span
          className={`font-mono text-xs ${foci.includes(node.id) ? 'rounded bg-primary px-1 text-primary-foreground' : ''}`}
          onClick={(e) => { e.preventDefault(); e.stopPropagation(); onFocus(node.id, e.metaKey || e.ctrlKey) }}>
          {n.name}{n.kind === 'func' ? '()' : ''}
        </span>
        {badge && <span className={`rounded-full px-1 text-[9px] font-bold text-white ${badge.cls}`}>{badge.text}</span>}
        {n.tests?.length ? <span className="text-[10px] text-green-600">✓{n.tests.length}</span> : null}
      </summary>
      {node.children.map((c, i) => (
        scope && !inScope(view, c.id, scope) ? (
          // 链路撞到领域外：不截断也不越界，给一行可点的横跳——
          // 截断会让人以为调用到此为止，越界会让这层树无边无际
          <div key={`${c.id}-${i}`} className="ml-4 cursor-pointer text-xs text-muted-foreground hover:underline"
            onClick={() => onCrossJump(c.id)}>
            ↗ {view.nodes[c.id]?.name} · {view.domains[nodeDomainPathOf(view, c.id)[0]]?.label ?? ''} 领域
          </div>
        ) : (
          <Row key={`${c.id}-${i}`} view={view} node={c} foci={foci} open={open}
            scope={scope} onToggle={onToggle} onFocus={onFocus} onCrossJump={onCrossJump} />
        )
      ))}
    </details>
  )
}

// CallTree 渲染全部已扫描入口，各自一棵 chainTree。
export function CallTree(props: {
  view: CgView; foci: string[]; open: Set<string>
  scope: string | null; onCrossJump: (id: string) => void
  onToggle: (id: string, open: boolean) => void
  onFocus: (id: string, additive: boolean) => void
}) {
  return (
    <nav className="w-80 shrink-0 overflow-auto border-r p-2 text-[13px]">
      {(props.scope ? leafRoots(props.view, props.scope) : scannedEntries(props.view)).map((e) => (
        <Row key={e} view={props.view} node={chainTree(props.view, e)} foci={props.foci}
          open={props.open} scope={props.scope} onToggle={props.onToggle} onFocus={props.onFocus}
          onCrossJump={props.onCrossJump} />
      ))}
    </nav>
  )
}
