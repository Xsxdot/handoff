// UserInstructionBlock —— 审核者指令的右对齐气泡（对话感的关键件）。
//
// 职责：send 回合起点展示 continue 指令 / reply 应答的原文。
// 边界：数据来自 turn_start 帧的 instructions（Task 1）；旧帧无此字段时
// 本组件不渲染（由 ConversationStream 判空决定），不在这里造假数据。
import { formatFull } from '../lib/format'

// UserInstructionBlock 渲染一条审核者消息。text 为指令原文，ts 为回合时刻。
export function UserInstructionBlock({ text, ts }: { text: string; ts: string }) {
  return (
    <div className="my-3 ml-auto w-fit max-w-[78%] rounded-xl rounded-br-sm bg-muted px-3 py-2 text-sm leading-relaxed">
      <div className="mb-0.5 text-right text-[11px] text-muted-foreground">审核者 · {formatFull(ts)}</div>
      <div className="whitespace-pre-wrap break-words">{text}</div>
    </div>
  )
}
