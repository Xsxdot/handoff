// FileTab —— 只读查看基准目录下的一个文件（spec §2.2）。
//
// 职责：取 GET /api/workspaces/file 并把正文原样显示。
//
// 边界：
//   - **只读**。写入与在线编辑不在本期（spec §0），所以这里不放保存按钮，
//     而是在头部明确写「只读」——不置灰、不给假承诺
//   - 不做语法高亮：本期判据是「点右侧文件能在中间打开」，高亮不影响这个判断，
//     而引一个高亮库会把包体和首屏都拖下去
//   - 不缓存。切走再切回来重新取一次，代价小于「文件已变但页面还是旧的」
//
// 错误处理：agentd 的中文错误原文原样透传（诚实展示纪律），不吞成「操作失败」。
import { useEffect, useState } from 'react'
import { fetchWorkspaceFile } from '../../api/client'
import { errorMessage } from '../lib/format'
import type { BaseDir } from './useWorkbench'

export function FileTab({ base, rel }: { base: BaseDir; rel: string }) {
  const [content, setContent] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    // cancelled 防止「快速连点两个文件」时先发的请求后到，把后选的内容盖掉
    let cancelled = false
    setContent(null)
    setError(null)
    fetchWorkspaceFile(base.path, rel, base.machine || undefined)
      .then((r) => {
        if (!cancelled) setContent(r.content)
      })
      .catch((err) => {
        if (!cancelled) setError(errorMessage(err))
      })
    return () => {
      cancelled = true
    }
  }, [base.path, base.machine, rel])

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-muted-foreground">
        <span className="truncate font-mono text-foreground">{rel}</span>
        <span className="ml-auto shrink-0 rounded border px-1.5 py-0.5">只读</span>
      </div>
      <div className="min-h-0 flex-1 overflow-auto">
        {error !== null ? (
          <p className="p-4 text-sm text-destructive">{error}</p>
        ) : content === null ? (
          <p className="p-4 text-sm text-muted-foreground">正在读取 {rel}…</p>
        ) : (
          <pre className="p-4 font-mono text-xs leading-relaxed whitespace-pre-wrap">{content}</pre>
        )}
      </div>
    </div>
  )
}
