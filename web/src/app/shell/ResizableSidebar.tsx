// ResizableSidebar —— 工作区左侧项目导航的宽度容器。
//
// 职责：
//   - 以项目树原型的 456px 为默认基准，给标题和机器归属留出可扫读的空间
//   - 提供鼠标拖拽与键盘方向键调整宽度，并将用户选择落到 localStorage
//   - 在调整期间锁定页面选区与光标，避免拖拽被树内容或文本选择打断
//
// 边界：
//   - 不负责项目树内容、导航数据或右侧工作区布局
//   - 不改变中央窗格的分栏拖拽；这里只调整 Shell 的左侧导航栏
//   - 宽度始终夹在可用范围内，避免项目名不可读或中央终端被挤没
import { useEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from 'react'
import {
  clampSidebarWidth,
  DEFAULT_SIDEBAR_WIDTH,
  MAX_SIDEBAR_WIDTH,
  MIN_SIDEBAR_WIDTH,
  SIDEBAR_KEYBOARD_STEP,
  SIDEBAR_WIDTH_KEY,
} from './sidebarResize'

function readStoredSidebarWidth(): number {
  try {
    const raw = window.localStorage.getItem(SIDEBAR_WIDTH_KEY)
    if (raw === null) return DEFAULT_SIDEBAR_WIDTH
    const stored = Number(raw)
    return Number.isFinite(stored) ? clampSidebarWidth(stored) : DEFAULT_SIDEBAR_WIDTH
  } catch {
    // 隐私模式或受限 WebView 可能禁用 localStorage；当前会话仍应可调整宽度。
    return DEFAULT_SIDEBAR_WIDTH
  }
}

function persistSidebarWidth(width: number): void {
  try {
    window.localStorage.setItem(SIDEBAR_WIDTH_KEY, String(width))
  } catch {
    // 持久化失败不应阻断已完成的拖拽，内存中的宽度仍然有效。
  }
}

export interface ResizableSidebarProps {
  children: ReactNode
}

export function ResizableSidebar({ children }: ResizableSidebarProps) {
  const [width, setWidth] = useState(readStoredSidebarWidth)
  const drag = useRef<{ startX: number; startWidth: number } | null>(null)

  useEffect(() => {
    persistSidebarWidth(width)
  }, [width])

  useEffect(() => {
    return () => {
      // 组件被路由卸载时也要恢复全局样式；否则下一页会继续显示 col-resize。
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }
  }, [])

  const beginDrag = (event: ReactPointerEvent<HTMLButtonElement>) => {
    event.preventDefault()
    // jsdom 没有实现 Pointer Capture；浏览器里使用它保证拖出把手后仍能继续调宽。
    event.currentTarget.setPointerCapture?.(event.pointerId)
    drag.current = { startX: event.clientX, startWidth: width }
    document.body.style.cursor = 'col-resize'
    document.body.style.userSelect = 'none'
    console.debug('shell.sidebar.resize.start', { width })
  }

  const moveDrag = (event: ReactPointerEvent<HTMLButtonElement>) => {
    const current = drag.current
    if (current === null) return
    setWidth(clampSidebarWidth(current.startWidth + event.clientX - current.startX))
  }

  const endDrag = (event?: ReactPointerEvent<HTMLButtonElement>) => {
    if (drag.current === null) return
    if (event && event.currentTarget.hasPointerCapture?.(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId)
    }
    drag.current = null
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
    console.debug('shell.sidebar.resize.end', { width })
  }

  const nudge = (delta: number) => {
    setWidth((current) => clampSidebarWidth(current + delta))
  }

  return (
    <aside
      role="complementary"
      aria-label="项目导航"
      className="relative flex min-h-0 max-w-full shrink-0 flex-col border-r bg-background"
      style={{ width: `${width}px` }}
    >
      {children}
      <button
        type="button"
        role="separator"
        aria-label="调整左侧栏宽度"
        aria-orientation="vertical"
        aria-valuemin={MIN_SIDEBAR_WIDTH}
        aria-valuemax={MAX_SIDEBAR_WIDTH}
        aria-valuenow={width}
        title="拖动调整左侧栏宽度"
        className="group absolute inset-y-0 -right-1 z-20 w-2 cursor-col-resize border-0 bg-transparent p-0 outline-none"
        onPointerDown={beginDrag}
        onPointerMove={moveDrag}
        onPointerUp={endDrag}
        onPointerCancel={() => endDrag()}
        onKeyDown={(event) => {
          if (event.key === 'ArrowLeft') {
            event.preventDefault()
            nudge(-SIDEBAR_KEYBOARD_STEP)
          } else if (event.key === 'ArrowRight') {
            event.preventDefault()
            nudge(SIDEBAR_KEYBOARD_STEP)
          }
        }}
      >
        <span aria-hidden className="absolute inset-y-0 left-1/2 w-px -translate-x-1/2 bg-transparent transition-colors group-hover:bg-[#cfcfcf] group-focus-visible:bg-[#999999]" />
      </button>
    </aside>
  )
}
