// UserInstructionBlock —— 审核者指令的右对齐气泡（对话感的关键件）。
//
// 职责：
//   - send 回合起点展示 continue 指令 / reply 应答的原文
//   - 会话流开头展示**派发指令**（label='派发指令'，正文来自 GET /api/tasks/{id}/plan）
//
// 边界：数据来自 turn_start 帧的 instructions（Task 1）；旧帧无此字段时
// 本组件不渲染（由 ConversationStream 判空决定），不在这里造假数据。
// 段内行距 1.5，与 TextBlock / ThinkingBlock 同档；气泡之间的间距靠 my-3，不受影响。
//
// 为什么长文要折起来：派发指令常常是一整份 plan（几十 KB）。整份铺开会把
// 「模型接下来做了什么」推到几屏之外——而那才是打开这个 tab 的目的。折起来的
// 是**长度**不是内容，一次点击就能看全文。
import { useState } from 'react'
import { formatFull } from '../lib/format'
import { CodeText } from './codeText'
import { cn } from '@/lib/utils'

// CLAMP_CHARS 是「长到需要折叠」的判据。
//
// 600 字符≈一屏内读得完的一段话：continue 指令通常两三行，不会被折；
// 派发的 plan 一定超，必折。
const CLAMP_CHARS = 600

export interface UserInstructionBlockProps {
  text: string
  ts: string
  // label 是气泡头部的身份词；缺省「审核者」。派发指令传「派发指令」，
  // 因为那一条不是回合里的应答，而是这个任务的起点
  label?: string
  // extra 是身份词后的补充小字（如 plan 文件名），可缺省
  extra?: string
}

export function UserInstructionBlock({ text, ts, label = '审核者', extra }: UserInstructionBlockProps) {
  const [expanded, setExpanded] = useState(false)
  const long = text.length > CLAMP_CHARS

  return (
    <div className="my-3 ml-auto w-fit max-w-[78%] rounded-xl rounded-br-sm bg-muted px-3 py-2 text-sm leading-[1.5]">
      <div className="mb-0.5 text-right text-[11px] text-muted-foreground">
        {label}
        {extra ? ` · ${extra}` : ''} · {formatFull(ts)}
      </div>
      {/* 折叠用 max-height 而不是 line-clamp：正文里可能有代码块（CodeText 渲染
          成 <pre>），line-clamp 只对纯文本行有效，遇到块级子元素会失灵 */}
      <div
        className={cn(
          'whitespace-pre-wrap break-words',
          long && !expanded && 'max-h-52 overflow-hidden',
        )}
      >
        <CodeText text={text} />
      </div>
      {long && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="mt-1 text-[11px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
        >
          {expanded ? '收起' : `展开全文（${text.length} 字）`}
        </button>
      )}
    </div>
  )
}
