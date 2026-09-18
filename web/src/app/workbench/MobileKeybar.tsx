// MobileKeybar —— 移动端终端的特殊键条（spec 实现决定：特殊键条与 IME 组合）。
//
// 职责：渲染一排常用特殊键（Esc/Tab/Shift+Tab/Ctrl-C/方向键），点击把对应的
//       终端字节序列交给调用方；调用方（TerminalTab）经 term.input() 喂给 xterm，
//       与手敲输入合流到同一条 onData（因此取证日志、WS 未就绪告警全部照常适用）。
// 边界：
//   - 不认识 xterm、不直接写 WS、不管 IME：IME 组合走 xterm 自己的 CompositionHelper
//     （WKWebView/Chromium 通用路径），本组件只补「物理键盘没有的键」
//   - P3 约束：本文件**不得**用 terminal* 前缀（工作台已有 5 个 terminal* 源文件，
//     再造第 6 个会把「终端 I/O 修正」这一单一职责组撑成第二个家族）
//   - onMouseDown 必须 preventDefault：否则点键条会把焦点从 xterm textarea 抢走，
//     点一下丢一次焦点，用户得重新点终端才能继续打字
export interface MobileKeybarProps {
  onKey: (seq: string) => void
}

// KEYBAR_KEYS 是键条的受控键集。seq 是标准终端序列；id 只用于 testid 与 React key。
export const KEYBAR_KEYS: { id: string; label: string; aria: string; seq: string }[] = [
  { id: 'esc', label: 'Esc', aria: 'Esc', seq: '\x1b' },
  { id: 'tab', label: 'Tab', aria: 'Tab', seq: '\t' },
  { id: 'shift-tab', label: '⇧Tab', aria: 'Shift+Tab', seq: '\x1b[Z' },
  { id: 'ctrl-c', label: '^C', aria: 'Ctrl-C', seq: '\x03' },
  { id: 'up', label: '↑', aria: '方向上', seq: '\x1b[A' },
  { id: 'down', label: '↓', aria: '方向下', seq: '\x1b[B' },
  { id: 'left', label: '←', aria: '方向左', seq: '\x1b[D' },
  { id: 'right', label: '→', aria: '方向右', seq: '\x1b[C' },
]

export function MobileKeybar({ onKey }: MobileKeybarProps) {
  return (
    <div
      data-testid="mobile-keybar"
      aria-label="终端特殊键"
      className="flex shrink-0 gap-1 overflow-x-auto border-t bg-background px-1 py-1"
    >
      {KEYBAR_KEYS.map((key) => (
        <button
          key={key.id}
          type="button"
          data-testid={`keybar-${key.id}`}
          aria-label={key.aria}
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => onKey(key.seq)}
          className="shrink-0 rounded border px-2 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          {key.label}
        </button>
      ))}
    </div>
  )
}
