// MachineDetail —— 开发机分区右侧的选中机器详情（只读）。
//
// 字段来源严格按 spec §4 的表（name/addr/reachable/version/probe_ms/executors/
// default_executor/active_tasks/error），后端没有的字段不渲染：
//   - 操作系统：后端没有这个数据，整格不渲染（spec §0）
//   - 可用执行者：只读列表 + 默认执行者标记，**没有任何开关**（执行器开关属
//     机器级配置，W3b 不实现；开关以 NOT_WIRED 按钮形式给出「尚未实现」说明）
//   - 最后心跳：后端没有该字段，前端记录「本页打开以来」的探活成功时刻并注明，
//     不冒充服务端心跳（spec §9 诚实）
import { useState } from 'react'
import { formatRelative } from '../lib/format'
import { cn } from '@/lib/utils'
import type { Machine } from '../../api/types'
import { MachineDiscipline } from './MachineDiscipline'
import { MachineEnv } from './MachineEnv'

// NOT_WIRED 是三个「形态已定、后端未做」的操作。点击后就地展开一句说明——
// 不置灰（置灰承诺"以后能用"，用户会反复点），也不静默无反应。
const NOT_WIRED = [
  { key: 'executors', label: '可用执行者', note: '执行者开关尚未实现：需要 agentd 提供机器级配置的写接口。' },
  { key: 'restart', label: '重启 agent', note: '重启尚未实现：需要 agentd 提供自重启接口，且要先想清楚重启期间在跑的任务怎么办。' },
  { key: 'terminal', label: '打开终端', note: '终端尚未实现：PTY 后端未做，当前请用 handoff attach <task>。' },
]

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
  // openNote 记录当前展开的「尚未实现」说明条；null=全部收起。
  const [openNote, setOpenNote] = useState<string | null>(null)
  return (
    <section className="rounded-lg border bg-background p-4">
      <h2 className="text-sm font-semibold">{machineLabel(machine.name)}</h2>
      <p className="mt-0.5 font-mono text-xs text-muted-foreground">{machine.addr}</p>

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

      <div className="mt-3">
        <p className="text-xs font-medium text-muted-foreground">机器操作</p>
        <div className="mt-1 flex flex-wrap gap-1.5">
          {NOT_WIRED.map((item) => (
            <div key={item.key}>
              <button
                type="button"
                onClick={() => setOpenNote(item.key)}
                aria-expanded={openNote === item.key}
                className="rounded-md border px-2 py-0.5 text-xs hover:bg-accent"
              >
                {item.label}
              </button>
              {openNote === item.key && (
                <p className="mt-1 max-w-md text-[11px] text-muted-foreground">{item.note}</p>
              )}
            </div>
          ))}
        </div>
      </div>

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
