// ReviewPanel —— 审阅取证：改动（diff）/ 跑命令（run）/ 读文件（file）。
//
// 数据源（全部只读，不改变任务状态）：
//   - diff：GET /api/tasks/{id}/diff[?base=]
//   - run： POST /api/tasks/{id}/run {cmd}，返回 {stdout, exit_code}；
//     非零退出也是 200（退出码在响应体里，10 分钟超时退出码 124）
//   - file：GET /api/tasks/{id}/file?path=
//
// 展示纪律：diff 用等宽字体直接展示，**不引入语法高亮/diff 渲染库**（那是 W4）；
// 所有失败把 agentd 的错误原文透出（「任务不存在」「状态不允许」等带着解法）。
import { useCallback, useEffect, useState } from 'react'
import { FileText, Play, Scale } from 'lucide-react'
import type { RunResult } from '../../api/types'
import { fetchTaskDiff, fetchTaskFile, runTaskCommand } from '../../api/client'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { errorMessage } from '../lib/format'
import { cn } from '@/lib/utils'

type ReviewTab = 'diff' | 'run' | 'file'

const TABS: { id: ReviewTab; label: string; icon: typeof Scale }[] = [
  { id: 'diff', label: '改动', icon: Scale },
  { id: 'run', label: '跑命令', icon: Play },
  { id: 'file', label: '读文件', icon: FileText },
]

export function ReviewPanel({ taskId }: { taskId: string }) {
  const [tab, setTab] = useState<ReviewTab>('diff')

  return (
    <section className="flex flex-col gap-2 rounded-lg border bg-background p-4">
      <h2 className="flex items-center gap-2 text-sm font-medium">
        <Scale className="size-4" />
        审阅取证
      </h2>
      <div className="flex gap-1">
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            onClick={() => setTab(t.id)}
            className={cn(
              'inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-colors',
              tab === t.id ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-accent',
            )}
          >
            <t.icon className="size-3.5" />
            {t.label}
          </button>
        ))}
      </div>
      {tab === 'diff' && <DiffSection taskId={taskId} />}
      {tab === 'run' && <RunSection taskId={taskId} />}
      {tab === 'file' && <FileSection taskId={taskId} />}
    </section>
  )
}

// pre 是统一的结果展示容器：等宽、可横向滚动、不吞空白。
function ResultPre({ text, placeholder }: { text: string; placeholder: string }) {
  return (
    <pre className="max-h-80 overflow-auto rounded-md bg-muted/30 p-3 font-mono text-xs leading-relaxed">
      {text === '' ? <span className="text-muted-foreground">{placeholder}</span> : text}
    </pre>
  )
}

// input 类名：与其他表单输入一致。
const inputCls =
  'flex-1 rounded-md border border-input bg-background px-2 py-1.5 font-mono text-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

function DiffSection({ taskId }: { taskId: string }) {
  const [base, setBase] = useState('')
  const [diff, setDiff] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(
    async (b: string) => {
      setLoading(true)
      setError(null)
      try {
        const r = await fetchTaskDiff(taskId, b.trim() || undefined)
        setDiff(r.diff)
      } catch (err) {
        setError(errorMessage(err))
      } finally {
        setLoading(false)
      }
    },
    [taskId],
  )

  // 进面板自动加载一次（默认基准）；换任务重载
  useEffect(() => {
    setDiff(null)
    void load('')
  }, [load])

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <input
          className={inputCls}
          placeholder="基准分支（默认按仓库默认分支推导）"
          value={base}
          onChange={(e) => setBase(e.target.value)}
        />
        <Button size="sm" variant="outline" disabled={loading} onClick={() => void load(base)}>
          {loading ? '加载中…' : '加载'}
        </Button>
      </div>
      {error && (
        <p role="alert" className="break-words text-sm text-destructive">
          {error}
        </p>
      )}
      {diff !== null && (
        <ResultPre text={diff} placeholder="没有差异（分支与基准一致）。" />
      )}
    </div>
  )
}

function RunSection({ taskId }: { taskId: string }) {
  const [cmd, setCmd] = useState('')
  const [result, setResult] = useState<RunResult | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const run = async () => {
    if (cmd.trim() === '') return
    setLoading(true)
    setError(null)
    try {
      setResult(await runTaskCommand(taskId, cmd))
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <input
          className={inputCls}
          placeholder="例如：go test ./..."
          value={cmd}
          onChange={(e) => setCmd(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void run()
          }}
        />
        <Button size="sm" disabled={loading || cmd.trim() === ''} onClick={() => void run()}>
          {loading ? '执行中…' : '运行'}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        在任务工作树执行（非零退出也是 200，退出码在响应里；10 分钟超时退出码 124）。
      </p>
      {error && (
        <p role="alert" className="break-words text-sm text-destructive">
          {error}
        </p>
      )}
      {result && (
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <Badge variant={result.exit_code === 0 ? 'secondary' : 'destructive'}>
              退出码 {result.exit_code}
            </Badge>
            {result.exit_code === 124 && <span className="text-xs text-muted-foreground">（命令超时被终止）</span>}
          </div>
          <ResultPre text={result.stdout} placeholder="（无输出）" />
        </div>
      )}
    </div>
  )
}

function FileSection({ taskId }: { taskId: string }) {
  const [path, setPath] = useState('')
  const [content, setContent] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const read = async () => {
    if (path.trim() === '') return
    setLoading(true)
    setError(null)
    try {
      const r = await fetchTaskFile(taskId, path)
      setContent(r.content)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="flex flex-col gap-2">
      <div className="flex flex-wrap items-center gap-2">
        <input
          className={inputCls}
          placeholder="相对仓库根路径，如 internal/proto/proto.go"
          value={path}
          onChange={(e) => setPath(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter') void read()
          }}
        />
        <Button size="sm" disabled={loading || path.trim() === ''} onClick={() => void read()}>
          {loading ? '读取中…' : '读取'}
        </Button>
      </div>
      {error && (
        <p role="alert" className="break-words text-sm text-destructive">
          {error}
        </p>
      )}
      {content !== null && <ResultPre text={content} placeholder="（空文件）" />}
    </div>
  )
}
