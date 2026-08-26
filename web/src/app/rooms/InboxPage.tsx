// 收件箱页（spec §3.3 答复直达 + §7 注意力面第二件：待回复收件箱）。
//
// 职责：
//   - 轮询 GET /api/inbox，三源分区渲染（decision / ticket / mention）
//   - decision 条目渲染候选项按钮，点击直调既有 answerDecision（不新造第二答复通道，
//     契约 §3.3），答复后显示「答复已落账，等待协调者唤醒」并轮询刷新（open 裁决消失）
//
// 边界：
//   - 协调者不在场检测本期无数据源（wire 无 live 字段），答复后一律明示等待文案
//   - 决策 payload 是 Decision 对象（C6 投影），解析失败渲染降级行不炸
import { useState } from 'react'
import { answerDecision } from '../../api/ledger'
import type { Decision } from '../../api/ledger'
import { fetchInbox } from '../../api/rooms'
import type { InboxItem } from '../../api/rooms'
import { usePoll } from '../data/usePoll'
import { errorMessage } from '../lib/format'
import { COLLAB_POLL_MS } from './constants'

function DecisionCard({ item, onAnswered }: { item: InboxItem; onAnswered: (id: number) => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const decision = item.payload as Decision | undefined
  const decisionId = decision?.id ?? Number(item.ref_id)
  if (!decision || Number.isNaN(decisionId)) {
    return (
      <div className="rounded-md border px-3 py-2 text-sm">
        <p className="font-medium">{item.title}</p>
        <p className="text-xs text-muted-foreground">（裁决详情缺失）</p>
      </div>
    )
  }
  const answer = async (option: string) => {
    if (busy) return
    setBusy(true)
    setError('')
    try {
      await answerDecision(decisionId, option)
      onAnswered(decisionId)
    } catch (err) {
      console.debug('[inbox] 答复失败', decisionId, errorMessage(err))
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="rounded-md border border-amber-200 bg-amber-50 px-3 py-2 text-sm">
      <p className="font-medium">{decision.body}</p>
      <div className="mt-1.5 flex flex-wrap gap-1.5">
        {(decision.options ?? []).map((option) => (
          <button
            key={option}
            type="button"
            onClick={() => void answer(option)}
            disabled={busy}
            className="rounded-md border bg-background px-2.5 py-1 text-xs"
          >
            {option}
          </button>
        ))}
      </div>
      {error !== '' && <p role="alert" className="mt-1 text-xs text-destructive">{error}</p>}
    </div>
  )
}

export function InboxPage() {
  const poll = usePoll(fetchInbox, COLLAB_POLL_MS)
  const items = poll.data ?? []
  const [answered, setAnswered] = useState<number | null>(null)

  const byOrigin = (origin: string) => items.filter((item) => item.origin === origin)

  return (
    <main className="flex h-full min-h-0 w-full flex-col bg-background">
      <header className="flex items-center gap-2 border-b px-4 py-2.5">
        <span className="text-sm font-semibold">待回复收件箱</span>
        {poll.disconnected && <span className="text-[11px] text-amber-700">断线：{poll.errorText}</span>}
      </header>
      {answered !== null && (
        <p role="status" className="border-b bg-green-50 px-4 py-1.5 text-xs text-green-800">
          答复已落账，等待协调者唤醒。
        </p>
      )}
      <div className="min-h-0 flex-1 space-y-3 overflow-y-auto p-3">
        <section aria-label="简报与裁决">
          <h2 className="mb-1.5 text-xs font-semibold text-muted-foreground">需要你 · 简报</h2>
          {byOrigin('decision').map((item) => (
            <DecisionCard key={item.ref_id} item={item} onAnswered={setAnswered} />
          ))}
          {byOrigin('decision').length === 0 && (
            <p className="text-sm text-muted-foreground">（无待答复简报）</p>
          )}
        </section>
        <section aria-label="工单">
          <h2 className="mb-1.5 text-xs font-semibold text-muted-foreground">兜底上浮工单</h2>
          {byOrigin('ticket').map((item) => (
            <div key={item.ref_id} className="rounded-md border px-3 py-2 text-sm">
              <p className="font-medium">{item.title}</p>
              <p className="font-mono text-[11px] text-muted-foreground">{item.ref_id}</p>
            </div>
          ))}
          {byOrigin('ticket').length === 0 && (
            <p className="text-sm text-muted-foreground">（无上浮工单）</p>
          )}
        </section>
        <section aria-label="提及">
          <h2 className="mb-1.5 text-xs font-semibold text-muted-foreground">@你</h2>
          {byOrigin('mention').map((item) => (
            <div key={item.ref_id} className="rounded-md border px-3 py-2 text-sm">
              <p className="font-medium">{item.title}</p>
              {item.card_id !== undefined && item.card_id !== '' && (
                <p className="font-mono text-[11px] text-muted-foreground">{item.card_id}</p>
              )}
            </div>
          ))}
          {byOrigin('mention').length === 0 && (
            <p className="text-sm text-muted-foreground">（无提及）</p>
          )}
        </section>
      </div>
    </main>
  )
}