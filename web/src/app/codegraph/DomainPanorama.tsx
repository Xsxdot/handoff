// DomainPanorama —— 领域全景：本层领域卡 + 领域间调用连线。
//
// 一层全景只画一件事：这些领域之间谁调谁。卡上是职责一句话与成员统计，
// 连线粗细是调用处数——点卡看领域详情、点线看「谁调用了谁的接口」、
// 「进入 ▸」下钻。本级之外的领域画成虚线占位卡：不能因为下钻就丢掉跨界关系。
//
// 布局来自 domainlayout（确定性），拖拽只改本组件的位置状态，不回写数据。
import { useEffect, useMemo, useRef, useState } from 'react'
import { domainAgg } from './domains'
import type { DomainCard } from './domains'
import { layoutDomains } from './domainlayout'
import type { CgView } from './graphmath'

const CARD_W = 252
const EXT_W = 196

interface DomainPanoramaProps {
  view: CgView
  scope: string | null
  selectedDomain: string
  selectedEdge: string
  onSelectDomain: (id: string) => void
  onSelectEdge: (key: string) => void
  onEnter: (id: string) => void
}

// cardStats 汇总一张卡的展示数字：类数 / 方法数 / 对外接口数 / 入口（已扫描/总）
// / 本视图的加改删计数。
//
// 入口刻意给「已扫描/总数」两个数：只给总数会被读成「入口都在图里了」，
// 而实际扫描常常只追了一部分链——那正是这张图最容易骗人的地方。
function cardStats(view: CgView, card: DomainCard, ifaceCount: number) {
  const classes = card.containers.filter((cid) => !view.containers[cid]?.entry).length
  const funcs = card.nodes.filter((id) => view.nodes[id]?.kind === 'func').length
  const scanned = card.entries.filter((id) => !view.nodes[id]?.unscanned).length
  const all = [...card.nodes, ...card.entries]
  const count = (s: string) => all.filter((id) => view.nodes[id]?.status === s).length
  return {
    classes, funcs, ifaceCount, scanned, entries: card.entries.length,
    added: count('added'), modified: count('modified'), deleted: count('deleted'),
  }
}

export function DomainPanorama(props: DomainPanoramaProps) {
  const { view, scope, selectedDomain, selectedEdge, onSelectDomain, onSelectEdge, onEnter } = props
  const agg = useMemo(() => domainAgg(view, scope), [view, scope])
  const ids = useMemo(
    () => Object.keys(agg.cards).filter((id) => {
      const c = agg.cards[id]
      return c.ext || c.nodes.length + c.entries.length > 0
    }).sort(),
    [agg],
  )
  // 位置：进入新的一层就按布局算一次；拖拽只改这里的状态，不回写数据
  const [pos, setPos] = useState<Record<string, [number, number]>>({})
  useEffect(() => { setPos(layoutDomains(agg, ids)) }, [agg, ids])
  // 拖拽标志：拖完紧接着会冒出一个 click，用它把那次 click 吞掉，
  // 否则「拖动卡片」会被误判成「点击选中」
  const dragged = useRef(false)
  // 平移/缩放：与叶子领域的焦点图同一套手势，全景不该是另一种操作方式
  const wrap = useRef<HTMLDivElement>(null)
  const [pan, setPan] = useState({ x: 0, y: 0 })
  const [zoom, setZoom] = useState(1)
  // 换一层领域就回到原点：上一层的视口位置对新的一层没有意义
  useEffect(() => { setPan({ x: 0, y: 0 }); setZoom(1) }, [scope])

  // 空白拖动平移：卡片/连线标签/控制按钮上按下不算，交给它们自己的处理
  const onPan = (ev: React.MouseEvent) => {
    if ((ev.target as HTMLElement).closest('[data-domain],[data-dedge],[data-relayout]')) return
    const sx = ev.clientX
    const sy = ev.clientY
    const o = pan
    const move = (e: MouseEvent) => setPan({ x: o.x + e.clientX - sx, y: o.y + e.clientY - sy })
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    ev.preventDefault()
  }

  // 滚轮：普通=平移；ctrl/⌘=以光标为不动点缩放（触控板捏合也走 ctrlKey 路径）。
  // 必须 passive:false 才能 preventDefault，否则 ⌘+滚轮会被浏览器当成页面缩放。
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
          setPan((p) => ({ x: mx - (mx - p.x) * (nz / z), y: my - (my - p.y) * (nz / z) }))
          return nz
        })
      } else {
        setPan((p) => ({ x: p.x - ev.deltaX, y: p.y - ev.deltaY }))
      }
    }
    el.addEventListener('wheel', onWheel, { passive: false })
    return () => el.removeEventListener('wheel', onWheel)
  }, [])

  const onDrag = (id: string, ev: React.MouseEvent) => {
    const sx = ev.clientX
    const sy = ev.clientY
    const orig = pos[id] ?? [0, 0]
    dragged.current = false
    const move = (e: MouseEvent) => {
      // 屏幕位移要除以缩放才是画布位移，否则放大后卡片跟不上光标
      const dx = (e.clientX - sx) / zoom
      const dy = (e.clientY - sy) / zoom
      // 4px 阈值：手抖不算拖动，否则选中会变得很难点
      if (Math.abs(dx) > 4 || Math.abs(dy) > 4) dragged.current = true
      if (!dragged.current) return
      setPos((p) => ({ ...p, [id]: [Math.max(10, orig[0] + dx), Math.max(60, orig[1] + dy)] }))
    }
    const up = () => {
      window.removeEventListener('mousemove', move)
      window.removeEventListener('mouseup', up)
    }
    window.addEventListener('mousemove', move)
    window.addEventListener('mouseup', up)
    ev.preventDefault()
  }

  // 点击：占位卡横跳到那个领域，实卡选中看详情；刚拖过就不算点击
  const onCardClick = (id: string) => {
    if (dragged.current) return
    if (agg.cards[id].ext) onEnter(id.slice(4))
    else onSelectDomain(id)
  }

  const W = Math.max(1200, ...ids.map((id) => (pos[id]?.[0] ?? 0) + 420))
  const H = Math.max(620, ...ids.map((id) => (pos[id]?.[1] ?? 0) + 300))
  const center = (id: string): [number, number] => {
    const p = pos[id] ?? [0, 0]
    const w = agg.cards[id].ext ? EXT_W : CARD_W
    return [p[0] + w / 2, p[1] + 62]
  }

  return (
    <div ref={wrap} className="relative min-w-0 flex-1 cursor-grab overflow-hidden" onMouseDown={onPan}>
      {/* 拖乱了一键重排：从当前布局重新松弛，不是推倒重来 */}
      <button data-relayout onClick={() => setPos(layoutDomains(agg, ids, pos))}
        className="absolute right-3 top-2.5 z-30 rounded border bg-background px-2 py-0.5 text-xs"
        title="拖乱了就重排一次">重新布局</button>
      <div className="relative"
        style={{ width: W, height: H, transform: `translate(${pan.x}px, ${pan.y}px) scale(${zoom})`, transformOrigin: '0 0' }}>
        <svg width={W} height={H} className="absolute inset-0">
          {[...agg.edges.entries()].map(([key, de]) => {
            if (!pos[de.from] || !pos[de.to]) return null
            const [x1, y1] = center(de.from)
            const [x2, y2] = center(de.to)
            const dx = x2 - x1
            const dy = y2 - y1
            const len = Math.hypot(dx, dy) || 1
            // 控制点垂直偏移：双向调用各弯一边，不叠在一起
            const mx = (x1 + x2) / 2 - (dy / len) * 30
            const my = (y1 + y2) / 2 + (dx / len) * 30
            const live = de.pairs.filter((p) => p.status !== 'deleted')
            const added = live.length > 0 && live.every((p) => p.status === 'added')
            const sel = selectedEdge === key
            return (
              <path key={key} d={`M ${x1} ${y1} Q ${mx} ${my} ${x2} ${y2}`} fill="none"
                stroke={added ? '#16a34a' : sel ? '#171717' : '#a8a8a8'}
                strokeWidth={1.5 + Math.min(de.pairs.length, 8) * 0.45} />
            )
          })}
        </svg>
        {[...agg.edges.entries()].map(([key, de]) => {
          if (!pos[de.from] || !pos[de.to]) return null
          const [x1, y1] = center(de.from)
          const [x2, y2] = center(de.to)
          const dx = x2 - x1
          const dy = y2 - y1
          const len = Math.hypot(dx, dy) || 1
          const added = de.pairs.filter((p) => p.status === 'added').length
          return (
            <div key={key} data-dedge={key} onClick={() => onSelectEdge(key)}
              className={'absolute z-10 -translate-x-1/2 -translate-y-1/2 cursor-pointer rounded-full border bg-background px-2 py-0.5 font-mono text-[10.5px] '
                + (selectedEdge === key ? 'border-primary text-primary' : '')}
              style={{ left: (x1 + x2) / 2 - (dy / len) * 30, top: (y1 + y2) / 2 + (dx / len) * 30 }}>
              {de.pairs.length} 处调用{added ? <span className="text-green-600"> +{added}</span> : null}
            </div>
          )
        })}
        {ids.map((id) => {
          const card = agg.cards[id]
          const meta = view.domains[card.ext ? id.slice(4) : id]
          if (!meta) return null
          const p = pos[id] ?? [0, 0]
          if (card.ext) {
            return (
              <div key={id} data-domain={id} onMouseDown={(e) => onDrag(id, e)} onClick={() => onCardClick(id)}
                className="absolute z-20 cursor-pointer select-none rounded-xl border-2 border-dashed bg-background/90 px-3 py-2 text-xs hover:border-primary"
                style={{ left: p[0], top: p[1], width: EXT_W }}>
                <div className="font-semibold text-muted-foreground">◇ {meta.label}<span className="ml-1.5 text-[10.5px] font-normal">{meta.kind}</span></div>
                <div className="mt-0.5 text-[11.5px] text-muted-foreground">领域外 · 点击进入</div>
              </div>
            )
          }
          const st = cardStats(view, card, agg.ifaces[id]?.size ?? 0)
          return (
            <div key={id} data-domain={id} onMouseDown={(e) => onDrag(id, e)} onClick={() => onCardClick(id)}
              className={'absolute z-20 cursor-grab select-none rounded-xl border-2 bg-background text-xs shadow-md '
                + (selectedDomain === id ? 'outline outline-2 outline-primary ' : '')}
              style={{ left: p[0], top: p[1], width: CARD_W }}>
              <div className="flex items-center gap-1.5 px-3.5 pb-1 pt-2 text-[13.5px] font-bold">
                {card.entries.length ? <span className="text-primary">⚑</span> : null}
                {meta.label}
                <span className="text-[10.5px] font-normal text-muted-foreground">{meta.kind}</span>
                <button title={'下钻到领域内部：' + meta.label}
                  onMouseDown={(e) => e.stopPropagation()}
                  onClick={(e) => { e.stopPropagation(); onEnter(id) }}
                  className="ml-auto rounded bg-muted px-2 py-0.5 text-[11px] hover:bg-primary hover:text-primary-foreground">
                  进入 ▸
                </button>
              </div>
              {meta.summary && <div className="px-3.5 pb-2 text-[11.5px] leading-relaxed text-muted-foreground">{meta.summary}</div>}
              <div className="flex flex-wrap gap-2.5 border-t px-3.5 py-1.5 text-[11px] text-muted-foreground">
                <span>{st.classes} 类</span>
                <span>{st.funcs} 方法</span>
                <span>⇢ {st.ifaceCount} 接口</span>
                {st.entries ? <span>⚑ {st.scanned}/{st.entries} 入口</span> : null}
                {st.added ? <span data-badge="add" className="rounded-full bg-green-600 px-1.5 font-bold text-white">+{st.added}</span> : null}
                {st.modified ? <span data-badge="mod" className="rounded-full bg-amber-500 px-1.5 font-bold text-white">~{st.modified}</span> : null}
                {st.deleted ? <span data-badge="del" className="rounded-full bg-red-600 px-1.5 font-bold text-white">-{st.deleted}</span> : null}
              </div>
            </div>
          )
        })}
      </div>
    </div>
  )
}
