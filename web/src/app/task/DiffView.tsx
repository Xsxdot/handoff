// DiffView —— diff 的按文件分组着色渲染。
//
// 职责：parseUnifiedDiff 的产物 → 可折叠文件组（±统计 + 行级着色）+ trailer。
// 边界：解析失败（null）整体回退裸 <pre>——diff 是审阅核心证据，绝不吞内容。
import { useMemo, useState } from 'react'
import { cn } from '@/lib/utils'
import { type FileDiff, parseUnifiedDiff } from './diff'

// LINE_CLS 是四种行的着色。绿/红沿用项目 diff 惯例色阶，深浅模式各自可读。
const LINE_CLS = {
  add: 'bg-green-500/10 text-green-800 dark:text-green-300',
  del: 'bg-red-500/10 text-red-800 dark:text-red-300',
  hunk: 'bg-muted text-muted-foreground',
  ctx: '',
} as const

// FileGroup 渲染一个文件的可折叠改动组；默认第一组展开由父级控制。
function FileGroup({ file, defaultOpen }: { file: FileDiff; defaultOpen: boolean }) {
  const [open, setOpen] = useState(defaultOpen)
  return (
    <div className="mb-2 overflow-hidden rounded-lg border">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="flex w-full items-center gap-2 bg-muted/40 px-2.5 py-1.5 text-left font-mono text-xs hover:bg-muted"
      >
        <span className="min-w-0 flex-1 truncate">{file.path}</span>
        <span className="shrink-0 text-green-700 dark:text-green-400">+{file.adds}</span>
        <span className="shrink-0 text-red-700 dark:text-red-400">−{file.dels}</span>
      </button>
      {open && (
        <div className="overflow-x-auto font-mono text-xs leading-normal">
          {file.lines.map((l, i) => (
            <div key={i} className={cn('whitespace-pre px-2.5', LINE_CLS[l.kind])}>{l.text}</div>
          ))}
        </div>
      )}
    </div>
  )
}

// DiffView 渲染整份 diff 文本。text 为 fetchTaskDiff 返回的原文。
export function DiffView({ text }: { text: string }) {
  const parsed = useMemo(() => parseUnifiedDiff(text), [text])
  if (text.trim() === '') {
    return <p className="text-sm text-muted-foreground">没有差异（分支与基准一致）。</p>
  }
  if (parsed === null) {
    // 解析不出：整体回退裸文本，一个字都不能丢
    return <pre className="overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/30 p-3 font-mono text-xs leading-relaxed">{text}</pre>
  }
  const totalAdds = parsed.files.reduce((n, f) => n + f.adds, 0)
  const totalDels = parsed.files.reduce((n, f) => n + f.dels, 0)
  return (
    <div>
      <p className="mb-2 text-xs text-muted-foreground">
        {parsed.files.length} 个文件
        {' · '}<span className="text-green-700 dark:text-green-400">+{totalAdds}</span>
        {' '}<span className="text-red-700 dark:text-red-400">−{totalDels}</span>
      </p>
      {parsed.files.map((f, i) => (
        <FileGroup key={f.path + i} file={f} defaultOpen={i === 0} />
      ))}
      {parsed.trailer !== '' && (
        <div className="mt-2">
          <p className="mb-1 text-xs text-muted-foreground">提交列表</p>
          <pre className="overflow-auto rounded-md bg-muted/30 p-2.5 font-mono text-xs">{parsed.trailer}</pre>
        </div>
      )}
    </div>
  )
}
