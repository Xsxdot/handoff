// meta.tsx —— 会话流「元数据行」的统一容器。
//
// 职责：思维链/工具行/事件行共用的一套视觉语言（左对齐、12px、muted、同一左轨、
// 行首小符号），保证非正文元素只有一种形态——主次靠这个约定成立（spec §2.2）。
// 边界：只管容器样式，不认识任何具体块类型；tone=warn 只换文字颜色，不加底色。
import type { ReactNode } from 'react'
import { cn } from '@/lib/utils'

// MetaRow 渲染一行元数据。glyph 是行首符号位（宽度固定对齐左轨）。
export function MetaRow({ glyph, tone = 'info', children }: {
  glyph: ReactNode
  tone?: 'info' | 'warn'
  children: ReactNode
}) {
  return (
    <div
      className={cn(
        'my-1 flex items-center gap-2 py-0.5 text-xs',
        tone === 'warn' ? 'text-amber-600 dark:text-amber-500' : 'text-muted-foreground',
      )}
    >
      <span className="w-3.5 shrink-0 text-center">{glyph}</span>
      {children}
    </div>
  )
}
