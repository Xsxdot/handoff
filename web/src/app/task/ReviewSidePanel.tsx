// ReviewSidePanel —— 审阅取证右滑栏（ReviewPanel 的继任者）。
//
// 职责：改动（DiffView + 基准下拉）/ 跑命令 / 读文件 三个子 tab；栏内自滚。
// 边界：
//   - 全部只读取证，不改任务状态（与 ReviewPanel 相同）
//   - 分支接口失败退化为仅「自动推导」，diff 不受影响（spec §6.2）
//   - 何时可见由 TuiTab 决定（waiting_review），本组件不判状态机
import { useCallback, useEffect, useState } from 'react'
import type { BranchesResult, RunResult } from '../../api/types'
import { fetchTaskBranches, fetchTaskDiff, fetchTaskFile, runTaskCommand } from '../../api/client'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { errorMessage } from '../lib/format'
import { cn } from '@/lib/utils'
import { DiffView } from './DiffView'

type ReviewTab = 'diff' | 'run' | 'file'

// autoBaseHint 返回「自动推导」项的括注文本。
//
// 为什么要分三态（B65）：diff 的缺省基准优先用任务自己的 base_commit，
// 只有它为空才按仓库推导。若这里恒显示推导出的分支名，控制台会在有任务基线时
// 显示一个 diff 根本没用的值——一个当场可见的谎。
function autoBaseHint(branches: BranchesResult | null): string {
  if (!branches) return ''
  if (branches.task_base) return `（任务基线 ${branches.task_base.slice(0, 8)}）`
  if (branches.default) return `（${branches.default}）`
  return ''
}

// ReviewSidePanel 渲染审阅栏。onClose 由页头的开关与栏内 ✕ 共用。
export function ReviewSidePanel({ taskId, onClose }: { taskId: string; onClose: () => void }) {
  const [tab, setTab] = useState<ReviewTab>('diff')
  return (
    <aside className="flex h-full min-h-0 w-[44%] min-w-[400px] max-w-[620px] flex-col border-l bg-background">
      <div className="flex items-center gap-1.5 border-b px-3 py-2">
        {(['diff', 'run', 'file'] as const).map((t) => (
          <button
            key={t}
            type="button"
            onClick={() => setTab(t)}
            className={cn(
              'rounded-md px-3 py-1 text-xs',
              tab === t ? 'bg-muted font-medium' : 'text-muted-foreground hover:bg-muted/50',
            )}
          >
            {t === 'diff' ? '改动' : t === 'run' ? '跑命令' : '读文件'}
          </button>
        ))}
        <button type="button" onClick={onClose} title="收起" className="ml-auto px-1 text-muted-foreground hover:text-foreground">✕</button>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-3">
        {tab === 'diff' && <DiffSection taskId={taskId} />}
        {tab === 'run' && <RunSection taskId={taskId} />}
        {tab === 'file' && <FileSection taskId={taskId} />}
      </div>
    </aside>
  )
}

// DiffSection：基准下拉（fetchTaskBranches，失败退化）+ DiffView。
function DiffSection({ taskId }: { taskId: string }) {
  const [branches, setBranches] = useState<BranchesResult | null>(null)
  const [base, setBase] = useState('')
  const [diff, setDiff] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const load = useCallback(async (b: string) => {
    setLoading(true)
    setError(null)
    try {
      const r = await fetchTaskDiff(taskId, b || undefined)
      setDiff(r.diff)
    } catch (err) {
      setError(errorMessage(err))
    } finally {
      setLoading(false)
    }
  }, [taskId])

  // 进栏自动加载默认基准的 diff；分支列表失败不拦 diff（退化下拉）
  useEffect(() => {
    setDiff(null)
    setBase('')
    void load('')
    fetchTaskBranches(taskId)
      .then(setBranches)
      .catch(() => setBranches(null))
  }, [taskId, load])

  return (
    <div className="flex flex-col gap-2">
      <label className="flex items-center gap-2 text-xs text-muted-foreground">
        基准
        <select
          className="flex-1 rounded-md border border-input bg-background px-2 py-1.5 font-mono text-xs"
          value={base}
          onChange={(e) => { setBase(e.target.value); void load(e.target.value) }}
        >
          <option value="">自动推导{autoBaseHint(branches)}</option>
          {branches?.branches.map((b) => <option key={b} value={b}>{b}</option>)}
        </select>
        {loading && <span>加载中…</span>}
      </label>
      {error && <p role="alert" className="break-words text-sm text-destructive">{error}</p>}
      {diff !== null && <DiffView text={diff} />}
    </div>
  )
}

// ResultPre 是跑命令/读文件共用的原文容器：保留空白，不把审阅证据折叠成摘要。
function ResultPre({ text, placeholder }: { text: string; placeholder: string }) {
  return (
    <pre className="max-h-80 overflow-auto rounded-md bg-muted/30 p-3 font-mono text-xs leading-relaxed">
      {text === '' ? <span className="text-muted-foreground">{placeholder}</span> : text}
    </pre>
  )
}

// inputCls 与 ReviewPanel 保持一致，确保三个审阅入口的输入控件行为统一。
const inputCls = 'flex-1 rounded-md border border-input bg-background px-2 py-1.5 font-mono text-xs focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring'

// RunSection 执行任务工作树内的审阅命令；非零退出仍展示结果，不静默当请求失败。
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
          onKeyDown={(e) => { if (e.key === 'Enter') void run() }}
        />
        <Button size="sm" disabled={loading || cmd.trim() === ''} onClick={() => void run()}>
          {loading ? '执行中…' : '运行'}
        </Button>
      </div>
      <p className="text-xs text-muted-foreground">
        在任务工作树执行（非零退出也是 200，退出码在响应里；10 分钟超时退出码 124）。
      </p>
      {error && <p role="alert" className="break-words text-sm text-destructive">{error}</p>}
      {result && (
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <Badge variant={result.exit_code === 0 ? 'secondary' : 'destructive'}>退出码 {result.exit_code}</Badge>
            {result.exit_code === 124 && <span className="text-xs text-muted-foreground">（命令超时被终止）</span>}
          </div>
          <ResultPre text={result.stdout} placeholder="（无输出）" />
        </div>
      )}
    </div>
  )
}

// FileSection 读取任务工作树内的相对路径；错误原文透出，空文件也明确渲染。
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
      setContent((await fetchTaskFile(taskId, path)).content)
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
          onKeyDown={(e) => { if (e.key === 'Enter') void read() }}
        />
        <Button size="sm" disabled={loading || path.trim() === ''} onClick={() => void read()}>
          {loading ? '读取中…' : '读取'}
        </Button>
      </div>
      {error && <p role="alert" className="break-words text-sm text-destructive">{error}</p>}
      {content !== null && <ResultPre text={content} placeholder="（空文件）" />}
    </div>
  )
}
