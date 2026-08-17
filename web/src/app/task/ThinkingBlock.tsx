// ThinkingBlock —— 思维链的折叠行（元数据行语言）。
//
// 职责：默认一行「思维链 · N 字」，点开是左边线引文块。
// 边界：思维链绝不混入正文（W4a 纪律）；不做 markdown 渲染，原文展示。
// 展开后的引文块行距与 TextBlock 同档（1.5）：同一条时间线上两种段内行距会显得没对齐。
import { useState } from 'react'
import { cn } from '@/lib/utils'

// ThinkingBlock 渲染一段已合并的思维链增量。text 为完整思维链文本。
export function ThinkingBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="my-1 text-xs text-muted-foreground">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 py-0.5 hover:text-foreground"
      >
        <span className={cn('w-3.5 shrink-0 text-center transition-transform', open && 'rotate-90')}>▸</span>
        思维链 · {[...text].length} 字
      </button>
      {open && (
        <div className="ml-[7px] whitespace-pre-wrap break-words border-l-2 border-border py-1 pl-3 leading-[1.5]">
          {text}
        </div>
      )}
    </div>
  )
}
