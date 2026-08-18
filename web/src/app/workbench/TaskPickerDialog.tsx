// TaskPickerDialog —— 给当前 tab 选一个任务打开（spec §2）。
//
// 职责：列出当前基准所属项目的全部任务（跨机器、含已结束），带搜索，
// 选中后把 taskId 抛给调用方。
//
// 边界：
//   - 不自己开 tab：选中即回调，是 setContent 还是 open 由调用方决定
//   - 不发任何请求：任务来自已有的 2.5s 任务流，项目归属来自已有的项目树
//   - 不做筛选条、不分状态栏——那是看板的形态。这里是「给这个 tab 选一个」，
//     一个搜索框加一个列表就够了
//
// 为什么不复用看板弹层：看板是全屏、按状态分栏、带筛选条的**纵览**形态；
// 把「我只是想在这个 tab 里开个任务」变成一次全屏导航是不对等的交换。
// 两者都能开 TUI，但意图不同（spec §2.1）。
import { useEffect, useMemo, useState } from 'react'
import { Search } from 'lucide-react'
import type { ProjectTreeResp, Task } from '../../api/types'
import { StateDot } from '../board/StateDot'
import { stateTone } from '../board/columns'
import type { BaseDir } from './useWorkbench'

export interface TaskPickerDialogProps {
  open: boolean
  base: BaseDir
  tree: ProjectTreeResp | null
  tasks: Task[]
  onPick: (taskId: string) => void
  onClose: () => void
}

// TERMINAL_STATES 是「已结束」的判据。
//
// 与看板的分栏口径共用同一批字符串——一个任务在看板上归「完成」、在这里却
// 出现在「进行中」，两个面就自相矛盾了。看板当前的终态是 completed / failed。
const TERMINAL_STATES = new Set(['completed', 'failed'])

// isTerminalState 判断一个任务状态是否终态。
export function isTerminalState(state: string): boolean {
  return TERMINAL_STATES.has(state)
}

// projectIdOfBase 在项目树上反查基准目录所属的项目 id；查不到返回 null。
//
// 为什么不用 base.projectName：登记名只在一台机器内唯一，同一个项目在两台机器
// 上可以叫不同的名字（proto 的 ProjectNode.Name 注释写明了这条）。project_id
// 才是跨机同一的身份。
//
// 返回 null 的两种情形都是真实的：树还没加载完，或这个目录已经不在树上
// （工作树被删但 tab 还开着）。调用方应显示空态而不是当异常处理。
export function projectIdOfBase(tree: ProjectTreeResp | null, base: BaseDir): string | null {
  if (tree === null) return null
  for (const p of tree.projects) {
    for (const loc of p.locations) {
      if (loc.workspaces.some((ws) => ws.path === base.path)) return p.project_id
    }
  }
  return null
}

// dirLabelOfTask 给一行任务配一个能认人的目录短名。
//
// 这个弹层存在的理由就是「打开**别的分支**的任务」，不显示分支等于让用户在
// 一堆同名任务里猜。work_dir 为空（原地模式）时说「主目录」，不编一个假分支名。
function dirLabelOfTask(t: Task): string {
  if (t.branch !== '') return t.branch
  if (t.work_dir === '') return '主目录'
  const seg = t.work_dir.split('/').filter(Boolean)
  return seg.length > 0 ? seg[seg.length - 1] : t.work_dir
}

// taskName 与左栏同一口径：名字 → 计划摘要 → 兜底。
function taskName(t: Task): string {
  return t.name || t.plan_summary || '（无名称）'
}

// TaskPickerDialog 渲染任务选择弹层。
//
// 参数：props.open 控制显隐；base/tree 确定项目范围；tasks 是任务流；onPick/onClose
// 分别报告选择与关闭动作。
// 返回：弹层节点；open=false 时返回 null。
export function TaskPickerDialog({ open, base, tree, tasks, onPick, onClose }: TaskPickerDialogProps) {
  const [query, setQuery] = useState('')
  // doneOpen 记「已结束那一段展开了没」。默认收起：历史堆积（实测单机 60 条）
  // 会把正在做的活挤出视口，这与左栏「已结束」分组默认收起是同一条理由
  const [doneOpen, setDoneOpen] = useState(false)
  const [selectedIndex, setSelectedIndex] = useState(0)

  // 每次重新打开都回到初始态：上次搜的词留着会让人以为「这个项目只有这几个任务」
  useEffect(() => {
    if (open) {
      setQuery('')
      setDoneOpen(false)
      setSelectedIndex(0)
    }
  }, [open])

  // Esc 关闭，↑↓ 移动选中项，Enter 确认。挂 window 而不是容器：弹层打开时它是
  // 唯一的交互焦点，不存在「该归谁」的竞争（与 BlankTab 的 ⌘T 不同，那里可能
  // 有两个面板同时在屏上）。
  useEffect(() => {
    if (!open) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        onClose()
        return
      }
      if (e.key === 'ArrowDown') {
        e.preventDefault()
        setSelectedIndex((i) => Math.min(i + 1, selectable.length - 1))
        return
      }
      if (e.key === 'ArrowUp') {
        e.preventDefault()
        setSelectedIndex((i) => Math.max(i - 1, 0))
        return
      }
      if (e.key === 'Enter') {
        const selected = selectable[selectedIndex]
        if (selected) {
          e.preventDefault()
          onPick(selected.id)
        }
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  })

  const projectId = useMemo(() => projectIdOfBase(tree, base), [tree, base])

  const { live, done } = useMemo(() => {
    const q = query.trim().toLowerCase()
    const mine = tasks.filter((t) => projectId !== null && t.project_id === projectId)
    const hit = q === '' ? mine : mine.filter((t) => taskName(t).toLowerCase().includes(q))
    // 按 updated_at 倒序：最近动过的最可能是要找的那个
    const byRecent = (a: Task, b: Task) => (a.updated_at < b.updated_at ? 1 : a.updated_at > b.updated_at ? -1 : 0)
    return {
      live: hit.filter((t) => !isTerminalState(t.state)).sort(byRecent),
      done: hit.filter((t) => isTerminalState(t.state)).sort(byRecent),
    }
  }, [tasks, projectId, query])

  const selectable = doneOpen ? [...live, ...done] : live

  if (!open) return null

  const row = (t: Task, index: number) => (
    <li key={t.id}>
      <button
        type="button"
        aria-current={selectedIndex === index ? 'true' : undefined}
        onMouseEnter={() => setSelectedIndex(index)}
        onClick={() => onPick(t.id)}
        className={`flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left text-[13px] hover:bg-accent ${
          selectedIndex === index ? 'bg-accent' : ''
        }`}
      >
        <StateDot tone={stateTone(t.state)} />
        <span className="min-w-0 flex-1 truncate">{taskName(t)}</span>
        <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{dirLabelOfTask(t)}</span>
      </button>
    </li>
  )

  return (
    // z-50 与既有 Overlay 同层：浮窗（z-40）应当被盖住
    <div
      className="fixed inset-0 z-50 flex items-start justify-center bg-black/40 pt-[12vh]"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-label="选择要打开的任务"
        className="flex max-h-[60vh] w-[min(560px,90vw)] flex-col overflow-hidden rounded-lg border bg-background shadow-xl"
        // 点内容区不该关掉弹层——遮罩上那次 onClick 会冒泡上来
        onClick={(e) => e.stopPropagation()}
      >
        <label className="flex shrink-0 items-center gap-2 border-b px-3 py-2">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <input
            autoFocus
            value={query}
            onChange={(e) => {
              setQuery(e.target.value)
              setSelectedIndex(0)
            }}
            placeholder="搜索任务"
            className="min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          />
        </label>
        <div className="min-h-0 flex-1 overflow-y-auto p-1.5">
          {live.length === 0 && done.length === 0 ? (
            // 空态必须有话说：一片空白会被当成加载失败
            <p className="px-2 py-6 text-center text-sm text-muted-foreground">
              {projectId === null ? '这个目录还没归到任何项目下。' : '这个项目下还没有任务。'}
            </p>
          ) : (
            <>
              <ul>{live.map((t, i) => row(t, i))}</ul>
              {done.length > 0 && (
                <>
                  <button
                    type="button"
                    aria-expanded={doneOpen}
                    onClick={() => {
                      setDoneOpen((v) => !v)
                      setSelectedIndex(0)
                    }}
                    className="mt-1 w-full px-2 py-1 text-left text-[11px] font-medium uppercase tracking-wide text-muted-foreground hover:text-foreground"
                  >
                    已结束 {done.length}
                  </button>
                  {doneOpen && <ul>{done.map((t, i) => row(t, live.length + i))}</ul>}
                </>
              )}
            </>
          )}
        </div>
      </div>
    </div>
  )
}
