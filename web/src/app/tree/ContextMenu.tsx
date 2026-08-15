// ContextMenu —— 右键菜单。
//
// 职责：在鼠标位置弹一份菜单项，处理关闭（点项 / 点外部 / Esc）与键盘移动。
//
// 边界：
//   - 不认识菜单项的语义，`onSelect` 干什么由调用方决定；也不做二次确认，
//     破坏性动作的确认弹层归调用方（`danger` 只影响配色）
//   - 不管「同时只能有一个菜单」：那由调用方用一份状态承担，本组件挂载即显示
//
// 为什么自己写而不是引依赖：`components/ui/` 只有 badge/button/card，本仓库
// 至今零处右键菜单。为了一个单项菜单引一整套 dropdown 依赖不划算。
import { useEffect, useLayoutEffect, useRef, useState } from 'react'
import { cn } from '@/lib/utils'

export interface ContextMenuItem {
  label: string
  onSelect: () => void
  danger?: boolean
}

export interface ContextMenuProps {
  x: number
  y: number
  items: ContextMenuItem[]
  onClose: () => void
}

export function ContextMenu({ x, y, items, onClose }: ContextMenuProps) {
  const ref = useRef<HTMLDivElement>(null)
  // pos 先用点击坐标，挂载后按实测尺寸向内翻转。
  // 为什么不在渲染前算：菜单宽高取决于最长的那条文案，只有量过才知道
  const [pos, setPos] = useState({ left: x, top: y })

  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const { width, height } = el.getBoundingClientRect()
    // 4px 是贴边留白，纯观感；越界时改成「从点击点向左/上展开」
    setPos({
      left: x + width > window.innerWidth ? Math.max(4, x - width) : x,
      top: y + height > window.innerHeight ? Math.max(4, y - height) : y,
    })
    el.querySelector<HTMLButtonElement>('[role="menuitem"]')?.focus()
  }, [x, y])

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.stopPropagation()
        onClose()
      }
    }
    // 捕获阶段：菜单外的任意 pointerdown 都关掉它。菜单内的由下面那句挡住
    const onDown = (e: PointerEvent) => {
      if (ref.current?.contains(e.target as Node)) return
      onClose()
    }
    document.addEventListener('keydown', onKey)
    document.addEventListener('pointerdown', onDown, true)
    return () => {
      document.removeEventListener('keydown', onKey)
      document.removeEventListener('pointerdown', onDown, true)
    }
  }, [onClose])

  return (
    <div
      ref={ref}
      role="menu"
      style={{ left: pos.left, top: pos.top }}
      className="fixed z-50 min-w-32 rounded-md border border-border bg-popover p-1 text-popover-foreground shadow-lg"
    >
      {items.map((it) => (
        <button
          key={it.label}
          type="button"
          role="menuitem"
          onClick={() => {
            // 先执行再关：反过来的话调用方在 onSelect 里 setState 会撞上
            // 本组件正在卸载，React 会警告「更新一个未挂载的组件」
            it.onSelect()
            onClose()
          }}
          className={cn(
            'flex w-full cursor-pointer items-center rounded px-2 py-1.5 text-left text-[12.5px] hover:bg-accent',
            it.danger && 'text-destructive',
          )}
        >
          {it.label}
        </button>
      ))}
    </div>
  )
}
