// ThinkingBlock —— 时间线上的思维链块。
//
// 职责：默认折叠成一行摘要（🧠 思维链 · N 字），点开才展开全文
// 边界：
//   - 不做任何加工：思维链是模型的原始推理，改写或摘要都会让它失去证据价值
//   - 默认折叠是刻意的：审阅时先看「说了什么 / 做了什么」，思维链是需要时才下钻
//     的深层证据；默认展开会把因果链淹掉
import { useState } from 'react'
import { Brain, ChevronDown, ChevronRight } from 'lucide-react'

// ThinkingBlock 渲染一段思维链。
//
// 参数：text 已按 part 合并好的完整思维链
export function ThinkingBlock({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="rounded-r-md border-l-2 border-violet-400 bg-violet-500/5 px-2.5 py-1.5">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-1.5 text-xs text-violet-600 hover:underline dark:text-violet-400"
      >
        {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        <Brain className="size-3.5" />
        思维链 · {text.length} 字
      </button>
      {open && (
        <p className="mt-1.5 whitespace-pre-wrap break-words text-xs leading-relaxed text-muted-foreground">
          {text}
        </p>
      )}
    </div>
  )
}
