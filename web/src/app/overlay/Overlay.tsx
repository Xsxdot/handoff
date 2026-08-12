// Overlay —— 弹出层基座：遮罩 + 标题栏 + Esc 关闭 + 点遮罩关闭。
//
// 职责：给看板与工单两个弹层提供同一套壳，让它们的关闭方式、层级、尺寸一致。
//
// 边界：
//   - 不管内容。内容组件自己取数、自己排版
//   - **同时只允许一个弹层**（spec §0）：这条约束由调用方（Shell 的 OverlayKind）
//     保证，本组件不做栈——做了栈就等于允许叠加，Esc 关哪个又要另定规则
//
// 焦点：挂载时把焦点收到面板上，卸载时不主动还原（还原到哪个元素在 tab 系统里
// 不好界定，交给浏览器默认行为比猜一个错的好）。
import { useEffect, useRef, type ReactNode } from 'react'
import { X } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface OverlayProps {
  title: string
  onClose: () => void
  children: ReactNode
  // wide 给看板用：四列横排需要更宽的面板
  wide?: boolean
}

export function Overlay({ title, onClose, children, wide }: OverlayProps) {
  const panelRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    panelRef.current?.focus()
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-6">
      <div
        data-testid="overlay-backdrop"
        className="absolute inset-0 bg-black/40"
        onClick={onClose}
      />
      <div
        ref={panelRef}
        role="dialog"
        aria-modal="true"
        aria-label={title}
        tabIndex={-1}
        className={cn(
          'relative flex max-h-full w-full flex-col rounded-lg border bg-background shadow-xl outline-none',
          wide ? 'max-w-6xl' : 'max-w-3xl',
        )}
      >
        <header className="flex items-center gap-2 border-b px-4 py-2.5">
          <h2 className="text-sm font-semibold">{title}</h2>
          <button
            type="button"
            aria-label="关闭"
            onClick={onClose}
            className="ml-auto rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <X className="size-4" />
          </button>
        </header>
        <div className="min-h-0 flex-1 overflow-auto">{children}</div>
      </div>
    </div>
  )
}
