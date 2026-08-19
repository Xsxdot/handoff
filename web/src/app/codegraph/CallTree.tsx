// CallTree —— 代码图左树：已扫描入口的调用树导航。
// 展开状态由父组件持有（Set<string>），点名字换焦点、⌘/Ctrl+点做并集追加。
import type { ChainTreeNode, CgView } from './graphmath'
import { chainTree, scannedEntries } from './graphmath'

const STATUS_BADGE: Record<string, { text: string; cls: string }> = {
  added: { text: '加', cls: 'bg-green-600' },
  modified: { text: '改', cls: 'bg-amber-500' },
  deleted: { text: '删', cls: 'bg-red-600' },
}

function Row({ view, node, foci, open, onToggle, onFocus }: {
  view: CgView; node: ChainTreeNode; foci: string[]; open: Set<string>
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
        <Row key={`${c.id}-${i}`} view={view} node={c} foci={foci} open={open} onToggle={onToggle} onFocus={onFocus} />
      ))}
    </details>
  )
}

// CallTree 渲染全部已扫描入口，各自一棵 chainTree。
export function CallTree(props: {
  view: CgView; foci: string[]; open: Set<string>
  onToggle: (id: string, open: boolean) => void
  onFocus: (id: string, additive: boolean) => void
}) {
  return (
    <nav className="w-80 shrink-0 overflow-auto border-r p-2 text-[13px]">
      {scannedEntries(props.view).map((e) => (
        <Row key={e} view={props.view} node={chainTree(props.view, e)} foci={props.foci}
          open={props.open} onToggle={props.onToggle} onFocus={props.onFocus} />
      ))}
    </nav>
  )
}
