// ConfirmDialog —— 轻量二次确认弹层。
//
// 为什么自己实现而不是引入 Radix Dialog：W2 只需要「不可逆操作先确认一次」这一
// 个弹层形态，手写一个十几行的遮罩 + 焦点面板即可，避免为一个按钮形态新增
// 一整个依赖树。键盘：Escape 等同取消。
//
// 参数：
//   - open: 是否显示
//   - title: 标题（如「停止任务」）
//   - description: 说明文字（写明后果）
//   - confirmLabel: 确认按钮文案
//   - destructive: 确认按钮是否用 destructive 视觉
//   - busy: 确认中置为不可点（防重复提交）
//   - onConfirm / onCancel
import { useEffect } from 'react'
import { Button } from '@/components/ui/button'

export interface ConfirmDialogProps {
  open: boolean
  title: string
  description: string
  confirmLabel: string
  destructive?: boolean
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel,
  destructive = false,
  busy = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [open, onCancel])

  if (!open) return null

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="presentation"
      onClick={onCancel}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="confirm-title"
        className="w-full max-w-md rounded-lg border bg-background p-5 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="confirm-title" className="text-base font-semibold">
          {title}
        </h2>
        <p className="mt-2 whitespace-pre-wrap text-sm text-muted-foreground">{description}</p>
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" onClick={onCancel} disabled={busy}>
            取消
          </Button>
          <Button variant={destructive ? 'destructive' : 'default'} onClick={onConfirm} disabled={busy}>
            {busy ? '提交中…' : confirmLabel}
          </Button>
        </div>
      </div>
    </div>
  )
}
