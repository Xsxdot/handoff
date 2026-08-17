// UsageChip —— 页头 ctx 小表 + 两口径账目弹出（usage/cumulative 展示回归 TUI）。
//
// 职责：一眼读数（迷你条 + "ctx 41.2k / 200k"），点开完整账目。
// 边界：
//   - executor 没报 context_window 时只显绝对值——前端**不猜分母**（现有纪律）
//   - usage 与 cumulative 都缺席时整体不渲染（返回 null）：没有账目不画空表
import { useState } from 'react'
import type { Cumulative, Usage } from '../../api/types'
import { formatCost, formatTokens } from '../lib/format'

// UsageChip 渲染 ctx 读数。usage=当前占用，cumulative=累计消耗，均可缺席。
export function UsageChip({ usage, cumulative }: { usage?: Usage; cumulative?: Cumulative }) {
  const [open, setOpen] = useState(false)
  if (!usage && !cumulative) return null

  const pct = usage?.context_window
    ? Math.min(100, Math.round((usage.context_tokens / usage.context_window) * 100))
    : null

  return (
    <span className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="inline-flex items-center gap-1.5 hover:text-foreground"
      >
        {pct !== null && (
          <span className="h-[5px] w-14 overflow-hidden rounded-full bg-muted">
            <span className="block h-full rounded-full bg-green-600" style={{ width: `${pct}%` }} />
          </span>
        )}
        {usage && (
          <span>
            ctx {formatTokens(usage.context_tokens)}
            {usage.context_window ? ` / ${formatTokens(usage.context_window)}` : ''}
          </span>
        )}
        {!usage && <span>累计 {formatTokens(cumulative!.total_tokens)}</span>}
      </button>
      {open && (
        <div className="absolute left-0 top-6 z-10 w-64 rounded-lg border bg-background p-3 text-xs shadow-lg">
          {usage && (
            <>
              <div className="mb-1 font-semibold">当前占用</div>
              <dl className="mb-2 grid grid-cols-[auto_1fr] gap-x-3">
                <dt className="text-muted-foreground">context</dt>
                <dd className="text-right font-mono">
                  {usage.context_tokens.toLocaleString()}
                  {usage.context_window ? ` / ${usage.context_window.toLocaleString()}（${pct}%）` : ''}
                </dd>
              </dl>
            </>
          )}
          {cumulative && (
            <>
              <div className="mb-1 font-semibold">累计消耗</div>
              <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5">
                <dt className="text-muted-foreground">输入</dt>
                <dd className="text-right font-mono">{formatTokens(cumulative.input_tokens)}</dd>
                <dt className="text-muted-foreground">缓存命中</dt>
                <dd className="text-right font-mono">{formatTokens(cumulative.cached_tokens)}</dd>
                <dt className="text-muted-foreground">输出</dt>
                <dd className="text-right font-mono">{formatTokens(cumulative.output_tokens)}</dd>
                <dt className="text-muted-foreground">合计</dt>
                <dd className="text-right font-mono">{formatTokens(cumulative.total_tokens)}</dd>
                {cumulative.cost && (
                  <>
                    <dt className="text-muted-foreground">花费</dt>
                    <dd className="text-right font-mono">
                      {formatCost(cumulative.cost).text}
                      <span className="ml-1 font-sans text-muted-foreground">{formatCost(cumulative.cost).hint}</span>
                    </dd>
                  </>
                )}
              </dl>
            </>
          )}
        </div>
      )}
    </span>
  )
}
