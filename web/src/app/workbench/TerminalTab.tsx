// TerminalTab —— 终端 tab 的壳（spec §2.4）。
//
// 职责：把「终端」这一 tab 种类的外形做到位——能开、能命名、能关、能参与分屏，
// 内容区诚实说明 PTY 后端尚未实现，并给出当前真正可用的替代路径。
//
// 边界：
//   - 不连 PTY、不渲染 xterm。本期的目标是形态校准，不是把终端跑通
//   - **不渲染置灰按钮**（W3b §0 既有纪律）：置灰控件承诺「以后能用」，
//     用户会反复点它。这里干脆不放控件，只放一句说明
//
// 为什么要把一个空壳做出来：三种 tab 里少任何一种，「中央区是一个 tab 系统」
// 这件事就没法在真实页面上验证——而这一期交付的正是那个判断。
import { TerminalSquare } from 'lucide-react'
import type { BaseDir } from './useWorkbench'

export function TerminalTab({ base, seq }: { base: BaseDir; seq: number }) {
  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
        <TerminalSquare className="size-3.5" />
        <span className="font-mono">
          bash · {base.label}
          {seq > 1 && ` (${seq})`}
        </span>
        <span className="ml-auto font-mono">{base.path}</span>
      </div>
      <div className="flex flex-1 items-center justify-center p-8">
        <div className="max-w-md space-y-2 text-center">
          <p className="text-sm">PTY 后端尚未实现。</p>
          <p className="text-xs text-muted-foreground">
            当前查看 executor 现场请用 <code className="font-mono">handoff attach &lt;task&gt;</code>。
          </p>
        </div>
      </div>
    </div>
  )
}
