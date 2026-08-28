// QueuePanel.tsx —— 看板工具条中的服务端排队快照。
// 边界：只呈现 GET /api/queue 的字段并回传卡片打开事件，不重排规则、不重算 ready。
import { useState } from 'react'
import type { ReactElement } from 'react'
import type { QueueEntry } from '../../api/scheduling'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'

export interface QueuePanelProps {
  entries: readonly QueueEntry[]
  open: boolean
  loading: boolean
  disconnected: boolean
  sessionExpired: boolean
  errorText: string
  onToggle: () => void
  onOpenCard: (cardId: string) => void
}

/** 返回每张卡的最早服务端位次；不排序、不把空卡号写入结果。 */
// eslint-disable-next-line react-refresh/only-export-components
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

function entryLocation(entry: QueueEntry): string {
  return entry.node ? `${entry.node} · ${entry.squad}` : entry.squad
}

/** 参数：队列快照与交互回调；返回：可折叠队列工具条；不触发队列规则计算。 */
export function QueuePanel({
  entries,
  open,
  loading,
  disconnected,
  sessionExpired,
  errorText,
  onToggle,
  onOpenCard,
}: QueuePanelProps): ReactElement {
  const [localOpen, setLocalOpen] = useState(false)
  const expanded = open || localOpen
  const orderedEntries = [...entries].sort((left, right) => left.position - right.position)
  const panelId = 'cards-queue-panel'

  const toggle = () => {
    setLocalOpen(open ? false : (current) => !current)
    onToggle()
  }

  return (
    <section className="mx-4 mt-2 rounded-lg border bg-background p-3" aria-label="排队中">
      <button
        type="button"
        className="flex w-full items-center justify-between gap-2 text-left text-xs font-semibold"
        aria-expanded={expanded}
        aria-controls={panelId}
        onClick={toggle}
      >
        <span>⧗ 排队中 {entries.length}</span>
        <span aria-hidden="true">{expanded ? '⌃' : '⌄'}</span>
      </button>
      <div id={panelId} hidden={!expanded}>
        {sessionExpired && <SessionExpiredBanner />}
        {disconnected && !sessionExpired && <DisconnectedBanner compact message={errorText || '网络断开'} />}
        {loading && entries.length === 0 && <p className="mt-2 text-xs text-muted-foreground">正在读取队列…</p>}
        {!loading && entries.length === 0 && !disconnected && !sessionExpired && <p className="mt-2 text-xs text-muted-foreground">当前没有排队项。</p>}
        {orderedEntries.length > 0 && (
          <ol className="mt-2 space-y-1">
            {orderedEntries.map((entry) => (
              <li key={`${entry.kind}/${entry.id}`} className="flex flex-wrap items-center gap-2 rounded border px-2 py-1.5 text-xs">
                <span className="font-mono font-semibold">#{entry.position}</span>
                <button type="button" className="font-mono underline" aria-label={`打开 ${entry.card}`} onClick={() => onOpenCard(entry.card)}>
                  <span>{entry.card}</span>
                </button>
                <span>{entryLabel(entry)}</span>
                <span className="text-muted-foreground">{entryLocation(entry)}</span>
                {entry.target && <span className="text-muted-foreground">目标 · {entry.target}</span>}
                {entry.executor && <span className="text-muted-foreground">执行器 · {entry.executor}</span>}
                {entry.model && <span className="text-muted-foreground">模型 · {entry.model}</span>}
                {entry.priority && <><span className="text-muted-foreground">优先级</span><span>{entry.priority}</span></>}
                <span className={entry.ready ? 'text-green-600' : 'text-amber-700'}>{entry.ready ? '就绪' : '未就绪'}</span>
                <span className="text-muted-foreground">actor ·</span><span>{entry.actor}</span>
                <span className="text-muted-foreground">seq ·</span><span>{entry.seq}</span>
              </li>
            ))}
          </ol>
        )}
        {orderedEntries.length === 0 && disconnected && <p role="alert" className="mt-2 text-xs text-destructive">队列尚未成功读取：{errorText || '网络断开'}</p>}
      </div>
    </section>
  )
}
