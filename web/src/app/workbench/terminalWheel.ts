// terminalWheel —— alt-screen 鼠标追踪下，把滚轮像素折成多格鼠标报告。
//
// 职责：按指针格子（或像素编码下的像素）生成与 xterm CoreMouseService 同形的
//       滚轮序列。xterm 每个 DOM wheel 只发一格；这里补格数、横滑、修饰位。
// 边界：
//   - 不写 PTY、不读 xterm 实例（encoding 由调用方传入）
//   - 不造假坐标。col/row 必须是指针格子
//   - 不改成方向键
//   - 不在这里决定「划词放行」——那是 wheelForcesSelection，调用方据此根本不调用本函数

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

// wheelForcesSelection 对齐 xterm SelectionService.shouldForceSelection：
// Mac + macOptionClickForcesSelection 时 Option 强制划词；其它平台是 Shift。
export function wheelForcesSelection(
  ev: { altKey: boolean; shiftKey: boolean },
  isMac: boolean,
): boolean {
  return isMac ? ev.altKey : ev.shiftKey
}

// mouseEncodingOf 读 xterm 当前鼠标编码。公开 IModes 没有 encoding 字段，
// 写死 SGR 会在应用要了 SGR_PIXELS（1016）时把格子坐标当像素发出去再偏一次，
// 所以这里读私有 _core.coreMouseService.activeEncoding；读不到退 SGR。
export function mouseEncodingOf(
  term: { _core?: { coreMouseService?: { activeEncoding?: string } } },
): 'SGR' | 'SGR_PIXELS' | 'DEFAULT' {
  const enc = term._core?.coreMouseService?.activeEncoding
  if (enc === 'SGR_PIXELS' || enc === 'SGR' || enc === 'DEFAULT') return enc
  return 'SGR'
}

function eventCode(wheelBtn: 64 | 65 | 66 | 67, shift: boolean, alt: boolean, ctrl: boolean): number {
  return wheelBtn | (shift ? 4 : 0) | (alt ? 8 : 0) | (ctrl ? 16 : 0)
}

function encodeOne(
  encoding: 'SGR' | 'SGR_PIXELS' | 'DEFAULT',
  code: number,
  col: number,
  row: number,
  pixelX: number,
  pixelY: number,
): string {
  if (encoding === 'SGR_PIXELS') return `\x1b[<${code};${pixelX};${pixelY}M`
  if (encoding === 'DEFAULT') {
    const pb = code + 32
    const px = col + 32
    const py = row + 32
    if (pb > 255 || px > 255 || py > 255) return ''
    return `\x1b[M${String.fromCharCode(pb, px, py)}`
  }
  return `\x1b[<${code};${col};${row}M`
}

function ticksFromDelta(delta: number, cell: number, rem: { value: number }): number {
  if (delta === 0) return 0
  const px = cell > 0 ? cell : 16
  rem.value += delta / px
  const lines = Math.trunc(rem.value)
  rem.value -= lines
  if (lines === 0) return 0
  return Math.min(Math.abs(lines), wheelTickCap) * Math.sign(lines)
}

// altBufferWheelReports 把一次滚轮换成若干格同坐标鼠标报告。
//
// 参数：delta 与格子尺寸按轴分别累计；remainder 就地改；col/row 是指针格子；
//       pixelX/Y 只在 SGR_PIXELS 下使用；shift/alt/ctrl 并进按钮码。
// 返回：要写入 PTY 的序列；凑不满一格或格子非法时返回空串。
export function altBufferWheelReports(p: {
  deltaX: number; deltaY: number; cellWidth: number; cellHeight: number
  remainder: { x: number; y: number }
  col: number; row: number; pixelX: number; pixelY: number
  shift: boolean; alt: boolean; ctrl: boolean
  encoding: 'SGR' | 'SGR_PIXELS' | 'DEFAULT'
}): string {
  if (!Number.isFinite(p.col) || !Number.isFinite(p.row) || p.col < 1 || p.row < 1) return ''
  const yHold = { value: p.remainder.y }
  const xHold = { value: p.remainder.x }
  const yTicks = ticksFromDelta(p.deltaY, p.cellHeight, yHold)
  const xTicks = ticksFromDelta(p.deltaX, p.cellWidth, xHold)
  p.remainder.y = yHold.value
  p.remainder.x = xHold.value
  let out = ''
  if (yTicks !== 0) {
    const btn: 64 | 65 = yTicks < 0 ? 64 : 65
    const one = encodeOne(p.encoding, eventCode(btn, p.shift, p.alt, p.ctrl), p.col, p.row, p.pixelX, p.pixelY)
    out += one.repeat(Math.abs(yTicks))
  }
  if (xTicks !== 0) {
    const btn: 66 | 67 = xTicks < 0 ? 66 : 67
    const one = encodeOne(p.encoding, eventCode(btn, p.shift, p.alt, p.ctrl), p.col, p.row, p.pixelX, p.pixelY)
    out += one.repeat(Math.abs(xTicks))
  }
  return out
}
