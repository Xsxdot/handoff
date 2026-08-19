// SeqView —— 时序图辅助视角：单入口链路的跨类调用顺序。
// 列 = 链上出现的容器（按首次出现排）；每条边一行箭头，自上而下即调用顺序。
import { useMemo } from 'react'
import type { CgView } from './graphmath'
import { buildAdj } from './graphmath'

interface SeqViewProps {
  view: CgView; entry: string; onSelect: (id: string) => void
}

export function SeqView(props: SeqViewProps) {
  const { cols, calls } = useMemo(() => {
    if (!props.entry || !props.view.nodes[props.entry]) return { cols: [], calls: [] as { from: string; to: string }[] }
    const { adj } = buildAdj(props.view)
    const cols: string[] = []
    const calls: { from: string; to: string }[] = []
    const seen = new Set<string>([props.entry])
    const colOf = (id: string) => {
      const c = props.view.nodes[id].container
      if (!cols.includes(c)) cols.push(c)
      return c
    }
    colOf(props.entry)
    const walk = (id: string) => {
      for (const t of adj[id] ?? []) {
        calls.push({ from: id, to: t })
        colOf(t)
        if (!seen.has(t)) { seen.add(t); walk(t) }
      }
    }
    walk(props.entry)
    return { cols, calls }
  }, [props.view, props.entry])

  const x = (c: string) => 90 + cols.indexOf(c) * 190
  const height = 80 + calls.length * 40
  return (
    <div className="min-w-0 flex-1 overflow-auto">
      <svg width={Math.max(700, 90 + cols.length * 190)} height={height}>
        {cols.map((c) => (
          <g key={c}>
            <text x={x(c)} y={28} textAnchor="middle" className="fill-current font-mono text-[11px] font-semibold">{props.view.containers[c]?.label}</text>
            <line x1={x(c)} y1={40} x2={x(c)} y2={height - 10} stroke="#d4d4d4" strokeDasharray="3 3" />
          </g>
        ))}
        {calls.map((call, i) => {
          const y = 70 + i * 40
          const x1 = x(props.view.nodes[call.from].container)
          const x2 = x(props.view.nodes[call.to].container)
          const self = x1 === x2
          return (
            <g key={i} className="cursor-pointer" onClick={() => props.onSelect(call.to)}>
              {self ? (
                <path d={'M ' + x1 + ' ' + y + ' C ' + (x1 + 46) + ' ' + y + ', ' + (x1 + 46) + ' ' + (y + 18) + ', ' + x1 + ' ' + (y + 18)} fill="none" stroke="#525252" markerEnd="url(#cgArrow)" />
              ) : (
                <line x1={x1} y1={y} x2={x2} y2={y} stroke="#525252" markerEnd="url(#cgArrow)" />
              )}
              <text x={(x1 + x2) / 2} y={y - 5} textAnchor="middle" className="fill-current font-mono text-[10.5px] hover:underline">
                {props.view.nodes[call.to].name}
              </text>
            </g>
          )
        })}
        <defs>
          <marker id="cgArrow" markerWidth="7" markerHeight="7" refX="6" refY="3.5" orient="auto">
            <path d="M0,0 L7,3.5 L0,7 z" fill="#525252" />
          </marker>
        </defs>
      </svg>
    </div>
  )
}
