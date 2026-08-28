// QueuePanel.tsx —— 看板的排队快照区。
//
// 职责：显示 GET /api/queue 的后端位次，并把卡号接回抽屉。
// 边界：不重排、不重算 ready、不调用队列规则；断线语义由 usePoll 提供。
import type { QueueEntry } from '../../api/scheduling'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'

export interface QueuePanelProps {
  entries: QueueEntry[]
  loading: boolean
  disconnected: boolean
  sessionExpired: boolean
  errorText: string
  onOpenCard: (cardId: string) => void
}

/** 返回每张卡的最早服务端位次；不排序、不把空卡号写入结果。 */
export function queuePositionByCard(entries: readonly QueueEntry[]): ReadonlyMap<string, number> {
  const positions = new Map<string, number>()
  for (const entry of entries) {
    if (entry.card === '') continue
    const current = positions.get(entry.card)
    if (current === undefined || entry.position < current) positions.set(entry.card, entry.position)
  }
  return positions
}

function entryLabel(entry: QueueEntry): string {
  return entry.kind === 'launch_queue' ? '拉起' : entry.kind === 'ignition_queue' ? '点火' : entry.kind
}

/** 参数：轮询快照与打开卡抽屉回调；返回：后端顺序的队列表；不触发规则计算。 */
export function QueuePanel({ entries, loading, disconnected, sessionExpired, errorText, onOpenCard }: QueuePanelProps) {
  return <section className="mx-4 mt-2 rounded-lg border bg-background p-3" aria-label="排队中">
    <div className="mb-2 flex items-center gap-2">
      <h2 className="text-xs font-semibold">排队中</h2>
      <span className="text-[11px] text-muted-foreground">{entries.length} 条 · 后端顺序</span>
    </div>
    {sessionExpired && <SessionExpiredBanner />}
    {disconnected && !sessionExpired && <DisconnectedBanner compact message={errorText} />}
    {loading && entries.length === 0 && <p className="text-xs text-muted-foreground">正在读取队列…</p>}
    {!loading && entries.length === 0 && !disconnected && !sessionExpired && <p className="text-xs text-muted-foreground">当前没有排队项。</p>}
    {entries.length > 0 && <ol className="space-y-1">{entries.map((entry) => <li key={entry.kind + '/' + entry.id} className="flex flex-wrap items-center gap-2 rounded border px-2 py-1.5 text-xs">
      <span className="font-mono font-semibold">#{entry.position}</span>
      <button type="button" className="font-mono underline" onClick={() => onOpenCard(entry.card)}>{entry.card}</button>
      <span>{entryLabel(entry)}</span>
      <span className="text-muted-foreground">{entry.node || entry.squad}</span>
      {entry.priority && <span className="text-muted-foreground">优先级 {entry.priority}</span>}
      <span className={entry.ready ? 'text-green-600' : 'text-amber-700'}>{entry.ready ? '可运行' : '等待条件'}</span>
    </li>)}</ol>}
    {entries.length === 0 && disconnected && <p role="alert" className="text-xs text-destructive">队列尚未成功读取：{errorText}</p>}
  </section>
}
