// JoinCardDialog —— 拉卡进群的卡选择器（B358.8 #8：手输卡号改列表搜索 + 多选）。
// 数据源：打开时拉既有全量卡列表（fetchCards，零新增端点）；已在本会话内的卡
// 禁选并标注「已在会话」；确认回调 onJoin(cardIds[])——逐张发送由 SessionTab 承接
// （端点仍是单卡 POST …/cards）。进群 ≠ 配人，配人仍走 B307。
import { useEffect, useMemo, useState } from 'react'
import { fetchCards } from '../../api/ledger'
import type { CardView } from '../../api/ledger'
import { errorMessage } from '../lib/format'

export function JoinCardDialog({ open, busy, error, existingCardIds = [], onCancel, onJoin }: {
  open: boolean
  busy: boolean
  error: string
  existingCardIds?: string[]
  onCancel: () => void
  onJoin: (cardIds: string[]) => void
}) {
  const [cards, setCards] = useState<CardView[] | null>(null)
  const [loadError, setLoadError] = useState('')
  const [query, setQuery] = useState('')
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set())

  // 每次打开重置选择并重新拉卡列表：会话卡与卡板可能在两次打开之间变化。
  useEffect(() => {
    if (!open) return
    let alive = true
    setCards(null)
    setLoadError('')
    setQuery('')
    setSelected(new Set())
    fetchCards('')
      .then((response) => { if (alive) setCards(response.cards) })
      .catch((err: unknown) => { if (alive) setLoadError(errorMessage(err)) })
    return () => { alive = false }
  }, [open])

  const existing = useMemo(() => new Set(existingCardIds), [existingCardIds])
  // 搜索按标题/卡号大小写不敏感过滤；空串 = 不过滤。
  const filtered = useMemo(() => {
    if (cards === null) return []
    const q = query.trim().toLowerCase()
    if (q === '') return cards
    return cards.filter((card) => card.id.toLowerCase().includes(q) || card.title.toLowerCase().includes(q))
  }, [cards, query])

  if (!open) return null

  const toggle = (cardId: string) => {
    setSelected((prev) => {
      const next = new Set(prev)
      if (next.has(cardId)) next.delete(cardId)
      else next.add(cardId)
      return next
    })
  }

  return (
    <div role="dialog" aria-label="拉卡进群" className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="flex max-h-[80vh] w-96 flex-col rounded-xl border bg-background p-4 shadow-xl">
        <h3 className="text-sm font-semibold">拉卡进群</h3>
        <p className="mt-1 text-xs text-muted-foreground">进群只建讨论面；配不配人仍在卡上按三颗按钮。</p>
        <input aria-label="搜索卡" value={query} onChange={(event) => setQuery(event.target.value)}
          placeholder="按标题或卡号筛选" className="mt-3 w-full rounded-md border px-2 py-1 text-sm" />
        {loadError !== '' && <p role="alert" className="mt-2 text-xs text-destructive">卡列表读取失败：{loadError}</p>}
        <div className="mt-2 min-h-0 flex-1 overflow-y-auto" data-testid="join-card-list">
          {cards === null && loadError === '' && <p className="py-4 text-center text-xs text-muted-foreground">正在读取卡列表…</p>}
          {cards !== null && filtered.length === 0 && <p className="py-4 text-center text-xs text-muted-foreground">（没有匹配的卡）</p>}
          {filtered.map((card) => {
            const inSession = existing.has(card.id)
            return (
              <label key={card.id} className={`flex items-center gap-2 rounded-md px-2 py-1.5 text-sm ${inSession ? 'opacity-50' : 'cursor-pointer hover:bg-accent/50'}`}>
                <input type="checkbox" aria-label={`选择 ${card.id}`} disabled={inSession || busy}
                  checked={selected.has(card.id)} onChange={() => toggle(card.id)} />
                <b className="shrink-0 font-mono">{card.id}</b>
                <span className="min-w-0 flex-1 truncate">{card.title}</span>
                <span className={`shrink-0 text-xs ${inSession ? 'text-amber-700' : 'text-muted-foreground'}`}>{inSession ? '已在会话' : card.status}</span>
              </label>
            )
          })}
        </div>
        {error !== '' && <p role="alert" className="mt-2 whitespace-pre-wrap text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={onCancel} className="rounded-md border px-2.5 py-1 text-xs">取消</button>
          <button type="button" disabled={selected.size === 0 || busy}
            onClick={() => onJoin([...selected])}
            className="rounded-md bg-slate-900 px-2.5 py-1 text-xs text-white disabled:opacity-50">
            {busy ? '提交中…' : `确认拉卡（${selected.size}）`}
          </button>
        </div>
      </div>
    </div>
  )
}
