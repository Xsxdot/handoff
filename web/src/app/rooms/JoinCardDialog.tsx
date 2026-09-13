// JoinCardDialog —— 拉卡进群的极简对话框（卡号手输；进群 ≠ 配人，配人仍走 B307）。
import { useState } from 'react'

export function JoinCardDialog({ open, busy, error, onCancel, onJoin }: {
  open: boolean; busy: boolean; error: string
  onCancel: () => void
  onJoin: (cardId: string) => void
}) {
  const [cardId, setCardId] = useState('')
  if (!open) return null
  return (
    <div role="dialog" aria-label="拉卡进群" className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="w-72 rounded-xl border bg-background p-4 shadow-xl">
        <h3 className="text-sm font-semibold">拉卡进群</h3>
        <p className="mt-1 text-xs text-muted-foreground">进群只建讨论面；配不配人仍在卡上按三颗按钮。</p>
        <input aria-label="卡号" value={cardId} onChange={(event) => setCardId(event.target.value)} placeholder="B233.14"
          className="mt-3 w-full rounded-md border px-2 py-1 font-mono text-sm" />
        {error !== '' && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={() => { setCardId(''); onCancel() }} className="rounded-md border px-2.5 py-1 text-xs">取消</button>
          <button type="button" aria-label="确认拉卡" disabled={cardId.trim() === '' || busy}
            onClick={() => onJoin(cardId.trim())}
            className="rounded-md bg-slate-900 px-2.5 py-1 text-xs text-white disabled:opacity-50">确认拉卡</button>
        </div>
      </div>
    </div>
  )
}
