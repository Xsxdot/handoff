// MobileKeybar —— 移动端终端的特殊键条（spec 实现决定：特殊键条与 IME 组合）。
//
// 职责：渲染一排常用特殊键（B369.10 对齐原型 12 键暗色：Esc/Ctrl/Ctrl-C/Tab/
//       ⇧Tab/四方向/|/~/⌫），点击把对应的终端字节序列交给调用方；调用方
//       （TerminalTab）经 term.input() 喂给 xterm，与手敲输入合流到同一条 onData
//      （因此取证日志、WS 未就绪告警全部照常适用）。
// 边界：
//   - 不认识 xterm、不直接写 WS、不管 IME：IME 组合走 xterm 自己的 CompositionHelper
//     （WKWebView/Chromium 通用路径），本组件只补「物理键盘没有的键」
//   - ctrl 键位是粘滞组合开关（B369.10 岔口 3）：seq 为空即不直发序列，点击只
//     上抛 onCtrlToggle 由 TerminalTab 翻转 armed；armed 期间方向/字母的组合合成
//     归 TerminalTab 的 onKey 与 terminalInput 转发——本组件保持不认识 xterm
//   - P3 约束：本文件**不得**用 terminal* 前缀（工作台已有 5 个 terminal* 源文件，
//     再造第 6 个会把「终端 I/O 修正」这一单一职责组撑成第二个家族）
//   - onMouseDown 必须 preventDefault：否则点键条会把焦点从 xterm textarea 抢走，
//     点一下丢一次焦点，用户得重新点终端才能继续打字
//   - 暗色配色用字面量 hex（B369.10 岔口 7，原型 mobile-task.html 逐值转写）：
//     键条永远坐在 bg-[#0b0b0c] 的 pty-host 之下，是深色 chrome 而非主题化表面，
//     不引设计 token
//   - 12 键约 546px > 390 视口：容器 overflow-x-auto 横滑可达（原型 .keybar 同
//     形态；B369.9「无横滑」口径随本卡翻案，双依据记 plan §3.3 与卡台账）
export interface MobileKeybarProps {
  onKey: (seq: string) => void
  // 粘滞 Ctrl（B369.10）：ctrlArmed 控制 ctrl 键位的 on 态高亮与 aria-pressed，
  // onCtrlToggle 响应 ctrl 键位点击。可缺席——既有调用与测试不传即纯直发形态。
  ctrlArmed?: boolean
  onCtrlToggle?: () => void
}

// KEYBAR_KEYS 是键条的受控键集。seq 是标准终端序列；id 只用于 testid 与 React key。
// 既有 8 键的 id/label/seq 逐字节冻结（B369.9 验收口径底线）；B369.10 扩至原型
// 12 键，ctrl 的 seq 留空表示「点击不发序列、走 onCtrlToggle」。
export const KEYBAR_KEYS: { id: string; label: string; aria: string; seq: string }[] = [
  { id: 'esc', label: 'Esc', aria: 'Esc', seq: '\x1b' },
  { id: 'ctrl', label: 'Ctrl', aria: 'Ctrl 粘滞', seq: '' },
  { id: 'ctrl-c', label: '^C', aria: 'Ctrl-C', seq: '\x03' },
  { id: 'tab', label: 'Tab', aria: 'Tab', seq: '\t' },
  { id: 'shift-tab', label: '⇧Tab', aria: 'Shift+Tab', seq: '\x1b[Z' },
  { id: 'up', label: '↑', aria: '方向上', seq: '\x1b[A' },
  { id: 'down', label: '↓', aria: '方向下', seq: '\x1b[B' },
  { id: 'left', label: '←', aria: '方向左', seq: '\x1b[D' },
  { id: 'right', label: '→', aria: '方向右', seq: '\x1b[C' },
  { id: 'pipe', label: '|', aria: '管道符', seq: '\x7c' },
  { id: 'tilde', label: '~', aria: '波浪号', seq: '\x7e' },
  { id: 'backspace', label: '⌫', aria: '退格', seq: '\x7f' },
]

export function MobileKeybar({ onKey, ctrlArmed = false, onCtrlToggle }: MobileKeybarProps) {
  return (
    <div
      data-testid="mobile-keybar"
      aria-label="终端特殊键"
      className="flex shrink-0 gap-1 overflow-x-auto border-t border-[#2c2c2e] bg-[#1c1c1e] px-1 py-1"
    >
      {KEYBAR_KEYS.map((key) => {
        const isCtrl = key.seq === ''
        const armed = isCtrl && ctrlArmed
        return (
          <button
            key={key.id}
            type="button"
            data-testid={`keybar-${key.id}`}
            aria-label={key.aria}
            aria-pressed={isCtrl ? ctrlArmed : undefined}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => (isCtrl ? onCtrlToggle?.() : onKey(key.seq))}
            className={`shrink-0 min-w-10 rounded border px-2 py-1 text-xs ${
              armed
                ? 'border-[#636366] bg-[#636366] text-[#e5e5e5]'
                : 'border-[#3a3a3c] bg-[#3a3a3c] text-[#e5e5e5]'
            }`}
          >
            {key.label}
          </button>
        )
      })}
    </div>
  )
}
