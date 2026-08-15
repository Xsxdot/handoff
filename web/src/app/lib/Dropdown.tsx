// Dropdown —— 手写的下拉菜单（供看板筛选栏与项目向导共用）。
//
// 为什么手写而不是引入 react-dropdown-menu：本项目当前只依赖 @radix-ui/react-slot，
// 为一个下拉引入整套依赖树不划算；控件行为简单（点击外部关闭、Esc 关闭、方向键
// 移动、Enter 选中）且已被测试覆盖。
//
// multiple 语义：项前带勾选框指示、选中不自动关闭；单选选中后立即关闭。
// 选项的选中值一律走 value 字符串，与显示 label 分离。
import { useEffect, useRef, useState } from 'react'
import { Check, ChevronDown } from 'lucide-react'
import { cn } from '@/lib/utils'

export interface DropdownOption {
  value: string
  label: string
  extra?: string // 项右侧的补充说明（如任务数）
}

export interface DropdownProps {
  label: string // 触发器基础文案（如「项目」「开发机」）
  options: DropdownOption[]
  multiple?: boolean
  selected: string[] // 当前选中的 value 列表
  onSelect: (value: string) => void
}

export function Dropdown({ label, options, multiple = false, selected, onSelect }: DropdownProps) {
  const [open, setOpen] = useState(false)
  const [activeIndex, setActiveIndex] = useState(0)
  const rootRef = useRef<HTMLDivElement>(null)

  const selectedLabels = options.filter((o) => selected.includes(o.value)).map((o) => o.label)
  // 触发器文案：单选显示选中的 label，多选显示计数；未选中时只有基础 label。
  const triggerText = multiple
    ? selected.length > 0 ? `${label} · 已选 ${selected.length}` : label
    : selected.length > 0 ? `${label} · ${selectedLabels[0]}` : label

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false)
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setActiveIndex((i) => Math.min(i + 1, options.length - 1))
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setActiveIndex((i) => Math.max(i - 1, 0))
      }
      if (e.key === 'Enter' && options[activeIndex]) {
        onSelect(options[activeIndex].value)
        if (!multiple) setOpen(false)
      }
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open, options, activeIndex, multiple, onSelect])

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        onClick={() => {
          setOpen((o) => !o)
          setActiveIndex(0)
        }}
        className="inline-flex h-8 items-center gap-1.5 rounded-md border border-input bg-background px-2.5 text-xs shadow-sm transition-colors hover:bg-accent hover:text-accent-foreground"
      >
        {triggerText}
        <ChevronDown className="size-3.5 opacity-60" />
      </button>
      {open && (
        <div
          role="listbox"
          aria-multiselectable={multiple || undefined}
          className="absolute left-0 z-50 mt-1 min-w-44 rounded-md border bg-popover p-1 shadow-lg"
        >
          {options.map((opt, i) => {
            const isSelected = selected.includes(opt.value)
            return (
              <div
                key={opt.value}
                role="option"
                aria-selected={isSelected}
                onClick={() => {
                  onSelect(opt.value)
                  if (!multiple) setOpen(false)
                }}
                className={cn(
                  'flex cursor-pointer items-center gap-2 rounded-sm px-2 py-1.5 text-xs',
                  i === activeIndex && 'bg-accent',
                  isSelected && 'font-medium',
                )}
              >
                {multiple && (
                  <span
                    className={cn(
                      'flex size-3.5 shrink-0 items-center justify-center rounded-[3px] border',
                      isSelected ? 'border-primary bg-primary text-primary-foreground' : 'border-input',
                    )}
                  >
                    {isSelected && <Check className="size-2.5" />}
                  </span>
                )}
                <span className="min-w-0 flex-1 truncate">{opt.label}</span>
                {opt.extra !== undefined && (
                  <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground">{opt.extra}</span>
                )}
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
