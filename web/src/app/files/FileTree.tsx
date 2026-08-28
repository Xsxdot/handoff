// FileTree —— 右栏文件树（spec §4）。
//
// 职责：
//   - 头部（文件 / 刷新）+ 搜索框 + 根标题 + 可展开的树体
//   - 点文件 → 回调相对路径，由中央开 file tab
//   - 右键菜单：新建/改名/复制/删除/在文件夹中查找/在终端中打开，外加
//     复制路径、复制相对路径、折叠文件夹三条纯前端项（spec §6.2）
//   - 「相对基线已改动」的 M 角标
//
// 边界：
//   - 只在选中目录时渲染（挂不挂由 Shell 决定）
//   - 写操作（建/删/改名/复制）只在**已探测到的工作树内**：所有请求都带
//     path 白名单参数，服务端错误原文透传到弹层/面板，不吞成「操作失败」
//   - 搜索是两层：顶部输入框是**对已列举内容的前端过滤**（不发请求）；
//     右键「在文件夹中查找」才是对服务端发请求的内容搜索，结果进独立面板
//   - Reveal in Finder 只在「浏览器与 agentd 同在一台 macOS 上」时可点，
//     其余三种情形各给一条不同的置灰理由（B108，spec §4.3）。远程那条是
//     结构性的：在 mac-02 上 open -R 会在**没人看着**的桌面上弹窗
//
// 角标语义（不得含糊）：数据来自 `handoff diff` = `git diff base...HEAD`，只反映
// 已提交的改动。tooltip 写「相对基线已改动」，不写「工作区已修改」——后者是
// git status 的语义，这里给不出来。
import { useEffect, useRef, useState } from 'react'
import {
  ChevronDown, ChevronRight, CircleSlash, FileText, FolderClosed, FolderOpen, RefreshCw, Search, X,
} from 'lucide-react'
import {
  copyWorkspaceEntry,
  createWorkspaceEntry,
  deleteWorkspaceEntry,
  renameWorkspaceEntry,
  revealInFinder,
  searchWorkspace,
} from '../../api/client'
import type { DirEntry, SearchHit } from '../../api/types'
import type { BaseDir } from '../workbench/useWorkbench'
import { copyToClipboard } from '../lib/clipboard'
import { errorMessage } from '../lib/format'
import { ContextMenu, type ContextMenuEntry } from '../shared/ContextMenu'
import { useChangedFiles, type ChangeStatus } from './changedFiles'
import { useDirEntries, type DirEntriesApi } from './useDirEntries'
import { EntryNameDialog } from './EntryNameDialog'
import { cn } from '@/lib/utils'
import { DeleteEntryDialog } from './DeleteEntryDialog'

// CHANGED_TITLE 是改动角标 tooltip 的**后半句**。措辞是 spec §4 的硬要求，
// 不要改写；类别词只作为前缀拼在它前面（见 CHANGE_STYLE.title）。
const CHANGED_TITLE = '相对基线已改动（git diff base...HEAD，不含工作区未提交的编辑）'

// IGNORED_NAME / IGNORED_TITLE 是「被 .gitignore 排除」的弱化样式与说明。
//
// 斜体 + 半透明而不是直接藏起来：构建产物、缓存目录是**真实存在**的东西，
// 偶尔要点进去看一眼；藏掉等于把文件树和磁盘上的实情对不上。弱化到一眼扫过
// 时不占注意力，就够了。
const IGNORED_NAME = 'italic text-muted-foreground/70'
const IGNORED_TITLE = '被 .gitignore 排除（git check-ignore 判定，不归 git 跟踪）'

// IgnoredMark 是行尾那个 ⊘。title 挂在外层 span 而不是图标上——lucide 的
// 组件不接受 title 属性，而 tooltip 是这个符号唯一的解释来源，不能省。
function IgnoredMark() {
  return (
    <span title={IGNORED_TITLE} className="ml-auto flex shrink-0 items-center">
      <CircleSlash className="size-3 text-muted-foreground/60" />
    </span>
  )
}

// CHANGE_STYLE 把改动类别映射成「文件名颜色 + 角标字母 + tooltip 前缀」。
//
// 为什么文件名也着色而不是只留一个角标：角标钉在行尾，长文件名截断后它离名字
// 十万八千里，扫一列名字看不出哪些是这次改的。颜色让「改没改、是新增还是修改」
// 落在名字本身上。
//
// 配色仍走状态 token（新增=绿 / 修改=琥珀 / 删除=红），不引裸色值——同一个界面
// 里两种绿或两种橙看起来像 bug，这条规矩全仓一致。
const CHANGE_STYLE: Record<ChangeStatus, { name: string; badge: string; letter: string; title: string }> = {
  added: { name: 'text-state-active', badge: 'text-state-active', letter: 'A', title: `新增文件 · ${CHANGED_TITLE}` },
  modified: {
    name: 'text-state-intervention-text',
    badge: 'text-state-intervention-text',
    letter: 'M',
    title: CHANGED_TITLE,
  },
  // 删除的文件通常已不在列举结果里，这一档只在「diff 说删了、磁盘上还在」
  // （工作区未提交的恢复）时出现——真出现时必须看得出来，不能悄悄当未改动
  deleted: { name: 'text-destructive', badge: 'text-destructive', letter: 'D', title: `已删除 · ${CHANGED_TITLE}` },
}

export interface FileTreeProps {
  base: BaseDir
  // taskId 是这个目录上挂着的任务；为 null 表示没有任务，此时不显示角标
  taskId: string | null
  onOpenFile: (rel: string) => void
  // onOpenTerminal 让「在终端中打开」走既有的建终端能力，传的是子目录 rel
  //（空串 = 工作树根）。
  onOpenTerminal: (rel: string) => void
  // revealSupported 是本机 agentd 的「在访达中显示」平台能力位，三态。
  // null = 对端没上报，此时**放行**而不是禁用（见 useMachineCaps 的三态纪律）。
  revealSupported: boolean | null
  // refreshKey 变化时重取根目录一层。调用方（Shell）在中央区新建文件之后
  // 递增它——不刷新的话用户刚建的文件在右栏看不见，会以为没建成。
  //
  // 用 reload('') 而不是 refresh()：新文件建在根上，只有那一层需要重取；
  // refresh 会丢掉全部已展开层的缓存，用户展开的目录会全部塌回去。
  refreshKey?: number
  // 抽屉模式的关闭入口；默认右栏不传，保持旧的固定右栏形态。
  onClose?: () => void
}

// MenuEntry 是被右键的条目：菜单项按它算 dirOf 与可用的操作集合。
interface MenuEntry {
  rel: string
  isDir: boolean
  name: string
}

// NameDlg 描述当前打开的命名弹层；dirOf 是成功后要刷新的那一层。
//   - create-*：建在 target 目录里，刷新该目录本身
//   - rename：条目改名的落点是它的父层（列表里挂着该条目的那一层）
interface NameDlg {
  mode: 'create-file' | 'create-dir' | 'rename'
  dirOf: string
  target: string
  name: string
}

interface DeleteTarget {
  rel: string
  name: string
  isDir: boolean
  dirCount?: number
}

// SearchState 是「在文件夹中查找」面板；hits 为 null 表示还没搜过。
interface SearchState {
  dirOf: string
  q: string
  hits: SearchHit[] | null
  truncated: boolean
  busy: boolean
  error: string
}

// dirOf 返回四个「文件夹类」动作的落点：目录行用自身 rel，文件行用父 rel。
function dirOf(rel: string, isDir: boolean): string {
  return isDir ? rel : rel.split('/').slice(0, -1).join('/')
}

// parentOf 返回条目所在的那一层（父目录）。对文件与目录一致——删/改名/复制
// 之后这条目在**父层**的列表里，要刷新的是那一层而不是条目自身（目录的自身
// 是它的子层，刷新它看不见被删/被改名/被复制的条目）。
function parentOf(rel: string): string {
  return rel.split('/').slice(0, -1).join('/')
}

// isLoopbackHost 判断页面自己是不是从回环加载的。
//
// 已知不解的边缘：SSH 端口转发下 localhost 指向远程 agentd，这里会误判为本机，
// Finder 开在远程桌面。无解（隧道在设计上就是要让远程看起来像本地），后果也
// 有限——弹一个窗，而且是用户自己搭的隧道。如实记着，不假装挡住了。
function isLoopbackHost(host: string): boolean {
  return host === 'localhost' || host === '127.0.0.1' || host === '::1' || host === '[::1]'
}

export function FileTree({ base, taskId, onOpenFile, onOpenTerminal, revealSupported, refreshKey, onClose }: FileTreeProps) {
  const dirs = useDirEntries(base)
  const changed = useChangedFiles(taskId)
  const firstRef = useRef(true)
  const [expanded, setExpanded] = useState<Set<string>>(() => new Set())
  const [query, setQuery] = useState('')
  // menu：右键菜单当前打开的坐标与被右键条目
  const [menu, setMenu] = useState<{ x: number; y: number; entry: MenuEntry } | null>(null)
  const [nameDlg, setNameDlg] = useState<NameDlg | null>(null)
  const [nameBusy, setNameBusy] = useState(false)
  const [nameError, setNameError] = useState('')
  const [deleteTarget, setDeleteTarget] = useState<DeleteTarget | null>(null)
  const [deleteBusy, setDeleteBusy] = useState(false)
  const [deleteError, setDeleteError] = useState('')
  const [search, setSearch] = useState<SearchState | null>(null)
  // opError 承接没有弹层承载的操作错误（复制），显示成树顶一行可关闭的原文
  const [opError, setOpError] = useState('')

  // 挂载与换目录时取根层，并清掉所有弹层与右键态——旧目录的数据不该残留。
  // 子层在展开时由 Row 显式 ensure，不在渲染期取数。
  useEffect(() => {
    setExpanded(new Set())
    setMenu(null)
    setNameDlg(null)
    setDeleteTarget(null)
    setSearch(null)
    setOpError('')
    dirs.ensure('')
  }, [base.key, dirs])

  useEffect(() => {
    // 首次挂载不重取：那一层由树自己的 ensure 负责，这里再来一次是白打一个请求
    if (firstRef.current) {
      firstRef.current = false
      return
    }
    dirs.reload('')
  }, [refreshKey, dirs])

  const toggle = (rel: string) => {
    dirs.ensure(rel)
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(rel)) next.delete(rel)
      else next.add(rel)
      return next
    })
  }

  const onRowContextMenu = (e: React.MouseEvent, entry: DirEntry, rel: string) => {
    // 阻止浏览器原生菜单，换成这份；Shift+F10 与 ContextMenu 键也派发
    // 这个事件，键盘用户走的是同一条路
    e.preventDefault()
    let x = e.clientX
    let y = e.clientY
    // 键盘触发的右键事件坐标是 (0,0)，直接拿去菜单会钉在左上角；此时改用
    // 被右键行的 DOM 中心做落点。鼠标触发路径坐标非零，保持不变
    if (x === 0 && y === 0) {
      const rect = (e.currentTarget as HTMLElement).getBoundingClientRect()
      x = rect.left + rect.width / 2
      y = rect.top + rect.height / 2
    }
    setMenu({ x, y, entry: { rel, isDir: entry.is_dir, name: entry.name } })
  }

  // submitName 是命名弹层的提交：按模式发对应请求，成功后只刷新那一层。
  const submitName = async (name: string) => {
    if (!nameDlg) return
    setNameBusy(true)
    setNameError('')
    try {
      if (nameDlg.mode === 'rename') {
        await renameWorkspaceEntry(base.path, nameDlg.target, name, base.machine)
      } else {
        await createWorkspaceEntry(
          base.path,
          nameDlg.target,
          name,
          nameDlg.mode === 'create-file' ? 'file' : 'dir',
          base.machine,
        )
      }
      dirs.reload(nameDlg.dirOf)
      setNameDlg(null)
    } catch (err) {
      // agentd 的中文错误原文透出，不缩略成「操作失败」
      setNameError(errorMessage(err))
    } finally {
      setNameBusy(false)
    }
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    setDeleteBusy(true)
    setDeleteError('')
    try {
      await deleteWorkspaceEntry(base.path, deleteTarget.rel, base.machine)
      dirs.reload(parentOf(deleteTarget.rel))
      setDeleteTarget(null)
    } catch (err) {
      setDeleteError(errorMessage(err))
    } finally {
      setDeleteBusy(false)
    }
  }

  const copyEntry = async (rel: string) => {
    try {
      await copyWorkspaceEntry(base.path, rel, base.machine)
      dirs.reload(parentOf(rel))
    } catch (err) {
      setOpError(errorMessage(err))
    }
  }

  // revealEntry 在本机访达中显示条目。失败原文透传到 opError 面板——服务端
  // 的四条拒绝理由都写得比前端能猜的准，不要吞成「操作失败」。
  const revealEntry = async (rel: string) => {
    setMenu(null)
    try {
      await revealInFinder(base.path, rel)
    } catch (err) {
      setOpError(errorMessage(err))
    }
  }

  const openSearch = (dOf: string) => {
    setMenu(null)
    setSearch({ dirOf: dOf, q: '', hits: null, truncated: false, busy: false, error: '' })
  }

  const runSearch = async () => {
    if (!search) return
    const q = search.q.trim()
    if (q === '') return // 查询为空不给搜
    setSearch((s) => (s ? { ...s, busy: true, error: '' } : s))
    try {
      const r = await searchWorkspace(base.path, search.dirOf, q, base.machine || undefined)
      setSearch((s) => (s ? { ...s, busy: false, hits: r.hits, truncated: r.truncated } : s))
    } catch (err) {
      setSearch((s) => (s ? { ...s, busy: false, error: errorMessage(err) } : s))
    }
  }

  // revealReason 返回 Reveal in Finder 的置灰理由，空串表示可点。
  // 三条互斥的理由按代价从低到高判：machine 是纯前端已知，hostname 也是，
  // 平台位要等 /api/machines 回来——最后判它，免得能力表还没到就先灰一下再亮。
  const revealReason = (() => {
    if (base.machine) return `远程目录无法在本机的访达中打开（machine: ${base.machine}）`
    // Host 白名单不止回环（B104 会把本机网卡 IP 也放进来），所以「不是远程目录」
    // 不等于「浏览器和 agentd 在同一台机器上」。这里用页面自己的 host 判。
    if (!isLoopbackHost(window.location.hostname)) {
      return '你在通过网络访问这台 agentd，访达会开在 agentd 那台机器上'
    }
    if (revealSupported === false) return '这台机器的系统不支持在访达中显示（仅 macOS）'
    return ''
  })()

  // menuItems 按 spec §6.2 分组：文件夹类动作都落在 dirOf 上，条目类动作
  // 落在自身，折叠文件夹只给目录行，Reveal in Finder 只在同机 macOS 时可点。
  const menuItems = (entry: MenuEntry): ContextMenuEntry[] => {
    const dOf = dirOf(entry.rel, entry.isDir)
    // 桌面壳的 WKWebView 里 writeText 会被拒，必须走 copyToClipboard 的
    // 同步 execCommand 路径（见 lib/clipboard.ts 头注释）
    const clipboard = (text: string) => () => copyToClipboard(text)
    return [
      { label: '新文件', onSelect: () => setNameDlg({ mode: 'create-file', dirOf: dOf, target: dOf, name: '' }) },
      { label: '新建文件夹', onSelect: () => setNameDlg({ mode: 'create-dir', dirOf: dOf, target: dOf, name: '' }) },
      { label: '在终端中打开', onSelect: () => onOpenTerminal(dOf) },
      { label: '在文件夹中查找', onSelect: () => openSearch(dOf) },
      { separator: true },
      { label: '复制', onSelect: () => void copyEntry(entry.rel) },
      {
        label: '重命名',
        onSelect: () => setNameDlg({ mode: 'rename', dirOf: parentOf(entry.rel), target: entry.rel, name: entry.name }),
      },
      {
        label: '删除',
        onSelect: () => {
          setMenu(null)
          setDeleteError('')
          // 目录的已列举条目数随确认弹层一并给出，让文案能说「至少 N 项」
          setDeleteTarget({
            rel: entry.rel,
            name: entry.name,
            isDir: entry.isDir,
            dirCount: entry.isDir ? dirs.entriesOf(entry.rel)?.length : undefined,
          })
        },
      },
      { label: '复制路径', onSelect: clipboard(`${base.path}/${entry.rel}`) },
      { label: '复制相对路径', onSelect: clipboard(entry.rel) },
      { separator: true },
      ...(entry.isDir
        ? [
            {
              label: '折叠文件夹',
              onSelect: () =>
                setExpanded((prev) => {
                  const next = new Set(prev)
                  next.delete(entry.rel)
                  return next
                }),
            } satisfies ContextMenuEntry,
          ]
        : []),
      {
        label: 'Reveal in Finder',
        onSelect: () => void revealEntry(entry.rel),
        disabled: revealReason !== '',
        disabledReason: revealReason,
      },
    ]
  }

  return (
    <aside className="flex h-full min-h-0 flex-col border-l bg-background">
      <div className="flex items-center gap-1 border-b px-3 py-2">
        <span className="text-sm font-medium">文件</span>
        <button
          type="button"
          aria-label="刷新"
          onClick={dirs.refresh}
          className="ml-auto rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <RefreshCw className="size-3.5" />
        </button>
        {onClose && (
          <button
            type="button"
            aria-label="关闭文件抽屉"
            title="关闭文件抽屉"
            onClick={onClose}
            className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
          >
            <X className="size-3.5" />
          </button>
        )}
      </div>

      {opError !== '' && (
        <div className="flex items-center gap-2 border-b px-3 py-1.5 text-xs text-destructive">
          <span className="min-w-0 flex-1 break-words">{opError}</span>
          <button
            type="button"
            aria-label="关闭错误"
            onClick={() => setOpError('')}
            className="shrink-0 rounded p-0.5 hover:bg-accent hover:text-foreground"
          >
            <X className="size-3.5" />
          </button>
        </div>
      )}

      <div className="border-b px-3 py-2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="搜索文件…"
          className="w-full rounded-md border px-2 py-1 text-xs"
        />
      </div>

      {search ? (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="flex items-center gap-2 border-b px-3 py-1.5">
            <span className="min-w-0 flex-1 truncate text-[11px] text-muted-foreground">
              在「{search.dirOf || base.label}」中查找
            </span>
            <button
              type="button"
              aria-label="关闭搜索"
              onClick={() => setSearch(null)}
              className="rounded p-0.5 text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              <X className="size-3.5" />
            </button>
          </div>
          <div className="flex items-center gap-1 px-3 py-2">
            <input
              aria-label="关键词"
              value={search.q}
              onChange={(e) => setSearch((s) => (s ? { ...s, q: e.target.value } : s))}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && !search.busy && search.q.trim() !== '') void runSearch()
              }}
              placeholder="关键词…"
              className="min-w-0 flex-1 rounded-md border px-2 py-1 text-xs"
            />
            <button
              type="button"
              aria-label="搜索"
              onClick={() => void runSearch()}
              disabled={search.busy || search.q.trim() === ''}
              className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50"
            >
              <Search className="size-3.5" />
            </button>
          </div>
          {search.error !== '' && (
            <p role="alert" className="px-3 pb-2 text-[11px] text-destructive">
              {search.error}
            </p>
          )}
          <div className="min-h-0 flex-1 overflow-auto pb-2">
            {search.busy ? (
              <p className="px-3 text-[11px] text-muted-foreground">正在搜索…</p>
            ) : search.hits === null ? (
              <p className="px-3 text-[11px] text-muted-foreground">输入关键词开始搜索</p>
            ) : search.hits.length === 0 ? (
              <p className="px-3 text-[11px] text-muted-foreground">没有命中</p>
            ) : (
              <>
                {search.truncated && (
                  <p className="px-3 pb-1 text-[11px] text-muted-foreground">
                    结果过多，仅显示前 {search.hits.length} 条
                  </p>
                )}
                <ul>
                  {search.hits.map((h, i) => (
                    <li key={i}>
                      <button
                        type="button"
                        onClick={() => onOpenFile(h.rel)}
                        className="block w-full px-3 py-0.5 text-left text-[11px] hover:bg-accent"
                      >
                        <span className="font-mono text-muted-foreground">
                          {h.rel}:{h.line}
                        </span>{' '}
                        <span className="break-all">{h.text}</span>
                      </button>
                    </li>
                  ))}
                </ul>
              </>
            )}
          </div>
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-auto py-1">
          {/* 根标题与条目行同一档字号：它是这列的表头，比条目还小会显得是脚注 */}
          <p className="truncate px-3 py-1 font-mono text-xs text-muted-foreground">{base.label}</p>
          <DirLevel
            dirs={dirs}
            rel=""
            depth={0}
            expanded={expanded}
            onToggle={toggle}
            onOpenFile={onOpenFile}
            onContextMenu={onRowContextMenu}
            changed={changed}
            query={query.trim().toLowerCase()}
          />
        </div>
      )}

      {menu && (
        <ContextMenu
          x={menu.x}
          y={menu.y}
          items={menuItems(menu.entry)}
          onClose={() => setMenu(null)}
        />
      )}

      {nameDlg && (
        <EntryNameDialog
          title={nameDlg.mode === 'rename' ? '重命名' : nameDlg.mode === 'create-file' ? '新文件' : '新建文件夹'}
          initialName={nameDlg.name}
          submitLabel={nameDlg.mode === 'rename' ? '保存' : '创建'}
          busy={nameBusy}
          error={nameError || undefined}
          onSubmit={(name) => void submitName(name)}
          onCancel={() => setNameDlg(null)}
        />
      )}

      {deleteTarget && (
        <DeleteEntryDialog
          name={deleteTarget.name}
          isDir={deleteTarget.isDir}
          rel={deleteTarget.rel}
          dirCount={deleteTarget.dirCount}
          busy={deleteBusy}
          error={deleteError || undefined}
          onConfirm={() => void confirmDelete()}
          onCancel={() => setDeleteTarget(null)}
        />
      )}
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
  onContextMenu: (e: React.MouseEvent, entry: DirEntry, rel: string) => void
  changed: Map<string, ChangeStatus>
  query: string
}

// ENTRY_ROW 是文件行与目录行共用的一套排版。
//
// 13px / py-1（行高约 26px）而不是原来的 12px / py-0.5（约 18px）：右栏是要
// 长时间扫读的一列名字，行挨得太紧时层级缩进与状态色都糊在一起。gap-2 与
// size-4 的图标是同一件事的两半——图标小半档、间距紧半档，整列就显得毛糙。
const ENTRY_ROW = 'flex w-full items-center gap-2 py-1 pr-3 text-left text-[13px] hover:bg-accent'

// INDENT_STEP 是每层的缩进步长。16px 对应「子项的箭头正好落在父项图标下方」，
// 12px 时相邻两层几乎看不出差别。
const INDENT_STEP = 16

// DirLevel 渲染一层目录：加载中 / 该层失败 / 条目列表三种形态。
function DirLevel(props: LevelProps) {
  const { dirs, rel, depth, query } = props
  const entries = dirs.entriesOf(rel)
  const error = dirs.errorOf(rel)
  const pad = { paddingLeft: `${12 + depth * INDENT_STEP}px` }

  if (error !== undefined) {
    return (
      <p className="py-1 pr-3 text-xs text-destructive" style={pad}>
        {error}
      </p>
    )
  }
  if (entries === undefined) {
    return (
      <p className="py-1 pr-3 text-xs text-muted-foreground" style={pad}>
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

// Row 渲染一个条目。目录行负责拼接子层的相对路径并递归渲染 DirLevel；
// 文件行与目录行都能右键呼出菜单。
function Row({ entry, pad, ...rest }: LevelProps & { entry: DirEntry; pad: { paddingLeft: string } }) {
  const { rel: parentRel, depth, expanded, onToggle, onOpenFile, onContextMenu, changed } = rest
  const rel = parentRel ? `${parentRel}/${entry.name}` : entry.name
  const open = expanded.has(rel)

  if (entry.is_dir) {
    return (
      <li>
        <button
          type="button"
          onClick={() => onToggle(rel)}
          onContextMenu={(e) => onContextMenu(e, entry, rel)}
          className={ENTRY_ROW}
          style={pad}
        >
          {/* 箭头装在固定宽的格子里，文件行用同宽的空格子占位——两者的图标因此
              落在同一条竖线上。之前文件图标靠 ml-3 凑，与目录图标差 6px，
              层级看起来是歪的 */}
          <span className="flex size-4 shrink-0 items-center justify-center text-muted-foreground">
            {open ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
          </span>
          {open ? (
            <FolderOpen data-testid="dir-icon" className="size-4 shrink-0 text-muted-foreground" />
          ) : (
            <FolderClosed data-testid="dir-icon" className="size-4 shrink-0 text-muted-foreground" />
          )}
          <span className={cn('truncate', entry.ignored && IGNORED_NAME)}>{entry.name}</span>
          {entry.ignored && <IgnoredMark />}
        </button>
        {open && <DirLevel {...rest} rel={rel} depth={depth + 1} />}
      </li>
    )
  }

  // 改动类别决定整行的颜色（图标 + 文件名 + 角标）与角标字母；未改动的文件
  // 保持默认前景色
  const status = changed.get(rel)
  const style = status ? CHANGE_STYLE[status] : null
  const ignored = entry.ignored === true

  return (
    <li>
      <button
        type="button"
        onClick={() => onOpenFile(rel)}
        onContextMenu={(e) => onContextMenu(e, entry, rel)}
        className={ENTRY_ROW}
        style={pad}
      >
        <span className="size-4 shrink-0" />
        {/* 图标统一走次要灰，颜色留给「状态」说话：改动行整行同色，未改动行
            全灰。原型给文件图标定的那个蓝（--file-accent）在这里退场——它与
            新增绿/修改琥珀同时出现时，一行两三种颜色，反而看不出哪个要紧 */}
        <FileText
          data-testid="file-icon"
          className={cn('size-4 shrink-0', style ? style.name : 'text-muted-foreground')}
        />
        <span className={cn('truncate', style?.name, ignored && IGNORED_NAME)}>{entry.name}</span>
        {style && (
          <span title={style.title} className={cn('ml-auto shrink-0 text-[10px] font-semibold', style.badge)}>
            {style.letter}
          </span>
        )}
        {/* 被忽略的文件不可能同时有 diff 状态（git 不跟踪它），所以这两个角标
            不会打架，各自都能用 ml-auto 占右端 */}
        {ignored && <IgnoredMark />}
      </button>
    </li>
  )
}
