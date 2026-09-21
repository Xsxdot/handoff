// NewSessionDialog —— 新建会话对话框（B358.9：只有标题；owner 由服务端按解析
// 人名缺省，前端不传）。未配 console_user 时给可行动提示并禁用创建。
//
// 边界：不认识会话数据流；错误原文由调用方透传（服务端 403 文案含 console_user，
// 前端不二次加工）。
import { useEffect, useRef, useState } from 'react'

export function NewSessionDialog({ open, busy, error, configured, onCancel, onCreate }: {
  open: boolean; busy: boolean; error: string; configured: boolean
  onCancel: () => void
  onCreate: (title: string) => void
}) {
  const [title, setTitle] = useState('')
  // 打开瞬间（closed→open）重置标题：打开后的输入是用户的，不许后台刷新覆盖。
  const wasOpen = useRef(false)
  useEffect(() => {
    if (open && !wasOpen.current) setTitle('')
    wasOpen.current = open
  }, [open])
  if (!open) return null
  return (
    <div role="dialog" aria-label="新建会话" className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
      <div className="w-80 rounded-xl border bg-background p-4 shadow-xl">
        <h3 className="text-sm font-semibold">新建会话</h3>
        <label className="mt-3 block text-xs text-muted-foreground" htmlFor="new-session-title">标题</label>
        <input id="new-session-title" aria-label="会话标题" value={title} onChange={(event) => setTitle(event.target.value)}
          className="mt-1 w-full rounded-md border px-2 py-1 text-sm" />
        {!configured && (
          <p role="alert" className="mt-2 text-xs text-amber-700">
            本机未配置控制台人名 console_user，无法确定身份；请在 agentd 配置文件中设置 console_user 后重试。
          </p>
        )}
        {error !== '' && <p role="alert" className="mt-2 text-xs text-destructive">{error}</p>}
        <div className="mt-4 flex justify-end gap-2">
          <button type="button" onClick={onCancel} className="rounded-md border px-2.5 py-1 text-xs">取消</button>
          <button type="button" disabled={title.trim() === '' || !configured || busy}
            onClick={() => onCreate(title.trim())}
            className="rounded-md bg-slate-900 px-2.5 py-1 text-xs text-white disabled:opacity-50">创建</button>
        </div>
      </div>
    </div>
  )
}
