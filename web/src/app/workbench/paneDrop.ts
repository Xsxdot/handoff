// paneDrop —— 中央区投放区的判定规则（纯函数，无 React 依赖）。
//
// 职责：给一次拖放在某一栏内的横向位置，算出它落在左边缘 / 右边缘 / 中间。
//
// 边界：不认识 tab、不认识任务、不碰 DOM。调用方量好宽度与偏移量传进来。
import type { BaseDir } from './useWorkbench'

// DRAG_TASK_MIME / DRAG_BASE_MIME 是拖放数据的自定义类型。
//
// 为什么不用 text/plain：中央区只认这两个类型，从别处（浏览器地址栏、桌面
// 文件、编辑器选中文本）拖进来时不会被误判成一次任务拖放。
export const DRAG_TASK_MIME = 'text/handoff-task'
export const DRAG_BASE_MIME = 'text/handoff-base'

// EDGE_RATIO / EDGE_MAX_PX 共同决定边缘区有多宽，取两者的较小值。
//
// 为什么要有像素上限：栏可以被拖得很宽，一个 800px 的栏上 25% 就是 200px 的
// 边缘区，会让「我只是想在这栏开个 tab」频繁误触发分屏。
const EDGE_RATIO = 0.25
const EDGE_MAX_PX = 120

// DropZone 是一次拖放的三种落点。
export type DropZone = 'left' | 'right' | 'center'

// dropZoneAt 判定投放区。
//
// 参数：
//   - offsetX: 指针相对该栏左边缘的横向偏移（像素）
//   - width: 该栏的宽度（像素）
//   - canSplit: 现在还能不能再分出一栏（未到 MAX_GROUPS）
//
// 返回：'left' / 'right' 表示在该栏的那一侧插入新栏；'center' 表示在该栏开 tab。
//
// canSplit 为假时边缘退化成 center 而不是「无效投放」：拖放过程中没有地方放
// 一句「最多三栏」的提示，而一次落空的拖拽比一次「落在了这栏」更让人困惑
// （spec §3.2）。这与「分屏按钮到上限置灰」不冲突——按钮是常驻控件，说得起话。
export function dropZoneAt(offsetX: number, width: number, canSplit: boolean): DropZone {
  // 宽度量不到（jsdom、尚未布局完成）时一律 center：宁可这一次不分屏，
  // 也不要因为除以 0 得到 NaN 而让整次拖放失灵
  if (!canSplit || width <= 0) return 'center'
  const edge = Math.min(width * EDGE_RATIO, EDGE_MAX_PX)
  if (offsetX < edge) return 'left'
  if (offsetX > width - edge) return 'right'
  return 'center'
}

// readDragBase 从 dataTransfer 里取出拖源写进去的基准目录；没有或解析失败返回 null。
//
// 解析失败返回 null 而不是抛错：dataTransfer 里的内容不是我们能保证的
// （用户可能从别的应用拖了个同名类型进来），一次拖放不该把界面炸掉。
export function readDragBase(raw: string): BaseDir | null {
  if (raw === '' || raw === 'null') return null
  try {
    const value = JSON.parse(raw) as BaseDir
    return typeof value?.key === 'string' && typeof value?.path === 'string' ? value : null
  } catch {
    return null
  }
}
