// FocusGraph —— 竖向焦点子图：上游在上、焦点居中、下游在下。
//
// 交互契约（原型确认形态，spec §5）：单击换焦点、⌘/Ctrl+单击并集、
// 空白拖动/滚轮平移、ctrl+滚轮以光标为不动点缩放、◀▶ 历史、层级下拉。
// 算法全部来自 graphmath；本组件只做渲染、事件与 transform 状态。
import { useEffect, useMemo, useRef, useState } from 'react'
import type { CgView } from './graphmath'
import { layoutBands, neighborhood } from './graphmath'
import { inScope, nodeDomainPathOf } from './domains'

const NODE_W = 156

interface FocusGraphProps {
  view: CgView; foci: string[]; depth: number; staleIds: Set<string>
  onDepth: (d: number) => void
  onFocus: (id: string, additive: boolean) => void
  onSelect: (id: string) => void
  scope: string | null; onCrossJump: (id: string) => void
  canBack: boolean; canFwd: boolean; onBack: () => void; onFwd: () => void
}

export function FocusGraph(props: FocusGraphProps) {
  const { view, foci, depth, staleIds, onDepth, onFocus, onSelect,
    scope, onCrossJump, canBack, canFwd, onBack, onFwd } = props
  const wrap = useRef<HTMLDivElement>(null)
  const [pan, setPan] = useState<{ x: number; y: number } | null>(null)
  const [zoom, setZoom] = useState(1)

  const { dist, px, py, W, H, order } = useMemo(() => {
    // 领域边界：域外节点要入图显示关系，但不能成为 BFS 的新扩展起点。
    const dist = neighborhood(view, foci, depth, (id) => inScope(view, id, scope))
    return { dist, ...layoutBands(view, dist) }
  }, [view, foci, depth, scope])

  // 焦点/层级变化 → 平移重算：锚点是最后加入的焦点，垂直居中但顶部最多悬空 24px
  const anchor = foci[foci.length - 1]
  const focusKey = foci.join('|')
  useEffect(() => { setPan(null) }, [focusKey, depth])
  const effPan = useMemo(() => {
    if (pan) return pan
    const el = wrap.current
    if (!el || px[anchor] === undefined) return { x: 0, y: 0 }
    return {
      x: el.clientWidth / 2 - (px[anchor] + NODE_W / 2) * zoom,
      y: Math.min(el.clientHeight / 2 - (py[anchor] + 22) * zoom, 24),
    }
  }, [pan, px, py, anchor, zoom])

  // 拖动平移：mousedown 只认空白（卡片与控制条 stopPropagation 不到这里靠 closest 判断）
  const onMouseDown = (ev: React.MouseEvent) => {
    if ((ev.target as HTMLElement).closest('[data-node],[data-ctl]')) return
    const sx = ev.clientX
    const sy = ev.clientY
    const ox = effPan.x
    const oy = effPan.y
    const move = (e: MouseEvent) => setPan({ x: ox + e.clientX - sx, y: oy + e.clientY - sy })
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    ev.preventDefault()
  }

  // 滚轮：普通=平移；ctrl/⌘=以光标为不动点缩放（触控板捏合也走 ctrlKey 路径）
  useEffect(() => {
    const el = wrap.current
    if (!el) return
    const onWheel = (ev: WheelEvent) => {
      ev.preventDefault()
      if (ev.ctrlKey || ev.metaKey) {
        const r = el.getBoundingClientRect()
        const mx = ev.clientX - r.left
        const my = ev.clientY - r.top
        setZoom((z) => {
          const nz = Math.min(2.5, Math.max(0.3, z * Math.exp(-ev.deltaY * 0.0035)))
          setPan((p) => {
            const cur = p ?? effPan
            return { x: mx - (mx - cur.x) * (nz / z), y: my - (my - cur.y) * (nz / z) }
          })
          return nz
        })
      } else {
        setPan((p) => {
          const cur = p ?? effPan
          return { x: cur.x - ev.deltaX, y: cur.y - ev.deltaY }
        })
      }
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [effPan])

  const fociSet = new Set(foci)
  const nodeCls = (id: string) => {
    const n = view.nodes[id]
    let c = 'absolute w-[156px] cursor-pointer rounded-lg border bg-background px-2.5 py-1 shadow-sm '
    if (n.kind === 'entry') c += 'rounded-full bg-primary text-primary-foreground '
    if (n.status === 'added') c += 'border-green-600 bg-green-50 '
    if (n.status === 'modified') c += 'border-amber-500 bg-amber-50 '
    if (n.status === 'deleted') c += 'border-dashed border-red-600 '
    if (fociSet.has(id)) c += 'outline outline-2 outline-primary '
    return c
  }

  return (
    <div ref={wrap} className="relative min-w-0 flex-1 cursor-grab overflow-hidden" onMouseDown={onMouseDown}>
      <div data-ctl className="absolute left-3 top-2 z-10 flex flex-wrap items-center gap-1.5">
        <button className="rounded border px-2 py-0.5 text-xs disabled:opacity-40" disabled={!canBack} onClick={onBack}>◀ 后退</button>
        <button className="rounded border px-2 py-0.5 text-xs disabled:opacity-40" disabled={!canFwd} onClick={onFwd}>前进 ▶</button>
        <select title="上下游各展开几级" value={depth} onChange={(e) => onDepth(Number(e.target.value))}
          className="rounded border px-1 py-0.5 text-xs">
          <option value={1}>上下 1 级</option><option value={2}>上下 2 级</option>
          <option value={3}>上下 3 级</option><option value={0}>全部层级</option>
        </select>
        {foci.length > 1 && foci.map((f) => (
          <span key={f} className="flex items-center gap-1 rounded-full bg-primary px-2 py-0.5 font-mono text-[11px] text-primary-foreground">
            {view.nodes[f]?.name}
            <b className="cursor-pointer opacity-70 hover:opacity-100" onClick={() => onFocus(f, true)}>×</b>
          </span>
        ))}
        <span className="rounded-full border bg-muted px-2.5 py-0.5 text-[11px] text-muted-foreground">
          {foci.length > 1 ? '并集视图：N 个焦点的链叠加'
            : scope ? '单击：只看它的链 · ⌘+单击：并集 · 虚线卡=领域外，点击横跳'
            : '单击：只看它的链 · ⌘+单击：并集 · 空白拖动 · ⌃滚轮缩放'}
        </span>
      </div>
      <div className="absolute" style={{
        width: W,
        height: H,
        transform: 'translate(' + effPan.x + 'px, ' + effPan.y + 'px) scale(' + zoom + ')',
        transformOrigin: '0 0',
      }}>
        <svg width={W} height={H} className="absolute inset-0">
          {view.edges.map((e, i) => {
            if (!(e.from in dist) || !(e.to in dist)) return null
            if (e.status === 'deleted' && !fociSet.has(e.from) && !fociSet.has(e.to)) return null
            const x1 = px[e.from] + NODE_W / 2
            const y1 = py[e.from] + 44
            const x2 = px[e.to] + NODE_W / 2
            const y2 = py[e.to]
            const touch = fociSet.has(e.from) || fociSet.has(e.to)
            const color = e.status === 'added' ? '#16a34a' : e.status === 'deleted' ? '#dc2626' : touch ? '#404040' : '#b8b8b8'
            const d = 'M ' + x1 + ' ' + y1 + ' C ' + x1 + ' ' + (y1 + 46) + ', ' + x2 + ' ' + (y2 - 46) + ', ' + x2 + ' ' + y2
            return <path key={i} d={d} fill="none" stroke={color} strokeWidth={touch ? 2 : 1.5}
              strokeDasharray={e.status === 'deleted' ? '5 4' : undefined} />
          })}
        </svg>
        {order[0] < 0 && <div className="absolute text-[10px] tracking-widest text-muted-foreground" style={{ left: 60, top: 36 }}>↑ 上游（谁调用它）</div>}
        {order[order.length - 1] > 0 && <div className="absolute text-[10px] tracking-widest text-muted-foreground" style={{ left: 60, top: H - 22 }}>下游（它调用谁）↓</div>}
        {Object.keys(dist).map((id) => {
          const n = view.nodes[id]
          const ext = !!scope && !inScope(view, id, scope)
          return (
            <div key={id} data-node={id} data-ext={ext ? '1' : undefined}
              className={nodeCls(id) + (ext ? ' border-dashed bg-muted ' : '')}
              style={{ left: px[id], top: py[id] }}
              onClick={(e) => {
                if (ext) { onCrossJump(id); return }
                onSelect(id)
                if (!(foci.length === 1 && foci[0] === id)) onFocus(id, e.metaKey || e.ctrlKey)
              }}>
              <div className={'break-all font-mono text-[11px] font-semibold ' + (n.status === 'deleted' ? 'text-muted-foreground line-through' : '')}>
                {n.name}{n.kind === 'func' ? '()' : ''}{staleIds.has(id) ? ' ⚠' : ''}
              </div>
              <div className="flex gap-1 text-[9.5px] opacity-70">
                <span className={ext ? 'text-primary' : ''}>
                  {ext ? '◇ ' + (view.domains[nodeDomainPathOf(view, id)[0]]?.label ?? '') + ' 领域'
                    : view.containers[n.container]?.label ?? ''}
                </span>
                {n.tests?.length ? <span className="text-green-600">✓{n.tests.length}</span> : null}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
