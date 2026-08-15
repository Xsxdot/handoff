// MachinesPage —— 开发机分区（只读）。左侧机器卡片列表 + 右侧选中机器详情。
//
// 数据源：useMachines(true)（15s 探活，spec §6——只在设置页的开发机分区可见时开表）。
// 组件挂在设置页的开发机分区里，分区切走即卸载，usePoll 的 effect 清理自动停表。
//
// 诚实展示（spec §8）：
//   - 不可达机器仍然渲染，标「已断开」并透出 error 原文——绝不静默少一台
//   - 顶部台数统计**含不可达那台**：少一台就是静默丢机器
//   - 三个未接线的操作（可用执行者开关 / 重启 agent / 打开终端）本期**只渲染不接线**：
//     它们需要 agentd 侧的写接口，不在本期。按「不置灰」纪律（spec §0），它们可点，
//     点了给出明确的「尚未实现」说明，而不是一个永远按不动的灰按钮。
//     配对开发机与 Env 文件仍然不渲染——那两项连形态都还没定。
//
// 边界：只读页面，不含任何写操作入口（执行器开关属机器级配置，W3b 不实现）。
import { useEffect, useState } from 'react'
import type { Machine, ProjectTreeResp } from '../../api/types'
import { useMachines } from '../data/useMachines'
import { DisconnectedBanner, LoadFailed, SessionExpiredBanner } from '../lib/Banners'
import { MachineDetail } from './MachineDetail'
import { cn } from '@/lib/utils'

// machineLabel 把机器名转成展示文案：""=本机。
function machineLabel(name: string): string {
  return name === '' ? '本机' : name
}

// dirCountOf 统计项目树里某台机器名下的全部 workspace 目录数。
// probe_error 非空的位置探不出目录，按 0 计（与 counts.ts 口径一致）。
function dirCountOf(tree: ProjectTreeResp | null, machine: string): number {
  if (!tree) return 0
  return tree.projects.reduce((n, p) => {
    const loc = p.locations.find((l) => l.machine === machine)
    return n + (loc && loc.probe_error === '' ? loc.workspaces.length : 0)
  }, 0)
}

export function MachinesPage({ tree }: { tree: ProjectTreeResp | null }) {
  const machinesState = useMachines(true)
  const [selected, setSelected] = useState<string | null>(null)
  const [lastProbe, setLastProbe] = useState<number | null>(null)

  // 每次探活成功（data 有新值）就记下本页时刻，作为「最后心跳」的观测基准。
  // 首次尚无数据时保持 null 显示「—」——刚打开就显示「刚刚」而 agentd 已跑三天
  // 是一种好看的假象，必须标明这是本页打开以来的观测（见 MachineDetail 的注脚）。
  useEffect(() => {
    if (machinesState.data !== null) setLastProbe(Date.now())
  }, [machinesState.data])

  const machines = machinesState.data?.machines ?? []
  // 默认选中第一台（本机优先）；列表刷新后若已选机器仍在则保持，否则回落第一台。
  const activeMachine = machines.find((m) => m.name === (selected ?? '')) ?? machines[0] ?? null

  return (
    <div className="flex h-full min-h-0 flex-col gap-3 p-3">
      {machinesState.sessionExpired && <SessionExpiredBanner />}
      {machinesState.disconnected && !machinesState.sessionExpired && (
        <DisconnectedBanner message={machinesState.errorText} />
      )}

      {machinesState.data === null ? (
        machinesState.sessionExpired ? null : (
          <LoadFailed message={machinesState.errorText || '正在连接 agentd…'} onRetry={() => window.location.reload()} />
        )
      ) : (
        <>
          <dl className="flex items-center gap-8 rounded-lg border bg-background px-4 py-3 text-sm">
            <div>
              <dt className="text-xs text-muted-foreground">开发机</dt>
              <dd className="mt-0.5 text-lg font-semibold">{machines.length}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">在线</dt>
              <dd className="mt-0.5 text-lg font-semibold">{machines.filter((m) => m.reachable).length}</dd>
            </div>
            <div>
              <dt className="text-xs text-muted-foreground">运行任务</dt>
              <dd className="mt-0.5 text-lg font-semibold">{machines.reduce((n, m) => n + m.active_tasks, 0)}</dd>
            </div>
          </dl>

          <div className="flex items-start gap-4">
            <div className="flex w-80 shrink-0 flex-col gap-2">
              {machines.map((m) => (
                <MachineCard
                  key={m.name}
                  machine={m}
                  active={m.name === activeMachine?.name}
                  onSelect={() => setSelected(m.name)}
                />
              ))}
            </div>
            <div className="min-w-0 flex-1">
              {activeMachine && (
                <MachineDetail
                  machine={activeMachine}
                  dirCount={dirCountOf(tree, activeMachine.name)}
                  lastProbe={lastProbe}
                />
              )}
            </div>
          </div>
        </>
      )}
    </div>
  )
}

// MachineCard 是左侧一台机器的卡片：名称（""=本机）、地址、连接状态、活跃任务数。
// 点击选中；不可达机器照样渲染并标「已断开」。
function MachineCard({ machine, active, onSelect }: { machine: Machine; active: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      onClick={onSelect}
      aria-pressed={active}
      className={cn(
        'flex flex-col gap-1 rounded-lg border bg-background p-3 text-left transition-colors',
        active ? 'border-primary bg-accent/40' : 'hover:bg-accent/40',
      )}
    >
      <div className="flex items-center gap-2">
        <span className="min-w-0 flex-1 truncate text-sm font-medium">{machineLabel(machine.name)}</span>
        <span className={cn('shrink-0 text-[11px]', machine.reachable ? 'text-emerald-600' : 'text-amber-600')}>
          {machine.reachable ? '已连接' : '已断开'}
        </span>
      </div>
      <div className="truncate font-mono text-xs text-muted-foreground">{machine.addr}</div>
      <div className="text-[11px] text-muted-foreground">{machine.active_tasks} 活跃任务</div>
    </button>
  )
}
