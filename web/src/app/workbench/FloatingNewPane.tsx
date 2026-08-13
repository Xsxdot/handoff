// FloatingNewPane —— 右下角常驻的悬浮按钮（spec §2.5）。
//
// 职责：开一个**不挂在任何项目上**的 tab。与中央 `+` 的唯一区别是基准目录是
// 用户 home，不是当前工作树。
//
// 边界（安全，不得放宽）：
//   - **本期只有「新终端」一项**。以 $HOME 为基准浏览文件要求 agentd 的文件接口
//     接受 $HOME 作为根，而 ~/.handoff/config.yaml 里存着 agentd 主令牌；控制台
//     会话是刻意做得比主令牌弱的凭据（一次性 ticket 换取、可吊销、按设备记录），
//     能读 $HOME 即弱凭据当场提权成强凭据。$HOME 下还有 ~/.ssh/ 与各种 CLI 的
//     凭据文件，问题同理（spec §2.6）
//   - 因此**不要**把 BlankTab 的三项直接搬过来。home 基准的文件浏览需要单独设计
//     （排除清单 / 显式授权目录 / 用户在设置里逐个添加可浏览根），那是独立一轮的事
//
// 形态参照 Orca 的悬浮面板：收起时是一个圆钮，展开是一张小面板。
import { useState } from 'react'
import { Plus, TerminalSquare } from 'lucide-react'

export function FloatingNewPane({ onNewTerminal }: { onNewTerminal: () => void }) {
  const [open, setOpen] = useState(false)

  if (!open) {
    return (
      <button
        type="button"
        aria-label="新建（以 home 为基准）"
        onClick={() => setOpen(true)}
        className="fixed bottom-5 right-5 z-40 flex size-11 items-center justify-center rounded-full bg-[#10151b] text-white shadow-lg hover:opacity-90"
      >
        <Plus className="size-5" />
      </button>
    )
  }

  return (
    <div className="fixed bottom-5 right-5 z-40 w-64 rounded-lg border bg-background p-2 shadow-xl">
      <div className="flex items-center px-1 pb-1">
        <span className="text-xs text-muted-foreground">基准 home（不挂在任何项目上）</span>
        <button
          type="button"
          aria-label="收起"
          onClick={() => setOpen(false)}
          className="ml-auto rounded p-0.5 text-muted-foreground hover:bg-accent"
        >
          <Plus className="size-4 rotate-45" />
        </button>
      </div>
      <button
        type="button"
        onClick={() => {
          setOpen(false)
          onNewTerminal()
        }}
        className="flex w-full items-center gap-3 rounded-md px-2 py-2 text-left text-sm hover:bg-accent"
      >
        <TerminalSquare className="size-4 shrink-0 text-muted-foreground" />
        <span className="flex-1">新终端</span>
        <span className="font-mono text-[11px] text-muted-foreground">⌘T</span>
      </button>
    </div>
  )
}
