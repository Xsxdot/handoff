// DeliverySummaryCard —— 模型报工 trailer 的交付摘要卡。
//
// 职责：把 extractDelivery 命中的字段（分支/commit/摘要）渲染成结构化卡片。
// 边界：只渲染命中的字段；提取与判定在 delivery.ts，本组件不碰原始文本。
import { shortCommit } from '../lib/format'
import type { Delivery } from './delivery'

// DeliverySummaryCard 渲染一张交付摘要卡。delivery 至少含一个字段（调用方保证）。
export function DeliverySummaryCard({ delivery }: { delivery: Delivery }) {
  return (
    <div className="my-3 rounded-lg border bg-sidebar p-3 text-sm">
      <div className="mb-1.5 font-semibold">✅ 交付摘要</div>
      <dl className="grid grid-cols-[auto_1fr] gap-x-3 gap-y-0.5 text-[13px]">
        {delivery.branch && (
          <>
            <dt className="text-muted-foreground">分支</dt>
            <dd className="break-all font-mono text-xs">{delivery.branch}</dd>
          </>
        )}
        {delivery.commit && (
          <>
            <dt className="text-muted-foreground">commit</dt>
            <dd className="font-mono text-xs">{shortCommit(delivery.commit)}</dd>
          </>
        )}
        {delivery.summary && (
          <>
            <dt className="text-muted-foreground">摘要</dt>
            <dd className="break-words">{delivery.summary}</dd>
          </>
        )}
      </dl>
    </div>
  )
}
