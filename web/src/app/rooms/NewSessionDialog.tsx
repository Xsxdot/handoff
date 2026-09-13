// NewSessionDialog —— 新建会话对话框。owner 用统一记法 user:<name>/agent:<name>
// （B358.4 拍板①：成员身份不收机器位 web:）；客户端前缀预检只为快速失败，
// 服务端校验是权威（invalidSessionOwner 400 原文透传）。owner 来源=岔口 2 拍板：
// 对话框显式输入统一记法。
import { useState } from 'react'

export function NewSessionDialog({ open, busy, error, onCancel, onCreate }: {
  open: boolean; busy: boolean; error: string
  onCancel: () => void
  onCreate: (title: string, owner: string) => void
}) {
  const [title, setTitle] = useState('')
  const [owner, setOwner] = useState('')
  if (!open) return null
  const ownerOk = /^(user|agent):.+$/.test(owner.trim())
  return (
    <div role="dialog" aria-label="新建会话" className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="w-80 rounded-xl border bg-background p-4 shadow-xl">
        <h3 className="text-sm font-semibold">新建会话</h3>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-session-title">标题</label>
        <input id="new-session-title" aria-label="会话标题" value={title} onChange={(event) => setTitle(event.target.value)}
          className="mt-1 w-full rounded-md border px-2 py-1 text-sm" />
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-session-owner">群主身份（统一记法）</label>
        <input id="new-session-owner" aria-label="群主身份" value={owner} onChange={(event) => setOwner(event.target.value)}
          placeholder="user:sy" className="mt-1 w-full rounded-md border px-2 py-1 font-mono text-sm" />
        {!ownerOk && owner.trim() !== '' && <p className="mt-1 text-xs text-amber-700">{'须形如 user:<名字> 或 agent:<名字>'}</p>}
        {error !== '' && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={() => { setTitle(''); setOwner(''); onCancel() }} className="rounded-md border px-2.5 py-1 text-xs">取消</button>
          <button type="button" disabled={title.trim() === '' || !ownerOk || busy}
            onClick={() => onCreate(title.trim(), owner.trim())}
            className="rounded-md bg-slate-900 px-2.5 py-1 text-xs text-white disabled:opacity-50">创建</button>
        </div>
      </div>
    </div>
  )
}
