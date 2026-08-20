// MachineDetail —— 开发机分区右侧的选中机器详情。
//
// 字段来源严格按 spec §4 的表（name/addr/reachable/version/probe_ms/executors/
// default_executor/active_tasks/error），后端没有的字段不渲染：
//   - 操作系统：后端没有这个数据，整格不渲染（spec §0）
//   - 可用执行者：只读列表 + 默认执行者标记；缺省执行者与默认模型由详情块修改
//   - 最后心跳：后端没有该字段，前端记录「本页打开以来」的探活成功时刻并注明，
//     不冒充服务端心跳（spec §9 诚实）
import { formatRelative } from '../lib/format'
import { cn } from '@/lib/utils'
import type { Machine } from '../../api/types'
import { MachineDiscipline } from './MachineDiscipline'
import { MachineEnv } from './MachineEnv'
import { MachineExecutor } from './MachineExecutor'
import { machineEndpoint } from './machineEndpoint'

// machineLabel 把机器名转成展示文案：""=本机。
function machineLabel(name: string): string {
  return name === '' ? '本机' : name
}

export interface MachineDetailProps {
  machine: Machine
  dirCount: number // 该项目树里该机器名下的 workspace 总数（前端按机器聚合）
  lastProbe: number | null // 本页打开以来最近一次探活成功时刻（ms）；null=尚无观测
}

export function MachineDetail({ machine, dirCount, lastProbe }: MachineDetailProps) {
  const remote = machine.name !== ''
  return (
    <section className="rounded-lg border bg-background p-4">
      <h2 className="text-sm font-semibold">{machineLabel(machine.name)}</h2>
      <p className="mt-0.5 font-mono text-xs text-muted-foreground">{machineEndpoint(machine)}</p>

      <dl className="mt-3 grid grid-cols-[max-content_minmax(0,1fr)] gap-x-6 gap-y-2 text-xs">
        <DetailRow label="状态" value={machine.reachable ? '已连接' : '已断开'} />
        <DetailRow label="Agent 版本" value={machine.version || '—'} />
        {/* 延迟格只对远程机器显示：本机 probe_ms 恒为 0（进程内直查），显示「0ms」会误导 */}
        {remote && <DetailRow label="延迟" value={`${machine.probe_ms}ms`} />}
        <DetailRow label="运行任务数" value={String(machine.active_tasks)} />
        <DetailRow label="项目目录数" value={String(dirCount)} />
        {!machine.reachable && <DetailRow label="断开原因" value={machine.error} />}
        <DetailRow
          label="最后心跳"
          value={lastProbe === null ? '—' : formatRelative(new Date(lastProbe).toISOString())}
        />
      </dl>

      <div className="mt-3">
        <p className="text-xs font-medium text-muted-foreground">可用执行者</p>
        {machine.executors.length === 0 ? (
          <p className="mt-1 text-xs text-muted-foreground">（无）</p>
        ) : (
          <ul className="mt-1 flex flex-wrap gap-1.5">
            {machine.executors.map((ex) => (
              <li
                key={ex}
                className={cn(
                  'inline-flex items-center gap-1 rounded-md border px-2 py-0.5 text-xs',
                  ex === machine.default_executor ? 'border-primary/50 bg-primary/10' : '',
                )}
              >
                {ex}
                {ex === machine.default_executor && <span className="text-[10px] text-muted-foreground">默认</span>}
              </li>
            ))}
          </ul>
        )}
      </div>

      <MachineDiscipline machine={machine} />
      <MachineEnv machine={machine} />
      <MachineExecutor machine={machine} />

      <p className="mt-3 text-[11px] text-muted-foreground">
        「最后心跳」是本页打开以来的探活观测，不是 agentd 服务端心跳。
      </p>
    </section>
  )
}

// DetailRow 是详情里的一行 label/value。
function DetailRow({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="break-words text-foreground">{value}</dd>
    </>
  )
}
