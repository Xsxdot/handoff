// SessionDetail —— 会话 tab 的详情态：成员（状态只报可证实的四值词表）、
// 协调者派发的任务节点、会话 timeline 三块。detail 载荷原样透传（可对质，
// 不二次解释）；未知 kind 原样显示不白屏（sessionModel 兜底）。
import type { SessionDetail as SessionDetailDTO } from '../../api/rooms'
import { formatRelative } from '../lib/format'
import { memberStatusText, timelineKindLabel } from './sessionModel'

export function SessionDetail({ detail }: { detail: SessionDetailDTO }) {
  const { summary, nodes = [], timeline = [] } = detail
  const members = summary.members ?? []
  return (
    <div className="min-h-0 flex-1 overflow-y-auto p-3" aria-label="会话详情">
      <section aria-label="成员" className="mb-4 rounded-xl border bg-white/65 p-3 shadow-sm">
        <h3 className="mb-2 text-xs font-semibold text-muted-foreground">成员</h3>
        {members.length === 0 ? <p className="text-xs text-muted-foreground">（暂无成员）</p> : members.map((member, index) => (
          <div key={`${member.identity}:${member.card_id ?? ''}:${index}`} className="flex items-center gap-2 border-b py-2 text-sm last:border-b-0">
            <span className="min-w-0 flex-1 truncate">
              {member.identity !== '' ? member.identity : `（${member.card_title ?? member.card_id ?? ''} 无人推）`}
              {member.kind === 'seat' && member.identity !== '' && <span className="ml-1 text-xs text-muted-foreground">· {member.card_title ?? member.card_id}</span>}
            </span>
            <span data-testid={`member-status-${index}`} className="shrink-0 text-xs text-muted-foreground">{memberStatusText(member)}</span>
          </div>
        ))}
      </section>
      <section aria-label="任务节点" className="mb-4 rounded-xl border bg-white/65 p-3 shadow-sm">
        <h3 className="mb-2 text-xs font-semibold text-muted-foreground">协调者派发的任务节点</h3>
        {nodes.length === 0 ? <p className="text-xs text-muted-foreground">（暂无派发节点）</p> : nodes.map((node, index) => (
          <div key={`${node.card_id}:${node.node}:${index}`} data-testid={`session-node-${index}`} className="flex items-center gap-2 border-b py-1.5 text-xs last:border-b-0">
            <b className="font-mono">{node.card_id}</b>
            <span>{node.node}</span>
            <span className="ml-auto text-muted-foreground">{node.state}{node.target ? ` · ${node.target}` : ''}</span>
          </div>
        ))}
      </section>
      <section aria-label="会话 timeline" className="rounded-xl border bg-white/65 p-3 shadow-sm">
        <h3 className="mb-2 text-xs font-semibold text-muted-foreground">会话 timeline（结构事件）</h3>
        {timeline.length === 0 ? <p className="text-xs text-muted-foreground">（暂无结构事件）</p> : timeline.map((row, index) => (
          <div key={row.seq} data-testid={`timeline-row-${index}`} className="flex items-baseline gap-2 py-1 text-xs">
            <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{formatRelative(row.created_at)}</span>
            <span className="shrink-0 font-medium">{timelineKindLabel(row.kind)}</span>
            {row.card_id && <b className="shrink-0 font-mono">{row.card_id}</b>}
            {row.detail && <span className="min-w-0 flex-1 truncate text-muted-foreground" title={row.detail}>{row.detail}</span>}
            {row.actor && <span className="shrink-0 text-muted-foreground">· 由 {row.actor}</span>}
          </div>
        ))}
      </section>
    </div>
  )
}
