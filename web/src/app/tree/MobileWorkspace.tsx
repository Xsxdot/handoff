// MobileWorkspace projects Shell-owned resources by stable directory identity and card links.
// It neither polls nor changes resource lifetime; directory browsing and creation defaults are independent.
import { useEffect, useMemo, useRef, useState } from 'react'
import { Archive, ChevronDown, ChevronRight, FileText, Folder, MessageSquare, MoreHorizontal, Plus, Search, Terminal, X } from 'lucide-react'
import type { SessionSummary } from '../../api/rooms'
import type { MachineStatus, ProjectNode, PtySession, Task } from '../../api/types'
import type { OpenedWorkbenchItem, BaseDir } from '../workbench/tabs'
import { locationProblem, machineLabel, workspaceBase } from './ProjectTree'
import { useTreePrefs } from './useTreePrefs'
import { sortProjects } from './treePrefs'
import { formatRelative } from '../lib/format'

interface Props {
  projects: ProjectNode[]; machines: MachineStatus[] | undefined; tasks: Task[]
  sessions: SessionSummary[]; projectOfCard: (id: string) => string
  openedItems: OpenedWorkbenchItem[]; ptySessions: PtySession[] | null
  needsCount: number | null; ready: boolean; errorText: string; expired: boolean
  onOpenSession: (session: SessionSummary) => void; onOpenItem: (item: OpenedWorkbenchItem) => void
  onRestoreTerminal: (id: string, base: BaseDir) => void
  onOpenTerminal: (base: BaseDir) => void; onOpenDirectory: (base: BaseDir) => void
  onOpenProject: (id: string) => void; onEditProject: (project: ProjectNode) => void
  onAddProject: () => void; onOpenSessions: (archived: boolean) => void; onOpenCards: () => void
}

function loadLocation(key: string): string { try { return localStorage.getItem(key) ?? '' } catch { return '' } }
function rememberLocation(key: string, value: string) {
  try { localStorage.setItem(key, value); console.debug('mobile.workspace.location_saved', { key, baseKey: value }) }
  catch (error) { console.warn('mobile.workspace.location_save_failed', { key, error }) }
}
function locations(project: ProjectNode) { return project.locations.flatMap(loc => loc.workspaces.map(ws => ({ base: workspaceBase(project,loc.machine,ws), loc }))) }
function owns(project: ProjectNode, base: BaseDir) {
  return project.locations.some(loc => loc.machine === base.machine && loc.workspaces.some(ws => ws.path === base.path))
}

/** Shell injects the authoritative streams and existing navigation callbacks; unknown relationships remain explicit. */
export function MobileWorkspace(props: Props) {
  const [prefs] = useTreePrefs()
  const [collapsed, setCollapsed] = useState(new Set<string>())
  const [expanded, setExpanded] = useState(new Set<string>())
  const [special, setSpecial] = useState(new Set<string>())
  const [emptyBucketOpen, setEmptyBucketOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [searching, setSearching] = useState(false)
  const [menu, setMenu] = useState(false)
  const [actionProject, setActionProject] = useState<ProjectNode | null>(null)
  const sheetRef = useRef<HTMLElement>(null)
  useEffect(() => {
    if (!actionProject) return
    const previous = document.activeElement as HTMLElement | null
    sheetRef.current?.querySelector<HTMLButtonElement>('button')?.focus()
    const handle = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); setActionProject(null) }
      if (event.key !== 'Tab') return
      const controls = [...(sheetRef.current?.querySelectorAll<HTMLElement>('button:not(:disabled),select:not(:disabled),[tabindex="0"]') ?? [])]
      const first = controls[0], last = controls.at(-1)
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', handle)
    return () => { document.removeEventListener('keydown', handle); previous?.focus() }
  }, [actionProject])
  const [creation, setCreation] = useState('')
  const [browse, setBrowse] = useState('')
  const sortedProjects = sortProjects(props.projects.filter(p => !prefs.hiddenProjects.includes(p.project_id)),p => {
    const tasks = props.tasks.filter(t => t.project_id === p.project_id)
    return { name:p.name, active:tasks.filter(t => ['running','waiting_answer','waiting_review'].includes(t.state)).length, updatedAt:tasks.reduce((v,t)=>t.updated_at > v ? t.updated_at:v,'') }
  },prefs.projectSort)
  const activeSessions = props.sessions.filter(s => !s.archived)
  const associations = useMemo(() => new Map(activeSessions.map(session => {
    const names = [...new Set((session.cards ?? []).map(card=>props.projectOfCard(card.card_id)).filter(Boolean))]
    const unknown = (session.cards ?? []).some(card=>!props.projectOfCard(card.card_id))
    // An unresolved link must not make a partially-known cross-project session look exclusive.
    const owners = names.flatMap(name => props.projects.filter(p => p.name === name || p.locations.some(l => l.name === name)).map(p => p.project_id))
    const ids = [...new Set(owners)]
    return [session.id, !unknown && names.length === 1 && ids.length === 1 ? ids[0] : names.length > 1 ? '#cross' : '#other']
  })),[props.sessions,props.projectOfCard,props.projects])
  const openPty = new Set(props.openedItems.flatMap(i=>i.content.kind === 'terminal' && i.content.sessionId ? [i.content.sessionId] : []))
  const matches = (text:string)=>!query || text.toLocaleLowerCase().includes(query.toLocaleLowerCase())
  const ptyById = new Map((props.ptySessions ?? []).map(session=>[session.id,session]))
  const itemSearchText = (item:OpenedWorkbenchItem) => {
    const sessionId = item.content.kind === 'terminal' ? item.content.sessionId : undefined
    const session = sessionId ? ptyById.get(sessionId) : undefined
    return `${item.label} ${item.base.path} ${session?.shell ?? ''} ${session?.base_path ?? ''}`
  }
  const projectResources = (project:ProjectNode) => {
    const rooms = activeSessions.filter(s=>associations.get(s.id)===project.project_id && matches(s.title))
    const items = props.openedItems.filter(i=>i.content.kind!=='session' && i.content.kind!=='blank' && owns(project,i.base) && matches(itemSearchText(i)))
    const ptys = (props.ptySessions ?? []).filter(s=>s.exit_code===undefined && !openPty.has(s.id) && project.locations.some(l=>l.machine===s.machine && l.workspaces.some(w=>s.base_kind==='workspace' && w.path===s.base_path)) && matches(`${s.shell} ${s.base_path} ${s.base_kind}`))
    return {rooms,items,ptys}
  }
  // B429 A1：有资源置顶；空项目进「其余 N 个项目」桶（仍尊重排序偏好）。
  const projectHasResources = (project:ProjectNode) => {
    const rooms = activeSessions.filter(s=>associations.get(s.id)===project.project_id).length
    const items = props.openedItems.filter(i=>i.content.kind!=='session' && i.content.kind!=='blank' && owns(project,i.base)).length
    const ptys = (props.ptySessions ?? []).filter(s=>s.exit_code===undefined && !openPty.has(s.id) && project.locations.some(l=>l.machine===s.machine && l.workspaces.some(w=>s.base_kind==='workspace' && w.path===s.base_path))).length
    return rooms + items + ptys > 0
  }
  const projectVisible = (project:ProjectNode) => matches(project.name) || Object.values(projectResources(project)).some(rows=>rows.length>0)
  const richProjects = sortedProjects.filter(projectHasResources).filter(projectVisible)
  const emptyProjects = sortedProjects.filter(p => !projectHasResources(p)).filter(projectVisible)
  const emptyBucketExpanded = emptyBucketOpen || Boolean(query)
  const liveUnassignedPtys = (props.ptySessions ?? []).filter(s=>s.exit_code===undefined && !openPty.has(s.id) && !props.projects.some(p=>p.locations.some(l=>l.machine===s.machine && l.workspaces.some(w=>s.base_kind==='workspace' && w.path===s.base_path))))
  const ptyBase = (s:PtySession):BaseDir => ({key:`pty:${s.machine}:${s.base_kind}:${s.base_path}`,kind:s.base_kind==='home'?'home':s.base_kind==='scratch'?'scratch':'workspace',path:s.base_path,label:s.base_path || s.base_kind,projectName:'',machine:s.machine})
  const openActions = (project:ProjectNode) => {
    const all = locations(project)
    const fallback = all.find(i=>locationProblem(i.loc,props.machines)==='')?.base.key ?? all[0]?.base.key ?? ''
    setCreation(loadLocation(`handoff.mobile.create.${project.project_id}`) || fallback)
    setBrowse(loadLocation(`handoff.mobile.browse.${project.project_id}`) || fallback)
    setActionProject(project)
    console.debug('mobile.workspace.actions_open', {projectId:project.project_id})
  }
  const toggleProject = (projectId:string, isOpen:boolean) => {
    setCollapsed(old=>{const next=new Set(old); if(isOpen) next.add(projectId); else next.delete(projectId); return next})
    if(!isOpen) setExpanded(v=>new Set(v).add(projectId))
  }
  const renderProject = (project:ProjectNode) => {
    const {rooms,items,ptys} = projectResources(project)
    const resourceCount = rooms.length + items.length + ptys.length
    const isOpen = !collapsed.has(project.project_id) && (Boolean(query) || expanded.has(project.project_id) || resourceCount > 0)
    const emptyCollapsed = !isOpen && resourceCount === 0
    return <div key={project.project_id} className={emptyCollapsed ? 'mobile-project-group is-empty-collapsed' : 'mobile-project-group'}>
      <div className="mobile-project-heading"><button type="button" className="mobile-disclose" aria-label={`${isOpen?'收起':'展开'} ${project.name}`} aria-expanded={isOpen} onClick={()=>toggleProject(project.project_id,isOpen)}>{isOpen?<ChevronDown className="size-4"/>:<ChevronRight className="size-4"/>}</button><button type="button" className="mobile-project-name" data-testid="mobile-project-card" aria-label={project.name} onClick={()=>toggleProject(project.project_id,isOpen)}><Folder className="size-5 shrink-0"/><b>{project.name}</b></button><button type="button" className="mobile-project-actions" disabled={!props.ready} aria-label={`${project.name} 操作`} onClick={()=>openActions(project)}><MoreHorizontal className="size-5"/></button></div>
      {isOpen && <div className="mobile-project-resources">{rooms.map(sessionRow)}{items.map(item=><button key={item.tabId} className="mobile-resource-row" onClick={()=>props.onOpenItem(item)}>{item.content.kind==='file'?<FileText className="size-5 shrink-0"/>:<Terminal className="size-5 shrink-0"/>}<span className="min-w-0 flex-1"><b className="block truncate font-medium">{item.label}</b><span className="mobile-meta block truncate">{item.content.kind==='file'?'本设备打开 · ':''}{machineLabel(item.base.machine)} · {item.base.label}</span></span><ChevronRight className="size-4"/></button>)}{ptys.map(s=>{
        const loc=project.locations.find(l=>l.machine===s.machine)!;const ws=loc.workspaces.find(w=>w.path===s.base_path)!;const base=workspaceBase(project,loc.machine,ws)
        return <button key={s.id} disabled={!props.ready||locationProblem(loc,props.machines)!==''} className="mobile-resource-row" onClick={()=>props.onRestoreTerminal(s.id,base)}><Terminal className="size-5 shrink-0"/><span className="min-w-0 flex-1"><b className="block truncate font-medium">{s.shell} · {ws.branch || base.label}</b><span className="mobile-meta">{machineLabel(s.machine)} · 已有终端</span></span><ChevronRight className="size-4"/></button>
      })}{rooms.length+items.length+ptys.length===0 && <button type="button" className="mobile-empty-resource" disabled={!props.ready} onClick={()=>openActions(project)}>项目操作与目录<ChevronRight className="size-4"/></button>}</div>}
    </div>
  }
    const sessionRow = (s:SessionSummary) => <button type="button" className="mobile-resource-row" key={s.id} onClick={()=>props.onOpenSession(s)}>
    <MessageSquare className="size-5 shrink-0"/><span className="min-w-0 flex-1"><b className="block truncate font-medium">{s.title}</b><span className="mobile-meta line-clamp-1">{s.preview?.body ?? (s.needs_human ? '需要你处理' : '协作会话')}</span></span><span className="mobile-meta shrink-0">{s.unread > 0 ? `${s.unread} 未读` : formatRelative(s.last_activity)}</span><ChevronRight className="size-4 shrink-0"/>
  </button>
  return <section data-testid="mobile-workspace" className="mobile-workspace h-full overflow-auto">
    <header className="mobile-page-header"><h1>工作台</h1><button aria-label="搜索工作台" onClick={()=>setSearching(!searching)}><Search className="size-5"/></button><button aria-label="工作台操作" onClick={()=>setMenu(!menu)}><MoreHorizontal className="size-5"/></button></header>
    {searching && <input autoFocus className="mobile-search" aria-label="搜索项目与资源" placeholder="搜索项目与资源" value={query} onChange={e=>setQuery(e.target.value)}/>}
    {menu && <div className="mobile-inline-menu"><button data-testid="mobile-add-project" disabled={!props.ready} aria-label="添加项目" onClick={props.onAddProject}><Plus className="size-4"/>添加项目</button><button onClick={()=>props.onOpenSessions(false)}><MessageSquare className="size-4"/>全部协作会话</button></div>}
    {(() => {
      const needsKnown = props.needsCount !== null && (props.ready || props.errorText || props.expired)
      const needsZero = needsKnown && props.needsCount === 0
      const needsBright = needsKnown && (props.needsCount ?? 0) > 0
      return <button type="button" className={needsZero ? 'mobile-attention-row is-weak' : 'mobile-attention-row'} onClick={props.onOpenCards}>需要你处理 {needsBright && <span className="mobile-count is-bright">{props.needsCount}</span>}<ChevronRight className="ml-auto size-4"/></button>
    })()}
    {props.expired ? <div role="alert" className="mobile-data-notice"><b>连接凭据已失效</b><p>请重新打开控制台；若无法恢复，请在设置重新配对。</p></div> : props.errorText && <div role="alert" className="mobile-data-notice"><b>连接已断开，保留上次数据</b><details><summary>查看断开详情</summary><p>{props.errorText}</p></details></div>}
    {!props.ready && !props.errorText && !props.expired && <p className="mobile-empty">正在读取项目与资源…</p>}
    {richProjects.map(renderProject)}
    {emptyProjects.length > 0 && <div className="mobile-project-group mobile-empty-bucket" data-testid="mobile-empty-project-bucket">
      <button type="button" className="mobile-special-heading mobile-empty-bucket-heading" aria-label={`${emptyBucketExpanded?'收起':'展开'} 其余 ${emptyProjects.length} 个项目`} aria-expanded={emptyBucketExpanded} onClick={()=>setEmptyBucketOpen(v=>!v)}>
        {emptyBucketExpanded?<ChevronDown className="size-4 mobile-fold"/>:<ChevronRight className="size-4 mobile-fold"/>}
        <Folder className="size-5 shrink-0"/><b>其余 {emptyProjects.length} 个项目</b>
      </button>
      {emptyBucketExpanded && <div className="mobile-empty-bucket-body">{emptyProjects.map(renderProject)}</div>}
    </div>}
    {(['#cross','#other'] as const).map(key=>{
      const rows=activeSessions.filter(s=>associations.get(s.id)===key && matches(s.title));const orphan=key==='#other'?props.openedItems.filter(i=>!props.projects.some(p=>owns(p,i.base)) && !['session','blank'].includes(i.content.kind) && matches(itemSearchText(i))):[]
      const ptys=key==='#other'?liveUnassignedPtys.filter(s=>matches(`${s.shell} ${s.base_path} ${s.base_kind}`)):[]
      if(!rows.length&&!orphan.length&&!ptys.length)return null;const label=key==='#cross'?'跨项目协作':'其他工作';const expanded=special.has(key)||Boolean(query)
      return <div className="mobile-project-group" key={key}><button type="button" className="mobile-special-heading" aria-label={`${expanded?'收起':'展开'} ${label}`} aria-expanded={expanded} onClick={()=>setSpecial(old=>{const next=new Set(old);next.has(key)?next.delete(key):next.add(key);return next})}>{expanded?<ChevronDown className="size-4 mobile-fold"/>:<ChevronRight className="size-4 mobile-fold"/>}<Folder className="size-5 shrink-0"/><b>{label}</b></button>{expanded && <div className="mobile-project-resources">{rows.map(sessionRow)}{orphan.map(i=><button type="button" className="mobile-resource-row" key={i.tabId} onClick={()=>props.onOpenItem(i)}><Terminal className="size-5"/>{i.label}</button>)}{ptys.map(s=><button type="button" key={s.id} disabled={!props.ready} className="mobile-resource-row" onClick={()=>props.onRestoreTerminal(s.id,ptyBase(s))}><Terminal className="size-5"/><span className="min-w-0 flex-1"><b className="block truncate font-medium">{s.shell} · {s.base_path || s.base_kind}</b><span className="mobile-meta">{s.base_kind==='home'?'主目录终端':s.base_kind==='scratch'?'临时目录终端':'未匹配项目'} · {machineLabel(s.machine)}</span></span><ChevronRight className="size-4"/></button>)}</div>}</div>
    })}
    {props.ready && !richProjects.length && !emptyProjects.length && <div className="mobile-empty"><h2>还没有项目</h2><p>添加项目后，在这里查看会话、终端与文件。</p><button onClick={props.onAddProject}>添加项目</button></div>}
    <button className="mobile-archive-row" onClick={()=>props.onOpenSessions(true)}><Archive className="size-5"/>归档会话<ChevronRight className="ml-auto size-4"/></button>
    {actionProject && <div className="mobile-sheet-backdrop" onClick={()=>setActionProject(null)}><section ref={sheetRef} role="dialog" aria-modal="true" aria-label={`${actionProject.name} 操作与位置`} className="mobile-action-sheet" onClick={e=>e.stopPropagation()}>
      <header><h2>{actionProject.name}</h2><button type="button" aria-label="关闭项目操作" onClick={()=>setActionProject(null)}><X className="size-5"/></button></header>
      <div className="mobile-sheet-group mobile-sheet-group-primary" role="group" aria-labelledby="sheet-create-label">
        <div className="mobile-sheet-group-label" id="sheet-create-label">新终端默认位置</div>
        <label className="mobile-sheet-field">
          <select aria-label="新终端默认位置" value={creation} onChange={e=>{setCreation(e.target.value);rememberLocation(`handoff.mobile.create.${actionProject.project_id}`,e.target.value)}}>{locations(actionProject).map(({base,loc})=><option key={base.key} disabled={locationProblem(loc,props.machines)!==''} value={base.key}>{machineLabel(base.machine)} · {base.label}</option>)}</select>
        </label>
        <button type="button" className="mobile-sheet-action primary" disabled={!props.ready || !locations(actionProject).some(i=>i.base.key===creation && locationProblem(i.loc,props.machines)==='')} onClick={()=>{const chosen=locations(actionProject).find(i=>i.base.key===creation);if(chosen){setActionProject(null);props.onOpenTerminal(chosen.base)}}}><Terminal className="size-5"/>新建终端</button>
      </div>
      <div className="mobile-sheet-group mobile-sheet-group-primary" role="group" aria-labelledby="sheet-browse-label">
        <div className="mobile-sheet-group-label" id="sheet-browse-label">目录浏览位置</div>
        <label className="mobile-sheet-field">
          <select aria-label="目录浏览位置" value={browse} onChange={e=>{setBrowse(e.target.value);rememberLocation(`handoff.mobile.browse.${actionProject.project_id}`,e.target.value)}}>{locations(actionProject).map(({base,loc})=><option key={base.key} disabled={locationProblem(loc,props.machines)!==''} value={base.key}>{machineLabel(base.machine)} · {base.label}</option>)}</select>
        </label>
        <button type="button" className="mobile-sheet-action primary" disabled={!props.ready || !locations(actionProject).some(i=>i.base.key===browse && locationProblem(i.loc,props.machines)==='')} onClick={()=>{const chosen=locations(actionProject).find(i=>i.base.key===browse);if(chosen){setActionProject(null);props.onOpenDirectory(chosen.base)}}}><Folder className="size-5"/>浏览目录</button>
      </div>
      <div className="mobile-sheet-group mobile-sheet-group-secondary" role="group" aria-label="更多项目操作">
        <button type="button" className="mobile-sheet-action secondary" disabled={!props.ready} onClick={()=>{setActionProject(null);props.onOpenProject(actionProject.project_id)}}><Plus className="size-5"/><span>工作树与项目目录</span></button>
        <button type="button" className="mobile-sheet-action secondary" disabled={!props.ready} onClick={()=>{setActionProject(null);props.onEditProject(actionProject)}}><MoreHorizontal className="size-5"/><span>项目位置管理</span></button>
      </div>
      <p className="mobile-sheet-note">仅影响下次新建终端；已有终端位置保持不变，目录浏览单独记忆。</p>
    </section></div>}
  </section>
}
