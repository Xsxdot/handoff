// DeleteEntryDialog —— 文件树删除条目的不可逆确认弹层。
//
// 职责：把删除的后果讲清楚（未跟踪的文件 git 也救不回），拿到明确确认才
// 回调 onConfirm；真正的删除请求由调用方（FileTree）经 deleteWorkspaceEntry
// 发起。
//
// 边界：
//   - 不做条目统计：dirCount 只取「已列举的内容数」，未知就如实说「可能还有
//     更多内容」，**不编数字**、不为它多打一次接口
//   - 失败原文经 error prop 透出（agentd 的中文错误），不吞成「操作失败」
import { useEffect } from 'react'
import { Button } from '@/components/ui/button'

export interface DeleteEntryDialogProps {
  name: string
  isDir: boolean
  rel: string
  // dirCount：目录时「已列举到的条目数」；未知传 undefined。
  dirCount?: number
  busy?: boolean
  error?: string
  onConfirm: () => void
  onCancel: () => void
}

export function DeleteEntryDialog({
  name,
  isDir,
  dirCount,
  busy = false,
  error,
  onConfirm,
  onCancel,
}: DeleteEntryDialogProps) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onCancel()
    }
    document.addEventListener('keydown', onKey)
    return () => document.removeEventListener('keydown', onKey)
  }, [onCancel])

  // 目录的条数只敢说「至少 N 项」：N 是已列举到的，磁盘上可能还有更多。
  const countText =
    dirCount !== undefined ? `该目录下至少 ${dirCount} 项内容。` : '目录里可能还有更多内容，删除后无法恢复。'

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 p-4"
      role="presentation"
      onClick={onCancel}
    >
      <div
        role="dialog"
        aria-modal="true"
        aria-labelledby="delete-title"
        className="w-full max-w-md rounded-lg border bg-background p-5 shadow-lg"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="delete-title" className="text-base font-semibold">
          删除条目
        </h2>
        <p className="mt-2 text-sm text-muted-foreground">
          确定删除「{name}」吗？{isDir && countText}
          未被 git 跟踪的文件删除后无法恢复。
        </p>
        {error && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <Button variant="outline" onClick={onCancel} disabled={busy}>
            取消
          </Button>
          <Button variant="destructive" onClick={onConfirm} disabled={busy}>
            {busy ? '删除中…' : '删除'}
          </Button>
        </div>
      </div>
    </div>
  )
}
