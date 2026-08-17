// GroupDivider —— 中央工作区两栏之间那条可拖拽的分隔条。
//
// 职责：
//   - 画出 1px 的分隔线（命中区放宽到 5px，1px 的把手鼠标够不着）
//   - 把鼠标拖拽与 ← → 键换算成「本次位移占容器宽度的比例」交给上层
//
// 边界：
//   - **不认识分栏模型**：不知道自己是第几条、两侧是谁、有没有到下限。它只报
//     「移动了多少」，夹紧与分配都在 tabs.ts 的 resizeGroups 里
//   - 不持有宽度状态：宽度的唯一真相在 Workbench.sizes
//
// 为什么量的是 parentElement 的宽度：分隔条自己只有 5px，换算比例要的是**容器**
// 宽度，而容器就是它和各栏共同的那个 flex 父节点。但 parentElement 的宽度还包含
// 所有分隔条，必须先扣掉它们，才是各栏真正瓜分的可分配宽度。在事件里现量而不是
// 存进 state：窗口 resize、左右栏的显隐都会改容器宽，存下来的值随时会过期。
import { useRef } from 'react'
import { availablePaneWidth } from './tabs'

// KEY_STEP 是键盘每次调整的比例。2% 在 740px 的中央区里约合 15px，
// 连按可达且不会一步跨过整栏。
const KEY_STEP = 0.02

export interface GroupDividerProps {
  // onResize 收到本次位移比例（正数 = 分隔条右移）与当前容器宽度（px）。
  // 容器宽度一并交出去，是因为「最小栏宽」是像素量、只有拿到宽度才能换成比例，
  // 而量宽度这件事只有这里有 DOM。量不到时给 0，由上层决定怎么退化。
  onResize: (delta: number, containerWidth: number) => void
}

export function GroupDivider({ onResize }: GroupDividerProps) {
  // 拖拽中的起点：上一次派发位置的 clientX 与容器宽度。null = 没在拖
  const drag = useRef<{ lastX: number; width: number } | null>(null)

  const containerWidthOf = (el: HTMLElement): number => {
    const parent = el.parentElement
    if (!parent) return 0
    const separatorWidths = Array.from(
      parent.querySelectorAll<HTMLElement>('[role="separator"]'),
      (separator) => separator.getBoundingClientRect().width,
    )
    return availablePaneWidth(parent.getBoundingClientRect().width, separatorWidths)
  }

  return (
    <div
      role="separator"
      aria-orientation="vertical"
      aria-label="调整栏宽"
      tabIndex={0}
      className="w-[5px] shrink-0 cursor-col-resize bg-border hover:bg-primary/40 focus-visible:bg-primary/40 focus-visible:outline-none"
      onPointerDown={(e) => {
        e.preventDefault()
        e.currentTarget.setPointerCapture(e.pointerId)
        drag.current = { lastX: e.clientX, width: containerWidthOf(e.currentTarget) }
      }}
      onPointerMove={(e) => {
        const d = drag.current
        if (d === null) return
        // 派发**增量**而不是「相对起点的总位移」：增量在被 resizeGroups 夹住之后
        // 不会累积出一个看不见的欠账，往回拖立刻就有反应
        if (d.width > 0) onResize((e.clientX - d.lastX) / d.width, d.width)
        d.lastX = e.clientX
      }}
      onPointerUp={(e) => {
        e.currentTarget.releasePointerCapture(e.pointerId)
        drag.current = null
      }}
      onPointerCancel={() => {
        drag.current = null
      }}
      onKeyDown={(e) => {
        if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return
        e.preventDefault()
        onResize(e.key === 'ArrowRight' ? KEY_STEP : -KEY_STEP, containerWidthOf(e.currentTarget))
      }}
    />
  )
}
