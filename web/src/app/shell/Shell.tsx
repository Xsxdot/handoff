// Shell —— 控制台的三栏外框：左栏导航树 / 中央 tab 工作台 / 右栏文件树。
//
// 职责：
//   - 持有跨栏共享的数据流（任务流 2.5s、项目树流 30s）与**当前基准目录**这一
//     唯一全局选中态（useWorkbench）
//   - 把三栏接起来：左栏点目录 → 切基准；左栏点任务 → 切基准 + 开 TUI tab；
//     右栏点文件 → 开 file tab；中央按 tab 种类分发渲染
//   - 承载弹出层（看板、工单）、设置页与右下角悬浮按钮
//
// 边界：
//   - 不自己取目录内容（归 FileTree）、不自己取任务会话（归 TuiTab）
//   - 中央 tab 的具体渲染经 renderContent 注入 WorkbenchPage，Shell 只做分发
//   - 机器流只随登记向导开表（useMachines(wizardOpen)）：探活会向每台远程机发
//     GET /api/status，没人看的时候没有理由持续打扰它们（spec §6）
//
// 关于 ShellContext 的移除：W3 用 <Outlet context> 给三个子页面下发共享数据。
// 新 IA 里中央不再是路由页面而是 tab，Outlet 没有了消费者；看板与工单改为弹层，
// 它们要的数据直接由 Shell 以 props 传下去。留一个没人用的 context 只会误导。
import { useEffect, useMemo, useState } from 'react'
import { Navigate, Route, Routes, useNavigate, useParams } from 'react-router-dom'
import { deleteProject, deletePtySession, fetchPtySessions } from '../../api/client'
import type { ProjectTreeResp, Task } from '../../api/types'
import { useMachines } from '../data/useMachines'
import { useProjectTree } from '../data/useProjectTree'
import { useTasks } from '../data/useTasks'
import { usePtySupport } from '../data/usePtySupport'
import { DisconnectedBanner, SessionExpiredBanner } from '../lib/Banners'
import { ConfirmDialog } from '../lib/ConfirmDialog'
import { errorMessage } from '../lib/format'
import { AddProjectWizard } from '../projects/AddProjectWizard'
import { findBaseOfTask, ProjectTree } from '../tree/ProjectTree'
import { FileTree } from '../files/FileTree'
import { WorkbenchPage } from '../workbench/WorkbenchPage'
import { TerminalTab } from '../workbench/TerminalTab'
import { FileTab } from '../workbench/FileTab'
import { TuiTab } from '../workbench/TuiTab'
import { HomeDock } from '../homedock/HomeDock'
import { useHomeDock } from '../homedock/useHomeDock'
import { HOME_BASE, useWorkbench, type BaseDir } from '../workbench/useWorkbench'
import type { TabContent } from '../workbench/tabs'
import { usePtyRestore } from '../workbench/usePtyRestore'
import { BoardOverlay } from '../overlay/BoardOverlay'
import { TicketsOverlay } from '../overlay/TicketsOverlay'
import { useGlobalTickets } from '../overlay/useGlobalTickets'
import { SettingsPage } from '../settings/SettingsPage'
import { Breadcrumb } from './Breadcrumb'

// OverlayKind 是当前打开的弹层。同时只允许一个（spec §0）：两个叠在一起时
// Esc 该关哪个会变得含糊。
type OverlayKind = 'none' | 'board' | 'tickets'

export function Shell() {
  const tasksState = useTasks()
  const treeState = useProjectTree()
  const tasks = useMemo(() => tasksState.data ?? [], [tasksState.data])
  const wb = useWorkbench()
  const navigate = useNavigate()

  const [overlay, setOverlay] = useState<OverlayKind>('none')
  const [wizardOpen, setWizardOpen] = useState(false)
  const machinesState = useMachines(wizardOpen)
  const tickets = useGlobalTickets(tasks)
  const ptySupport = usePtySupport()
  // home 终端的浮窗状态完全独立于 wb：home 终端不挂在任何目录上（见 useHomeDock）
  const dock = useHomeDock()
  // 恢复服务端已有的终端会话（spec §6.1）。写入口用 restoreTerminal 而不是
  // openTerminal：它不会把用户的选中目录拽走。
  // 恢复出来的会话按基准分流：home 的收进浮窗，工作树的回中央工作区。
  // 不分流的话，Task 4 之后会出现「新建的在浮窗、刷新后恢复的却在中央」
  // 这种自相矛盾的状态。
  //
  // 用 adopt 而不是 newTerminal：adopt 不打开浮窗、不抢焦点——页面一加载
  // 就弹出浮窗，等于替用户点了一下
  const ptyRestore = usePtyRestore((b, sessionId) => {
    if (b.kind === 'home') {
      dock.adopt({ id: sessionId, seq: dock.tabs.length + 1, sessionId, machine: b.machine })
      return
    }
    wb.restoreTerminal(b, sessionId)
  })
  // closingPty 记「哪个终端 tab 正在等确认」。会话 id 与所在位置都要留着：
  // 确认之后要先删会话、再关那个 tab
  const [closingPty, setClosingPty] = useState<{ group: number; tabId: string; sessionId: string } | null>(null)
  const [closeBusy, setCloseBusy] = useState(false)
  const [closeError, setCloseError] = useState('')
  // closingBusyProc：这个会话里是不是还有前台命令。null = 还没问出来
  const [closingBusyProc, setClosingBusyProc] = useState<boolean | null>(null)
  // closingHome 记「哪个浮窗 tab 正在等确认」。与 closingPty 同构，只是归
  // 浮窗。为什么也要确认：关闭即终止不可逆，与中央 tab 同一条理由
  const [closingHome, setClosingHome] = useState<{ id: string; sessionId: string; machine: string } | null>(null)

  // ptyNote 把能力三态翻成一句给人看的话；空串 = 可用（或不知道，照常放行）
  const ptyNote = (machine: string): string => {
    if (ptySupport.supported(machine) === false) {
      return machine === ''
        ? '本机 agentd 运行在不支持 PTY 的平台上，终端不可用。'
        : `机器 ${machine} 的 agentd 运行在不支持 PTY 的平台上，终端不可用。`
    }
    // null 一律放行：老 agentd 没上报能力位，很可能是支持的。真不支持时
    // 建会话会返回 501，那句实话由 TerminalTab 显示
    return ''
  }

  // beforeCloseTab 拦下带会话的终端 tab：关它等于终止会话，必须先确认。
  //
  // 与 spec §6.2 的一处收紧（有意为之）：spec 只要求「有前台进程」才确认，
  // 这里只要是带会话的终端 tab 就弹。关闭即终止是不可逆操作，而「有没有前台
  // 进程」这个判据在用户点下 × 的那一瞬间可能刚好过期——宁可多问一句，也不
  // 静默杀掉跑了整个晚上的 build（这正是本设计不做空闲回收的同一条理由）。
  const beforeCloseTab = (c: TabContent, group: number, tabId: string): boolean => {
    if (c.kind !== 'terminal' || !c.sessionId) return true
    setClosingPty({ group, tabId, sessionId: c.sessionId })
    setCloseError('')
    setClosingBusyProc(null)
    // 问一句「它现在忙不忙」，只用于加重措辞，**不阻塞弹层出现**
    fetchPtySessions('all')
      .then((r) => setClosingBusyProc(r.sessions.some((s) => s.id === c.sessionId && s.foreground)))
      .catch(() => setClosingBusyProc(null))
    return false
  }

  // killPtySession 删一个服务端终端会话，失败时把原文交给 onError 呈现。
  // 中央 tab 的确认关闭与浮窗 tab 的 × 共用：两处都不许吞错误——点了 × 以为
  // 会话关了，服务端却还留着一个 shell。返回是否删成功
  const killPtySession = async (
    sessionId: string,
    machine: string | undefined,
    onError: (msg: string) => void,
  ): Promise<boolean> => {
    try {
      await deletePtySession(sessionId, machine)
      return true
    } catch (err) {
      onError(errorMessage(err))
      return false
    }
  }

  const confirmClosePty = async () => {
    if (!closingPty) return
    setCloseBusy(true)
    setCloseError('')
    if (await killPtySession(closingPty.sessionId, wb.base?.machine || undefined, setCloseError)) {
      wb.close(closingPty.group, closingPty.tabId)
      setClosingPty(null)
    }
    // 删失败不关 tab：关掉就等于把一个还活着的会话从视野里抹掉，
    // 而它仍在占着进程（错误已由 killPtySession 塞进 ConfirmDialog）
    setCloseBusy(false)
  }

  const confirmCloseHome = async () => {
    if (!closingHome) return
    setCloseBusy(true)
    setCloseError('')
    if (await killPtySession(closingHome.sessionId, closingHome.machine || undefined, setCloseError)) {
      dock.closeTab(closingHome.id)
      setClosingHome(null)
    }
    setCloseBusy(false)
  }

  // killHomeSession 是浮窗 tab × 的入口：找到会话、进确认弹层。为什么不吞错误：
  // 失败被吞掉的话，用户以为会话关了、实际服务端还留着一个 shell
  const killHomeSession = (id: string) => {
    const tab = dock.tabs.find((t) => t.id === id)
    if (!tab) return
    if (!tab.sessionId) {
      // 会话还没建成（比如刚点完新终端立刻点 ×），没有可删的东西，直接移掉
      dock.closeTab(id)
      return
    }
    setCloseError('')
    setClosingHome({ id, sessionId: tab.sessionId, machine: tab.machine })
  }

  const onUnregister = async (name: string, machine: string) => {
    await deleteProject(name, machine)
    treeState.refresh()
  }

  // openTaskTui 是「点一个任务 → 在它所在目录开 TUI tab」的唯一实现。
  // 左栏任务行、看板卡片、/tasks/:id 深链、工单弹层的「跳到该任务」都走它。
  // 首参为 null（工单弹层、未归属任务）时先用树解析任务自己的目录；解析不出
  // （任务真的不在树上）才退回「当前选中目录」，一个都没选中则 wb.open 空操作。
  const openTaskTui = (base: BaseDir | null, taskId: string) => {
    setOverlay('none')
    let target = base
    if (target === null && treeState.data) {
      target = findBaseOfTask(treeState.data, tasks, taskId)
    }
    wb.open({ kind: 'tui', taskId }, target ?? undefined)
  }

  // currentTaskId 是当前目录上「最该看的那个任务」，只用于右栏 M 角标的数据源。
  // 一个目录下可能有多个任务，取第一个正在跑的，没有就取第一个——角标是装饰，
  // 选谁都不影响正确性，但要稳定（不随渲染抖动）。
  const currentTaskId = useMemo(() => {
    if (!wb.base || wb.base.kind !== 'workspace') return null
    const under = tasks.filter((t) => t.work_dir === wb.base?.path)
    return under.find((t) => t.state === 'running')?.id ?? under[0]?.id ?? null
  }, [tasks, wb.base])

  return (
    <div className="flex h-dvh bg-background">
      <aside role="complementary" className="flex w-[260px] shrink-0 flex-col overflow-y-auto border-r bg-sidebar">
        {treeState.sessionExpired && <SessionExpiredBanner />}
        {treeState.disconnected && !treeState.sessionExpired && (
          <DisconnectedBanner message={treeState.errorText} compact />
        )}
        {ptyRestore.error !== '' && (
          <DisconnectedBanner message={`终端会话恢复失败：${ptyRestore.error}`} compact />
        )}
        {treeState.data && (
          <ProjectTree
            tree={treeState.data}
            tasks={tasks}
            selectedKey={wb.base?.key ?? null}
            ticketCount={tickets.count}
            onSelectDir={wb.select}
            onOpenTask={openTaskTui}
            onOpenBoard={() => setOverlay('board')}
            onOpenTickets={() => setOverlay('tickets')}
            onOpenSettings={() => navigate('/settings')}
            onAddProject={() => setWizardOpen(true)}
            onUnregister={onUnregister}
          />
        )}
      </aside>

      <div className="flex min-w-0 flex-1 flex-col">
        {wb.base && <Breadcrumb base={wb.base} onSplit={wb.split} />}
        <main className="min-h-0 flex-1">
          <Routes>
            <Route
              path="/settings"
              element={<SettingsPage onClose={() => navigate('/')} />}
            />
            <Route path="/machines" element={<Navigate to="/settings" replace />} />
            <Route path="/tasks/:id" element={<TaskDeepLink tree={treeState.data} tasks={tasks} onOpen={openTaskTui} />} />
            <Route
              path="*"
              element={
                <WorkbenchPage
                  api={wb}
                  onAddProject={() => setWizardOpen(true)}
                  terminalUnavailable={wb.base ? ptyNote(wb.base.machine) : ''}
                  onBeforeClose={beforeCloseTab}
                  renderContent={(c, base, group, tabId) => {
                    switch (c.kind) {
                      case 'terminal': {
                        const note = ptyNote(base.machine)
                        if (note !== '') {
                          return <p className="p-4 text-sm text-muted-foreground">{note}</p>
                        }
                        return (
                          <TerminalTab
                            base={base}
                            seq={c.seq}
                            sessionId={c.sessionId}
                            // 会话 id 必须写回这个 tab：不写回的话切一次 tab
                            // 就会再建一个会话，用户每切一次多留一个 shell
                            onSession={(id) => wb.setContent(group, tabId, { ...c, sessionId: id })}
                          />
                        )
                      }
                      case 'file':
                        return <FileTab base={base} rel={c.rel} />
                      case 'tui':
                        return <TuiTab taskId={c.taskId} />
                      default:
                        return null
                    }
                  }}
                />
              }
            />
          </Routes>
        </main>
      </div>

      {wb.base && wb.base.kind === 'workspace' && (
        <div className="w-[280px] shrink-0">
          <FileTree
            base={wb.base}
            taskId={currentTaskId}
            onOpenFile={(rel) => wb.open({ kind: 'file', rel })}
          />
        </div>
      )}

      {/* home 终端走独立浮窗，不进 wb 的 tab 组——它不挂在任何目录上，
          塞进按目录组织的容器里就会跟着目录切换走 */}
      {ptySupport.supported('') !== false && (
        <HomeDock
          dock={dock}
          onKill={killHomeSession}
          renderTab={(t) => (
            <TerminalTab
              base={HOME_BASE}
              seq={t.seq}
              sessionId={t.sessionId}
              onSession={(id) => dock.setSession(t.id, id)}
            />
          )}
        />
      )}

      {overlay === 'board' && (
        <BoardOverlay
          tasksState={tasksState}
          tree={treeState.data}
          onOpenTask={openTaskTui}
          onClose={() => setOverlay('none')}
        />
      )}
      {overlay === 'tickets' && (
        <TicketsOverlay
          tickets={tickets}
          onOpenTask={openTaskTui}
          onClose={() => setOverlay('none')}
        />
      )}

      <ConfirmDialog
        open={closingPty !== null || closingHome !== null}
        title="关闭终端会话"
        description={
          '关闭会终止这个终端会话，里面正在运行的命令会被一并结束。\n' +
          '只是想切走的话直接切到别的 tab——会话会继续在后台跑。' +
          (closingBusyProc === true ? '\n\n⚠ 这个终端里现在还有命令在运行。' : '')
        }
        confirmLabel="关闭并终止"
        destructive
        busy={closeBusy}
        error={closeError}
        onConfirm={() => void (closingPty ? confirmClosePty() : confirmCloseHome())}
        onCancel={() => { setClosingPty(null); setClosingHome(null) }}
      />

      <AddProjectWizard
        open={wizardOpen}
        machines={machinesState.data?.machines ?? []}
        onClose={() => setWizardOpen(false)}
        onDone={() => treeState.refresh()}
      />
    </div>
  )
}

// TaskDeepLink 承接 /tasks/:id 这条 W3b 留下的深链。
//
// 为什么保留：已有书签与 --notify 的通知文案里都可能带这个地址，直接删路由会
// 让它们 404。行为改为「选中该任务所在目录 + 开它的 TUI tab + 换回 /」——地址栏
// 不停在一个不再有对应页面的路径上。
function TaskDeepLink({
  tree,
  tasks,
  onOpen,
}: {
  tree: ProjectTreeResp | null
  tasks: Task[]
  onOpen: (base: BaseDir | null, taskId: string) => void
}) {
  const { id } = useParams<{ id: string }>()
  const navigate = useNavigate()
  const [done, setDone] = useState(false)

  useEffect(() => {
    // 等树到位再解析目录：树还没来时目录解析不出来，会把 tab 开在错的基准上
    if (!id || done || !tree) return
    onOpen(findBaseOfTask(tree, tasks, id), id)
    setDone(true)
    navigate('/', { replace: true })
  }, [id, done, tree, tasks, onOpen, navigate])

  return <p className="p-4 text-sm text-muted-foreground">正在打开任务…</p>
}
