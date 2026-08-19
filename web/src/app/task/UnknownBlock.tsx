// UnknownBlock —— 本前端版本不认识的帧类型。
//
// 职责：把未知帧渲染成中性条目，可展开看原始 JSON
// 边界：不猜测语义、不尝试渲染成别的块
//
// 为什么必须有这个组件：契约会演进，而前端比后端晚部署是常态。遇到新类型就
// 白屏或静默吞掉，都是不可接受的失败模式——尤其静默吞掉，会让审阅者以为
// 「模型这段时间什么也没做」。
import { useState } from 'react'
import { ChevronDown, ChevronRight, HelpCircle } from 'lucide-react'

// UnknownBlock 渲染一个未知类型的帧。
//
// 参数：
//   - type: 帧的 type 字段原文
//   - raw: 整帧的 JSON 原文
export function UnknownBlock({ type, raw }: { type: string; raw: string }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="rounded-md border border-dashed px-2.5 py-1.5 text-xs text-muted-foreground">
      <button type="button" onClick={() => setOpen((v) => !v)} className="flex items-center gap-1.5 hover:underline">
        {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
        <HelpCircle className="size-3.5" />
        未知帧类型 <span className="font-mono">{type}</span>（本前端版本尚不认识，已原样保留）
      </button>
      {open && <pre className="mt-1.5 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[11px]">{raw}</pre>}
    </div>
  )
}
