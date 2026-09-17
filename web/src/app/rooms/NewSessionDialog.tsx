// NewSessionDialog —— 新建会话对话框。owner 用统一记法 user:<name>/agent:<name>
// （B358.4 拍板①：成员身份不收机器位 web:）；客户端前缀预检只为快速失败，
// 服务端校验是权威（invalidSessionOwner 400 原文透传）。
// B358.8 #7：owner 缺省链 = localStorage 记忆（合法才用）→ 成员投影首个合法身份
// （user:* 人席优先）——记忆缺席/跨端创建（CLI 建的会话不写浏览器记忆）时打开
// 也能直接提交。候选只给 user:/agent: 合法集：cli:/web:/裸名进 datalist 就是
// 选了无法创建的死选项（走查 09-17）。
// 控制台无「当前登录用户」概念（web:<host> 是机器位不配当 owner，B358.4 拍板①），
// 缺省链是零后端改动下的落点：真·首次（无记忆、无成员）仍需输一次。
import { useEffect, useRef, useState } from 'react'
import { firstUsableIdentity, isOwnerNotation, loadLastSessionOwner } from './sessionOwnerPrefs'

export function NewSessionDialog({ open, busy, error, memberIdentities = [], onCancel, onCreate }: {
  open: boolean; busy: boolean; error: string
  memberIdentities?: string[]
  onCancel: () => void
  onCreate: (title: string, owner: string) => void
}) {
  const [title, setTitle] = useState('')
  const [owner, setOwner] = useState('')
  // 打开瞬间（closed→open）重置标题、确定 owner 缺省：记忆合法即用，否则回退
  // 成员中的首个合法身份（走查 09-17）。只认边沿——轮询刷新 members 时不重算，
  // 打开后的输入是用户的，不许后台刷新覆盖。
  const wasOpen = useRef(false)
  useEffect(() => {
    if (open && !wasOpen.current) {
      setTitle('')
      const remembered = loadLastSessionOwner()
      setOwner(isOwnerNotation(remembered) ? remembered.trim() : firstUsableIdentity(memberIdentities))
    }
    wasOpen.current = open
  }, [open, memberIdentities])
  if (!open) return null
  const ownerOk = isOwnerNotation(owner)
  // 候选 = 成员合法子集 ∪ 当前输入（改半截时仍可见；非法输入不进候选——
  // 不提供选了无法创建的死选项）。
  const candidates = [...new Set([...memberIdentities.filter(isOwnerNotation), owner].filter((value) => value !== '' && isOwnerNotation(value)))]
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
        {owner.trim() === '' && <p className="mt-1 text-xs text-muted-foreground">留空不可创建；自动预填上次使用的身份（没有则取已有成员中的首个），候选只列可当群主的身份。</p>}
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
