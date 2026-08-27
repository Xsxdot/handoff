// terminalWheel —— alt-screen 鼠标追踪下，把滚轮像素折成多格 SGR 报告。
//
// 职责：按指针所在格子生成 `ESC[<64/65;col;rowM` 序列。xterm 开了鼠标追踪时
//       每个 DOM wheel 只发一格，触控板一甩的像素全被丢掉，TUI 就要滑好多轮
//       才翻一页。原生终端会按滑过的行数连发同一坐标的滚轮报告。
// 边界：
//   - 不写 PTY、不读 xterm。调用方负责上送和拦截 xterm 那一格重复报告
//   - 不造假坐标。col/row 必须是指针格子；曾经用屏幕 35% 当坐标，TUI
//     把整屏（含输入框）当拖动划走，resize 救不回来
//   - 不改成方向键。方向键在 OpenCode 里是输入历史

const wheelTickCap = 8

// WheelEvent.deltaMode：0 像素 / 1 行 / 2 页。触控板走像素，鼠标滚轮常走行。
const DOM_DELTA_LINE = 1
const DOM_DELTA_PAGE = 2

export interface WheelDelta {
  deltaY: number
  deltaMode?: number
}

// wheelPixelDeltaY 把一次滚轮的 deltaY 统一成像素。
//
// 参数：ev 至少有 deltaY；cellHeight 一行像素；rows 一页行数（页模式用）。
// 返回：与触控板像素 delta 同一量纲的有符号像素，上负下正。
export function wheelPixelDeltaY(ev: WheelDelta, cellHeight: number, rows: number): number {
  const px = cellHeight > 0 ? cellHeight : 16
  const mode = ev.deltaMode ?? 0
  if (mode === DOM_DELTA_LINE) return ev.deltaY * px
  if (mode === DOM_DELTA_PAGE) return ev.deltaY * px * Math.max(1, rows)
  return ev.deltaY
}

export interface PointerRect {
  left: number
  top: number
  width: number
  height: number
}

// pointerCell 把指针像素换成 1-based 格子。
//
// 参数：clientX/Y 相对视口；rect 是终端画面的盒子；cols/rows 是当前 PTY 尺寸。
// 返回：SGR 用的 col/row，至少是 1，不超过 cols/rows；量不出时 {0,0}。
export function pointerCell(
  clientX: number,
  clientY: number,
  rect: PointerRect,
  cols: number,
  rows: number,
): { col: number; row: number } {
  if (cols <= 0 || rows <= 0 || rect.width <= 0 || rect.height <= 0) {
    return { col: 0, row: 0 }
  }
  const cellW = rect.width / cols
  const cellH = rect.height / rows
  const col = Math.min(cols, Math.max(1, Math.floor((clientX - rect.left) / cellW) + 1))
  const row = Math.min(rows, Math.max(1, Math.floor((clientY - rect.top) / cellH) + 1))
  return { col, row }
}

// altBufferWheelSgr 把一次滚轮换成若干格同坐标 SGR 报告。
//
// 参数：
//   - deltaY：WheelEvent.deltaY（上负下正）
//   - cellHeight：一行像素高度，<=0 时按 16 兜底
//   - remainder：像素级小数行残留，函数会就地改它
//   - col/row：1-based 指针格子
// 返回：要写入 PTY 的序列；凑不满一行或格子非法时返回空串。
export function altBufferWheelSgr(
  deltaY: number,
  cellHeight: number,
  remainder: { value: number },
  col: number,
  row: number,
): string {
  if (deltaY === 0) return ''
  if (!Number.isFinite(col) || !Number.isFinite(row) || col < 1 || row < 1) return ''
  const px = cellHeight > 0 ? cellHeight : 16
  remainder.value += deltaY / px
  const lines = Math.trunc(remainder.value)
  remainder.value -= lines
  if (lines === 0) return ''
  const n = Math.min(Math.abs(lines), wheelTickCap)
  const btn = lines < 0 ? 64 : 65
  const one = `\x1b[<${btn};${Math.floor(col)};${Math.floor(row)}M`
  return one.repeat(n)
}
