// NewSessionDialog —— 新建会话对话框。owner 用统一记法 user:<name>/agent:<name>
// （B358.4 拍板①：成员身份不收机器位 web:）；客户端前缀预检只为快速失败，
// 服务端校验是权威（invalidSessionOwner 400 原文透传）。
// B358.8 #7：owner 预填 = localStorage 记忆的上次成功创建 owner（sessionOwnerPrefs，
// 读写容错隐私模式）——第二次起零输入可直接提交；输入挂既有成员候选（Shell 从
// 会话流投影 identities 下传）+ 当前输入，保留改选能力。
// 控制台无「当前登录用户」概念（web:<host> 是机器位不配当 owner，B358.4 拍板①），
// 记忆方案是有意的落点：首次使用仍需输一次。
import { useEffect, useState } from 'react'
import { loadLastSessionOwner } from './sessionOwnerPrefs'

export function NewSessionDialog({ open, busy, error, memberIdentities = [], onCancel, onCreate }: {
  open: boolean; busy: boolean; error: string
  memberIdentities?: string[]
  onCancel: () => void
  onCreate: (title: string, owner: string) => void
}) {
  const [title, setTitle] = useState('')
  const [owner, setOwner] = useState('')
  // 打开时重置标题、预填上次成功创建的 owner（记忆值非空时打开即可直接提交）。
  useEffect(() => {
    if (!open) return
    setTitle('')
    setOwner(loadLastSessionOwner())
  }, [open])
  if (!open) return null
  const ownerOk = /^(user|agent):.+$/.test(owner.trim())
  // 候选 = 既有成员 identities ∪ 当前输入（当前输入已在改半截时仍可见）。
  const candidates = [...new Set([...memberIdentities, owner].filter((value) => value !== ''))]
  return (
    <div role="dialog" aria-label="新建会话" className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="w-80 rounded-xl border bg-background p-4 shadow-xl">
        <h3 className="text-sm font-semibold">新建会话</h3>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-session-title">标题</label>
        <input id="new-session-title" aria-label="会话标题" value={title} onChange={(event) => setTitle(event.target.value)}
          className="mt-1 w-full rounded-md border px-2 py-1 text-sm" />
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-session-owner">群主身份（统一记法）</label>
        <input id="new-session-owner" aria-label="群主身份" value={owner} onChange={(event) => setOwner(event.target.value)}
          list="new-session-owner-candidates" placeholder="user:sy"
          className="mt-1 w-full rounded-md border px-2 py-1 font-mono text-sm" />
        <datalist id="new-session-owner-candidates">
          {candidates.map((identity) => <option key={identity} value={identity} />)}
        </datalist>
        {!ownerOk && owner.trim() !== '' && <p className="mt-1 text-xs text-amber-700">{'须形如 user:<名字> 或 agent:<名字>'}</p>}
        {owner.trim() === '' && <p className="mt-1 text-xs text-muted-foreground">留空不可创建；上次使用的身份会自动预填。</p>}
        {error !== '' && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={onCancel} className="rounded-md border px-2.5 py-1 text-xs">取消</button>
          <button type="button" disabled={title.trim() === '' || !ownerOk || busy}
            onClick={() => onCreate(title.trim(), owner.trim())}
            className="rounded-md bg-slate-900 px-2.5 py-1 text-xs text-white disabled:opacity-50">创建</button>
        </div>
      </div>
    </div>
  )
}
