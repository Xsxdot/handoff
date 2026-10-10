// SessionDetail —— 会话详情抽屉的 body（B358.8 #3：「⋯」从整面切换改右侧抽屉，
// 五块内容齐收：成员（群主标识）、会话卡（原 chips 数据改列表行）、协调者派发的
// 任务节点、会话 timeline、归档入口。detail 载荷原样透传（可对质，不二次解释）；
// 未知 kind 原样显示不白屏（sessionModel 兜底）。
import type { SessionDetail as SessionDetailDTO } from '../../api/rooms'
import { formatRelative } from '../lib/format'
import { memberStatusText, timelineKindLabel } from './sessionModel'

export function SessionDetail({ detail, onOpenCard, onArchive, archiveBusy, archiveError, compact = false }: {
  detail: SessionDetailDTO
  onOpenCard?: (cardId: string) => void
  onArchive?: () => void
  archiveBusy?: boolean
  archiveError?: string
  compact?: boolean
}) {
  const { summary, nodes = [], timeline = [] } = detail
  const members = summary.members ?? []
  const cards = summary.cards ?? []
  // SessionDetail.nodes is the server's task-mirror history projection. These rows
  // describe recorded dispatches, not proof of currently running work.
  const executionRecords = nodes
  const structureEvents = timeline
  const renderTimelineRow = (row: typeof timeline[number], index: number, prefix = 'timeline') => (
    <div key={row.seq} data-testid={`${prefix}-row-${index}`} className="flex items-baseline gap-2 py-1 text-xs">
      <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{formatRelative(row.created_at)}</span>
      <span className="shrink-0 font-medium">{timelineKindLabel(row.kind)}</span>
      {row.card_id && <b className="shrink-0 font-mono">{row.card_id}</b>}
      {row.detail && <span className="min-w-0 flex-1 truncate text-muted-foreground" title={row.detail}>{row.detail}</span>}
      {row.actor && <span className="shrink-0 text-muted-foreground">· 由 {row.actor}</span>}
    </div>
  )
  const renderExecutionRow = (node: typeof nodes[number], index: number) => (
    <div key={`${node.card_id}:${node.node}:${index}`} data-testid={`execution-row-${index}`} className="flex flex-wrap items-baseline gap-x-2 gap-y-1 border-b py-2 text-xs last:border-b-0">
      <b className="shrink-0 font-mono">{node.card_id}</b>
      {node.title && <span className="min-w-0 flex-1">{node.title}</span>}
      <span>{node.node}</span>
      {node.round !== undefined && <span className="shrink-0 text-muted-foreground">第 {node.round} 轮</span>}
      <span className="shrink-0 font-medium">{node.state}</span>
      {node.target && <span className="basis-full text-muted-foreground">目标：{node.target}</span>}
    </div>
  )
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
            {member.identity !== '' && member.identity === summary.owner && (
              <span data-testid={`member-owner-${index}`} className="shrink-0 rounded-full border bg-slate-100 px-1.5 text-[10px] font-semibold text-slate-700">群主</span>
            )}
            <span data-testid={`member-status-${index}`} className="shrink-0 text-xs text-muted-foreground">{memberStatusText(member)}</span>
          </div>
        ))}
      </section>
      <section aria-label="会话卡" className="mb-4 rounded-xl border bg-white/65 p-3 shadow-sm">
        <h3 className="mb-2 text-xs font-semibold text-muted-foreground">会话卡</h3>
        {cards.length === 0 ? <p className="text-xs text-muted-foreground">（会话还没有卡）</p> : cards.map((card) => (
          <button key={card.card_id} type="button" data-testid="session-card-row" onClick={() => onOpenCard?.(card.card_id)}
            className="flex w-full items-center gap-2 border-b py-2 text-left text-sm last:border-b-0 hover:bg-accent/40">
            <b className="shrink-0 font-mono">{card.card_id}</b>
            <span className="min-w-0 flex-1 truncate">{card.title ?? ''}</span>
            {card.status && <span className="shrink-0 text-xs text-muted-foreground">{card.status}</span>}
            <span className={`shrink-0 text-xs ${card.seat ? 'text-muted-foreground' : 'text-amber-700'}`}>{card.seat ?? '空座 · 还没配人'}</span>
          </button>
        ))}
      </section>
      {!compact && <section aria-label="任务节点" className="mb-4 rounded-xl border bg-white/65 p-3 shadow-sm">
        <h3 className="mb-2 text-xs font-semibold text-muted-foreground">协调者派发的任务节点</h3>
        {nodes.length === 0 ? <p className="text-xs text-muted-foreground">（暂无派发节点）</p> : nodes.map((node, index) => (
          <div key={`${node.card_id}:${node.node}:${index}`} data-testid={`session-node-${index}`} className="flex items-center gap-2 border-b py-1.5 text-xs last:border-b-0">
            <b className="font-mono">{node.card_id}</b>
            <span>{node.node}</span>
            <span className="ml-auto text-muted-foreground">{node.state}{node.target ? ` · ${node.target}` : ''}</span>
          </div>
        ))}
      </section>}
      {compact ? (
        <section aria-label="历史记录" className="mb-4 border-t">
          <h3 className="py-2 text-xs font-semibold text-muted-foreground">历史记录</h3>
          <details data-testid="session-execution-history" className="border-b py-1">
            <summary aria-label={`执行记录 ${executionRecords.length}`} className="cursor-pointer py-2 text-sm">执行记录 <span className="text-muted-foreground">{executionRecords.length} 条记录</span></summary>
            {executionRecords.length === 0 ? <p className="pb-2 text-xs text-muted-foreground">（暂无执行记录）</p> : executionRecords.map(renderExecutionRow)}
          </details>
          <details data-testid="session-structure-history" className="border-b py-1">
            <summary aria-label={`结构事件 ${structureEvents.length}`} className="cursor-pointer py-2 text-sm">结构事件 <span className="text-muted-foreground">{structureEvents.length} 条记录</span></summary>
            {structureEvents.length === 0 ? <p className="pb-2 text-xs text-muted-foreground">（暂无结构事件）</p> : structureEvents.map((row, index) => renderTimelineRow(row, index, 'structure'))}
          </details>
        </section>
      ) : (
      <section aria-label="会话 timeline" className="mb-4 rounded-xl border bg-white/65 p-3 shadow-sm">
        <h3 className="mb-2 text-xs font-semibold text-muted-foreground">会话 timeline（结构事件）</h3>
        {timeline.length === 0 ? <p className="text-xs text-muted-foreground">（暂无结构事件）</p> : timeline.map((row, index) => (
          renderTimelineRow(row, index, 'timeline')
        ))}
      </section>
      )}
      <section aria-label="会话管理" className="rounded-xl border bg-white/65 p-3 shadow-sm">
        <h3 className="mb-2 text-xs font-semibold text-muted-foreground">会话管理</h3>
        {summary.archived ? (
          <p className="text-xs text-muted-foreground">会话已归档（只读）。</p>
        ) : (
          <>
            <button type="button" data-testid="session-archive" onClick={onArchive} disabled={archiveBusy}
              className="rounded-md border border-destructive/40 px-2 py-1 text-xs text-destructive hover:bg-destructive/10 disabled:opacity-50">
              归档会话
            </button>
            <p className="mt-1 text-[11px] text-muted-foreground">归档后本会话转为只读。</p>
            {archiveError !== '' && <p role="alert" className="mt-1 text-xs text-destructive">{archiveError}</p>}
          </>
        )}
      </section>
    </div>
  )
}
