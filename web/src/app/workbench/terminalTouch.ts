// terminalTouch —— 把手指滑动折成和桌面滚轮同一条 TUI 滚动。
//
// 职责：判断这一下该上送滚轮报告、方向键，还是留给 xterm 滚 scrollback；
//       以及这一下该不该锁住 viewport，免得浏览器把整屏 TUI 滑走。
// 边界：
//   - 不写 PTY、不读 xterm 实例。编码和格数在 terminalWheel。
//   - 不造假坐标。指针格子由调用方用 touch 的 clientX/Y 去算。
//   - 主屏且没开鼠标追踪时必须放行，否则 shell 的回滚被掐死。

import { wheelForcesSelection } from './terminalWheel'

export interface TouchPoint {
  x: number
  y: number
}

// touchWheelDelta 把两次触点换成与 WheelEvent 同号的像素。
//
// 手指上滑（y 变小）是滚轮向下（正），左滑（x 变小）是滚轮向右（正）。
// 参数：prev 是上一触点，next 是这一触点，都用 client 坐标。
// 返回：deltaX/deltaY，可直接喂给 altBufferWheelReports。
export function touchWheelDelta(prev: TouchPoint, next: TouchPoint): { deltaX: number; deltaY: number } {
  return { deltaX: prev.x - next.x, deltaY: prev.y - next.y }
}

// 小于这个距离的移动不写入 PTY，避免把静止点击收成滚动。
// 取消浏览器默认滚动不看这个距离：第一下不取消，WebView 会收走整段手势。
const touchSlopPx = 10

// touchPastSlop 判断手指是否已经离开点击死区。
//
// 参数：origin 是 touchstart 的触点，next 是当前触点。距离按像素直线。
// 返回：大于等于 10px 才写入 PTY。浏览器滚动的取消不看这个结果。
export function touchPastSlop(origin: TouchPoint, next: TouchPoint): boolean {
  const dx = next.x - origin.x
  const dy = next.y - origin.y
  return dx * dx + dy * dy >= touchSlopPx * touchSlopPx
}

export type TuiTouchIntent = 'passthrough' | 'wheel' | 'arrows'

// tuiTouchIntent 决定手指移动进哪条桌面滚轮的既有路径。
//
// 开了鼠标追踪：跟桌面滚轮一样发按钮 64/65，不能让 viewport 的 ydisp 动。
// 备用屏但没开追踪：跟 xterm 自己的滚轮回落一样发方向键。
// 主屏且没开追踪：shell 回滚，调用方不许 preventDefault。
// 多指、以及 Option/Shift 划词，放行。
export function tuiTouchIntent(p: {
  bufferType: 'normal' | 'alternate'
  mouseTracking: boolean
  altKey: boolean
  shiftKey: boolean
  isMac: boolean
  touches: number
}): TuiTouchIntent {
  if (p.touches !== 1) return 'passthrough'
  if (wheelForcesSelection(p, p.isMac)) return 'passthrough'
  if (p.mouseTracking) return 'wheel'
  if (p.bufferType === 'alternate') return 'arrows'
  return 'passthrough'
}

// tuiTouchLocksViewport 为真时，浏览器不能滚 xterm 的 viewport。
//
// xterm 在鼠标追踪开启时对 touchmove 直接返回，不 preventDefault。
// viewport 是 overflow-y: scroll，手指就把 ydisp 滑走，整屏 TUI 跟着动。
// 备用屏没有 scrollback，滑到边之后事件还会冒泡，WebView 把整页带着走。
export function tuiTouchLocksViewport(p: {
  bufferType: 'normal' | 'alternate'
  mouseTracking: boolean
}): boolean {
  return p.mouseTracking || p.bufferType === 'alternate'
}

// tuiTouchMoveAction 决定这一下 touchmove 要不要从浏览器手里抢走。
//
// 浏览器在第一下没有 preventDefault 的 touchmove 上把整段手势收走，
// 后面的事件变成不可取消。10px 死区若推迟取消，慢滑整段都被 WebView
// 拿去滚 viewport，看起来和没拦截一样。死区只推迟写入 PTY。
// 主屏放行时两者都不做，shell 回滚仍走浏览器。
export function tuiTouchMoveAction(p: {
  intent: TuiTouchIntent
  pastSlop: boolean
}): { prevent: boolean; emit: boolean } {
  if (p.intent === 'passthrough') return { prevent: false, emit: false }
  return { prevent: true, emit: p.pastSlop }
}
