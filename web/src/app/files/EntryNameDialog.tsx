// EntryNameDialog —— 文件树新建/重命名的单层名输入弹层。
//
// 职责：让用户输入一个条目名，校验后回传；「新文件 / 新建文件夹 / 重命名」
// 三个入口共用，真正的请求与成功后刷新由调用方（FileTree）发起。
//
// 边界：
//   - 只收**单层名**：名字含 / 或 \ 时提交 disabled 并给出理由——跨目录移动
//     与嵌套不在本期（服务端同判，这里先挡在输入层）
//   - 不做服务端校验：撞名、路径逃逸、命中 .git 等由 agentd 的中文原文经
//     error prop 透出，这里不吞成「操作失败」
//   - 不决定关闭时机：Esc 只通知 onCancel，提交结果由调用方收口
import { useEffect, useState } from 'react'
import { Button } from '@/components/ui/button'

export interface EntryNameDialogProps {
  title: string
  initialName: string
  submitLabel: string
  busy?: boolean
  error?: string
  onSubmit: (name: string) => void
  onCancel: () => void
}

// INPUT_CLASS 与登记向导、编辑弹层的输入框保持同一套词汇。
const INPUT_CLASS =
  'mt-3 w-full rounded-md border border-input bg-background px-2.5 py-1.5 text-sm shadow-sm outline-none focus-visible:ring-1 focus-visible:ring-ring'

export function EntryNameDialog({
  title,
  initialName,
  submitLabel,
  busy = false,
  error,
  onSubmit,
  onCancel,
}: EntryNameDialogProps) {
  const [name, setName] = useState(initialName)
  // 空名字与含分隔符都不许提交；前者是「没有目标」，后者是「不是单层名」
  const invalid = name.includes('/') || name.includes('\\')
  const empty = name.trim() === ''
  const blocked = invalid || empty

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onCancel])

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="presentation"
      onClick={onCancel}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="entry-name-title"
        className="w-full max-w-sm rounded-lg border bg-background p-5 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="entry-name-title" className="text-base font-semibold">
          {title}
        </h2>
        <input
          aria-label="名称"
          value={name}
          onChange={(e) => setName(e.target.value)}
          // 初始值聚焦并全选：重命名时用户直接打字就覆盖旧名，不用先删一遍
          autoFocus
          onFocus={(e) => e.target.select()}
          disabled={busy}
          className={INPUT_CLASS}
        />
        {invalid && <p className="mt-2 text-xs text-destructive">名字不能包含 / 或 \</p>}
        {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" onClick={onCancel} disabled={busy}>
            取消
          </Button>
          <Button onClick={() => onSubmit(name)} disabled={blocked || busy}>
            {busy ? '提交中…' : submitLabel}
          </Button>
        </div>
      </div>
    </div>
  )
}
