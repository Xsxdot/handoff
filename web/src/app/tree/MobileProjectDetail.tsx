// MobileProjectDetail —— 移动项目详情面（B369.10 岔口 2）。
//
// 职责：对齐原型 mobile-project.html 的五段形态——scenehead（返回+项目名+副题）、
//       locbar 位置切换条（同项目跨机位置平级，离线灰显可看不可点）、选中位置的
//       工作树卡（分支/主目录徽标/全路径/在跑任务行 + 「打开终端/浏览文件」双动作）、
//       「从分支建工作树」（直接挂既有 NewWorktreeDialog，弹层零翻案换入口）、
//       「这个项目的终端会话」区（fetchPtySessions('all') 口径，服务端托管、
//       关 App 不死的承诺靠「活着即列出」兑现——不用本浏览器 openedItems 投影）。
// 边界：
//   - 只做编排与呈现，不自发轮询（ptySessions 由 Shell usePoll 供给——SettingsHub
//     「只做编排不做内容」同款纪律）
//   - locbar 选中位是组件内 state 不进 URL（岔口 2 裁决：位置是详情面内的临时
//     选择，级数浅且无深链消费场景；刷新回落第一个可用位可接受）
//   - 不认识 router：返回/进任务现场/开目录都经回调，base 解析在本组件内完成
//     （树上按 session.base_path 找同机 workspace，findBaseByKey 思路；回落该机
//     主目录，再回落机器 home 基准——形状与 workspaceBase 的 `~@machine` 同构）
//   - 终端/文件入口只有双动作这两条（onOpenTerminalAt/onOpenDirectory 都是 Shell
//     既有回调），不造第三条终端/文件入口
import { useState } from 'react'
import { ChevronLeft } from 'lucide-react'
import { TOUCH_BASELINE } from '@/lib/touch'
import { cn } from '@/lib/utils'
import type {
  MachineStatus,
  ProjectLocationNode,
  ProjectNode,
  PtySession,
  Task,
  Workspace,
} from '../../api/types'
import { NewWorktreeDialog } from './NewWorktreeDialog'
import { locationProblem, machineLabel, tasksOfWorkspace, workspaceBase } from './ProjectTree'
import { sortWorkspaces } from './sortWorkspaces'
import { taskDisplayName } from '../lib/taskName'
import type { BaseDir } from '../workbench/useWorkbench'

export interface MobileProjectDetailProps {
  project: ProjectNode
  machines: MachineStatus[] | undefined
  tasks: Task[]
  // ptySessions 是 Shell 的 usePoll(fetchPtySessions('all'), 30_000) 结果；
  // null = 还没拉到（列表区按空态呈现，不报错——恢复清单缺席不是异常）。
  ptySessions: PtySession[] | null
  onBack: () => void
  onOpenTerminalAt: (base: BaseDir) => void
  onOpenDirectory: (base: BaseDir) => void
  onOpenTask: (base: BaseDir, taskId: string) => void
  onWorktreeCreated: (project: ProjectNode, machine: string, ws: Workspace) => void
  // base 由组件解析后回抛（合同 (sessionId, base)）：Shell 只负责 restoreTerminal
  // + 下钻，不做树上反查——树流在本组件 props 里，Shell 不必为它再开缝。
  onReopenPtySession: (sessionId: string, base: BaseDir) => void
}

// isAwaitingUs 与 ProjectTree 的 wsCounts 同口径：在跑 + 等待作答/裁决。
function isAwaitingUs(t: Task): boolean {
  return t.state === 'running' || t.state === 'waiting_answer' || t.state === 'waiting_review'
}

// resolvePtyBase 按会话的落点解析 BaseDir：workspace 会话按 base_path 找同机
// 工作树；找不到（树未到/目录已删）回落该机主目录；连主目录都不在树上时落
// 机器 home 基准（key 形状与 workspaceBase 的 `~@machine` 同构，select 不撞键）。
function resolvePtyBase(session: PtySession, project: ProjectNode): BaseDir {
  if (session.base_kind === 'workspace') {
    for (const loc of project.locations) {
      if (loc.machine !== session.machine) continue
      const ws = loc.workspaces.find((w) => w.path === session.base_path)
      if (ws) return workspaceBase(project, loc.machine, ws)
    }
  }
  for (const loc of project.locations) {
    if (loc.machine !== session.machine) continue
    const main = loc.workspaces.find((w) => w.is_main)
    if (main) return workspaceBase(project, loc.machine, main)
  }
  return {
    key: session.machine ? `~@${session.machine}` : '~',
    kind: 'home',
    path: '~',
    label: 'home',
    projectName: project.name,
    machine: session.machine,
  }
}

export function MobileProjectDetail({
  project,
  machines,
  tasks,
  ptySessions,
  onBack,
  onOpenTerminalAt,
  onOpenDirectory,
  onOpenTask,
  onWorktreeCreated,
  onReopenPtySession,
}: MobileProjectDetailProps) {
  // 选中位置：null = 跟随「第一个可用位」；点过 pill 后固定在该位（哪怕它转离线
  // ——用户自己的选择不被自动跳走，卡只是整体禁用）。
  const [selectedMachine, setSelectedMachine] = useState<string | null>(null)
  const [newWtOpen, setNewWtOpen] = useState(false)

  const locations = project.locations
  const firstHealthy = locations.find((loc) => locationProblem(loc, machines) === '')
  const activeLoc: ProjectLocationNode | undefined =
    locations.find((loc) => loc.machine === selectedMachine) ?? firstHealthy ?? locations[0]
  const offline = activeLoc !== undefined && locationProblem(activeLoc, machines) !== ''
  const activeTasks = tasks.filter((t) => t.project_id === project.project_id && isAwaitingUs(t)).length

  const ordered = activeLoc
    ? sortWorkspaces(activeLoc.workspaces, (ws) => ({
        tickets: 0,
        tasks: tasksOfWorkspace(tasks, project, activeLoc.machine, ws).filter(isAwaitingUs).length,
        createdAt: ws.created_at,
      }))
    : []
  const ptyHere = (ptySessions ?? []).filter(
    (s) => s.machine === activeLoc?.machine && s.exit_code === undefined && s.base_kind === 'workspace' && activeLoc.workspaces.some(ws => ws.path === s.base_path),
  )

  return (
    <div data-testid="mobile-project-detail" className={cn('absolute inset-0 z-10 flex min-h-0 flex-col bg-background', TOUCH_BASELINE)}>
      {/* scenehead：返回 + 项目名 + 副题（原型 .scenehead 同构） */}
      <div className="flex items-center gap-2.5 border-b bg-background px-3.5 pb-2.5 pt-4">
        <button
          type="button"
          data-testid="project-detail-back"
          aria-label="返回项目列表"
          onClick={onBack}
          className="-ml-1 shrink-0 rounded p-1 text-xl leading-none text-foreground hover:bg-accent"
        >
          <ChevronLeft className="size-5" />
        </button>
        <div className="min-w-0 flex-1">
          <div className="truncate text-base font-bold">{project.name}</div>
          <div className="text-[11px] text-muted-foreground">
            {locations.length} 处位置 · {activeTasks} 个活跃任务
          </div>
        </div>
      </div>

      {/* locbar：位置平级切换条（原型 .locbar 同构）。离线位 disabled 灰显——
          原型 note ①：不做离线只读缓存，标记状态，重连才恢复 */}
      <div data-testid="project-locbar" className="flex gap-2 overflow-x-auto border-b bg-background px-3.5 py-2.5">
        {locations.map((loc) => {
          const problem = locationProblem(loc, machines)
          const on = activeLoc?.machine === loc.machine
          return (
            <button
              key={loc.machine}
              type="button"
              data-testid="project-loc-pill"
              aria-pressed={on}
              disabled={problem !== ''}
              onClick={() => setSelectedMachine(loc.machine)}
              className={cn(
                'inline-flex shrink-0 items-center gap-1.5 rounded-full border px-3 py-1 text-xs',
                on ? 'border-primary font-semibold text-foreground' : 'border-border text-muted-foreground',
                problem !== '' && 'opacity-55',
              )}
            >
              <span
                aria-hidden
                className={cn('size-1.5 rounded-full', problem === '' ? 'bg-state-active' : 'bg-[#d4d4d4]')}
              />
              {machineLabel(loc.machine)}
              {problem !== '' && <span>· 离线</span>}
            </button>
          )
        })}
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-3.5 pb-3.5">
        {activeLoc === undefined ? (
          <p className="mt-4 text-sm text-muted-foreground">这个项目还没有登记任何位置。</p>
        ) : (
          <>
            {/* 工作树卡区：离线位置整卡禁用（原型 .wt.off），路径写全（原型 note ②） */}
            {ordered.map((ws) => {
              const base = workspaceBase(project, activeLoc.machine, ws)
              const running = tasksOfWorkspace(tasks, project, activeLoc.machine, ws).filter(isAwaitingUs)
              return (
                <div
                  key={ws.path}
                  data-testid="project-wt-card"
                  className={cn('mb-2.5 rounded-[14px] border bg-background p-3', offline && 'opacity-55')}
                >
                  <div className="flex items-center gap-2">
                    <span className="min-w-0 flex-1 truncate font-mono text-[13px] font-semibold">{ws.branch === '' ? base.label : ws.branch}</span>
                    {ws.is_main && (
                      <span className="shrink-0 rounded-full border px-2 py-px text-[10.5px] text-muted-foreground">主目录</span>
                    )}
                  </div>
                  <div className="mt-1 font-mono text-[11px] text-muted-foreground">{ws.path}</div>
                  {running.map((t) => (
                    <button
                      key={t.id}
                      type="button"
                      data-testid="project-wt-task"
                      disabled={offline}
                      onClick={() => onOpenTask(base, t.id)}
                      className={cn(
                        'mt-1 block w-full truncate text-left text-[11.5px] text-state-active',
                        !offline && 'hover:underline',
                      )}
                    >
                      ● {t.state === 'running' ? '在跑' : '等你'} · {taskDisplayName(t)}
                    </button>
                  ))}
                  <div className="mt-2.5 flex gap-2">
                    <button
                      type="button"
                      data-testid="project-wt-terminal"
                      disabled={offline}
                      onClick={() => onOpenTerminalAt(base)}
                      className="flex-1 rounded-lg border border-primary bg-primary py-1.5 text-center text-[12.5px] font-semibold text-primary-foreground disabled:bg-muted"
                    >
                      打开终端
                    </button>
                    <button
                      type="button"
                      data-testid="project-wt-files"
                      disabled={offline}
                      onClick={() => onOpenDirectory(base)}
                      className="flex-1 rounded-lg border py-1.5 text-center text-[12.5px] text-foreground disabled:bg-muted"
                    >
                      浏览文件
                    </button>
                  </div>
                </div>
              )
            })}

            <button
              type="button"
              data-testid="project-new-worktree"
              disabled={offline}
              onClick={() => setNewWtOpen(true)}
              className="w-full rounded-[14px] border-[1.5px] border-dashed py-2.5 text-[13px] text-muted-foreground disabled:opacity-55"
            >
              ＋ 从分支建工作树
            </button>

            {/* 终端会话区：这台机器上活着的会话（exit_code 缺席 = 活着）。
                数据口径 fetchPtySessions('all')——服务端托管的恢复真相源 */}
            <div className="mt-4 text-xs text-muted-foreground">这个项目的终端会话</div>
            <div data-testid="project-pty-list" className="mt-2">
              {ptySessions === null ? (<p className="text-sm text-muted-foreground">正在读取终端…</p>) : ptyHere.length === 0 ? (
                <p className="text-sm text-muted-foreground">这个项目目前没有运行中的终端。</p>
              ) : (
                ptyHere.map((session) => (
                  <div key={session.id} data-testid="project-pty-row" className="mb-2.5 rounded-[14px] border bg-background p-3">
                    <div className="flex items-center gap-2">
                      <span className="min-w-0 flex-1 truncate text-[13px] font-semibold">终端 · {session.shell}</span>
                      <span className="shrink-0 text-[11px] text-state-active">活着</span>
                    </div>
                    <div className="mt-1 font-mono text-[11px] text-muted-foreground">{session.base_path === '' ? '~' : session.base_path}</div>
                    <button
                      type="button"
                      data-testid="project-pty-resume"
                      onClick={() => onReopenPtySession(session.id, resolvePtyBase(session, project))}
                      className="mt-2.5 w-full rounded-lg border border-primary bg-primary py-1.5 text-center text-[12.5px] font-semibold text-primary-foreground"
                    >
                      回到这个终端
                    </button>
                  </div>
                ))
              )}
            </div>
          </>
        )}
      </div>

      {activeLoc !== undefined && (
        <NewWorktreeDialog
          open={newWtOpen}
          projectName={activeLoc.name}
          machine={activeLoc.machine}
          onClose={() => setNewWtOpen(false)}
          onCreated={(ws) => {
            setNewWtOpen(false)
            onWorktreeCreated(project, activeLoc.machine, ws)
          }}
        />
      )}
    </div>
  )
}
