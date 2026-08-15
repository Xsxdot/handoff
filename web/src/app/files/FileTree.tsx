// FileTree —— 右栏文件树（spec §4）。
//
// 职责：
//   - 头部（文件 / 刷新 / 折叠全部）+ 搜索框 + 根标题 + 可展开的树体
//   - 点文件 → 回调相对路径，由中央开 file tab
//   - 「相对基线已改动」的 M 角标
//
// 边界：
//   - 只在选中目录时渲染（挂不挂由 Shell 决定）
//   - 只读：不发写请求，不提供新建/重命名/删除
//   - 搜索是**对已列举内容的前端过滤**，不发请求、不展开未展开的层
//
// 角标语义（不得含糊）：数据来自 `handoff diff` = `git diff base...HEAD`，只反映
// 已提交的改动。tooltip 写「相对基线已改动」，不写「工作区已修改」——后者是
// git status 的语义，这里给不出来。
import { useEffect, useState } from 'react'
import { ChevronDown, ChevronRight, File, FolderClosed, FolderOpen, RefreshCw } from 'lucide-react'
import type { DirEntry } from '../../api/types'
import type { BaseDir } from '../workbench/useWorkbench'
import { useChangedFiles } from './changedFiles'
import { useDirEntries, type DirEntriesApi } from './useDirEntries'

// CHANGED_TITLE 是 M 角标的 tooltip。措辞是 spec §4 的硬要求，不要改写。
const CHANGED_TITLE = '相对基线已改动（git diff base...HEAD，不含工作区未提交的编辑）'

export interface FileTreeProps {
  base: BaseDir
  // taskId 是这个目录上挂着的任务；为 null 表示没有任务，此时不显示角标
  taskId: string | null
  onOpenFile: (rel: string) => void
}

export function FileTree({ base, taskId, onOpenFile }: FileTreeProps) {
  const dirs = useDirEntries(base)
  const changed = useChangedFiles(taskId)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [query, setQuery] = useState('')

  // 挂载与换目录时取根层。子层在展开时由 Row 显式 ensure，不在渲染期取数——
  // 渲染期调用会 setState 的函数容易被 lint 判为副作用，也难推理
  useEffect(() => {
    setExpanded(new Set())
    dirs.ensure('')
  }, [base.key, dirs])

  const toggle = (rel: string) => {
    dirs.ensure(rel)
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(rel)) next.delete(rel)
      else next.add(rel)
      return next
    })
  }

  return (
    <aside className="flex h-full min-h-0 flex-col border-l bg-background">
      <div className="flex items-center gap-1 border-b px-3 py-2">
        <span className="text-sm font-medium">文件</span>
        <div className="ml-auto flex items-center gap-0.5">
          <button
            type="button"
            aria-label="刷新"
            onClick={dirs.refresh}
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <RefreshCw className="size-3.5" />
          </button>
          <button
            type="button"
            aria-label="折叠全部"
            onClick={() => setExpanded(new Set())}
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <ChevronDown className="size-3.5" />
          </button>
        </div>
      </div>

      <div className="border-b px-3 py-2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索文件…"
          className="w-full rounded-md border px-2 py-1 text-xs"
        />
      </div>

      <div className="min-h-0 flex-1 overflow-auto py-1">
        <p className="truncate px-3 py-1 font-mono text-[11px] text-muted-foreground">{base.label}</p>
        <DirLevel
          dirs={dirs}
          rel=""
          depth={0}
          expanded={expanded}
          onToggle={toggle}
          onOpenFile={onOpenFile}
          changed={changed}
          query={query.trim().toLowerCase()}
        />
      </div>
    </aside>
  )
}

interface LevelProps {
  dirs: DirEntriesApi
  rel: string
  depth: number
  expanded: Set<string>
  onToggle: (rel: string) => void
  onOpenFile: (rel: string) => void
  changed: Set<string>
  query: string
}

// DirLevel 渲染一层目录：加载中 / 该层失败 / 条目列表三种形态。
function DirLevel(props: LevelProps) {
  const { dirs, rel, depth, query } = props
  const entries = dirs.entriesOf(rel)
  const error = dirs.errorOf(rel)
  const pad = { paddingLeft: `${12 + depth * 12}px` }

  if (error !== undefined) {
    return (
      <p className="py-1 pr-3 text-[11px] text-destructive" style={pad}>
        {error}
      </p>
    )
  }
  if (entries === undefined) {
    return (
      <p className="py-1 pr-3 text-[11px] text-muted-foreground" style={pad}>
        正在列举…
      </p>
    )
  }

  // 前端过滤：目录始终保留（否则通往匹配文件的展开路径会被过滤断掉），
  // 文件按名字匹配
  const shown = query ? entries.filter((e) => e.is_dir || e.name.toLowerCase().includes(query)) : entries

  return (
    <ul>
      {shown.map((e) => (
        <Row key={e.name} entry={e} pad={pad} {...props} />
      ))}
    </ul>
  )
}

// Row 渲染一个条目。目录行负责拼接子层的相对路径并递归渲染 DirLevel。
function Row({ entry, pad, ...rest }: LevelProps & { entry: DirEntry; pad: { paddingLeft: string } }) {
  const { rel: parentRel, depth, expanded, onToggle, onOpenFile, changed } = rest
  const rel = parentRel ? `${parentRel}/${entry.name}` : entry.name
  const open = expanded.has(rel)

  if (entry.is_dir) {
    return (
      <li>
        <button
          type="button"
          onClick={() => onToggle(rel)}
          className="flex w-full items-center gap-1.5 py-0.5 pr-3 text-left text-xs hover:bg-accent"
          style={pad}
        >
          {open ? <ChevronDown className="size-3 shrink-0" /> : <ChevronRight className="size-3 shrink-0" />}
          {open ? (
            <FolderOpen data-testid="dir-icon" className="size-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <FolderClosed data-testid="dir-icon" className="size-3.5 shrink-0 text-muted-foreground" />
          )}
          <span className="truncate">{entry.name}</span>
        </button>
        {open && <DirLevel {...rest} rel={rel} depth={depth + 1} />}
      </li>
    )
  }

  return (
    <li>
      <button
        type="button"
        onClick={() => onOpenFile(rel)}
        className="flex w-full items-center gap-1.5 py-0.5 pr-3 text-left text-xs hover:bg-accent"
        style={pad}
      >
        {/* 文件着强调色、文件夹保持灰——原型正是靠这个对比让右栏不显得平
            （.file-row > svg:nth-of-type(2) { color: var(--blue) }） */}
        <File data-testid="file-icon" className="ml-3 size-3.5 shrink-0 text-file-accent" />
        <span className="truncate">{entry.name}</span>
        {/* 用状态 token 而非裸 amber：同一个界面里两种橙看起来像 bug
            ——这条规矩 ProjectTree 的工单角标已经在守，这里补齐 */}
        {changed.has(rel) && (
          <span title={CHANGED_TITLE} className="ml-auto shrink-0 font-mono text-[10px] text-state-intervention-text">
            M
          </span>
        )}
      </button>
    </li>
  )
}
