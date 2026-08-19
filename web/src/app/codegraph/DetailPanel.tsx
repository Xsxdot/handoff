// DetailPanel —— 代码图右详情（常显，跟随焦点/选中节点）。
// 区块：职责/签名(新旧对照)/参数/返回/字段/关联测试/被谁调用/调用了/源码。
// 源码按 file:line 经 agentd 实时读——不落地缓存，保鲜以真实文件为准。
import { useEffect, useState } from 'react'
import { fetchCodegraphSource } from '../../api/client'
import type { CgSourceResp } from '../../api/types'
import type { CgView } from './graphmath'

interface DetailPanelProps {
  project: string; view: CgView; nodeId: string; stale: Set<string>; onJump: (id: string) => void
}

function Sec({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="mb-3.5">
      <div className="mb-1 text-[11px] uppercase tracking-wide text-muted-foreground">{label}</div>
      {children}
    </div>
  )
}

export function DetailPanel(props: DetailPanelProps) {
  const n = props.view.nodes[props.nodeId]
  const [src, setSrc] = useState<CgSourceResp | null>(null)
  const [srcOpen, setSrcOpen] = useState(false)
  useEffect(() => { setSrc(null); setSrcOpen(false) }, [props.nodeId])
  useEffect(() => {
    if (srcOpen && !src && n?.file) {
      fetchCodegraphSource(props.project, n.file, n.line)
        .then(setSrc)
        .catch(() => setSrc({ file: n.file, from: 0, lines: ['（源码读取失败）'] }))
    }
  }, [srcOpen, src, n, props.project])
  if (!n) return <aside className="w-[340px] shrink-0 overflow-y-auto border-l p-3.5" />
  const callers = props.view.edges.filter((e) => e.to === props.nodeId && e.status !== 'deleted').map((e) => e.from)
  const callees = props.view.edges.filter((e) => e.from === props.nodeId && e.status !== 'deleted').map((e) => e.to)
  return (
    <aside className="w-[340px] shrink-0 overflow-y-auto border-l p-3.5 text-sm">
      <h3 className="break-all font-mono text-sm font-semibold">{n.name}</h3>
      <div className="mb-2.5 font-mono text-[11px] text-muted-foreground">{n.file}:{n.line} · {props.view.containers[n.container]?.label}</div>
      {props.stale.has(props.nodeId) && <div className="mb-2.5 rounded border border-amber-300 bg-amber-50 px-2 py-1 text-xs text-amber-700">⚠ 疑似失鲜：file:line 与真实源码对不上，建议重扫后再采信</div>}
      {n.summary && <Sec label="职责"><div>{n.summary}</div></Sec>}
      {n.signature && (
        <Sec label="签名">
          {n.signatureOld && <pre className="mb-1 whitespace-pre-wrap rounded bg-muted px-2 py-1.5 font-mono text-[11.5px] line-through opacity-60">{n.signatureOld}</pre>}
          <pre className="whitespace-pre-wrap rounded bg-muted px-2 py-1.5 font-mono text-[11.5px]">{n.signature}</pre>
        </Sec>
      )}
      {n.params?.length ? (
        <Sec label="参数">
          <table className="w-full text-xs"><tbody>
            {n.params.map(([pn, pt, ps], i) => (
              <tr key={i} className="border-t"><td className="py-0.5 pr-2 font-mono">{pn}</td>
                <td className="pr-2 font-mono text-muted-foreground">{pt}</td><td>{ps ?? ''}</td></tr>
            ))}
          </tbody></table>
        </Sec>
      ) : null}
      {n.returns && <Sec label="返回"><span className="font-mono text-xs">{n.returns}</span></Sec>}
      {n.fields?.length ? (
        <Sec label="字段">
          <table className="w-full text-xs"><tbody>
            {n.fields.map(([fn, ft, fs], i) => (
              <tr key={i} className="border-t"><td className="py-0.5 pr-2 font-mono">{fn}</td>
                <td className="pr-2 font-mono text-muted-foreground">{ft}</td><td>{fs ?? ''}</td></tr>
            ))}
          </tbody></table>
        </Sec>
      ) : null}
      <Sec label="关联测试">
        {n.tests?.length ? n.tests.map((t) => (
          <details key={t.name} className="mb-1">
            <summary className="cursor-pointer font-mono text-xs text-green-700">{t.name} <span className="text-muted-foreground">{t.file}</span></summary>
            {t.snippet && <pre className="mt-1 overflow-x-auto rounded bg-muted p-2 text-[11px]">{t.snippet}</pre>}
          </details>
        )) : <div className="text-xs text-muted-foreground">无——这也是暴露的信号：该方法没有测试覆盖</div>}
      </Sec>
      {[['被谁调用', callers], ['调用了', callees]].map(([label, ids]) => (
        <Sec key={label as string} label={label as string}>
          {(ids as string[]).length ? (ids as string[]).map((id) => (
            <div key={id} className="cursor-pointer font-mono text-xs text-primary hover:underline" onClick={() => props.onJump(id)}>
              {label === '被谁调用' ? '←' : '→'} {props.view.nodes[id]?.name}
            </div>
          )) : <div className="text-xs text-muted-foreground">（图内无记录）</div>}
        </Sec>
      ))}
      <details open={srcOpen} onToggle={(e) => setSrcOpen((e.target as HTMLDetailsElement).open)}>
        <summary className="cursor-pointer text-xs text-muted-foreground">源码（实时读自 {n.file}:{n.line}）</summary>
        {src && (
          <pre className="mt-1 overflow-x-auto rounded bg-muted p-2 text-[11px] leading-relaxed">
            {src.lines.map((l, i) => String(src.from + i).padStart(4) + ' ' + l).join('\n')}
          </pre>
        )}
      </details>
    </aside>
  )
}
