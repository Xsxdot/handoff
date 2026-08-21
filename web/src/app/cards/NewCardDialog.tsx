// NewCardDialog —— 建卡对话框。
//
// 职责：收集建一张卡所需的最小字段（标题/工作流/优先级/父卡/基线分支），
// 调 createCard，把新卡号交回调用方（通常用来立刻打开抽屉）。
//
// 边界：
//   - 不管建完之后干什么——打开抽屉、刷新列表都由调用方决定
//   - 不做项目选择：项目由调用方从当前视图上下文传进来
//   - **基线分支只在这里能填**：建卡后不可改（改基线会让已派出去的任务与卡
//     的说法对不上），所以表单上要写清这一点，而不是让人建完才发现改不了
import { useState } from 'react'
import { createCard } from '../../api/ledger'
import { errorMessage } from '../lib/format'

export function NewCardDialog({
  open, project, workflows, parent, onClose, onCreated,
}: {
  open: boolean
  project: string
  workflows: string[]
  parent?: string
  onClose: () => void
  onCreated: (id: string) => void
}) {
  const [title, setTitle] = useState('')
  const [workflow, setWorkflow] = useState(workflows[0] ?? 'feature')
  const [priority, setPriority] = useState('中')
  const [baseBranch, setBaseBranch] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  if (!open) return null

  const submit = async () => {
    const trimmed = title.trim()
    if (!trimmed) return
    setBusy(true)
    setError('')
    try {
      const result = await createCard({
        title: trimmed, project, workflow, priority,
        ...(parent ? { parent } : {}),
        ...(baseBranch.trim() ? { base_branch: baseBranch.trim() } : {}),
      })
      setTitle('')
      setBaseBranch('')
      onCreated(result.id)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/30 p-4">
      <div className="w-full max-w-md rounded-lg border bg-background p-4 shadow-lg">
        <h2 className="text-base font-semibold">新建工作项</h2>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-title">标题</label>
        <input
          id="new-card-title" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
          value={title} onChange={(e) => setTitle(e.target.value)} autoFocus
        />
        <div className="mt-3 grid grid-cols-2 gap-3">
          <div>
            <label className="block text-xs text-muted-foreground" htmlFor="new-card-workflow">工作流</label>
            <select
              id="new-card-workflow" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              value={workflow} onChange={(e) => setWorkflow(e.target.value)}
            >
              {workflows.map((name) => <option key={name} value={name}>{name}</option>)}
            </select>
          </div>
          <div>
            <label className="block text-xs text-muted-foreground" htmlFor="new-card-priority">优先级</label>
            <select
              id="new-card-priority" className="mt-1 w-full rounded border px-2 py-1.5 text-sm"
              value={priority} onChange={(e) => setPriority(e.target.value)}
            >
              {['高', '中', '低'].map((level) => <option key={level} value={level}>{level}</option>)}
            </select>
          </div>
        </div>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-card-base">基线分支</label>
        <input
          id="new-card-base" className="mt-1 w-full rounded border px-2 py-1.5 font-mono text-sm"
          placeholder={parent ? '留空 = 继承父卡' : '留空 = 项目主线'}
          value={baseBranch} onChange={(e) => setBaseBranch(e.target.value)}
        />
        <p className="mt-1 text-xs text-muted-foreground">
          这张卡的合并目标。<b>建卡后不可改</b>——已派出去的任务会按它工作。
        </p>
        {error !== '' && <p className="mt-3 rounded border border-amber-500/40 bg-amber-500/10 p-2 text-xs">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" className="rounded border px-3 py-1.5 text-sm" onClick={onClose}>取消</button>
          <button
            type="button" className="rounded bg-primary px-3 py-1.5 text-sm text-primary-foreground disabled:opacity-50"
            disabled={busy || title.trim() === ''}
            onClick={() => void submit()}
          >建卡</button>
        </div>
      </div>
    </div>
  )
}
