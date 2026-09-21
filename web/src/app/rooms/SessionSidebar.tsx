// SessionSidebar —— 左栏「会话」tab 的列表面（B361：v1 中央区第一栏整体迁入）。
// 边界：纯展示——数据由 Shell 的 usePoll 持有（徽章要跨 tab 活），本组件只渲染
// 与回调；行点击 = 开工作台 tab 的入口，新建走 Shell 的 NewSessionDialog。
// B358.8 #1：行可拖——dragstart 写 DRAG_SESSION_MIME，落点语义由 WorkbenchPage/
// TabBar 的既有拖放面承接（拖出即分屏/开组），本组件只负责发载荷与拖源提示。
import { useState } from 'react'
import type { DragEvent } from 'react'
import type { SessionSummary } from '../../api/rooms'
import { formatRelative } from '../lib/format'
import { DRAG_SESSION_MIME } from '../workbench/paneDrop'
import { filterSessionsByProject, totalUnread } from './sessionModel'

export interface SessionSidebarProps {
  sessions: SessionSummary[]
  loading: boolean
  errorText: string
  needsOnly: boolean
  onToggleNeeds: () => void
  onOpen: (session: SessionSummary) => void
  onCreate: () => void
  // 项目筛选（B358.8 #6）：选项与映射由 Shell 从 cardsState 投影下传（零新增端点）。
  projectFilter: string
  onProjectFilter: (project: string) => void
  projectOptions: string[]
  projectOfCard: (cardId: string) => string
}

export function SessionSidebar({ sessions, loading, errorText, needsOnly, onToggleNeeds, onOpen, onCreate,
  projectFilter, onProjectFilter, projectOptions, projectOfCard }: SessionSidebarProps) {
  const byProject = filterSessionsByProject(sessions, projectOfCard, projectFilter)
  const visible = needsOnly ? byProject.filter((session) => session.needs_human) : byProject
  const needsCount = sessions.filter((session) => session.needs_human).length
  // draggingId 拖源提示：被拖行降低透明度；dragend 兜底复位（drop 在列表外完成时不回流）。
  const [draggingId, setDraggingId] = useState<string | null>(null)
  const startDrag = (event: DragEvent<HTMLElement>, session: SessionSummary) => {
    event.dataTransfer.setData(DRAG_SESSION_MIME, JSON.stringify({ sessionId: session.id, title: session.title }))
    event.dataTransfer.effectAllowed = 'move'
    setDraggingId(session.id)
  }
  return (
    <div className="flex min-h-0 flex-1 flex-col" data-testid="session-list">
      {/* 未读徽章的聚合读数挂在列表头：tab 级徽章由 Shell 用同一数据另算 */}
      <div className="flex shrink-0 items-center justify-between px-3 py-2.5">
        <span className="text-sm font-semibold">会话</span>
        <button type="button" aria-label="新建会话" onClick={onCreate} className="rounded-md border px-2 py-1 text-xs hover:bg-accent">＋ 新建会话</button>
      </div>
      {/* 筛选单行（走查 09-17，对 board.html 原型 .im-filters）：项目下拉 + 需要你
          开关 + 计数同处一行，纯文字项不做成带边框表单控件 */}
      <div data-testid="session-filters" className="flex shrink-0 items-center gap-1 border-b px-3 py-1.5 text-xs">
        <label htmlFor="session-project-filter" className="shrink-0 text-muted-foreground">▦ 项目</label>
        <select id="session-project-filter" data-testid="session-project-filter" value={projectFilter}
          onChange={(event) => onProjectFilter(event.target.value)}
          className="min-w-0 max-w-[8rem] flex-1 cursor-pointer appearance-none bg-transparent py-0.5 pl-0.5 text-xs outline-none">
          <option value="">全部项目</option>
          {projectOptions.map((project) => <option key={project} value={project}>{project}</option>)}
        </select>
        <span aria-hidden="true" className="shrink-0 text-[10px] text-muted-foreground">∨</span>
        <span aria-hidden="true" className="mx-1 h-3.5 w-px shrink-0 bg-border" />
        <button type="button" aria-pressed={needsOnly} onClick={onToggleNeeds} className={needsOnly ? 'font-semibold text-amber-700' : 'text-muted-foreground'}>
          ⚑ 需要你 <span data-testid="needs-count" className="rounded-full bg-amber-100 px-1.5 text-[10px] font-semibold text-amber-700">{needsCount}</span>
        </button>
        <span className="ml-auto shrink-0 text-muted-foreground" data-testid="session-total">{loading ? '读取中' : `${visible.length} 个会话`}</span>
      </div>
      {errorText !== '' && <p role="alert" className="shrink-0 border-b bg-amber-50 px-3 py-1.5 text-xs text-amber-800">会话列表已断开：{errorText}</p>}
      <div className="min-h-0 flex-1 overflow-y-auto p-1">
        {!loading && visible.length === 0 ? (
          <p className="p-2 text-sm text-muted-foreground">（暂无会话）</p>
        ) : visible.map((session) => (
          <button key={session.id} type="button" data-testid="session-row" aria-label={`会话 ${session.title}`}
            draggable
            data-drag-session={session.id}
            onDragStart={(event) => startDrag(event, session)}
            onDragEnd={() => setDraggingId(null)}
            onClick={() => onOpen(session)}
            className={`flex w-full items-start gap-2 rounded-xl px-2.5 py-2 text-left transition-colors ${session.needs_human ? 'bg-amber-50 hover:bg-amber-100' : 'hover:bg-accent/60'} ${draggingId === session.id ? 'opacity-50' : ''}`}>
            <span className="relative flex size-10 shrink-0 items-center justify-center rounded-md bg-slate-200 text-[11px] font-semibold text-slate-700">
              {session.title.slice(0, 2)}
              {session.unread > 0 && <span data-testid="session-unread" className="absolute -right-1 -top-1 min-w-4 rounded-full bg-red-500 px-1 text-center text-[10px] leading-4 text-white">{session.unread}</span>}
            </span>
            <span className="min-w-0 flex-1">
              <span className="flex items-baseline gap-1.5">
                <span className={`truncate text-sm ${session.unread > 0 ? 'font-semibold' : ''}`}>{session.title}</span>
                <span className="ml-auto shrink-0 text-[10px] text-muted-foreground">{formatRelative(session.last_activity)}</span>
              </span>
              <span className="mt-0.5 flex items-center gap-1 text-xs text-muted-foreground">
                {session.needs_human && <span className="shrink-0 rounded-full border border-amber-200 bg-amber-100 px-1.5 text-[10px] font-semibold text-amber-700">需要你</span>}
                {session.archived && <span className="shrink-0 rounded-full border px-1.5 text-[10px] text-muted-foreground">已归档</span>}
                <span className="truncate">{session.preview?.body ?? '暂无预览'}</span>
              </span>
            </span>
          </button>
        ))}
      </div>
      {/* 徽章读数对账锚：单测断言 Σ unread 用 */}
      <span hidden data-testid="sidebar-unread-value">{totalUnread(sessions)}</span>
    </div>
  )
}
