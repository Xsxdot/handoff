// IconMenu —— 一个图标触发的小菜单：点图标弹出若干项，点项即回调。
//
// 职责：只管「弹出 / 收起 / 选中」这三件事，长什么样由调用方给的 items 决定。
//
// 为什么不复用 lib/Dropdown：那个是「带文字的筛选器触发按钮 + 选中态勾选」，
// 语义是选值（listbox）；这里是动作菜单（menu），触发器只有一个图标，也没有
// 选中态。硬套过去要给 Dropdown 加两套分支，反而更贵。
//
// 边界：
//   - **菜单挂在 document.body 上（portal）**，不留在触发器的 DOM 子树里。
//     承重：tab 条是 overflow-x-auto、浮窗外框是 overflow-hidden，菜单留在
//     原地会被裁掉，只露出一条边
//   - 不持有任何业务状态；items 每次渲染现给即可
import { useEffect, useRef, useState, type ReactNode } from 'react'
import { Check } from 'lucide-react'
import { createPortal } from 'react-dom'
import { cn } from '@/lib/utils'

export interface IconMenuItem {
  key: string
  label: string
  // kind 决定这一项是什么：缺省 'action'（点了就关，老用法一字不改）、
  // 'check' 独立开关、'radio' 单选组成员、'header' 不可点的分组标题。
  kind?: 'action' | 'check' | 'radio' | 'header'
  checked?: boolean   // check/radio 的选中态
  // keepOpen=true 时点完不关菜单。连着调三个开关是常态，每点一次就关掉
  // 等于逼人开三次。
  keepOpen?: boolean
  icon?: ReactNode // 项左侧的图标；不给就只有文字
  hotkey?: string // 项右侧的快捷键提示，仅展示
  onSelect?: () => void   // header 不需要
}

export interface IconMenuProps {
  label: string // 触发器的 aria-label / title
  icon: ReactNode // 触发器图标
  items: IconMenuItem[]
  className?: string // 触发器的额外类名，交给调用方对齐自己的工具条
  // dark = 用悬浮窗那套深色配色而不是主题色。
  //
  // 为什么要这个开关而不是一律走主题变量：悬浮窗（HomeWindow）的外壳是一组
  // 写死的深色，不跟随主题；菜单在它上面弹出，用 bg-popover 会在浅色主题下
  // 弹出一块白板贴在深色终端上
  dark?: boolean
}

export function IconMenu({ label, icon, items, className, dark = false }: IconMenuProps) {
  const [open, setOpen] = useState(false)
  // pos 是菜单左上角的视口坐标，按下时由触发器的 rect 算出。
  // 用 fixed + 实测坐标而不是 absolute：菜单已经 portal 到 body，
  // 它和触发器之间不再有共同的定位祖先
  const [pos, setPos] = useState({ left: 0, top: 0 })
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      // 点触发器自己不在这里关：它的 onClick 会 toggle，两处一起动就成了「点不开」
      if (triggerRef.current?.contains(e.target as Node)) return
      // **承重：点在菜单里也不能在这里关**。mousedown 早于 click，这里一关，
      // React 会在同一拍把菜单项从 DOM 里摘掉，mouseup 落到空处，click 根本
      // 不会发生——菜单看起来「点了没反应」。走查实测：单测用 fireEvent.click
      // 不带 mousedown，测不出这条
      if (menuRef.current?.contains(e.target as Node)) return
      setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
    }
    // mousedown 而不是 click：菜单项自己的 click 要先跑完
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  return (
    <>
      <button
        ref={triggerRef}
        type="button"
        aria-label={label}
        title={label}
        aria-haspopup="menu"
        aria-expanded={open}
        // stopPropagation：浮窗标题栏/tab 条上按下会被当成拖动起点，
        // 不拦的话点一下 + 浮窗会跟着指针走
        onPointerDown={(e) => e.stopPropagation()}
        onClick={() => {
          const r = triggerRef.current?.getBoundingClientRect()
          // 菜单挂在触发器正下方 4px；rect 取不到（jsdom）时退回原点，不抛错
          if (r) setPos({ left: r.left, top: r.bottom + 4 })
          setOpen((o) => !o)
        }}
        className={className}
      >
        {icon}
      </button>
      {open &&
        createPortal(
          <div
            ref={menuRef}
            role="menu"
            aria-label={label}
            // z-[60]：要盖住浮窗（z-40）与弹层（z-50）。菜单是瞬时的，
            // 盖在谁上面都只是这一下
            className={cn(
              'fixed z-[60] max-h-[min(60vh,420px)] min-w-40 overflow-y-auto rounded-md border p-1 shadow-lg',
              dark ? 'border-[#2b3542] bg-[#0c1622]' : 'bg-popover',
            )}
            style={{ left: pos.left, top: pos.top }}
          >
            {items.map((item) => {
              const kind = item.kind ?? 'action'
              if (kind === 'header') {
                return (
                  <div
                    key={item.key}
                    className={cn(
                      'px-2 pb-0.5 pt-1.5 text-[10px] font-medium uppercase tracking-wide',
                      dark ? 'text-[#8e9bab]' : 'text-muted-foreground',
                    )}
                  >
                    {item.label}
                  </div>
                )
              }
              const role = kind === 'check' ? 'menuitemcheckbox' : kind === 'radio' ? 'menuitemradio' : 'menuitem'
              return (
                <button
                  key={item.key}
                  type="button"
                  role={role}
                  aria-checked={kind === 'action' ? undefined : item.checked === true}
                  onClick={() => {
                    // keepOpen 的项不关菜单；关闭要排在 onSelect 之前，
                    // 与改造前的次序保持一致（onSelect 可能自己再开别的层）
                    if (!item.keepOpen) setOpen(false)
                    item.onSelect?.()
                  }}
                  className={cn(
                    'flex w-full cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-left text-xs',
                    dark ? 'text-[#d7dde5] hover:bg-[#1a2430]' : 'hover:bg-accent',
                  )}
                >
                  {/* 选中标记占等宽位：不占位的话，勾选状态一变行文字就左右跳 */}
                  {kind !== 'action' && (
                    <span className="flex size-3.5 shrink-0 items-center justify-center">
                      {item.checked === true &&
                        (kind === 'check' ? (
                          <Check className="size-3.5" />
                        ) : (
                          <span className="size-1.5 rounded-full bg-current" />
                        ))}
                    </span>
                  )}
                  {item.icon}
                  <span className="flex-1">{item.label}</span>
                  {item.hotkey !== undefined && (
                    <span className={cn('font-mono text-[10px]', dark ? 'text-[#8e9bab]' : 'text-muted-foreground')}>
                      {item.hotkey}
                    </span>
                  )}
                </button>
              )
            })}
          </div>,
          document.body,
        )}
    </>
  )
}
